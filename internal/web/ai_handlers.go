package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
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

func (s *Server) loadAIEvaluations(ctx context.Context, limit int) ([]store.Evaluation, error) {
	if s.aiStore == nil {
		return []store.Evaluation{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	evals, err := s.aiStore.ListEvaluations(ctx, limit)
	if err != nil {
		return nil, err
	}
	if evals == nil {
		evals = []store.Evaluation{}
	}

	// Enrich open calls with the latest tracked price and time in trade so the
	// UI can show live PnL before an outcome is recorded.
	if s.evaluator != nil {
		prices := s.evaluator.LivePrices()
		now := time.Now()
		for i := range evals {
			if price, ok := prices[evals[i].ID]; ok && price > 0 {
				evals[i].CurrentPrice = price
			}
			if strings.EqualFold(evals[i].Status, "PENDING") && !evals[i].Timestamp.IsZero() {
				evals[i].TimeInTradeSeconds = int(now.Sub(evals[i].Timestamp).Seconds())
				if evals[i].TimeInTradeSeconds < 0 {
					evals[i].TimeInTradeSeconds = 0
				}
			}
		}
	}
	return evals, nil
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

	evals, err := s.loadAIEvaluations(r.Context(), limit)
	if err != nil {
		s.log.Error("failed to list ai evaluations", "event", "ai_evaluations_list_failed", "error", err,
			"request_id", logging.RequestID(r.Context()))
		s.writeRouteError(w, r, http.StatusInternalServerError, "failed to list evaluations: "+err.Error(), nil)
		return
	}

	s.writeRouteJSON(w, r, http.StatusOK, evaluationsResponse{Evaluations: evals})
}

type aiSignalRow struct {
	Time         string
	Setup        string
	SourceLabel  string
	SourceColor  template.CSS
	Action       string
	ActionColor  template.CSS
	Entry        string
	LiveExit     template.HTML
	Target       string
	Stop         string
	Score        template.HTML
	Pnl          template.HTML
	Status       template.HTML
	RemoveButton template.HTML
}

type aiSignalsRowsData struct {
	Rows []aiSignalRow
}

// handleAISignalsRows renders the AI calls table body server-side. The modal
// still uses the JSON stats endpoint for the metric cards, but the expensive
// 50-row table no longer has to be rebuilt in JavaScript.
func (s *Server) handleAISignalsRows(w http.ResponseWriter, r *http.Request) {
	evals, err := s.loadAIEvaluations(r.Context(), 50)
	if err != nil {
		s.log.Error("failed to load ai evaluations for partial", "event", "ai_signals_partial_failed",
			"error", err, "request_id", logging.RequestID(r.Context()))
		http.Error(w, "failed to load evaluations", http.StatusInternalServerError)
		return
	}
	cfg := s.configSnapshot()
	loc := cfg.Location()
	if loc == nil {
		loc = time.UTC
	}
	rows := make([]aiSignalRow, 0, len(evals))
	for _, eval := range evals {
		rows = append(rows, buildAISignalRow(eval, loc, cfg.AI.RoundTripCostPct))
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.tmpl.ExecuteTemplate(w, "ai-signals-rows", aiSignalsRowsData{Rows: rows}); err != nil {
		s.log.Error("render ai signals rows", "event", "ai_signals_render_failed", "error", err)
	}
}

func buildAISignalRow(e store.Evaluation, loc *time.Location, costPct float64) aiSignalRow {
	timeStr := "—"
	if !e.Timestamp.IsZero() {
		timeStr = e.Timestamp.In(loc).Format("15:04:05")
	}

	parts := strings.Split(e.InstrumentKey, "|")
	strikeVal, optType := "", ""
	if len(parts) >= 4 {
		strikeVal = parts[3]
	}
	if len(parts) >= 5 {
		optType = parts[4]
	}
	setup := strings.TrimSpace(e.Underlying + " " + strikeVal + " " + optType)
	if setup == "" {
		setup = e.InstrumentKey
	}

	source := strings.ToLower(strings.TrimSpace(e.Source))
	sourceLabel := "⚡ Jev"
	sourceColor := template.CSS("var(--text-muted,#888)")
	switch {
	case strings.Contains(source, "agent+jev"):
		sourceLabel = "🤝 Agent+Jev"
		sourceColor = template.CSS("var(--accent,#f59e0b)")
	case strings.Contains(source, "agent"):
		sourceLabel = "🧠 Agent"
		sourceColor = template.CSS("var(--accent,#f59e0b)")
	case strings.Contains(source, "manual"):
		sourceLabel = "✍️ Manual"
	}

	actionColor := template.CSS("var(--green,#22c55e)")
	if strings.Contains(strings.ToUpper(e.Action), "PE") {
		actionColor = template.CSS("var(--red,#ef4444)")
	}

	pending := strings.EqualFold(e.Status, "PENDING")
	current := e.CurrentPrice
	liveExit := template.HTML("—")
	if pending {
		if current > 0 {
			liveExit = template.HTML("₹" + formatIndian(current)) // #nosec G203 -- server-formatted number
		}
	} else if e.ExitPrice > 0 {
		liveExit = template.HTML("₹" + formatIndian(e.ExitPrice)) // #nosec G203 -- server-formatted number
	}

	pnlValue := e.PnLPct
	if pending && current > 0 && e.OptionLTP > 0 {
		pnlValue = ((current-e.OptionLTP)/e.OptionLTP)*100 - costPct
	}
	pnlHTML := template.HTML("—")
	if (pending && current > 0) || (!pending && e.PnLPct != 0) {
		color := "var(--green,#22c55e)"
		if pnlValue < 0 {
			color = "var(--red,#ef4444)"
		}
		prefix := ""
		if pnlValue > 0 {
			prefix = "+"
		}
		opacity := ""
		if pending {
			opacity = " opacity:0.85;"
		}
		pnlSpan := fmt.Sprintf(
			`<span style="font-weight:700; color:%s;%s">%s%s%%</span>`,
			color, opacity, prefix, formatIndian(pnlValue))
		pnlHTML = template.HTML(pnlSpan) // #nosec G203 -- server-formatted number and CSS
	}

	scoreHTML := template.HTML("—")
	if e.Score > 0 {
		scoreSpan := fmt.Sprintf(`%d/5 <span style="color:var(--text-muted,#888);">· %d%%</span>`,
			e.Score, int(math.Round(e.Confidence*100)))
		scoreHTML = template.HTML(scoreSpan) // #nosec G203 -- integers from the database
	}

	label := html.EscapeString(setup)
	removeHTML := fmt.Sprintf(
		`<button type="button" class="ai-call-remove" data-ai-eval-id="%d" data-ai-eval-label="%s" title="Remove this call (stop tracking)" aria-label="Remove call %s">✕</button>`,
		e.ID, label, label)
	removeButton := template.HTML(removeHTML) // #nosec G203 -- label is HTML-escaped above

	return aiSignalRow{
		Time:         timeStr,
		Setup:        setup,
		SourceLabel:  sourceLabel,
		SourceColor:  sourceColor,
		Action:       e.Action,
		ActionColor:  actionColor,
		Entry:        "₹" + formatIndian(e.OptionLTP),
		LiveExit:     liveExit,
		Target:       "₹" + formatIndian(e.TargetPrice),
		Stop:         "₹" + formatIndian(e.StopLossPrice),
		Score:        scoreHTML,
		Pnl:          pnlHTML,
		Status:       aiStatusBadge(e.Status),
		RemoveButton: removeButton,
	}
}

func aiStatusBadge(status string) template.HTML {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "TARGET_HIT":
		return template.HTML(`<span class="ai-status-pill ai-status-target">TARGET HIT</span>`)
	case "SL_HIT":
		return template.HTML(`<span class="ai-status-pill ai-status-sl">SL HIT</span>`)
	case "EXPIRED":
		return template.HTML(`<span class="ai-status-pill" style="background:rgba(148,163,184,0.15); color:var(--text-muted,#888);">EXPIRED</span>`)
	case "REJECTED":
		return template.HTML(`<span class="ai-status-pill" style="background:rgba(148,163,184,0.15); color:var(--text-muted,#888);">REJECTED</span>`)
	default:
		return template.HTML(`<span class="ai-status-pill ai-status-pending">PENDING</span>`)
	}
}

// formatIndian formats a number with Indian digit grouping (12,34,567.89).
func formatIndian(v float64) string {
	raw := strconv.FormatFloat(v, 'f', 2, 64)
	neg := strings.HasPrefix(raw, "-")
	if neg {
		raw = raw[1:]
	}
	intPart, frac := raw, ""
	if i := strings.IndexByte(raw, '.'); i >= 0 {
		intPart, frac = raw[:i], raw[i:]
	}
	if len(intPart) > 3 {
		last3 := intPart[len(intPart)-3:]
		rest := intPart[:len(intPart)-3]
		var groups []string
		for len(rest) > 2 {
			groups = append([]string{rest[len(rest)-2:]}, groups...)
			rest = rest[:len(rest)-2]
		}
		if rest != "" {
			groups = append([]string{rest}, groups...)
		}
		intPart = strings.Join(groups, ",") + "," + last3
	}
	out := intPart + frac
	if neg {
		out = "-" + out
	}
	return out
}

type deleteEvaluationResponse struct {
	Success bool  `json:"success"`
	ID      int64 `json:"id"`
}

// handleDeleteAIEvaluation removes a stored evaluation and stops the evaluator
// from tracking it against live ticks.
func (s *Server) handleDeleteAIEvaluation(w http.ResponseWriter, r *http.Request) {
	if s.aiStore == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "ai store not configured", nil)
		return
	}

	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid evaluation id", err)
		return
	}

	// Stop tick tracking first so no outcome can be recorded after deletion.
	if s.evaluator != nil {
		s.evaluator.UntrackEvaluation(id)
	}

	if err := s.aiStore.DeleteEvaluation(r.Context(), id); err != nil {
		s.log.Error("failed to delete ai evaluation", "event", "ai_evaluation_delete_failed", "id", id, "error", err,
			"request_id", logging.RequestID(r.Context()))
		s.writeRouteError(w, r, http.StatusInternalServerError, "failed to delete evaluation: "+err.Error(), nil)
		return
	}

	s.log.Info("ai evaluation deleted", "event", "ai_evaluation_deleted", "id", id,
		"request_id", logging.RequestID(r.Context()))

	s.writeRouteJSON(w, r, http.StatusOK, deleteEvaluationResponse{Success: true, ID: id})
}

