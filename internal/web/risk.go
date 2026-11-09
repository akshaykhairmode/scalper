package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
	"github.com/akshaykhairmode/scalper/internal/logging"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

// riskState keeps short-lived order counters used by the self-imposed rate
// caps. It is intentionally in-memory: a restart clears the counters.
type riskState struct {
	mu     sync.Mutex
	orders map[string][]time.Time
	exits  map[string][]time.Time
}

func newRiskState() *riskState {
	return &riskState{
		orders: make(map[string][]time.Time),
		exits:  make(map[string][]time.Time),
	}
}

type brokerPosition struct {
	InstrumentKey   string
	ExchangeSegment string
	TradingSymbol   string
	Underlying      string
	Quantity        int64
	LotSize         int64
	PnL             float64
}

func (s *Server) checkOrderRisk(ctx context.Context, session kotakapi.Session, o kotakapi.Order) error {
	trading := s.configSnapshot().Trading
	cfg := trading.Risk

	option, hasOption := s.findOrderOption(o)
	lotSize := int64(1)
	underlying := strings.TrimSpace(o.TradingSymbol)
	if hasOption {
		if option.LotSize > 0 {
			lotSize = option.LotSize
		}
		if strings.TrimSpace(option.Underlying) != "" {
			underlying = option.Underlying
		}
	}
	lots := quantityLots(o.Quantity, lotSize)

	var positions []brokerPosition
	reduces := false
	raw, err := s.getBook(ctx, bookPositions, session)
	hardCapsNeedPositions := cfg.MaxOpenLotsPerIndex > 0 || cfg.MaxDailyLossINR > 0
	if err != nil {
		if hardCapsNeedPositions {
			return fmt.Errorf("order blocked: position book unavailable for risk check: %w", err)
		}
		s.log.Warn("proceeding with risk check with unavailable position book",
			"event", "position_check_degraded", "error", err)
	} else {
		positions, err = parseBrokerPositions(raw)
		if err != nil {
			if hardCapsNeedPositions {
				return fmt.Errorf("order blocked: cannot parse position book for risk check: %w", err)
			}
			s.log.Warn("proceeding with risk check with unparseable position book",
				"event", "position_check_degraded", "error", err)
		} else {
			s.enrichPositionMetadata(positions)
			reduces = orderReducesPosition(o, option, hasOption, positions)
		}
	}

	if !reduces && cfg.MaxLotsPerOrder > 0 && lots > int64(cfg.MaxLotsPerOrder) {
		return fmt.Errorf("order blocked: %d lots exceeds max_lots_per_order=%d", lots, cfg.MaxLotsPerOrder)
	}

	if !reduces {
		if err := s.checkMarketDataFreshness(cfg.BlockOnDataStaleMS); err != nil {
			return err
		}
	}

	price, priceErr := s.orderPrice(ctx, o)
	if priceErr != nil && ((cfg.MaxOrderValueINR > 0 && !reduces) || (trading.Margin.CheckBeforeOrder && !reduces)) {
		return fmt.Errorf("order blocked: live price unavailable for risk check: %w", priceErr)
	}
	orderValue := price * float64(o.Quantity)
	if cfg.MaxOrderValueINR > 0 && !reduces {
		if price <= 0 {
			return fmt.Errorf("order blocked: max_order_value_inr is set but no order price is available")
		}
		if orderValue > float64(cfg.MaxOrderValueINR) {
			return fmt.Errorf("order blocked: order value ₹%.2f exceeds max_order_value_inr=%d", orderValue, cfg.MaxOrderValueINR)
		}
	}

	if cfg.MaxOpenLotsPerIndex > 0 && !reduces {
		exposure := exposureAfterOrder(positions, o, option, hasOption, underlying)
		if exposure > int64(cfg.MaxOpenLotsPerIndex) {
			return fmt.Errorf("order blocked: %s open exposure would be %d lots (max_open_lots_per_index=%d)",
				underlying, exposure, cfg.MaxOpenLotsPerIndex)
		}
	}

	if cfg.MaxDailyLossINR > 0 && !reduces {
		loss := -totalPositionPnL(positions)
		if loss >= float64(cfg.MaxDailyLossINR) {
			return fmt.Errorf("order blocked: daily loss ₹%.2f reached max_daily_loss_inr=%d", loss, cfg.MaxDailyLossINR)
		}
	}

	if trading.Margin.CheckBeforeOrder && !reduces {
		if err := s.checkOrderMargin(ctx, session, orderValue, trading.Margin.FailClosed); err != nil {
			return err
		}
	}

	if s.risk != nil {
		if err := s.risk.reserve(session.SID, strings.ToUpper(o.TransactionType), cfg, time.Now(), reduces); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) writeRiskError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Warn("live order blocked by risk control", "event", "order_risk_blocked",
		"path", r.URL.Path, "error", err, "request_id", logging.RequestID(r.Context()))
	_ = writeJSONError(w, http.StatusConflict, err.Error(), logging.RequestID(r.Context()))
}

