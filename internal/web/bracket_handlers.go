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
			Validity:        "GFD",
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