type evaluationStatsResponse struct {
	Stats            store.EvaluationStats    `json:"stats"`
	Scanner          *evaluator.ScannerStatus `json:"scanner,omitempty"`
	TargetPct        float64                  `json:"target_pct,omitempty"`
	StopLossPct      float64                  `json:"stop_loss_pct,omitempty"`
	RoundTripCostPct float64                  `json:"round_trip_cost_pct,omitempty"`
}

func (s *Server) handleGetAIEvaluationStats(w http.ResponseWriter, r *http.Request) {
	var stats store.EvaluationStats
	if s.aiStore != nil {
		var err error
		stats, err = s.aiStore.GetEvaluationStats(r.Context())
		if err != nil {
			s.log.Error("failed to get ai evaluation stats", "event", "ai_evaluation_stats_failed", "error", err,
				"request_id", logging.RequestID(r.Context()))
			s.writeRouteError(w, r, http.StatusInternalServerError, "failed to get evaluation stats: "+err.Error(), nil)
			return
		}
	}

	var scannerStatus *evaluator.ScannerStatus
	if s.evaluator != nil {
		st := s.evaluator.ScannerStatus()
		scannerStatus = &st
	}

	cfg := s.configSnapshot()
	s.writeRouteJSON(w, r, http.StatusOK, evaluationStatsResponse{
		Stats:            stats,
		Scanner:          scannerStatus,
		TargetPct:        cfg.AI.TargetPct,
		StopLossPct:      cfg.AI.StopLossPct,
		RoundTripCostPct: cfg.AI.RoundTripCostPct,
	})
}

