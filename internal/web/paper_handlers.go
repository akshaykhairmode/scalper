package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
	"github.com/akshaykhairmode/scalper/internal/paper"
)

func paperPositionRows(s *Server) []map[string]any {
	if s.paperExec == nil {
		return nil
	}
	positions := s.paperExec.Positions()
	rows := make([]map[string]any, 0, len(positions))
	for _, p := range positions {
		rows = append(rows, map[string]any{
			"instrument_key":   p.InstrumentKey,
			"exchange_segment": p.ExchangeSegment,
			"trading_symbol":   p.TradingSymbol,
			"symbol":           p.TradingSymbol,
			"product":          p.Product,
			"net_qty":          p.NetQuantity,
			"quantity":         p.NetQuantity,
			"average_price":    p.AveragePrice,
			"ltp":              p.LTP,
			"realised_pnl":     p.RealizedPnL,
			"unrealised_pnl":   p.UnrealizedPnL,
			"pnl":              p.RealizedPnL + p.UnrealizedPnL,
			"mode":             modePaper,
		})
	}
	return rows
}

func paperOrderRow(o paper.Order) map[string]any {
	return map[string]any{
		"order_id":         o.ID,
		"nOrdNo":           o.ID,
		"id":               o.ID,
		"tag":              o.Tag,
		"bracket_id":       o.BracketID,
		"trading_symbol":   o.TradingSymbol,
		"symbol":           o.TradingSymbol,
		"exchange_segment": o.ExchangeSegment,
		"instrument_key":   o.InstrumentKey,
		"side":             o.TransactionType,
		"transaction_type": o.TransactionType,
		"qty":              o.Quantity,
		"quantity":         o.Quantity,
		"filled_qty":       o.FilledQuantity,
		"price":            o.Price,
		"avg_price":        o.FilledPrice,
		"trigger_price":    o.TriggerPrice,
		"order_type":       o.OrderType,
		"product":          o.Product,
		"status":           o.Status,
		"timestamp":        o.CreatedAt.Unix(),
		"created_at":       o.CreatedAt.Unix(),
		"mode":             modePaper,
	}
}

func paperOrderRows(s *Server) []map[string]any {
	if s.paperExec == nil {
		return nil
	}
	orders := s.paperExec.Orders()
	rows := make([]map[string]any, 0, len(orders))
	for _, o := range orders {
		rows = append(rows, paperOrderRow(o))
	}
	return rows
}

func paperTradeRows(ctx context.Context, s *Server) []map[string]any {
	if s.paperExec == nil {
		return nil
	}
	trades, err := s.paperExec.Trades(ctx, 500)
	if err != nil {
		s.log.Warn("failed to read paper trades", "error", err)
		return nil
	}
	rows := make([]map[string]any, 0, len(trades))
	for _, t := range trades {
		rows = append(rows, map[string]any{
			"trade_id":         t.ID,
			"order_id":         t.OrderID,
			"trading_symbol":   t.TradingSymbol,
			"symbol":           t.TradingSymbol,
			"instrument_key":   t.InstrumentKey,
			"side":             t.TransactionType,
			"transaction_type": t.TransactionType,
			"qty":              t.Quantity,
			"quantity":         t.Quantity,
			"price":            t.Price,
			"timestamp":        t.ExecutedAt.Unix(),
			"mode":             modePaper,
		})
	}
	return rows
}

func paperLimits(s *Server) map[string]any {
	if s.paperExec == nil {
		return map[string]any{"available_margin": 0, "used_margin": 0, "total_margin": 0, "mode": modePaper}
	}
	l := s.paperExec.Limits()
	return map[string]any{
		"available_margin": l.Available,
		"used_margin":      l.Used,
		"total_margin":     l.Total,
		"available":        l.Available,
		"used":             l.Used,
		"total":            l.Total,
		"mode":             modePaper,
	}
}

// writePaperBook serves the account-book routes from the paper ledger.
func (s *Server) writePaperBook(w http.ResponseWriter, r *http.Request, kind string) {
	if !s.paperAvailable() {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "paper account is not enabled", nil)
		return
	}
	switch kind {
	case bookPositions:
		s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"data": paperPositionRows(s)})
	case bookOrders:
		s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"data": paperOrderRows(s)})
	case bookTrades:
		s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"data": paperTradeRows(r.Context(), s)})
	case bookHoldings:
		s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"data": []any{}})
	case bookLimits:
		s.writeRouteJSON(w, r, http.StatusOK, paperLimits(s))
	default:
		s.writeRouteError(w, r, http.StatusBadRequest, "unknown paper book", nil)
	}
}

// writePaperOrder serves one paper order and its simplified history.
func (s *Server) writePaperOrder(w http.ResponseWriter, r *http.Request, id string, history bool) {
	if !s.paperAvailable() {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "paper account is not enabled", nil)
		return
	}
	id = strings.TrimSpace(id)
	for _, o := range s.paperExec.Orders() {
		if o.ID != id {
			continue
		}
		row := paperOrderRow(o)
		if history {
			s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"data": []any{row}})
			return
		}
		s.writeRouteJSON(w, r, http.StatusOK, row)
		return
	}
	s.writeRouteError(w, r, http.StatusNotFound, "paper order not found", nil)
}

