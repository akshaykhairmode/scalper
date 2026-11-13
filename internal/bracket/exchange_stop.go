package bracket

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/kotakapi"
)

// ExchangeStopConfig controls how the optional exchange-side catastrophe stop
// is placed and cancelled. It is a default for new orders; a bracket's own
// ExchangeStopMode always wins.
type ExchangeStopConfig struct {
	Mode               string
	DistanceMode       string
	DistanceValue      float64
	SLLOffsetTicks     int
	PlaceOnPartialFill bool
	CancelRetries      int
	CancelRetryDelay   time.Duration
	OnCancelFailure    string
	ReconcileOnStartup bool
}

// DefaultExchangeStopConfig returns the safe defaults: the feature is off.
func DefaultExchangeStopConfig() ExchangeStopConfig {
	return ExchangeStopConfig{
		Mode:               ExchangeStopOff,
		DistanceMode:       "multiplier",
		DistanceValue:      2.5,
		SLLOffsetTicks:     2,
		PlaceOnPartialFill: true,
		CancelRetries:      3,
		CancelRetryDelay:   100 * time.Millisecond,
		OnCancelFailure:    "alert",
		ReconcileOnStartup: true,
	}
}

// exchangeStopTickSize is the NSE/BSE options tick used when a distance is
// expressed in ticks. Futures/commodities with a different tick are still
// safe: the value only widens the catastrophe trigger.
const exchangeStopTickSize = 0.05

// NormalizeExchangeStopMode maps user and config spellings onto the canonical
// values. An empty result means "inherit the global default".
func NormalizeExchangeStopMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "default", "inherit":
		return ""
	case "off", "none", "disabled", "no":
		return ExchangeStopOff
	case "sl-m", "slm", "sl_m", "stop-market", "stop_market":
		return ExchangeStopSLM
	case "sl", "sl-l", "sll", "sl_l", "stop-limit", "stop_limit":
		return ExchangeStopSLL
	default:
		return strings.ToUpper(strings.TrimSpace(v))
	}
}

func validExchangeStopMode(mode string) bool {
	switch mode {
	case "", ExchangeStopOff, ExchangeStopSLM, ExchangeStopSLL:
		return true
	default:
		return false
	}
}

// SetExchangeStopConfig installs the global exchange-stop defaults.
func (e *Engine) SetExchangeStopConfig(cfg ExchangeStopConfig) {
	e.mu.Lock()
	e.exchangeStopCfg = cfg
	e.mu.Unlock()
}

func (e *Engine) exchangeStopConfig() ExchangeStopConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.exchangeStopCfg
}

// resolveExchangeStopModeLocked returns the effective mode for a bracket.
// The caller must hold e.mu (read or write).
func (e *Engine) resolveExchangeStopModeLocked(b *Bracket) string {
	if b == nil {
		return ""
	}
	if mode := NormalizeExchangeStopMode(b.ExchangeStopMode); mode != "" {
		return mode
	}
	return NormalizeExchangeStopMode(e.exchangeStopCfg.Mode)
}

func (e *Engine) exchangeStopDistanceLocked(b *Bracket) float64 {
	cfg := e.exchangeStopCfg
	if b.EntryPrice <= 0 {
		return 0
	}
	normalPts := 0.0
	if b.StopLossPrice > 0 {
		if b.TransactionType == "BUY" {
			normalPts = b.EntryPrice - b.StopLossPrice
		} else {
			normalPts = b.StopLossPrice - b.EntryPrice
		}
	}
	var distance float64
	switch strings.ToLower(strings.TrimSpace(cfg.DistanceMode)) {
	case "percent":
		distance = b.EntryPrice * cfg.DistanceValue / 100
	case "points":
		distance = cfg.DistanceValue
	case "ticks":
		distance = cfg.DistanceValue * exchangeStopTickSize
	default: // multiplier
		if normalPts > 0 {
			distance = normalPts * cfg.DistanceValue
		} else {
			// Without a server-side stop there is no distance to multiply,
			// so treat the value as a percentage of the entry price.
			distance = b.EntryPrice * cfg.DistanceValue / 100
		}
	}
	// The exchange backstop must sit farther from the entry than the server
	// stop; otherwise it would fire first and defeat the design.
	if normalPts > 0 && distance <= normalPts {
		distance = normalPts + exchangeStopTickSize
	}
	return distance
}

func (e *Engine) exchangeStopTriggerLocked(b *Bracket) float64 {
	if b.ExchangeStopPrice > 0 {
		return b.ExchangeStopPrice
	}
	distance := e.exchangeStopDistanceLocked(b)
	if distance <= 0 {
		return 0
	}
	if b.TransactionType == "BUY" {
		return b.EntryPrice - distance
	}
	return b.EntryPrice + distance
}

