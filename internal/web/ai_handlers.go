package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/ai/evaluator"
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

type evaluationStatsResponse struct {
	Stats store.EvaluationStats `json:"stats"`
}

func (s *Server) handleGetAIEvaluationStats(w http.ResponseWriter, r *http.Request) {
	if s.aiStore == nil {
		s.writeRouteJSON(w, r, http.StatusOK, evaluationStatsResponse{Stats: store.EvaluationStats{}})
		return
	}

	stats, err := s.aiStore.GetEvaluationStats(r.Context())
	if err != nil {
		s.log.Error("failed to get ai evaluation stats", "event", "ai_evaluation_stats_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
		s.writeRouteError(w, r, http.StatusInternalServerError, "failed to get evaluation stats: "+err.Error(), nil)
		return
	}

	s.writeRouteJSON(w, r, http.StatusOK, evaluationStatsResponse{Stats: stats})
}

type triggerScanResponse struct {
	Success     bool               `json:"success"`
	Evaluations []store.Evaluation `json:"evaluations"`
	Message     string             `json:"message,omitempty"`
}

func (s *Server) handleTriggerAIScan(w http.ResponseWriter, r *http.Request) {
	if s.evaluator == nil || s.evaluator.JevClient() == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "ai evaluator or jev client not configured", nil)
		return
	}

	underlying := strings.TrimSpace(r.URL.Query().Get("underlying"))
	if underlying == "" {
		underlying = "NIFTY"
	}

	evals, err := s.evaluator.ScanNow(r.Context(), underlying)
	if err != nil {
		s.log.Error("manual ai scan failed", "event", "ai_scan_trigger_failed", "underlying", underlying, "error", err,
			"request_id", logging.RequestID(r.Context()))
		s.writeRouteError(w, r, http.StatusInternalServerError, "evaluation scan failed: "+err.Error(), nil)
		return
	}

	if evals == nil {
		evals = []store.Evaluation{}
	}

	s.writeRouteJSON(w, r, http.StatusOK, triggerScanResponse{
		Success:     true,
		Evaluations: evals,
		Message:     fmt.Sprintf("Scan completed for %s: %d setups evaluated", underlying, len(evals)),
	})
}

type askEvaluationRequest struct {
	Underlying    string  `json:"underlying"`
	InstrumentKey string  `json:"instrument_key"`
	Symbol        string  `json:"symbol"`
	Strike        string  `json:"strike"`
	OptionType    string  `json:"option_type"`
	LTP           float64 `json:"ltp"`
}

type askEvaluationResponse struct {
	Success       bool             `json:"success"`
	Action        string           `json:"action"`
	Confidence    float64          `json:"confidence"`
	Score         int              `json:"score"`
	TargetPrice   float64          `json:"target_price"`
	StopLossPrice float64          `json:"stop_loss_price"`
	TargetPct     float64          `json:"target_pct"`
	StopLossPct   float64          `json:"stop_loss_pct"`
	Reasoning     string           `json:"reasoning,omitempty"`
	AgentAnalysis string           `json:"agent_analysis,omitempty"`
	Evaluation    store.Evaluation `json:"evaluation"`
}

func (s *Server) handleAskAIEvaluation(w http.ResponseWriter, r *http.Request) {
	if s.evaluator == nil || s.evaluator.JevClient() == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "ai evaluator or jev client not configured", nil)
		return
	}

	var req askEvaluationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid request payload: "+err.Error(), err)
		return
	}

	if req.LTP <= 0 {
		s.writeRouteError(w, r, http.StatusBadRequest, "current LTP must be greater than zero", nil)
		return
	}

	if strings.TrimSpace(req.Underlying) == "" {
		req.Underlying = "NIFTY"
	}
	if strings.TrimSpace(req.OptionType) == "" {
		req.OptionType = "CE"
	}

	eval, err := s.evaluator.EvaluateContract(r.Context(), evaluator.ContractSetup{
		Underlying:    req.Underlying,
		InstrumentKey: req.InstrumentKey,
		Symbol:        req.Symbol,
		Strike:        req.Strike,
		OptionType:    req.OptionType,
		OptionLTP:     req.LTP,
	})
	if err != nil {
		s.log.Error("ai contract evaluation failed", "event", "ai_ask_failed", "instrument", req.InstrumentKey, "error", err,
			"request_id", logging.RequestID(r.Context()))
		s.writeRouteError(w, r, http.StatusInternalServerError, "evaluation failed: "+err.Error(), nil)
		return
	}

	s.configMu.RLock()
	targetPct := s.cfg.AI.TargetPct
	stopLossPct := s.cfg.AI.StopLossPct
	s.configMu.RUnlock()
	if targetPct <= 0 {
		targetPct = 8.0
	}
	if stopLossPct <= 0 {
		stopLossPct = 5.0
	}

	var agentAnalysis string
	if s.agentClient != nil {
		agentCtx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		prompt := fmt.Sprintf("Underlying %s spot, evaluating %s strike %s at ₹%.2f. System 1 verdict is %s (score %d/5, confidence %.2f). Give a concise 1-sentence tactical recommendation on entering this scalp setup.",
			req.Underlying, req.OptionType, req.Strike, req.LTP, eval.Action, eval.Score, eval.Confidence)
		if reply, aerr := s.agentClient.Chat(agentCtx, prompt); aerr == nil && reply != "" {
			agentAnalysis = strings.TrimSpace(reply)
		}
	}

	s.log.Info("ai contract ask evaluated", "event", "ai_ask_evaluated",
		"instrument", req.InstrumentKey, "action", eval.Action, "score", eval.Score, "conf", eval.Confidence,
		"target", eval.TargetPrice, "stop", eval.StopLossPrice, "request_id", logging.RequestID(r.Context()))

	s.writeRouteJSON(w, r, http.StatusOK, askEvaluationResponse{
		Success:       true,
		Action:        eval.Action,
		Confidence:    eval.Confidence,
		Score:         eval.Score,
		TargetPrice:   eval.TargetPrice,
		StopLossPrice: eval.StopLossPrice,
		TargetPct:     targetPct,
		StopLossPct:   stopLossPct,
		Reasoning:     fmt.Sprintf("Conviction score %d/5 with confidence %.0f%%.", eval.Score, eval.Confidence*100),
		AgentAnalysis: agentAnalysis,
		Evaluation:    eval,
	})
}
