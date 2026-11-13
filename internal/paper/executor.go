package paper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
)

// maxQuoteAge is how old a cached tick may be before a market order refuses to
// fill from it. It matches the server-side cached-LTP window.
const maxQuoteAge = 10 * time.Second

// Options configures the paper executor.
type Options struct {
	Store           Store
	Logger          *slog.Logger
	Now             func() time.Time
	StartingBalance float64
	Slippage        float64
	ShortMarginPct  float64
	QuoteSource     func(ctx context.Context, instrumentKey string) (Quote, bool)
	// ShortNotional returns a per-share notional (typically the strike or spot)
	// used to reserve margin for short positions. Nil falls back to the fill
	// price.
	ShortNotional func(instrumentKey string) (float64, bool)
}

// Executor simulates order placement and tick-driven fills for one paper
// account. It implements bracket.Executor.
type Executor struct {
	mu             sync.Mutex
	store          Store
	log            *slog.Logger
	now            func() time.Time
	slippage       float64
	shortMarginPct float64
	quoteSource    func(ctx context.Context, instrumentKey string) (Quote, bool)
	shortNotional  func(instrumentKey string) (float64, bool)
	onFill         func(bracket.Fill)

	quotes    map[string]Quote
	lastFill  map[string]float64
	orders    map[string]*Order
	openByKey map[string]map[string]*Order
	positions map[string]*Position
	account   Account
	seq       atomic.Uint64
}

// Limits is the paper account's margin summary.
type Limits struct {
	Available float64
	Used      float64
	Total     float64
}

// PositionView is a paper position enriched with the latest price.
type PositionView struct {
	Position
	LTP           float64
	UnrealizedPnL float64
}