func (s *Server) findOrderOption(o kotakapi.Order) (scrip.Option, bool) {
	if s.market == nil {
		return scrip.Option{}, false
	}
	return s.market.FindOption(o.ExchangeSegment, o.TradingSymbol)
}

func (s *Server) orderPrice(ctx context.Context, o kotakapi.Order) (float64, error) {
	if o.Price > 0 {
		return o.Price, nil
	}
	option, ok := s.findOrderOption(o)
	if !ok {
		return 0, fmt.Errorf("contract %s/%s is not in the Scrip Master", o.ExchangeSegment, o.TradingSymbol)
	}
	if s.market == nil {
		return 0, fmt.Errorf("market data is not configured")
	}
	quote, err := s.market.Quote(ctx, s.marketBaseURL(), option.InstrumentKey)
	if err != nil {
		return 0, err
	}
	if quote.LTP <= 0 {
		return 0, fmt.Errorf("contract has no live quote")
	}
	return quote.LTP, nil
}

func (s *Server) checkMarketDataFreshness(staleMS int) error {
	if staleMS <= 0 || s.hsm == nil {
		return nil
	}
	if s.configSnapshot().Market.DataMode != "websocket" {
		return nil
	}
	status := s.hsm.Status()
	if status.Status != "connected" {
		return fmt.Errorf("order blocked: market feed is %s", status.Status)
	}
	if status.LastEvent.IsZero() {
		return fmt.Errorf("order blocked: market feed has not delivered data yet")
	}
	if age := time.Since(status.LastEvent); age > time.Duration(staleMS)*time.Millisecond {
		return fmt.Errorf("order blocked: market feed is stale by %s (block_on_data_stale_ms=%d)", age.Round(time.Millisecond), staleMS)
	}
	return nil
}

func (s *Server) checkOrderMargin(ctx context.Context, session kotakapi.Session, orderValue float64, failClosed bool) error {
	raw, err := s.getBook(ctx, bookLimits, session)
	if err != nil {
		if failClosed {
			return fmt.Errorf("order blocked: margin unavailable and fail_closed is enabled: %w", err)
		}
		s.log.Warn("allow order with unavailable margin (fail_closed=false)", "event", "margin_check_degraded", "error", err)
		return nil
	}
	available, ok := parseAvailableMargin(raw)
	if !ok {
		if failClosed {
			return fmt.Errorf("order blocked: margin response could not be parsed and fail_closed is enabled")
		}
		s.log.Warn("allow order with unparseable margin (fail_closed=false)", "event", "margin_check_degraded")
		return nil
	}
	if orderValue > available {
		return fmt.Errorf("order blocked: required margin ₹%.2f exceeds available ₹%.2f", orderValue, available)
	}
	return nil
}