// EnsureExchangeStop places the exchange backstop for a bracket if the resolved
// mode calls for one and none is already working. It is safe to call repeatedly.
func (e *Engine) EnsureExchangeStop(ctx context.Context, bracketID string) {
	e.ensureExchangeStop(ctx, bracketID)
}

func (e *Engine) ensureExchangeStop(ctx context.Context, bracketID string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if e.exec == nil {
		return
	}

	e.mu.Lock()
	b, ok := e.brackets[bracketID]
	if !ok {
		e.mu.Unlock()
		return
	}
	mode := e.resolveExchangeStopModeLocked(b)
	if mode == "" || mode == ExchangeStopOff {
		e.mu.Unlock()
		return
	}
	if b.Status != StatusInPosition {
		e.mu.Unlock()
		return
	}
	if b.ExchangeStopStatus == ExchangeStopPlacing ||
		b.ExchangeStopStatus == ExchangeStopWorking ||
		b.ExchangeStopStatus == ExchangeStopCancelling {
		e.mu.Unlock()
		return
	}
	if b.EntryPrice <= 0 || b.Quantity <= 0 {
		e.mu.Unlock()
		return
	}
	// Kotak treats a second live sell as a short. If another bracket on the
	// same contract already has a working backstop, leave this one server-only.
	for _, other := range e.brackets {
		if other.ID == b.ID || !other.ExchangeStopIsWorking() {
			continue
		}
		sameInstrument := b.InstrumentKey != "" && other.InstrumentKey == b.InstrumentKey
		sameContract := strings.EqualFold(other.ExchangeSegment, b.ExchangeSegment) &&
			strings.EqualFold(other.TradingSymbol, b.TradingSymbol)
		if sameInstrument || sameContract {
			e.mu.Unlock()
			e.log.Warn("exchange backstop skipped: another live backstop already protects this contract",
				"bracket_id", b.ID, "other_bracket_id", other.ID)
			return
		}
	}
	trigger := e.exchangeStopTriggerLocked(b)
	if trigger <= 0 {
		e.mu.Unlock()
		return
	}

	exitSide := "SELL"
	if b.TransactionType == "SELL" {
		exitSide = "BUY"
	}
	orderType := ExchangeStopSLM
	price := 0.0
	if mode == ExchangeStopSLL {
		orderType = "SL"
		offset := float64(e.exchangeStopCfg.SLLOffsetTicks) * exchangeStopTickSize
		if exitSide == "SELL" {
			price = trigger - offset
		} else {
			price = trigger + offset
		}
		if price <= 0 {
			price = exchangeStopTickSize
		}
	}

	order := kotakapi.Order{
		Tag:             exchangeStopTag(b.ID),
		InstrumentKey:   b.InstrumentKey,
		ExchangeSegment: b.ExchangeSegment,
		TradingSymbol:   b.TradingSymbol,
		TransactionType: exitSide,
		Product:         b.Product,
		OrderType:       orderType,
		Quantity:        b.Quantity,
		Price:           price,
		TriggerPrice:    trigger,
		Validity:        "DAY",
	}

	b.ExchangeStopMode = mode
	b.ExchangeStopPrice = trigger
	b.ExchangeStopStatus = ExchangeStopPlacing
	b.UpdatedAt = time.Now()
	placing := *b
	retries := e.exchangeStopCfg.CancelRetries
	e.mu.Unlock()

	// Persist PLACING first: if the process dies during the broker call, a
	// restart can look up the tag instead of blindly placing a second sell.
	e.persistBracket(ctx, placing)

	var orderID string
	var placeErr error
	if retries < 1 {
		retries = 1
	}
	if retries > 3 {
		retries = 3
	}
placeLoop:
	for attempt := 0; attempt < retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				placeErr = ctx.Err()
				break placeLoop
			case <-time.After(e.exchangeStopConfig().CancelRetryDelay):
			}
		}
		orderID, placeErr = e.exec.Place(ctx, order)
		if placeErr == nil && orderID != "" {
			break
		}
	}
	if placeErr == nil && orderID == "" {
		placeErr = errors.New("executor returned an empty order id")
	}

	e.mu.Lock()
	curr, ok := e.brackets[bracketID]
	if !ok {
		e.mu.Unlock()
		return
	}
	if placeErr != nil || orderID == "" {
		curr.ExchangeStopStatus = ExchangeStopFailed
		if curr.ExchangeStopPrice <= 0 {
			curr.ExchangeStopPrice = trigger
		}
		curr.UpdatedAt = time.Now()
		notified := *curr
		e.mu.Unlock()
		e.log.Error("CRITICAL: failed to place exchange backstop stop-loss",
			"bracket_id", bracketID, "mode", orderType, "trigger", trigger, "error", placeErr)
		e.persistBracket(ctx, notified)
		e.notify(notified)
		return
	}
	curr.ExchangeStopOrderID = orderID
	curr.ExchangeStopStatus = ExchangeStopWorking
	curr.ExchangeStopPrice = trigger
	curr.UpdatedAt = time.Now()
	notified := *curr
	e.mu.Unlock()

	e.log.Info("exchange backstop stop-loss placed",
		"bracket_id", bracketID, "order_id", orderID, "mode", orderType,
		"trigger", trigger, "quantity", order.Quantity)
	e.persistBracket(ctx, notified)
	e.notify(notified)
}