// New creates and restores the paper executor.
func New(ctx context.Context, opts Options) (*Executor, error) {
	if opts.Store == nil {
		return nil, errors.New("paper: store is required")
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	starting := opts.StartingBalance
	if starting <= 0 {
		starting = 500000
	}
	shortPct := opts.ShortMarginPct
	if shortPct <= 0 || shortPct > 100 {
		shortPct = 20
	}
	e := &Executor{
		store:          opts.Store,
		log:            log,
		now:            now,
		slippage:       opts.Slippage,
		shortMarginPct: shortPct,
		quoteSource:    opts.QuoteSource,
		shortNotional:  opts.ShortNotional,
		quotes:         make(map[string]Quote),
		lastFill:       make(map[string]float64),
		orders:         make(map[string]*Order),
		openByKey:      make(map[string]map[string]*Order),
		positions:      make(map[string]*Position),
	}
	account, err := e.store.EnsurePaperAccount(ctx, starting)
	if err != nil {
		return nil, fmt.Errorf("paper: load account: %w", err)
	}
	e.account = account
	if err := e.restore(ctx); err != nil {
		return nil, err
	}
	return e, nil
}

// SetOnFill installs the callback that delivers fills to the bracket engine.
func (e *Executor) SetOnFill(fn func(bracket.Fill)) {
	e.mu.Lock()
	e.onFill = fn
	e.mu.Unlock()
}

// SetSlippage updates the market-order slippage at runtime.
func (e *Executor) SetSlippage(v float64) {
	e.mu.Lock()
	e.slippage = v
	e.mu.Unlock()
}

// SetShortMarginPct updates the short-position margin percentage at runtime.
func (e *Executor) SetShortMarginPct(v float64) {
	if v <= 0 || v > 100 {
		return
	}
	e.mu.Lock()
	e.shortMarginPct = v
	e.mu.Unlock()
}

func (e *Executor) restore(ctx context.Context) error {
	openOrders, err := e.store.ListOpenPaperOrders(ctx)
	if err != nil {
		return fmt.Errorf("paper: load open orders: %w", err)
	}
	recent, err := e.store.ListPaperOrders(ctx, 1000)
	if err != nil {
		return fmt.Errorf("paper: load orders: %w", err)
	}
	now := e.now()
	seen := make(map[string]struct{}, len(openOrders)+len(recent))
	for i := range openOrders {
		o := openOrders[i]
		seen[o.ID] = struct{}{}
		if isExpired(&o, now) {
			o.Status = StatusCancelled
			o.UpdatedAt = now
			pctx, cancel := persistContext(ctx)
			if perr := e.store.UpdatePaperOrder(pctx, o); perr != nil {
				e.log.Warn("paper: expire order on restore", "order_id", o.ID, "error", perr)
			}
			cancel()
			e.orders[o.ID] = &o
			continue
		}
		e.orders[o.ID] = &o
		e.indexOpenLocked(&o)
	}
	for i := range recent {
		o := recent[i]
		if _, ok := seen[o.ID]; ok {
			continue
		}
		e.orders[o.ID] = &o
	}
	positions, err := e.store.ListPaperPositions(ctx)
	if err != nil {
		return fmt.Errorf("paper: load positions: %w", err)
	}
	for i := range positions {
		p := positions[i]
		e.positions[p.InstrumentKey] = &p
	}
	return nil
}

func (e *Executor) indexOpenLocked(o *Order) {
	if o.InstrumentKey == "" {
		return
	}
	m := e.openByKey[o.InstrumentKey]
	if m == nil {
		m = make(map[string]*Order)
		e.openByKey[o.InstrumentKey] = m
	}
	m[o.ID] = o
}

func (e *Executor) unindexOpenLocked(o *Order) {
	m := e.openByKey[o.InstrumentKey]
	if m == nil {
		return
	}
	delete(m, o.ID)
	if len(m) == 0 {
		delete(e.openByKey, o.InstrumentKey)
	}
}

// Place simulates an order. Market orders fill immediately when a live quote
// is available; limit and stop orders rest until a tick crosses them.
func (e *Executor) Place(ctx context.Context, o kotakapi.Order) (string, error) {
	side := normalizeSide(o.TransactionType)
	if side == "" {
		return "", errors.New("paper: transaction type must be BUY or SELL")
	}
	if o.Quantity <= 0 {
		return "", errors.New("paper: quantity must be positive")
	}
	key := strings.TrimSpace(o.InstrumentKey)
	if key == "" {
		return "", errors.New("paper: instrument key is required")
	}
	orderType := normalizeOrderType(o.OrderType)
	if orderType == "" {
		return "", fmt.Errorf("paper: unsupported order type %q", o.OrderType)
	}
	if orderType == "L" && o.Price <= 0 {
		return "", errors.New("paper: limit price must be positive")
	}
	if isStopOrder(orderType) && o.TriggerPrice <= 0 {
		return "", errors.New("paper: trigger price must be positive")
	}

	var quote Quote
	if orderType == "MKT" {
		q, ok := e.quote(ctx, key)
		if !ok || q.LTP <= 0 {
			return "", fmt.Errorf("paper: no fresh live quote for %s", key)
		}
		quote = q
	}

	now := e.now()
	id := fmt.Sprintf("paper_%d_%d", now.UnixNano(), e.seq.Add(1))
	order := &Order{
		ID:              id,
		BracketID:       bracketIDFromTag(o.Tag),
		ExchangeSegment: strings.TrimSpace(o.ExchangeSegment),
		TradingSymbol:   strings.TrimSpace(o.TradingSymbol),
		InstrumentKey:   key,
		TransactionType: side,
		Product:         strings.ToUpper(strings.TrimSpace(o.Product)),
		OrderType:       orderType,
		Quantity:        o.Quantity,
		Price:           o.Price,
		TriggerPrice:    o.TriggerPrice,
		Status:          StatusOpen,
		Validity:        normalizeValidity(o.Validity),
		Tag:             strings.TrimSpace(o.Tag),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if order.Product == "" {
		order.Product = "MIS"
	}
	if isStopOrder(orderType) {
		order.Status = StatusTriggerPending
	}

	e.mu.Lock()
	e.orders[id] = order
	e.indexOpenLocked(order)
	var fills []bracket.Fill
	if orderType == "MKT" {
		price := marketFillPrice(side, quote, e.slippage)
		fill, ferr := e.fillNewLocked(ctx, order, price, order.Quantity)
		if ferr != nil {
			delete(e.orders, id)
			e.unindexOpenLocked(order)
			e.mu.Unlock()
			return "", fmt.Errorf("paper: fill order: %w", ferr)
		}
		fills = append(fills, fill)
	} else {
		pctx, cancel := persistContext(ctx)
		err := e.store.CreatePaperOrder(pctx, *order)
		cancel()
		if err != nil {
			delete(e.orders, id)
			e.unindexOpenLocked(order)
			e.mu.Unlock()
			return "", fmt.Errorf("paper: persist order: %w", err)
		}
	}
	e.mu.Unlock()

	e.log.Info("paper order placed",
		"event", "paper_order_placed", "mode", "paper", "order_id", id,
		"instrument", key, "side", side, "order_type", orderType,
		"quantity", o.Quantity, "price", o.Price, "tag", order.Tag)
	e.deliver(fills)
	return id, nil
}

// Cancel marks a resting order cancelled. Cancelling an already complete order
// is a no-op so the bracket engine can clean up safely.
func (e *Executor) Cancel(ctx context.Context, id string) error {
	e.mu.Lock()
	o, ok := e.orders[id]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("paper: order %s not found", id)
	}
	if !o.Open() {
		e.mu.Unlock()
		return nil
	}
	if err := e.cancelLocked(ctx, o); err != nil {
		e.mu.Unlock()
		return err
	}
	tag := o.Tag
	e.mu.Unlock()
	if isEntryTag(tag) {
		e.deliver([]bracket.Fill{{OrderID: id, Tag: tag, Status: StatusCancelled}})
	}
	return nil
}

func (e *Executor) cancelLocked(ctx context.Context, o *Order) error {
	next := *o
	next.Status = StatusCancelled
	next.UpdatedAt = e.now()
	pctx, cancel := persistContext(ctx)
	err := e.store.UpdatePaperOrder(pctx, next)
	cancel()
	if err != nil {
		return fmt.Errorf("paper: cancel order %s: %w", o.ID, err)
	}
	*o = next
	e.unindexOpenLocked(o)
	e.log.Info("paper order cancelled",
		"event", "paper_order_cancelled", "mode", "paper", "order_id", o.ID,
		"instrument", o.InstrumentKey, "tag", o.Tag)
	return nil
}

// Modify updates a resting order's price, trigger, quantity or type.
func (e *Executor) Modify(ctx context.Context, id string, o kotakapi.Order) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	existing, ok := e.orders[id]
	if !ok {
		return fmt.Errorf("paper: order %s not found", id)
	}
	if !existing.Open() {
		return fmt.Errorf("paper: order %s is %s and cannot be modified", id, existing.Status)
	}
	next := *existing
	if o.Quantity > 0 {
		next.Quantity = o.Quantity
	}
	if o.Price > 0 {
		next.Price = o.Price
	}
	if o.TriggerPrice > 0 {
		next.TriggerPrice = o.TriggerPrice
	}
	if orderType := normalizeOrderType(o.OrderType); orderType != "" && orderType != "MKT" {
		next.OrderType = orderType
	}
	if next.OrderType == "L" && next.Price <= 0 {
		return errors.New("paper: limit price must be positive")
	}
	if isStopOrder(next.OrderType) && next.TriggerPrice <= 0 {
		return errors.New("paper: trigger price must be positive")
	}
	next.UpdatedAt = e.now()
	pctx, cancel := persistContext(ctx)
	err := e.store.UpdatePaperOrder(pctx, next)
	cancel()
	if err != nil {
		return fmt.Errorf("paper: modify order %s: %w", id, err)
	}
	*existing = next
	e.log.Info("paper order modified",
		"event", "paper_order_modified", "mode", "paper", "order_id", id,
		"instrument", next.InstrumentKey, "price", next.Price, "trigger", next.TriggerPrice)
	return nil
}