func (r *riskState) reserve(sessionID, side string, cfg config.Risk, now time.Time, isExitOpt ...bool) error {
	if cfg.MaxOrdersPerMinute <= 0 && cfg.MaxExitOrdersPerSecond <= 0 {
		return nil
	}
	key := sessionID
	if key == "" {
		key = "default"
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	orders := pruneBefore(r.orders[key], now.Add(-time.Minute))
	r.orders[key] = orders
	exits := pruneBefore(r.exits[key], now.Add(-time.Second))
	r.exits[key] = exits

	isExit := side == "S" || side == "SELL"
	if len(isExitOpt) > 0 {
		isExit = isExitOpt[0]
	}
	if !isExit && cfg.MaxOrdersPerMinute > 0 && len(orders) >= cfg.MaxOrdersPerMinute {
		return fmt.Errorf("order blocked: max_orders_per_minute=%d reached", cfg.MaxOrdersPerMinute)
	}
	if isExit && cfg.MaxExitOrdersPerSecond > 0 && len(exits) >= cfg.MaxExitOrdersPerSecond {
		return fmt.Errorf("order blocked: max_exit_orders_per_second=%d reached", cfg.MaxExitOrdersPerSecond)
	}

	r.orders[key] = append(orders, now)
	if isExit {
		r.exits[key] = append(exits, now)
	}
	return nil
}

func (r *riskState) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.orders = make(map[string][]time.Time)
	r.exits = make(map[string][]time.Time)
}

func pruneBefore(values []time.Time, cutoff time.Time) []time.Time {
	kept := values[:0]
	for _, value := range values {
		if value.After(cutoff) {
			kept = append(kept, value)
		}
	}
	return kept
}

func quantityLots(quantity, lotSize int64) int64 {
	if quantity <= 0 {
		return 0
	}
	if lotSize <= 1 {
		return quantity
	}
	return (quantity + lotSize - 1) / lotSize
}

func (s *Server) enrichPositionMetadata(positions []brokerPosition) {
	if s.market == nil {
		return
	}
	for i := range positions {
		var option scrip.Option
		var ok bool
		if positions[i].InstrumentKey != "" {
			option, ok = s.market.Option(positions[i].InstrumentKey)
		} else if positions[i].ExchangeSegment != "" && positions[i].TradingSymbol != "" {
			option, ok = s.market.FindOption(positions[i].ExchangeSegment, positions[i].TradingSymbol)
		}
		if !ok {
			continue
		}
		if positions[i].InstrumentKey == "" {
			positions[i].InstrumentKey = option.InstrumentKey
		}
		if positions[i].Underlying == "" {
			positions[i].Underlying = option.Underlying
		}
		if positions[i].LotSize <= 0 {
			positions[i].LotSize = option.LotSize
		}
	}
}

func orderReducesPosition(o kotakapi.Order, option scrip.Option, hasOption bool, positions []brokerPosition) bool {
	current := positionQuantityForOrder(positions, o, option, hasOption)
	if current == 0 {
		return false
	}
	delta := o.Quantity
	if !isBuy(o.TransactionType) {
		delta = -delta
	}
	next := current + delta
	return absInt64(next) < absInt64(current)
}

func exposureAfterOrder(positions []brokerPosition, o kotakapi.Order, option scrip.Option, hasOption bool, underlying string) int64 {
	var total int64
	var currentQty int64
	var currentLots int64
	orderLotSize := int64(1)
	if hasOption && option.LotSize > 0 {
		orderLotSize = option.LotSize
	}
	for _, p := range positions {
		lots := quantityLots(absInt64(p.Quantity), p.LotSize)
		if sameUnderlying(p.Underlying, underlying) {
			total += lots
		}
		if samePositionInstrument(p, o, option, hasOption) {
			currentQty += p.Quantity
			currentLots += lots
		}
	}
	total -= currentLots
	delta := o.Quantity
	if !isBuy(o.TransactionType) {
		delta = -delta
	}
	total += quantityLots(absInt64(currentQty+delta), orderLotSize)
	return total
}

func positionQuantityForOrder(positions []brokerPosition, o kotakapi.Order, option scrip.Option, hasOption bool) int64 {
	var current int64
	for _, p := range positions {
		if samePositionInstrument(p, o, option, hasOption) {
			current += p.Quantity
		}
	}
	return current
}

func samePositionInstrument(p brokerPosition, o kotakapi.Order, option scrip.Option, hasOption bool) bool {
	if hasOption && p.InstrumentKey != "" && p.InstrumentKey == option.InstrumentKey {
		return true
	}
	if p.ExchangeSegment != "" && p.TradingSymbol != "" {
		return strings.EqualFold(p.ExchangeSegment, o.ExchangeSegment) &&
			strings.EqualFold(p.TradingSymbol, o.TradingSymbol)
	}
	return false
}

