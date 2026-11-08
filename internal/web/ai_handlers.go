package web

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/ai/agent"
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
	if (s.evaluator == nil || s.evaluator.JevClient() == nil) && s.agentClient == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "no AI evaluation provider (Jev or Agent) is configured. Set TYPESAFE_API_KEY or OPENROUTER_API_KEY / AI_AGENT_API_KEY.", nil)
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

	s.configMu.RLock()
	targetPct := s.cfg.AI.TargetPct
	stopLossPct := s.cfg.AI.StopLossPct
	shadowMode := s.cfg.AI.ShadowMode
	agentModel := s.cfg.AI.Agent.Model
	s.configMu.RUnlock()
	if targetPct <= 0 {
		targetPct = 8.0
	}
	if stopLossPct <= 0 {
		stopLossPct = 5.0
	}

	var eval store.Evaluation
	var jevErr error

	// 1. Try Jev (System 1) fast decision model if available
	if s.evaluator != nil && s.evaluator.JevClient() != nil {
		eval, jevErr = s.evaluator.EvaluateContract(r.Context(), evaluator.ContractSetup{
			Underlying:    req.Underlying,
			InstrumentKey: req.InstrumentKey,
			Symbol:        req.Symbol,
			Strike:        req.Strike,
			OptionType:    req.OptionType,
			OptionLTP:     req.LTP,
		})
	}

	var agentAnalysis string
	var reasoning string

	// 2. If Jev failed or was unconfigured, fall back to Agent (System 2: OpenRouter / Ollama / Gemini)
	if jevErr != nil || eval.Action == "" {
		if jevErr != nil {
			s.log.Warn("jev evaluation unavailable or failed, attempting agent fallback", "event", "jev_fallback_to_agent",
				"instrument", req.InstrumentKey, "error", jevErr)
		}

		if s.agentClient != nil {
			spot, _ := s.GetSpotPrice(r.Context(), req.Underlying)
			if spot <= 0 {
				spot = req.LTP
			}
			var macroBias string
			if s.aiStore != nil {
				if pb, _ := s.aiStore.GetLatestPlaybook(r.Context()); pb != nil {
					macroBias = pb.Bias
				}
			}

			agentCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()

			decision, aerr := s.agentClient.EvaluateContract(agentCtx, agent.ContractSetup{
				Underlying:    req.Underlying,
				SpotPrice:     spot,
				OptionType:    req.OptionType,
				Strike:        req.Strike,
				OptionLTP:     req.LTP,
				MacroBias:     macroBias,
				OrderBookDesc: "real-time market depth",
			})
			if aerr != nil {
				s.log.Error("ai agent evaluation fallback failed", "event", "ai_agent_fallback_failed", "error", aerr)
				s.writeRouteError(w, r, http.StatusServiceUnavailable, "AI evaluation failed: "+aerr.Error(), aerr)
				return
			}

			action := strings.ToUpper(strings.TrimSpace(decision.Action))
			if action == "BUY" {
				if req.OptionType == "CE" {
					action = "BUY_CE"
				} else {
					action = "BUY_PE"
				}
			}

			targetPrice := math.Round(req.LTP*(1.0+targetPct/100.0)*100) / 100
			stopLossPrice := math.Round(req.LTP*(1.0-stopLossPct/100.0)*100) / 100

			eval = store.Evaluation{
				Timestamp:     time.Now(),
				InstrumentKey: req.InstrumentKey,
				Underlying:    req.Underlying,
				SpotPrice:     spot,
				OptionLTP:     req.LTP,
				Action:        action,
				Confidence:    decision.Confidence,
				Score:         decision.Score,
				DurationMS:    0,
				StateSnapshot: fmt.Sprintf(`{"agent_model":"%s","source":"agent"}`, agentModel),
				Shadow:        shadowMode,
				TargetPrice:   targetPrice,
				StopLossPrice: stopLossPrice,
				Status:        "PENDING",
			}

			if s.aiStore != nil {
				id, serr := s.aiStore.SaveEvaluation(r.Context(), eval)
				if serr == nil {
					eval.ID = id
				}
			}

			if s.evaluator != nil && (action == "BUY_CE" || action == "BUY_PE" || action == "BUY") {
				s.evaluator.TrackEvaluation(eval)
			}

			agentAnalysis = decision.Reasoning
			reasoning = fmt.Sprintf("Agent conviction score %d/5 with confidence %.0f%%.", decision.Score, decision.Confidence*100)
		} else {
			s.log.Error("ai contract evaluation failed and no agent available", "event", "ai_ask_failed", "instrument", req.InstrumentKey, "error", jevErr)
			s.writeRouteError(w, r, http.StatusServiceUnavailable, "AI evaluation failed: "+jevErr.Error(), jevErr)
			return
		}
	} else {
		reasoning = fmt.Sprintf("Conviction score %d/5 with confidence %.0f%%.", eval.Score, eval.Confidence*100)
		if s.agentClient != nil {
			agentCtx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
			defer cancel()
			prompt := fmt.Sprintf("Underlying %s spot, evaluating %s strike %s at ₹%.2f. System 1 verdict is %s (score %d/5, confidence %.2f). Give a concise 1-sentence tactical recommendation on entering this scalp setup.",
				req.Underlying, req.OptionType, req.Strike, req.LTP, eval.Action, eval.Score, eval.Confidence)
			if reply, aerr := s.agentClient.Chat(agentCtx, prompt); aerr == nil && reply != "" {
				agentAnalysis = strings.TrimSpace(reply)
			}
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
		Reasoning:     reasoning,
		AgentAnalysis: agentAnalysis,
		Evaluation:    eval,
	})
}
