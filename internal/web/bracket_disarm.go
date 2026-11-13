package web

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/bracket"
)

// brokerOrderID extracts the broker order ID from a place-order response.
func brokerOrderID(raw json.RawMessage) string {
	var resp struct {
		NOrdNo  string `json:"nOrdNo"`
		OrderID string `json:"order_id"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ""
	}
	switch {
	case strings.TrimSpace(resp.NOrdNo) != "":
		return strings.TrimSpace(resp.NOrdNo)
	case strings.TrimSpace(resp.OrderID) != "":
		return strings.TrimSpace(resp.OrderID)
	default:
		return strings.TrimSpace(resp.ID)
	}
}

func normalizeOrderSide(side string) string {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "B", "BUY":
		return "BUY"
	case "S", "SELL":
		return "SELL"
	default:
		return ""
	}
}

// disarmLiveBracketsAfterExit waits for a manual exit order to fill and then
// disarms the live brackets it closed. Waiting for the fill avoids removing the
// server SL/TP when the exit is rejected.
func (s *Server) disarmLiveBracketsAfterExit(parent context.Context, orderID, segment, symbol, product, side string, qty int64) {
	if s.bracketEngine == nil || strings.TrimSpace(orderID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 20*time.Second)
	defer cancel()
	for attempt := 0; attempt < 40; attempt++ {
		select {
		case <-ctx.Done():
			s.log.Error("CRITICAL: manual exit confirmation timed out; reconciling position",
				"event", "manual_exit_timeout", "order_id", orderID, "segment", segment, "symbol", symbol)
			s.reconcileManualExitTimeout(segment, symbol, product)
			return
		case <-time.After(500 * time.Millisecond):
		}
		_, status, price, err := s.lookupBrokerOrder(ctx, orderID, "")
		if err != nil {
			continue
		}
		if bracket.IsFilledStatus(status) {
			s.disarmLiveBracketsForExit(ctx, segment, symbol, product, side, qty, price)
			return
		}
		if bracket.IsCancelledStatus(status) {
			// The exit was rejected: restore the exchange backstop that was
			// cancelled before the order.
			s.rearmLiveBackstopsForContract(ctx, segment, symbol, product)
			return
		}
	}
	s.log.Error("CRITICAL: manual exit was not confirmed; reconciling position",
		"event", "manual_exit_timeout", "order_id", orderID, "segment", segment, "symbol", symbol)
	s.reconcileManualExitTimeout(segment, symbol, product)
}

// reconcileManualExitTimeout checks the broker position after an unconfirmed
// manual exit. It re-arms the backstop only if the position is still open.
func (s *Server) reconcileManualExitTimeout(segment, symbol, product string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	open, err := s.livePositionOpen(ctx, segment, symbol, product)
	if err != nil {
		s.log.Error("CRITICAL: could not reconcile manual exit timeout",
			"segment", segment, "symbol", symbol, "error", err)
		return
	}
	if open {
		s.rearmLiveBackstopsForContract(ctx, segment, symbol, product)
		return
	}
	for _, msg := range s.disarmLiveBracketsForContract(ctx, segment, symbol, product) {
		s.log.Warn("failed to disarm bracket after manual exit timeout", "error", msg)
	}
}

// livePositionOpen reports whether the broker still shows a position for the
// contract. It forces a fresh positions read.
func (s *Server) livePositionOpen(ctx context.Context, segment, symbol, product string) (bool, error) {
	session, ok := s.brokerSessionValue()
	if !ok {
		return false, errors.New("broker session unavailable")
	}
	s.books.expire(bookPositions)
	raw, err := s.getBook(ctx, bookPositions, session)
	if err != nil {
		return false, err
	}
	rows, err := decodeBookRows(raw)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if !strings.EqualFold(strings.TrimSpace(stringField(row, "exchange_segment", "exchangeSegment", "exSeg")), strings.TrimSpace(segment)) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(stringField(row, "trading_symbol", "tradingSymbol", "trdSym", "symbol")), strings.TrimSpace(symbol)) {
			continue
		}
		if product != "" && !strings.EqualFold(strings.TrimSpace(stringField(row, "product", "prod", "prd")), strings.TrimSpace(product)) {
			continue
		}
		qty, hasQty := numberField(row, "quantity", "net_qty", "netQty", "qty", "netqty", "net_quantity")
		if !hasQty || qty == 0 {
			bought, _ := numberField(row, "flBuyQty", "fl_buy_qty")
			sold, _ := numberField(row, "flSellQty", "fl_sell_qty")
			carriedBought, _ := numberField(row, "cfBuyQty", "cf_buy_qty")
			carriedSold, _ := numberField(row, "cfSellQty", "cf_sell_qty")
			qty = (bought + carriedBought) - (sold + carriedSold)
		}
		if int64(qty) != 0 {
			return true, nil
		}
	}
	return false, nil
}

// disarmLiveBracketsForExit marks the brackets closed by a manual exit. It
// skips brackets larger than the exit quantity so a partial exit keeps its
// protection.
func (s *Server) disarmLiveBracketsForExit(ctx context.Context, segment, symbol, product, side string, qty int64, price float64) {
	if s.bracketEngine == nil {
		return
	}
	exitSide := normalizeOrderSide(side)
	if exitSide == "" {
		return
	}
	active, err := s.bracketEngine.List(500)
	if err != nil {
		s.log.Warn("failed to list live brackets after manual exit", "error", err)
		return
	}
	var matching []bracket.Bracket
	for _, b := range active {
		if b.Status != bracket.StatusInPosition && b.Status != bracket.StatusExitFailed {
			continue
		}
		if product != "" && !strings.EqualFold(strings.TrimSpace(b.Product), strings.TrimSpace(product)) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(b.ExchangeSegment), strings.TrimSpace(segment)) ||
			!strings.EqualFold(strings.TrimSpace(b.TradingSymbol), strings.TrimSpace(symbol)) {
			continue
		}
		if normalizeOrderSide(b.TransactionType) == exitSide {
			continue
		}
		matching = append(matching, b)
	}
	if qty <= 0 {
		for _, b := range matching {
			if err := s.bracketEngine.MarkExternalExit(ctx, b.ID, price); err != nil {
				s.log.Warn("failed to disarm live bracket after manual exit", "bracket_id", b.ID, "error", err)
			}
		}
		return
	}

	remaining := qty
	for _, b := range matching {
		if remaining <= 0 {
			break
		}
		if b.Quantity <= remaining {
			remaining -= b.Quantity
			if err := s.bracketEngine.MarkExternalExit(ctx, b.ID, price); err != nil {
				s.log.Warn("failed to disarm live bracket after manual exit", "bracket_id", b.ID, "error", err)
			}
			continue
		}
		// Partial exit: shrink the bracket so its SL/TP matches the remaining
		// position instead of overshooting it.
		if err := s.bracketEngine.UpdateQuantity(ctx, b.ID, b.Quantity-remaining); err != nil {
			s.log.Warn("failed to resize live bracket after partial exit", "bracket_id", b.ID, "error", err)
		}
		remaining = 0
	}
}

// rearmLiveBackstopsForContract restores the exchange backstop for in-position
// brackets after a manual exit is rejected.
func (s *Server) rearmLiveBackstopsForContract(ctx context.Context, segment, symbol, product string) {
	if s.bracketEngine == nil {
		return
	}
	active, err := s.bracketEngine.List(500)
	if err != nil {
		return
	}
	for _, b := range active {
		if b.Status != bracket.StatusInPosition {
			continue
		}
		if product != "" && !strings.EqualFold(strings.TrimSpace(b.Product), strings.TrimSpace(product)) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(b.ExchangeSegment), strings.TrimSpace(segment)) ||
			!strings.EqualFold(strings.TrimSpace(b.TradingSymbol), strings.TrimSpace(symbol)) {
			continue
		}
		s.bracketEngine.EnsureExchangeStop(ctx, b.ID)
	}
}

// disarmLiveBracketsForContract disarms every active bracket for one contract.
// It is used after a square-off close succeeds so a fill that raced the close
// cannot leave a bracket armed against a flat position.
func (s *Server) disarmLiveBracketsForContract(ctx context.Context, segment, symbol, product string) []string {
	if s.bracketEngine == nil {
		return nil
	}
	active, err := s.bracketEngine.List(500)
	if err != nil {
		return []string{"list brackets: " + err.Error()}
	}
	var errs []string
	for _, b := range active {
		if b.Status != bracket.StatusInPosition &&
			b.Status != bracket.StatusExitFailed &&
			b.Status != bracket.StatusPendingEntry {
			continue
		}
		if product != "" && !strings.EqualFold(strings.TrimSpace(b.Product), strings.TrimSpace(product)) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(b.ExchangeSegment), strings.TrimSpace(segment)) ||
			!strings.EqualFold(strings.TrimSpace(b.TradingSymbol), strings.TrimSpace(symbol)) {
			continue
		}
		if err := s.bracketEngine.Disarm(ctx, b.ID); err != nil {
			errs = append(errs, b.TradingSymbol+": "+err.Error())
		}
	}
	return errs
}

// rearmLiveBrackets restores brackets that were disarmed for a square-off whose
// close order failed. Empty segment/symbol re-arms every supplied bracket.
func (s *Server) rearmLiveBrackets(ctx context.Context, brackets []bracket.Bracket, segment, symbol, product string) []string {
	if segment == "" || symbol == "" {
		return nil
	}
	return s.rearmBrackets(ctx, brackets, segment, symbol, product)
}

// rearmAllLiveBrackets is used when the positions book is unavailable and every
// disarmed bracket must be restored.
func (s *Server) rearmAllLiveBrackets(ctx context.Context, brackets []bracket.Bracket) []string {
	return s.rearmBrackets(ctx, brackets, "", "", "")
}

func (s *Server) rearmBrackets(ctx context.Context, brackets []bracket.Bracket, segment, symbol, product string) []string {
	if s.bracketEngine == nil {
		return nil
	}
	var errs []string
	for _, b := range brackets {
		if segment != "" && !strings.EqualFold(strings.TrimSpace(b.ExchangeSegment), strings.TrimSpace(segment)) {
			continue
		}
		if symbol != "" && !strings.EqualFold(strings.TrimSpace(b.TradingSymbol), strings.TrimSpace(symbol)) {
			continue
		}
		if product != "" && !strings.EqualFold(strings.TrimSpace(b.Product), strings.TrimSpace(product)) {
			continue
		}
		if err := s.bracketEngine.Rearm(ctx, b.ID); err != nil {
			errs = append(errs, b.TradingSymbol+": "+err.Error())
		}
	}
	return errs
}