// CancelExchangeStopsForInstrument cancels working exchange stops for opposite
// side brackets on the same contract. It is used before a manual exit placed
// through the generic order endpoint.
func (e *Engine) CancelExchangeStopsForInstrument(ctx context.Context, segment, symbol, orderSide string) {
	if e == nil {
		return
	}
	segment = strings.ToUpper(strings.TrimSpace(segment))
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	orderSide = strings.ToUpper(strings.TrimSpace(orderSide))
	switch orderSide {
	case "B":
		orderSide = "BUY"
	case "S":
		orderSide = "SELL"
	}
	if segment == "" || symbol == "" {
		return
	}
	var ids []string
	e.mu.RLock()
	for id, b := range e.brackets {
		if b.Status != StatusInPosition || !b.ExchangeStopIsWorking() {
			continue
		}
		if strings.ToUpper(b.ExchangeSegment) != segment || strings.ToUpper(b.TradingSymbol) != symbol {
			continue
		}
		// Opposite of the bracket entry means this order is a manual exit.
		if (b.TransactionType == "BUY" && orderSide == "SELL") ||
			(b.TransactionType == "SELL" && orderSide == "BUY") {
			ids = append(ids, id)
		}
	}
	e.mu.RUnlock()

	for _, id := range ids {
		_ = e.cancelExchangeStopForBracket(ctx, id)
	}
}

// CancelAllExchangeStops cancels every working exchange stop. Used before a
// square-off-all so no orphan sell remains live at the broker.
func (e *Engine) CancelAllExchangeStops(ctx context.Context) {
	if e == nil {
		return
	}
	var ids []string
	e.mu.RLock()
	for id, b := range e.brackets {
		if b.ExchangeStopOrderID != "" && b.ExchangeStopIsWorking() {
			ids = append(ids, id)
		}
	}
	e.mu.RUnlock()
	for _, id := range ids {
		_ = e.cancelExchangeStopForBracket(ctx, id)
	}
}

func (e *Engine) cancelExchangeStopForBracket(ctx context.Context, bracketID string) error {
	e.mu.RLock()
	b, ok := e.brackets[bracketID]
	if !ok {
		e.mu.RUnlock()
		return errors.New("bracket not found")
	}
	orderID := b.ExchangeStopOrderID
	e.mu.RUnlock()
	if orderID == "" {
		return nil
	}
	filled, price, err := e.cancelExchangeStopOrder(ctx, orderID)
	if filled {
		e.markExchangeStopFill(ctx, bracketID, price)
		return nil
	}
	if err != nil {
		e.log.Error("CRITICAL: failed to cancel exchange backstop",
			"bracket_id", bracketID, "order_id", orderID, "error", err)
		return err
	}
	e.setExchangeStopStatus(ctx, bracketID, ExchangeStopCancelled)
	return nil
}

func exchangeStopTag(bracketID string) string {
	return "xstop_" + bracketID
}

func exchangeStopTerminalStatus(status string) string {
	s := strings.ToUpper(strings.TrimSpace(status))
	if s == "" {
		return ""
	}
	if strings.Contains(s, "REJ") {
		return ExchangeStopRejected
	}
	if isCancelledStatus(s) {
		return ExchangeStopCancelled
	}
	return ""
}