// Lookup resolves an order by ID or client tag.
func (e *Executor) Lookup(_ context.Context, id, tag string) (string, string, float64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var found *Order
	if id != "" {
		found = e.orders[id]
	} else if tag != "" {
		for _, o := range e.orders {
			if o.Tag == tag {
				found = o
				break
			}
		}
	}
	if found == nil {
		return "", "", 0, bracket.ErrExecutorUnavailable
	}
	return found.ID, found.Status, found.FilledPrice, nil
}

// Order returns a copy of one paper order.
func (e *Executor) Order(id string) (Order, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	o, ok := e.orders[id]
	if !ok {
		return Order{}, false
	}
	return *o, true
}

// LastFillPrice returns the most recent simulated fill price for an instrument.
func (e *Executor) LastFillPrice(instrumentKey string) (float64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	price, ok := e.lastFill[instrumentKey]
	return price, ok
}

// OnTick updates the last price and fills any resting orders that the tick
// crosses.
func (e *Executor) OnTick(ctx context.Context, instrumentKey string, ltp, bid, ask float64) {
	instrumentKey = strings.TrimSpace(instrumentKey)
	if instrumentKey == "" || ltp <= 0 {
		return
	}
	now := e.now()
	e.mu.Lock()
	if _, exists := e.quotes[instrumentKey]; !exists && len(e.quotes) >= 2048 {
		for key := range e.quotes {
			delete(e.quotes, key)
			break
		}
	}
	e.quotes[instrumentKey] = Quote{LTP: ltp, Bid: bid, Ask: ask, At: now}
	open := e.openByKey[instrumentKey]
	if len(open) == 0 {
		e.mu.Unlock()
		return
	}
	var fills []bracket.Fill
	for _, o := range open {
		if isExpired(o, now) {
			tag := o.Tag
			if err := e.cancelLocked(ctx, o); err != nil {
				e.log.Warn("paper: expire resting order", "order_id", o.ID, "error", err)
				continue
			}
			fills = append(fills, bracket.Fill{OrderID: o.ID, Tag: tag, Status: StatusCancelled})
			continue
		}
		price, ok := e.triggerPrice(o, ltp)
		if !ok {
			continue
		}
		fill, err := e.fillLocked(ctx, o, price, o.Quantity)
		if err != nil {
			e.log.Warn("paper: fill resting order", "order_id", o.ID, "error", err)
			continue
		}
		fills = append(fills, fill)
	}
	e.mu.Unlock()
	e.deliver(fills)
}

