package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
)

type bracketListResponse struct {
	Brackets []bracket.Bracket `json:"brackets"`
}

func (s *Server) handleListBrackets(w http.ResponseWriter, r *http.Request) {
	mode := s.requestMode(r)
	if mode == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "mode must be live or paper", nil)
		return
	}
	engine := s.engineFor(mode)
	if engine == nil {
		s.writeRouteJSON(w, r, http.StatusOK, bracketListResponse{Brackets: []bracket.Bracket{}})
		return
	}
	list, err := engine.List(100)
	if err != nil {
		s.writeRouteError(w, r, http.StatusInternalServerError, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, bracketListResponse{Brackets: list})
}

func (s *Server) handleCreateBracket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		bracket.Bracket
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid bracket payload: "+err.Error(), err)
		return
	}
	b := req.Bracket
	mode := s.requestMode(r)
	if strings.TrimSpace(b.Mode) != "" {
		mode = normalizeTradingMode(b.Mode)
	}
	if mode == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "mode must be live or paper", nil)
		return
	}
	b.Mode = mode
	engine := s.engineFor(mode)
	if engine == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "bracket engine unavailable for "+mode, nil)
		return
	}
	if err := normalizeBracketExchangeStop(&b); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, err.Error(), err)
		return
	}
	if mode == modePaper && s.configSnapshot().Trading.Paper.ConfirmOrders && !req.Confirm {
		s.writeRouteError(w, r, http.StatusConflict, "explicit confirmation is required for paper brackets", nil)
		return
	}

	// If the bracket needs an entry order placed (EntryOrderID is empty), enforce risk controls
	if (b.Status == "" || b.Status == bracket.StatusPendingEntry) && b.EntryOrderID == "" {
		entryOrder := kotakapi.Order{
			InstrumentKey:   b.InstrumentKey,
			ExchangeSegment: b.ExchangeSegment,
			TradingSymbol:   b.TradingSymbol,
			TransactionType: b.TransactionType,
			Product:         b.Product,
			OrderType:       "L",
			Quantity:        b.Quantity,
			Price:           b.EntryPrice,
			Validity:        "DAY",
		}
		if b.EntryPrice <= 0 {
			entryOrder.OrderType = "MKT"
		}
		if mode == modePaper {
			if err := s.checkPaperOrderRisk(r.Context(), entryOrder); err != nil {
				s.writeRiskError(w, r, err)
				return
			}
		} else {
			session, _ := s.brokerSessionValue()
			if err := s.checkOrderRisk(r.Context(), session, entryOrder); err != nil {
				s.writeRiskError(w, r, err)
				return
			}
		}
	}

	created, err := engine.Create(r.Context(), b)
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, err.Error(), err)
		return
	}
	if mode == modePaper {
		s.ensurePaperSubscribed(created.InstrumentKey)
	}
	s.writeRouteJSON(w, r, http.StatusCreated, created)
}

type bracketUpdateRequest struct {
	EntryPrice        *float64 `json:"entry_price"`
	TargetPrice       *float64 `json:"target_price"`
	StopLossPrice     *float64 `json:"stop_loss_price"`
	ExchangeStopMode  *string  `json:"exchange_stop_mode"`
	ExchangeStopPrice *float64 `json:"exchange_stop_price"`
	Confirm           bool     `json:"confirm"`
}

func (s *Server) handleUpdateBracket(w http.ResponseWriter, r *http.Request) {
	mode := s.requestMode(r)
	if mode == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "mode must be live or paper", nil)
		return
	}
	engine := s.engineFor(mode)
	if engine == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "bracket engine unavailable for "+mode, nil)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "bracket id required", nil)
		return
	}
	var req bracketUpdateRequest
	if err := decodeRequest(w, r, &req); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid bracket update payload: "+err.Error(), err)
		return
	}
	if req.ExchangeStopPrice != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "exchange backstop price is managed at the broker; modify or cancel it in Kotak", nil)
		return
	}
	if req.EntryPrice == nil && req.TargetPrice == nil && req.StopLossPrice == nil && req.ExchangeStopMode == nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "at least one bracket level is required", nil)
		return
	}

	if req.EntryPrice != nil {
		if mode == modeLive && s.configSnapshot().Trading.ConfirmLiveOrders && !req.Confirm {
			s.writeRouteError(w, r, http.StatusConflict, "explicit confirmation is required for live orders", nil)
			return
		}
		existing, found := engine.Get(id)
		if !found {
			s.writeRouteError(w, r, http.StatusNotFound, "bracket not found", nil)
			return
		}
		entryOrder := kotakapi.Order{
			InstrumentKey:   existing.InstrumentKey,
			ExchangeSegment: existing.ExchangeSegment,
			TradingSymbol:   existing.TradingSymbol,
			TransactionType: existing.TransactionType,
			Product:         existing.Product,
			OrderType:       "L",
			Quantity:        existing.Quantity,
			Price:           *req.EntryPrice,
			Validity:        "DAY",
		}
		if mode == modePaper {
			if err := s.checkPaperOrderRisk(r.Context(), entryOrder); err != nil {
				s.writeRiskError(w, r, err)
				return
			}
		} else {
			session, ok := s.brokerSession(w, r)
			if !ok {
				return
			}
			if err := s.checkOrderRisk(r.Context(), session, entryOrder); err != nil {
				s.writeRiskError(w, r, err)
				return
			}
		}
	}

	updated, err := engine.UpdateLevels(r.Context(), id, bracket.LevelUpdates{
		EntryPrice:       req.EntryPrice,
		TargetPrice:      req.TargetPrice,
		StopLossPrice:    req.StopLossPrice,
		ExchangeStopMode: req.ExchangeStopMode,
	})
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, updated)
}