func sameUnderlying(a, b string) bool {
	return strings.TrimSpace(a) != "" && strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func totalPositionPnL(positions []brokerPosition) float64 {
	var total float64
	for _, p := range positions {
		total += p.PnL
	}
	return total
}

func parseBrokerPositions(raw json.RawMessage) ([]brokerPosition, error) {
	rows, err := decodeBookRows(raw)
	if err != nil {
		return nil, err
	}
	out := make([]brokerPosition, 0, len(rows))
	for _, row := range rows {
		qty, hasQty := numberField(row, "quantity", "net_qty", "netQty", "qty", "netqty", "net_quantity")
		if !hasQty || qty == 0 {
			bought, _ := numberField(row, "flBuyQty", "fl_buy_qty")
			sold, _ := numberField(row, "flSellQty", "fl_sell_qty")
			carriedBought, _ := numberField(row, "cfBuyQty", "cf_buy_qty")
			carriedSold, _ := numberField(row, "cfSellQty", "cf_sell_qty")
			if net := (bought + carriedBought) - (sold + carriedSold); net != 0 {
				qty = net
			}
		}
		lot, _ := numberField(row, "lot_size", "lotSize", "lot")
		pnl, hasPnL := numberField(row, "pnl", "PnL", "profit_loss")
		if !hasPnL {
			realised, _ := numberField(row, "realised_pnl", "realized_pnl", "rPnl", "rpnl")
			unrealised, _ := numberField(row, "unrealised_pnl", "unrealized_pnl", "unRlzPnL", "unrealisedPnl")
			pnl = realised + unrealised
		}
		out = append(out, brokerPosition{
			InstrumentKey:   stringField(row, "instrument_key", "instrumentKey"),
			ExchangeSegment: stringField(row, "exchange_segment", "exchangeSegment", "exSeg"),
			TradingSymbol:   stringField(row, "trading_symbol", "tradingSymbol", "trdSym", "symbol"),
			Underlying:      stringField(row, "underlying", "underlying_symbol"),
			Quantity:        int64(qty),
			LotSize:         int64(lot),
			PnL:             pnl,
		})
	}
	return out, nil
}

func decodeBookRows(raw json.RawMessage) ([]map[string]json.RawMessage, error) {
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err == nil {
		return rows, nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, fmt.Errorf("book response has no data")
	}
	if err := json.Unmarshal(envelope.Data, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func parseAvailableMargin(raw json.RawMessage) (float64, bool) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return findMarginValue(value)
}

func findMarginValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"available_margin", "availableMargin", "available", "Net", "net"} {
			if v, ok := typed[key]; ok {
				if n, ok := numericAny(v); ok {
					return n, true
				}
			}
		}
		for _, v := range typed {
			if n, ok := findMarginValue(v); ok {
				return n, true
			}
		}
	case []any:
		for _, item := range typed {
			if n, ok := findMarginValue(item); ok {
				return n, true
			}
		}
	}
	return 0, false
}

func numericAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		n, err := typed.Float64()
		return n, err == nil
	case string:
		var n float64
		if _, err := fmt.Sscan(strings.TrimSpace(typed), &n); err == nil {
			return n, true
		}
	}
	return 0, false
}

func numberField(row map[string]json.RawMessage, keys ...string) (float64, bool) {
	for _, key := range keys {
		raw, ok := row[key]
		if !ok {
			continue
		}
		var n float64
		if json.Unmarshal(raw, &n) == nil {
			return n, true
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			var parsed float64
			if _, err := fmt.Sscan(strings.TrimSpace(s), &parsed); err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

func stringField(row map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := row[key]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		var n json.Number
		if json.Unmarshal(raw, &n) == nil {
			return n.String()
		}
	}
	return ""
}

func isBuy(side string) bool {
	side = strings.ToUpper(strings.TrimSpace(side))
	return side == "B" || side == "BUY"
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