func (e *Executor) triggerPrice(o *Order, ltp float64) (float64, bool) {
	buy := o.TransactionType == "BUY"
	switch o.OrderType {
	case "L":
		if o.Price <= 0 {
			return 0, false
		}
		if buy && ltp <= o.Price {
			return o.Price, true
		}
		if !buy && ltp >= o.Price {
			return o.Price, true
		}
	case "SL-M":
		if buy && o.TriggerPrice > 0 && ltp >= o.TriggerPrice {
			return o.TriggerPrice + e.slippage, true
		}
		if !buy && o.TriggerPrice > 0 && ltp <= o.TriggerPrice {
			return mathMax(o.TriggerPrice-e.slippage, 0.01), true
		}
	case "SL":
		if buy && o.TriggerPrice > 0 && ltp >= o.TriggerPrice {
			if o.Price > 0 {
				return o.Price, true
			}
			return o.TriggerPrice + e.slippage, true
		}
		if !buy && o.TriggerPrice > 0 && ltp <= o.TriggerPrice {
			if o.Price > 0 {
				return o.Price, true
			}
			return mathMax(o.TriggerPrice-e.slippage, 0.01), true
		}
	}
	return 0, false
}

// planFill computes the next order/position/account/trade state without
// mutating anything.
func (e *Executor) planFill(o *Order, price float64, qty int64) (Order, Position, Account, Trade, float64) {
	now := e.now()
	filled := *o
	filled.FilledQuantity = qty
	filled.FilledPrice = price
	filled.Status = StatusComplete
	filled.UpdatedAt = now

	pos := Position{
		InstrumentKey:   o.InstrumentKey,
		ExchangeSegment: o.ExchangeSegment,
		TradingSymbol:   o.TradingSymbol,
		Product:         o.Product,
	}
	if existing := e.positions[o.InstrumentKey]; existing != nil {
		pos = *existing
	}
	realized := applyToPosition(&pos, o.TransactionType, qty, price)
	pos.UpdatedAt = now

	account := e.account
	account.RealizedPnL += realized
	account.UpdatedAt = now

	trade := Trade{
		OrderID:         o.ID,
		InstrumentKey:   o.InstrumentKey,
		TradingSymbol:   o.TradingSymbol,
		TransactionType: o.TransactionType,
		Quantity:        qty,
		Price:           price,
		ExecutedAt:      now,
	}
	return filled, pos, account, trade, realized
}

