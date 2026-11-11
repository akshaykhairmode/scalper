package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
)

type bracketListResponse struct {
	Brackets []bracket.Bracket `json:"brackets"`
}

func (s *Server) handleListBrackets(w http.ResponseWriter, r *http.Request) {
	if s.bracketEngine == nil {
		s.writeRouteJSON(w, r, http.StatusOK, bracketListResponse{Brackets: []bracket.Bracket{}})
		return
	}
	list, err := s.bracketEngine.List(100)
	if err != nil {
		s.writeRouteError(w, r, http.StatusInternalServerError, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, bracketListResponse{Brackets: list})
}

func (s *Server) handleCreateBracket(w http.ResponseWriter, r *http.Request) {
	if s.bracketEngine == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "bracket engine unavailable", nil)
		return
	}
	var b bracket.Bracket
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid bracket payload: "+err.Error(), err)
		return
	}

	// If the bracket needs an entry order placed (EntryOrderID is empty), enforce risk controls
	if (b.Status == "" || b.Status == bracket.StatusPendingEntry) && b.EntryOrderID == "" {
		session, _ := s.brokerSessionValue()

		entryOrder := kotakapi.Order{
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
		if err := s.checkOrderRisk(r.Context(), session, entryOrder); err != nil {
			s.writeRiskError(w, r, err)
			return
		}
	}

	created, err := s.bracketEngine.Create(r.Context(), b)
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusCreated, created)
}

type bracketUpdateRequest struct {
	EntryPrice    *float64 `json:"entry_price"`
	TargetPrice   *float64 `json:"target_price"`
	StopLossPrice *float64 `json:"stop_loss_price"`
	Confirm       bool     `json:"confirm"`
}

func (s *Server) handleUpdateBracket(w http.ResponseWriter, r *http.Request) {
	if s.bracketEngine == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "bracket engine unavailable", nil)
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
	if req.EntryPrice == nil && req.TargetPrice == nil && req.StopLossPrice == nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "at least one bracket level is required", nil)
		return
	}

	if req.EntryPrice != nil {
		if s.configSnapshot().Trading.ConfirmLiveOrders && !req.Confirm {
			s.writeRouteError(w, r, http.StatusConflict, "explicit confirmation is required for live orders", nil)
			return
		}
		session, ok := s.brokerSession(w, r)
		if !ok {
			return
		}
		existing, found := s.bracketEngine.Get(id)
		if !found {
			s.writeRouteError(w, r, http.StatusNotFound, "bracket not found", nil)
			return
		}
		entryOrder := kotakapi.Order{
			ExchangeSegment: existing.ExchangeSegment,
			TradingSymbol:   existing.TradingSymbol,
			TransactionType: existing.TransactionType,
			Product:         existing.Product,
			OrderType:       "L",
			Quantity:        existing.Quantity,
			Price:           *req.EntryPrice,
			Validity:        "DAY",
		}
		if err := s.checkOrderRisk(r.Context(), session, entryOrder); err != nil {
			s.writeRiskError(w, r, err)
			return
		}
	}

	updated, err := s.bracketEngine.UpdateLevels(r.Context(), id, bracket.LevelUpdates{
		EntryPrice:    req.EntryPrice,
		TargetPrice:   req.TargetPrice,
		StopLossPrice: req.StopLossPrice,
	})
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, updated)
}

func (s *Server) handleCancelBracket(w http.ResponseWriter, r *http.Request) {
	if s.bracketEngine == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "bracket engine unavailable", nil)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "bracket id required", nil)
		return
	}
	if err := s.bracketEngine.Cancel(r.Context(), id); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"success": true, "id": id})
}

func (s *Server) handleExitBracket(w http.ResponseWriter, r *http.Request) {
	if s.bracketEngine == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "bracket engine unavailable", nil)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "bracket id required", nil)
		return
	}
	if err := s.bracketEngine.Exit(r.Context(), id); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"success": true, "id": id})
}