// cancelExchangeStopOrder cancels the order and waits for confirmation when a
// lookup is available. It returns filled=true if the stop filled before the
// cancellation was confirmed, so the caller must not place a second sell.
func (e *Engine) cancelExchangeStopOrder(ctx context.Context, orderID string) (filled bool, fillPrice float64, err error) {
	if orderID == "" {
		return false, 0, nil
	}
	if e.exec == nil {
		return false, 0, errors.New("order cancellation is not available")
	}
	cfg := e.exchangeStopConfig()
	attempts := cfg.CancelRetries
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 && cfg.CancelRetryDelay > 0 {
			select {
			case <-ctx.Done():
				return false, 0, ctx.Err()
			case <-time.After(cfg.CancelRetryDelay):
			}
		}
		cancelErr := e.exec.Cancel(ctx, orderID)
		lastErr = cancelErr

		lookupMissing := false
		for poll := 0; poll < 5; poll++ {
			_, status, price, lookupErr := e.exec.Lookup(ctx, orderID, "")
			if errors.Is(lookupErr, ErrExecutorUnavailable) || errors.Is(lookupErr, ErrOrderNotFound) {
				lookupMissing = true
				break
			}
			if lookupErr == nil {
				if isFilledStatus(status) {
					return true, price, nil
				}
				if exchangeStopTerminalStatus(status) != "" {
					return false, 0, nil
				}
			}
			select {
			case <-ctx.Done():
				return false, 0, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
		if lookupMissing && cancelErr == nil {
			return false, 0, nil
		}
	}
	if lastErr != nil {
		return false, 0, lastErr
	}
	return false, 0, errors.New("exchange stop cancellation was not confirmed")
}

func (e *Engine) markExchangeStopFill(ctx context.Context, bracketID string, price float64) {
	e.mu.Lock()
	b, ok := e.brackets[bracketID]
	if !ok {
		e.mu.Unlock()
		return
	}
	e.applyExchangeStopFillLocked(b, price)
	notified := *b
	e.mu.Unlock()

	e.log.Warn("exchange backstop stop-loss filled", "bracket_id", bracketID, "price", price)
	e.persistBracket(ctx, notified)
	e.notify(notified)
}

func (e *Engine) applyExchangeStopFillLocked(b *Bracket, price float64) {
	b.ExchangeStopStatus = ExchangeStopFilled
	b.UpdatedAt = time.Now()
	if price > 0 {
		b.ExitPrice = price
	}
	// The exchange fill is the real exit. If the server had only marked a
	// target/stop hit but not yet placed an exit order, reflect the fill.
	canAdoptFill := b.Status == StatusInPosition || b.Status == StatusExitFailed ||
		((b.Status == StatusTargetHit || b.Status == StatusSLHit) && b.ExitOrderID == "")
	if canAdoptFill {
		e.unindexActiveInPosition(b)
		b.Status = StatusSLHit
		if b.ExitPrice > 0 {
			if b.TransactionType == "BUY" {
				b.PnL = (b.ExitPrice - b.EntryPrice) * float64(b.Quantity)
			} else {
				b.PnL = (b.EntryPrice - b.ExitPrice) * float64(b.Quantity)
			}
		}
	}
}

// failTriggerExit parks a bracket in EXIT_FAILED after a trigger could not be
// safely executed, re-indexing it so a later tick or the user can retry.
func (e *Engine) failTriggerExit(ctx context.Context, bracketID string) {
	e.mu.Lock()
	b, ok := e.brackets[bracketID]
	if !ok {
		e.mu.Unlock()
		return
	}
	b.Status = StatusExitFailed
	b.UpdatedAt = time.Now()
	e.indexActiveInPosition(b)
	notified := *b
	e.mu.Unlock()
	e.persistBracket(ctx, notified)
	e.notify(notified)
}

func (e *Engine) setExchangeStopStatus(ctx context.Context, bracketID, status string) {
	e.mu.Lock()
	b, ok := e.brackets[bracketID]
	if !ok {
		e.mu.Unlock()
		return
	}
	if b.ExchangeStopStatus == status {
		e.mu.Unlock()
		return
	}
	b.ExchangeStopStatus = status
	b.UpdatedAt = time.Now()
	notified := *b
	e.mu.Unlock()
	e.persistBracket(ctx, notified)
	e.notify(notified)
}

func (e *Engine) persistBracket(ctx context.Context, b Bracket) {
	if e.store == nil {
		return
	}
	if err := e.store.UpdateBracket(ctx, b); err != nil {
		e.log.Error("failed to persist bracket", "id", b.ID, "error", err)
	}
}

// Reconcile loads the broker truth for exchange stops after a restart.
func (e *Engine) Reconcile(ctx context.Context) {
	if e == nil || !e.exchangeStopConfig().ReconcileOnStartup {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	e.mu.RLock()
	ids := make([]string, 0, len(e.brackets))
	for id, b := range e.brackets {
		if b.Status == StatusInPosition {
			ids = append(ids, id)
		}
	}
	e.mu.RUnlock()
	for _, id := range ids {
		e.reconcileExchangeStop(ctx, id)
	}
}

func (e *Engine) reconcileExchangeStop(ctx context.Context, bracketID string) {
	e.mu.RLock()
	b, ok := e.brackets[bracketID]
	if !ok || b.Status != StatusInPosition {
		e.mu.RUnlock()
		return
	}
	mode := e.resolveExchangeStopModeLocked(b)
	orderID := b.ExchangeStopOrderID
	status := b.ExchangeStopStatus
	bracketMode := b.Mode
	e.mu.RUnlock()

	if mode == "" || mode == ExchangeStopOff {
		if orderID != "" {
			_ = e.cancelExchangeStopForBracket(ctx, bracketID)
		}
		return
	}

	if orderID != "" {
		if e.exec == nil {
			return
		}
		_, brokerStatus, price, err := e.exec.Lookup(ctx, orderID, "")
		if err != nil {
			if (errors.Is(err, ErrExecutorUnavailable) || errors.Is(err, ErrOrderNotFound)) && bracketMode == "paper" {
				// Paper orders are local; an unknown ID means the order was
				// never persisted, so it is safe to retry.
				e.setExchangeStopStatus(ctx, bracketID, ExchangeStopFailed)
				if e.allowExchangeStopRetry(bracketID) {
					e.ensureExchangeStop(ctx, bracketID)
				}
				return
			}
			// Unknown state: never place a second sell.
			e.log.Error("CRITICAL: could not verify exchange backstop on startup; verify manually",
				"bracket_id", bracketID, "order_id", orderID, "error", err)
			return
		}
		if isFilledStatus(brokerStatus) {
			e.markExchangeStopFill(ctx, bracketID, price)
			return
		}
		if terminal := exchangeStopTerminalStatus(brokerStatus); terminal != "" {
			e.setExchangeStopStatus(ctx, bracketID, terminal)
			if e.allowExchangeStopRetry(bracketID) {
				e.ensureExchangeStop(ctx, bracketID)
			}
			return
		}
		e.setExchangeStopStatus(ctx, bracketID, ExchangeStopWorking)
		return
	}

	if status == ExchangeStopPlacing {
		if e.exec == nil {
			return
		}
		foundID, brokerStatus, price, err := e.exec.Lookup(ctx, "", exchangeStopTag(bracketID))
		if err != nil || foundID == "" {
			if bracketMode == "paper" && (err == nil || errors.Is(err, ErrExecutorUnavailable) || errors.Is(err, ErrOrderNotFound)) {
				e.setExchangeStopStatus(ctx, bracketID, ExchangeStopFailed)
				if e.allowExchangeStopRetry(bracketID) {
					e.ensureExchangeStop(ctx, bracketID)
				}
				return
			}
			// The process may have died mid-placement. Do not risk a second
			// live sell; surface it for manual verification instead.
			e.log.Error("CRITICAL: exchange backstop was mid-placement at shutdown; verify manually",
				"bracket_id", bracketID, "tag", exchangeStopTag(bracketID), "error", err)
			e.setExchangeStopStatus(ctx, bracketID, ExchangeStopFailed)
			return
		}
		if isFilledStatus(brokerStatus) {
			e.markExchangeStopFill(ctx, bracketID, price)
			return
		}
		if terminal := exchangeStopTerminalStatus(brokerStatus); terminal != "" {
			e.setExchangeStopStatus(ctx, bracketID, terminal)
			return
		}
		e.mu.Lock()
		if curr, ok := e.brackets[bracketID]; ok {
			curr.ExchangeStopOrderID = foundID
			curr.ExchangeStopStatus = ExchangeStopWorking
			curr.UpdatedAt = time.Now()
			notified := *curr
			e.mu.Unlock()
			e.persistBracket(ctx, notified)
			e.notify(notified)
		} else {
			e.mu.Unlock()
		}
		return
	}

	if e.allowExchangeStopRetry(bracketID) {
		e.ensureExchangeStop(ctx, bracketID)
	}
}

// allowExchangeStopRetry permits at most one automatic retry per process, so a
// rejected broker order does not become a placement loop.
func (e *Engine) allowExchangeStopRetry(bracketID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.exchangeStopRetries == nil {
		e.exchangeStopRetries = make(map[string]int)
	}
	if e.exchangeStopRetries[bracketID] >= 1 {
		return false
	}
	e.exchangeStopRetries[bracketID]++
	return true
}