func (e *Executor) applyFillLocked(o *Order, filled Order, pos Position, account Account, price, realized float64) bracket.Fill {
	*o = filled
	e.positions[o.InstrumentKey] = &pos
	e.account = account
	e.lastFill[o.InstrumentKey] = price
	e.unindexOpenLocked(o)
	e.log.Info("paper order filled",
		"event", "paper_order_filled", "mode", "paper", "order_id", o.ID,
		"instrument", o.InstrumentKey, "side", o.TransactionType,
		"quantity", filled.FilledQuantity, "price", price, "realized_pnl", realized)
	return bracket.Fill{OrderID: o.ID, Tag: o.Tag, Status: StatusComplete, Price: price, Qty: filled.FilledQuantity}
}

// fillLocked updates an existing resting order and persists the fill.
func (e *Executor) fillLocked(ctx context.Context, o *Order, price float64, qty int64) (bracket.Fill, error) {
	filled, pos, account, trade, realized := e.planFill(o, price, qty)
	pctx, cancel := persistContext(ctx)
	err := e.store.RecordPaperFill(pctx, filled, pos, account, trade)
	cancel()
	if err != nil {
		return bracket.Fill{}, err
	}
	return e.applyFillLocked(o, filled, pos, account, price, realized), nil
}

// fillNewLocked inserts a market order and its fill in one transaction. A
// failed fill therefore never leaves an OPEN order row behind.
func (e *Executor) fillNewLocked(ctx context.Context, o *Order, price float64, qty int64) (bracket.Fill, error) {
	filled, pos, account, trade, realized := e.planFill(o, price, qty)
	pctx, cancel := persistContext(ctx)
	err := e.store.CreatePaperFill(pctx, filled, pos, account, trade)
	cancel()
	if err != nil {
		return bracket.Fill{}, err
	}
	return e.applyFillLocked(o, filled, pos, account, price, realized), nil
}

func (e *Executor) deliver(fills []bracket.Fill) {
	if len(fills) == 0 {
		return
	}
	e.mu.Lock()
	fn := e.onFill
	e.mu.Unlock()
	if fn == nil {
		return
	}
	for _, fill := range fills {
		fn(fill)
	}
}