// squareOffPaper cancels every working paper order, closes net positions and
// marks the brackets externally exited. Cancelling first prevents a resting
// entry or backstop from firing during or after the square-off.
func (s *Server) squareOffPaper(ctx context.Context) (int, []string) {
	if !s.paperAvailable() {
		return 0, []string{"paper account is not enabled"}
	}
	var errs []string

	for _, o := range s.paperExec.Orders() {
		if !o.Open() {
			continue
		}
		if err := s.paperExec.Cancel(ctx, o.ID); err != nil {
			errs = append(errs, o.TradingSymbol+": "+err.Error())
		}
	}

	// Phase 1: mark brackets exited before closing positions so a concurrent
	// tick cannot trigger SL/TP against a position that is already flat.
	var active []bracket.Bracket
	if s.paperEngine != nil {
		list, err := s.paperEngine.List(500)
		if err != nil {
			errs = append(errs, "list paper brackets: "+err.Error())
		} else {
			for _, b := range list {
				if b.Status != bracket.StatusInPosition &&
					b.Status != bracket.StatusPendingEntry &&
					b.Status != bracket.StatusExitFailed {
					continue
				}
				if err := s.paperEngine.MarkExternalExit(ctx, b.ID, 0); err != nil {
					errs = append(errs, b.TradingSymbol+": "+err.Error())
					continue
				}
				active = append(active, b)
			}
		}
	}

	// Phase 2: close net positions.
	closed := 0
	for _, p := range s.paperExec.Positions() {
		if p.NetQuantity == 0 {
			continue
		}
		side := "SELL"
		qty := p.NetQuantity
		if qty < 0 {
			side = "BUY"
			qty = -qty
		}
		_, err := s.paperExec.Place(ctx, kotakapi.Order{
			InstrumentKey:   p.InstrumentKey,
			ExchangeSegment: p.ExchangeSegment,
			TradingSymbol:   p.TradingSymbol,
			TransactionType: side,
			Product:         p.Product,
			OrderType:       "MKT",
			Quantity:        qty,
			Validity:        "DAY",
		})
		if err != nil {
			errs = append(errs, p.TradingSymbol+": "+err.Error())
			continue
		}
		closed++
	}

	// Phase 3: record the realized exit price/PnL now that the fills exist.
	for _, b := range active {
		price, ok := s.paperExec.LastFillPrice(b.InstrumentKey)
		if !ok {
			for _, p := range s.paperExec.Positions() {
				if p.InstrumentKey == b.InstrumentKey && p.LTP > 0 {
					price = p.LTP
					ok = true
					break
				}
			}
		}
		if !ok {
			price = b.EntryPrice
		}
		if err := s.paperEngine.MarkExternalExit(ctx, b.ID, price); err != nil {
			errs = append(errs, b.TradingSymbol+": "+err.Error())
		}
	}
	return closed, errs
}

// disarmPaperBracketsForInstrument marks in-position paper brackets externally
// exited after a manual order closes the whole position. Pending entries are
// intentionally left alone: they are separate orders, not part of this exit.
func (s *Server) disarmPaperBracketsForInstrument(ctx context.Context, instrumentKey, segment, symbol string) {
	if s.paperEngine == nil || s.paperExec == nil {
		return
	}
	net := int64(0)
	found := false
	for _, p := range s.paperExec.Positions() {
		if p.InstrumentKey == instrumentKey {
			net = p.NetQuantity
			found = true
			break
		}
	}
	if found && net != 0 {
		// Partial exit: keep the bracket's SL/TP protecting the remainder.
		return
	}
	active, err := s.paperEngine.List(500)
	if err != nil {
		s.log.Warn("failed to list paper brackets for manual exit", "error", err)
		return
	}
	for _, b := range active {
		if b.Status != bracket.StatusInPosition && b.Status != bracket.StatusExitFailed {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(b.ExchangeSegment), strings.TrimSpace(segment)) ||
			!strings.EqualFold(strings.TrimSpace(b.TradingSymbol), strings.TrimSpace(symbol)) {
			continue
		}
		price, _ := s.paperExec.LastFillPrice(b.InstrumentKey)
		if err := s.paperEngine.MarkExternalExit(ctx, b.ID, price); err != nil {
			s.log.Warn("failed to disarm paper bracket after manual exit", "bracket_id", b.ID, "error", err)
		}
	}
}

// paperMarginPreview estimates required margin for the frontend preview.
func (s *Server) paperMarginPreview(price float64, qty int64, side, instrumentKey string) map[string]any {
	required := price * float64(qty)
	if strings.EqualFold(strings.TrimSpace(side), "S") || strings.EqualFold(strings.TrimSpace(side), "SELL") {
		perShare := price
		if v, ok := s.paperShortNotional(instrumentKey); ok {
			perShare = v
		}
		required = float64(qty) * perShare
		if pct := s.configSnapshot().Trading.Paper.ShortMarginPct; pct > 0 {
			required *= pct / 100
		}
	}
	limits := paperLimits(s)
	available, _ := limits["available_margin"].(float64)
	shortfall := required - available
	if shortfall < 0 {
		shortfall = 0
	}
	return map[string]any{
		"stat": "ok",
		"data": map[string]any{
			"required_margin":    required,
			"ordMrgn":            required,
			"order_margin":       required,
			"available_margin":   available,
			"avlMrgn":            available,
			"price_available":    price > 0,
			"insufficient":       required > available,
			"insufFund":          shortfall,
			"insufficient_funds": shortfall,
		},
	}
}
