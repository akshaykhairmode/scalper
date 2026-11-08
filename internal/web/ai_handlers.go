package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/logging"
	"github.com/akshaykhairmode/scalper/internal/store"
)

type playbookResponse struct {
	Playbook *store.Playbook `json:"playbook"`
}

type savePlaybookResponse struct {
	Success  bool            `json:"success"`
	ID       int64           `json:"id"`
	Playbook *store.Playbook `json:"playbook"`
}

type evaluationsResponse struct {
	Evaluations []store.Evaluation `json:"evaluations"`
}

func (s *Server) handleGetAIPlaybook(w http.ResponseWriter, r *http.Request) {
	if s.aiStore == nil {
		s.writeRouteJSON(w, r, http.StatusOK, playbookResponse{Playbook: nil})
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	var p *store.Playbook
	var err error

	if date != "" {
		p, err = s.aiStore.GetPlaybookByDate(r.Context(), date)
	} else {
		p, err = s.aiStore.GetLatestPlaybook(r.Context())
	}

	if err != nil {
		s.log.Error("failed to get ai playbook", "event", "ai_playbook_get_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
		s.writeRouteError(w, r, http.StatusInternalServerError, "failed to load playbook: "+err.Error(), nil)
		return
	}

	s.writeRouteJSON(w, r, http.StatusOK, playbookResponse{Playbook: p})
}

func (s *Server) handleSaveAIPlaybook(w http.ResponseWriter, r *http.Request) {
	if s.aiStore == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "ai store not configured", nil)
		return
	}

	var req store.Playbook
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid playbook JSON payload: "+err.Error(), err)
		return
	}

	if strings.TrimSpace(req.PlaybookDate) == "" {
		req.PlaybookDate = time.Now().Format("2006-01-02")
	}
	if strings.TrimSpace(req.Bias) == "" {
		req.Bias = "NEUTRAL"
	}
	if strings.TrimSpace(req.Regime) == "" {
		req.Regime = "RANGE_BOUND"
	}
	if strings.TrimSpace(req.Source) == "" {
		req.Source = "antigravity"
	}

	id, err := s.aiStore.SavePlaybook(r.Context(), req)
	if err != nil {
		s.log.Error("failed to save ai playbook", "event", "ai_playbook_save_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
		s.writeRouteError(w, r, http.StatusInternalServerError, "failed to save playbook: "+err.Error(), nil)
		return
	}

	req.ID = id
	req.CreatedAt = time.Now()

	s.log.Info("ai playbook saved", "event", "ai_playbook_saved", "id", id, "date", req.PlaybookDate,
		"bias", req.Bias, "regime", req.Regime, "source", req.Source, "request_id", logging.RequestID(r.Context()))

	s.writeRouteJSON(w, r, http.StatusCreated, savePlaybookResponse{
		Success:  true,
		ID:       id,
		Playbook: &req,
	})
}

func (s *Server) handleListAIEvaluations(w http.ResponseWriter, r *http.Request) {
	if s.aiStore == nil {
		s.writeRouteJSON(w, r, http.StatusOK, evaluationsResponse{Evaluations: []store.Evaluation{}})
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	evals, err := s.aiStore.ListEvaluations(r.Context(), limit)
	if err != nil {
		s.log.Error("failed to list ai evaluations", "event", "ai_evaluations_list_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
		s.writeRouteError(w, r, http.StatusInternalServerError, "failed to list evaluations: "+err.Error(), nil)
		return
	}
	if evals == nil {
		evals = []store.Evaluation{}
	}

	s.writeRouteJSON(w, r, http.StatusOK, evaluationsResponse{Evaluations: evals})
}