func (e *Executor) quote(ctx context.Context, key string) (Quote, bool) {
	e.mu.Lock()
	q, ok := e.quotes[key]
	e.mu.Unlock()
	if ok && q.LTP > 0 && e.now().Sub(q.At) <= maxQuoteAge {
		return q, true
	}
	if e.quoteSource == nil {
		return Quote{}, false
	}
	q, ok = e.quoteSource(ctx, key)
	if ok && q.LTP > 0 {
		e.mu.Lock()
		e.quotes[key] = q
		e.mu.Unlock()
	}
	return q, ok
}

// Positions returns all paper positions with the latest price and unrealised PnL.
func (e *Executor) Positions() []PositionView {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]PositionView, 0, len(e.positions))
	for _, p := range e.positions {
		view := PositionView{Position: *p}
		if q, ok := e.quotes[p.InstrumentKey]; ok && q.LTP > 0 {
			view.LTP = q.LTP
			view.UnrealizedPnL = (q.LTP - p.AveragePrice) * float64(p.NetQuantity)
		} else {
			view.LTP = p.AveragePrice
		}
		out = append(out, view)
	}
	return out
}

// Orders returns the in-memory order book, newest first.
func (e *Executor) Orders() []Order {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Order, 0, len(e.orders))
	for _, o := range e.orders {
		out = append(out, *o)
	}
	sortOrdersNewestFirst(out)
	return out
}

// Trades returns the persisted trade book.
func (e *Executor) Trades(ctx context.Context, limit int) ([]Trade, error) {
	return e.store.ListPaperTrades(ctx, limit)
}

// Limits returns the paper account's margin summary.
func (e *Executor) Limits() Limits {
	e.mu.Lock()
	defer e.mu.Unlock()
	used := e.usedMarginLocked()
	total := e.account.StartingBalance + e.account.RealizedPnL
	available := total - used
	if available < 0 {
		available = 0
	}
	return Limits{Available: available, Used: used, Total: total}
}

func (e *Executor) usedMarginLocked() float64 {
	var used float64
	for _, p := range e.positions {
		if p.NetQuantity == 0 {
			continue
		}
		price := p.AveragePrice
		if q, ok := e.quotes[p.InstrumentKey]; ok && q.LTP > 0 {
			price = q.LTP
		}
		notional := absFloat64(float64(p.NetQuantity)) * price
		if p.NetQuantity < 0 {
			notional = absFloat64(float64(p.NetQuantity)) * e.shortNotionalFor(p.InstrumentKey, price)
			notional *= e.shortMarginPct / 100
		}
		used += notional
	}
	for _, o := range e.orders {
		if !o.Open() || !isEntryTag(o.Tag) {
			continue
		}
		price := o.Price
		if isStopOrder(o.OrderType) {
			price = o.TriggerPrice
		}
		if price <= 0 {
			if q, ok := e.quotes[o.InstrumentKey]; ok {
				price = q.LTP
			}
		}
		if price <= 0 {
			continue
		}
		notional := absFloat64(float64(o.Quantity)) * price
		if o.TransactionType == "SELL" {
			notional = absFloat64(float64(o.Quantity)) * e.shortNotionalFor(o.InstrumentKey, price)
			notional *= e.shortMarginPct / 100
		}
		used += notional
	}
	return used
}

func (e *Executor) shortNotionalFor(instrumentKey string, fallback float64) float64 {
	if e.shortNotional != nil {
		if v, ok := e.shortNotional(instrumentKey); ok && v > 0 {
			return v
		}
	}
	return fallback
}

// OpenInstrumentKeys returns the instruments that need live ticks: open orders
// and non-zero positions.
func (e *Executor) OpenInstrumentKeys() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	set := make(map[string]struct{})
	for key, orders := range e.openByKey {
		if len(orders) > 0 {
			set[key] = struct{}{}
		}
	}
	for key, p := range e.positions {
		if p.NetQuantity != 0 {
			set[key] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	return out
}

// PnL returns realized plus unrealised paper PnL.
func (e *Executor) PnL() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	total := e.account.RealizedPnL
	for _, p := range e.positions {
		if p.NetQuantity == 0 {
			continue
		}
		ltp := p.AveragePrice
		if q, ok := e.quotes[p.InstrumentKey]; ok && q.LTP > 0 {
			ltp = q.LTP
		}
		total += (ltp - p.AveragePrice) * float64(p.NetQuantity)
	}
	return total
}

func persistContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func applyToPosition(p *Position, side string, qty int64, price float64) float64 {
	if qty <= 0 {
		return 0
	}
	delta := qty
	if side == "SELL" {
		delta = -qty
	}
	old := p.NetQuantity
	if old == 0 || (old > 0 && delta > 0) || (old < 0 && delta < 0) {
		newQty := old + delta
		denominator := absFloat64(float64(old)) + absFloat64(float64(delta))
		if denominator > 0 {
			p.AveragePrice = (absFloat64(float64(old))*p.AveragePrice + absFloat64(float64(delta))*price) / denominator
		} else {
			p.AveragePrice = price
		}
		p.NetQuantity = newQty
		return 0
	}

	closeQty := minInt64(absInt64(old), absInt64(delta))
	realized := (price - p.AveragePrice) * float64(closeQty)
	if old < 0 {
		realized = (p.AveragePrice - price) * float64(closeQty)
	}
	newQty := old + delta
	switch {
	case newQty == 0:
		p.AveragePrice = 0
	case (old > 0 && newQty < 0) || (old < 0 && newQty > 0):
		p.AveragePrice = price
	}
	p.NetQuantity = newQty
	p.RealizedPnL += realized
	return realized
}

func normalizeSide(side string) string {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "B", "BUY":
		return "BUY"
	case "S", "SELL":
		return "SELL"
	default:
		return ""
	}
}

func normalizeOrderType(orderType string) string {
	switch strings.ToUpper(strings.TrimSpace(orderType)) {
	case "MKT", "MARKET", "M":
		return "MKT"
	case "L", "LIMIT":
		return "L"
	case "SL", "SL-L", "SLL", "STOP-LIMIT":
		return "SL"
	case "SL-M", "SLM", "STOP-MARKET":
		return "SL-M"
	default:
		return ""
	}
}

func normalizeValidity(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "" {
		return "DAY"
	}
	return v
}

func isStopOrder(orderType string) bool {
	return orderType == "SL" || orderType == "SL-M"
}

func isEntryTag(tag string) bool {
	tag = strings.TrimSpace(tag)
	return !strings.HasPrefix(tag, "exit_") && !strings.HasPrefix(tag, "xstop_")
}

func isExpired(o *Order, now time.Time) bool {
	if o == nil || o.CreatedAt.IsZero() {
		return false
	}
	if normalizeValidity(o.Validity) != "DAY" {
		return false
	}
	ist := time.FixedZone("IST", 5*60*60+30*60)
	return o.CreatedAt.In(ist).Format("2006-01-02") != now.In(ist).Format("2006-01-02")
}

func marketFillPrice(side string, q Quote, slippage float64) float64 {
	price := q.LTP
	if side == "BUY" && q.Ask > 0 {
		price = q.Ask
	}
	if side == "SELL" && q.Bid > 0 {
		price = q.Bid
	}
	if side == "BUY" {
		price += slippage
	} else {
		price -= slippage
	}
	if price <= 0 {
		price = q.LTP
	}
	return price
}

func bracketIDFromTag(tag string) string {
	tag = strings.TrimSpace(tag)
	for _, prefix := range []string{"exit_", "xstop_"} {
		if strings.HasPrefix(tag, prefix) {
			return strings.TrimPrefix(tag, prefix)
		}
	}
	return tag
}

func sortOrdersNewestFirst(orders []Order) {
	sort.Slice(orders, func(i, j int) bool {
		return orders[i].CreatedAt.After(orders[j].CreatedAt)
	})
}

func absFloat64(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func mathMax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