type triggerScanResponse struct {
	Success     bool                     `json:"success"`
	Evaluations []store.Evaluation       `json:"evaluations"`
	Scanner     *evaluator.ScannerStatus `json:"scanner,omitempty"`
	Message     string                   `json:"message,omitempty"`
}

func (s *Server) handleTriggerAIScan(w http.ResponseWriter, r *http.Request) {
	if s.evaluator == nil || (s.evaluator.JevClient() == nil && s.evaluator.AgentClient() == nil) {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "ai evaluator or ai provider (Jev/Agent) not configured", nil)
		return
	}

	underlying := strings.TrimSpace(r.URL.Query().Get("underlying"))
	if underlying == "" {
		underlying = "NIFTY"
	}

	cfg := s.configSnapshot()
	if !cfg.Trading.Session.AllowOutsideHours && !s.IsUnderlyingMarketOpen(underlying) {
		s.writeRouteError(w, r, http.StatusBadRequest,
			fmt.Sprintf("market is closed for %s; trading hours are 09:15-15:30 IST (09:00-23:55 IST for MCX)", underlying), nil)
		return
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

	var scannerStatus *evaluator.ScannerStatus
	if s.evaluator != nil {
		st := s.evaluator.ScannerStatus()
		scannerStatus = &st
	}

	s.writeRouteJSON(w, r, http.StatusOK, triggerScanResponse{
		Success:     true,
		Evaluations: evals,
		Scanner:     scannerStatus,
		Message:     fmt.Sprintf("Scan completed for %s: %d paper call(s) opened", underlying, len(evals)),
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
	// Prefer the evaluator's live clients: they are rebuilt when settings change.
	var agentClient *agent.Client
	if s.evaluator != nil {
		agentClient = s.evaluator.AgentClient()
	}
	if agentClient == nil {
		agentClient = s.agentClient
	}
	jevConfigured := s.evaluator != nil && s.evaluator.JevClient() != nil

	if !jevConfigured && agentClient == nil {
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
	var evalErr error

	if s.evaluator != nil {
		eval, evalErr = s.evaluator.EvaluateContract(r.Context(), evaluator.ContractSetup{
			Underlying:    req.Underlying,
			InstrumentKey: req.InstrumentKey,
			Symbol:        req.Symbol,
			Strike:        req.Strike,
			OptionType:    req.OptionType,
			OptionLTP:     req.LTP,
		})
	} else {
		// Legacy agent-only fallback used by tests / minimal deployments.
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

		agentTimeout := 40 * time.Second
		s.configMu.RLock()
		if s.cfg.AI.Agent.TimeoutMS > 0 {
			agentTimeout = time.Duration(s.cfg.AI.Agent.TimeoutMS) * time.Millisecond
		}
		s.configMu.RUnlock()

		agentCtx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		decision, aerr := agentClient.EvaluateContract(agentCtx, agent.ContractSetup{
			Underlying:    req.Underlying,
			SpotPrice:     spot,
			OptionType:    strings.ToUpper(strings.TrimSpace(req.OptionType)),
			Strike:        req.Strike,
			OptionLTP:     req.LTP,
			MacroBias:     macroBias,
			OrderBookDesc: "real-time market depth",
		})
		if aerr != nil {
			s.log.Error("ai agent evaluation failed", "event", "ai_agent_fallback_failed", "error", aerr)
			s.writeRouteError(w, r, http.StatusServiceUnavailable, "AI evaluation failed: "+aerr.Error(), aerr)
			return
		}

		action := strings.ToUpper(strings.TrimSpace(decision.Action))
		optionType := strings.ToUpper(strings.TrimSpace(req.OptionType))
		if optionType != "PE" {
			optionType = "CE"
		}
		actionable := action == "BUY" || strings.HasPrefix(action, "BUY_") || action == "LONG"

		eval = store.Evaluation{
			Timestamp:     time.Now(),
			InstrumentKey: req.InstrumentKey,
			Underlying:    req.Underlying,
			SpotPrice:     spot,
			OptionLTP:     req.LTP,
			Confidence:    decision.Confidence,
			Score:         decision.Score,
			StateSnapshot: fmt.Sprintf(`{"agent_model":"%s","source":"agent"}`, agentModel),
			Shadow:        shadowMode,
			Source:        "manual+agent",
			Reasoning:     decision.Reasoning,
		}

		if actionable {
			eval.Action = "BUY_" + optionType
			eval.TargetPrice = math.Round(req.LTP*(1.0+targetPct/100.0)*100) / 100
			eval.StopLossPrice = math.Round(req.LTP*(1.0-stopLossPct/100.0)*100) / 100
			eval.Status = "PENDING"

			if s.aiStore != nil {
				id, serr := s.aiStore.SaveEvaluation(r.Context(), eval)
				if serr != nil {
					s.log.Error("failed to save agent paper call", "event", "ai_paper_call_save_failed", "error", serr)
				} else {
					eval.ID = id
				}
			}
		} else {
			eval.Action = action
			eval.Status = "REJECTED"
		}
	}

	if evalErr != nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "AI evaluation failed: "+evalErr.Error(), evalErr)
		return
	}

	var agentAnalysis string
	var reasoning string
	if eval.Status == "REJECTED" {
		reasoning = fmt.Sprintf("Model returned %s; no paper position taken.", eval.Action)
	} else {
		reasoning = fmt.Sprintf("Conviction score %d/5 with confidence %.0f%% (source %s).",
			eval.Score, eval.Confidence*100, eval.Source)
	}

	if strings.Contains(eval.Source, "agent") && strings.TrimSpace(eval.Reasoning) != "" {
		agentAnalysis = strings.TrimSpace(eval.Reasoning)
	} else if agentClient != nil {
		agentCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		prompt := fmt.Sprintf("Underlying %s spot, evaluating %s strike %s at ₹%.2f. System 1 verdict is %s (score %d/5, confidence %.2f). Give a concise 1-sentence tactical recommendation on entering this scalp setup.",
			req.Underlying, req.OptionType, req.Strike, req.LTP, eval.Action, eval.Score, eval.Confidence)
		if reply, aerr := agentClient.Chat(agentCtx, prompt); aerr == nil && reply != "" {
			agentAnalysis = strings.TrimSpace(reply)
		}
	}

	s.log.Info("ai contract ask evaluated", "event", "ai_ask_evaluated",
		"instrument", req.InstrumentKey, "action", eval.Action, "score", eval.Score, "conf", eval.Confidence,
		"target", eval.TargetPrice, "stop", eval.StopLossPrice, "status", eval.Status,
		"source", eval.Source, "request_id", logging.RequestID(r.Context()))

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