func (s *Server) handleCancelBracket(w http.ResponseWriter, r *http.Request) {
	mode, ok := s.requireRequestMode(w, r)
	if !ok {
		return
	}
	engine := s.engineFor(mode)
	if engine == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "bracket engine unavailable", nil)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "bracket id required", nil)
		return
	}
	if err := engine.Cancel(r.Context(), id); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"success": true, "id": id})
}

func (s *Server) handleExitBracket(w http.ResponseWriter, r *http.Request) {
	mode, ok := s.requireRequestMode(w, r)
	if !ok {
		return
	}
	engine := s.engineFor(mode)
	if engine == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "bracket engine unavailable", nil)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "bracket id required", nil)
		return
	}
	if err := engine.Exit(r.Context(), id); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"success": true, "id": id})
}

// normalizeBracketExchangeStop canonicalises the per-order exchange stop choice
// and validates an explicitly supplied trigger price.
func normalizeBracketExchangeStop(b *bracket.Bracket) error {
	mode := bracket.NormalizeExchangeStopMode(b.ExchangeStopMode)
	switch mode {
	case "", bracket.ExchangeStopOff, bracket.ExchangeStopSLM, bracket.ExchangeStopSLL:
	default:
		return errors.New("exchange_stop_mode must be off, SL-M, or SL")
	}
	b.ExchangeStopMode = mode
	if mode == bracket.ExchangeStopOff {
		b.ExchangeStopPrice = 0
	}
	if b.ExchangeStopPrice <= 0 || mode == "" || mode == bracket.ExchangeStopOff {
		return nil
	}
	if b.ExchangeStopPrice < 0 {
		return errors.New("exchange_stop_price cannot be negative")
	}
	side := strings.ToUpper(strings.TrimSpace(b.TransactionType))
	if b.EntryPrice <= 0 {
		return nil
	}
	switch side {
	case "B":
		side = "BUY"
	case "S":
		side = "SELL"
	}
	switch side {
	case "BUY":
		if b.ExchangeStopPrice >= b.EntryPrice {
			return errors.New("exchange stop trigger must be below the entry price for a BUY bracket")
		}
		if b.StopLossPrice > 0 && b.ExchangeStopPrice >= b.StopLossPrice {
			return errors.New("exchange stop trigger must be farther than the server stop-loss for a BUY bracket")
		}
	case "SELL":
		if b.ExchangeStopPrice <= b.EntryPrice {
			return errors.New("exchange stop trigger must be above the entry price for a SELL bracket")
		}
		if b.StopLossPrice > 0 && b.ExchangeStopPrice <= b.StopLossPrice {
			return errors.New("exchange stop trigger must be farther than the server stop-loss for a SELL bracket")
		}
	}
	return nil
}

// lookupBrokerOrder resolves an exchange stop order by broker ID or by its
// client tag. The engine uses it to confirm cancellations and reconcile after
// a restart.
func (s *Server) lookupBrokerOrder(ctx context.Context, orderID, tag string) (string, string, float64, error) {
	if s.kotak == nil {
		return "", "", 0, errors.New("broker API is not configured")
	}
	session, ok := s.brokerSessionValue()
	if !ok {
		return "", "", 0, errors.New("kotak session unavailable")
	}
	orderID = strings.TrimSpace(orderID)
	tag = strings.TrimSpace(tag)
	if orderID == "" && tag == "" {
		return "", "", 0, errors.New("order id or tag required")
	}
	var raw json.RawMessage
	var err error
	if orderID != "" {
		raw, err = s.kotak.Order(ctx, session, orderID)
	} else {
		raw, err = s.kotak.Orders(ctx, session)
	}
	if err != nil {
		return "", "", 0, err
	}
	rows, err := decodeBookRows(raw)
	if err != nil {
		return "", "", 0, err
	}
	for _, row := range rows {
		rowID := stringField(row, "order_id", "nOrdNo", "nordno", "ordId", "ordid", "id")
		if orderID != "" {
			if rowID != "" && rowID != orderID {
				continue
			}
		} else {
			rowTag := stringField(row, "tag", "guiOrdId", "gui_order_id", "tag_id")
			if rowTag != tag {
				continue
			}
		}
		status := stringField(row, "status", "ordSt", "ordst", "order_status")
		price, _ := numberField(row, "avgPrc", "avgprc", "avg_price", "average_price", "price", "prc")
		return rowID, status, price, nil
	}
	return "", "", 0, errors.New("order not found")
}

func (s *Server) reconcileBracketsAfterStart() {
	select {
	case <-s.feedCtx.Done():
		return
	case <-time.After(3 * time.Second):
	}
	if s.bracketEngine == nil {
		return
	}
	if _, ok := s.brokerSessionValue(); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(s.feedCtx, 30*time.Second)
	defer cancel()
	s.bracketEngine.Reconcile(ctx)
}
