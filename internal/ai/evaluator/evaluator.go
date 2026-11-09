// Package evaluator coordinates periodic market scanning with a reasoning agent
// (System 2 candidate generation), TypeSafe Jev (System 1 validation), and
// paper-only outcome tracking against live feed ticks.
package evaluator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/akshaykhairmode/scalper/internal/ai/agent"
	"github.com/akshaykhairmode/scalper/internal/ai/features"
	"github.com/akshaykhairmode/scalper/internal/ai/jev"
	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/store"
)

// OutcomeCallback is invoked when an active call reaches Target, Stop-Loss, or expiry.
type OutcomeCallback func(eval store.Evaluation)

// NewCallCallback is invoked when a new validated paper call is opened.
type NewCallCallback func(eval store.Evaluation)

// TrackCallback is invoked when a paper call starts being tracked so the server
// can subscribe the contract to the live feed.
type TrackCallback func(eval store.Evaluation)

// ScanStatusCallback is invoked when periodic scanner timing or execution status updates.
type ScanStatusCallback func(status ScannerStatus)

// ActiveCall tracks an open paper position awaiting a market outcome.
type ActiveCall struct {
	ID            int64
	InstrumentKey string
	Underlying    string
	Action        string
	EntryPrice    float64
	TargetPrice   float64
	StopLossPrice float64
	Score         int
	Confidence    float64
	Source        string
	Reasoning     string
	CostPct       float64
	CreatedAt     time.Time
	LastPrice     float64
	LastTickAt    time.Time
}

// QuoteProvider returns current market quote data for an instrument or underlying.
type QuoteProvider interface {
	GetSpotPrice(ctx context.Context, underlying string) (float64, error)
	GetATMStrikes(ctx context.Context, underlying string) (ceKey, peKey string, ceLTP, peLTP float64, err error)
	GetMarketDepth(ctx context.Context, instrumentKey string) (features.OrderBookMetrics, error)
}

// marketSnapshotProvider optionally provides depth plus contract metadata in one call.
type marketSnapshotProvider interface {
	GetMarketSnapshot(ctx context.Context, instrumentKey string) (features.OrderBookMetrics, features.OptionInfo, error)
}

// liveQuoteProvider optionally provides a REST LTP fallback when the live feed
// is not delivering ticks for a tracked contract.
type liveQuoteProvider interface {
	GetOptionLTP(ctx context.Context, instrumentKey string) (float64, error)
}

// chainContextProvider optionally provides a near-ATM option ladder plus
// chain-level statistics (PCR, max pain, OI walls) for one underlying.
type chainContextProvider interface {
	GetChainContext(ctx context.Context, underlying string, spot float64) (features.ChainContext, error)
}

// underlyingContextProvider optionally provides index/commodity context such as
// day OHLC, VWAP, India VIX and time left in the session.
type underlyingContextProvider interface {
	GetUnderlyingContext(ctx context.Context, underlying string) (features.UnderlyingContext, error)
}

// AIStore abstracts database storage operations required by the evaluator.
type AIStore interface {
	SaveEvaluation(ctx context.Context, e store.Evaluation) (int64, error)
	UpdateEvaluationOutcome(ctx context.Context, id int64, status string, exitPrice, pnlPct float64, exitTime time.Time) error
	GetActiveEvaluations(ctx context.Context) ([]store.Evaluation, error)
	GetEvaluationStats(ctx context.Context) (store.EvaluationStats, error)
	ListEvaluations(ctx context.Context, limit int) ([]store.Evaluation, error)
	GetLatestPlaybook(ctx context.Context) (*store.Playbook, error)
}

// Evaluator manages periodic AI evaluations and live feed outcome tracking.
type Evaluator struct {
	cfgMu sync.RWMutex
	cfg   config.AI
	store AIStore
	log   *slog.Logger

	clientMu    sync.RWMutex
	jevClient   *jev.Client
	agentClient *agent.Client

	quoteProvider QuoteProvider

	activeMu    sync.RWMutex
	activeCalls map[int64]*ActiveCall
	activeCount int64 // atomic fast-path counter for OnTick

	ringMu    sync.RWMutex
	tickRings map[string]*features.TickRing

	lastSignalMu sync.Mutex
	lastSignal   map[string]time.Time

	onOutcome    OutcomeCallback
	onNewCall    NewCallCallback
	onTrack      TrackCallback
	onScanStatus ScanStatusCallback

	baseMu     sync.RWMutex
	baseCtx    context.Context
	scanCancel context.CancelFunc
	scanDone   chan struct{}
	running    atomic.Bool
	isScanning atomic.Bool

	scanMu           sync.RWMutex
	nextScanTime     time.Time
	lastScanTime     time.Time
	lastScanError    string
	lastCandidateCnt int
	lastCallsTaken   int

	// scanRunMu serializes scan cycles so the periodic scanner and a manual
	// "Scan Now" can never evaluate the same underlying concurrently and open
	// duplicate paper positions.
	scanRunMu sync.Mutex
}

// Options configures the Evaluator instance.
type Options struct {
	Config        config.AI
	Store         AIStore
	JevClient     *jev.Client
	AgentClient   *agent.Client
	QuoteProvider QuoteProvider
	Logger        *slog.Logger
	OnOutcome     OutcomeCallback
	OnNewCall     NewCallCallback
	OnTrack       TrackCallback
	OnScanStatus  ScanStatusCallback
}

// New creates an Evaluator ready to start scanning and tracking.
func New(opts Options) *Evaluator {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	e := &Evaluator{
		cfg:           opts.Config,
		store:         opts.Store,
		jevClient:     opts.JevClient,
		agentClient:   opts.AgentClient,
		quoteProvider: opts.QuoteProvider,
		log:           logger,
		activeCalls:   make(map[int64]*ActiveCall),
		tickRings:     make(map[string]*features.TickRing),
		lastSignal:    make(map[string]time.Time),
		onOutcome:     opts.OnOutcome,
		onNewCall:     opts.OnNewCall,
		onTrack:       opts.OnTrack,
		onScanStatus:  opts.OnScanStatus,
	}

	e.restorePendingCalls()
	return e
}

func (e *Evaluator) restorePendingCalls() {
	if e.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pending, err := e.store.GetActiveEvaluations(ctx)
	if err != nil {
		e.log.Warn("failed to restore pending ai calls", "event", "ai_restore_failed", "error", err)
		return
	}

	settings := e.scanSettings()
	now := time.Now()
	for _, p := range pending {
		expired := !isActionableAction(p.Action)
		if !expired && settings.maxHold > 0 && now.Sub(p.Timestamp) >= settings.maxHold {
			expired = true
		}
		if !expired && settings.maxHold <= 0 && p.Timestamp.Format("2006-01-02") != now.Format("2006-01-02") {
			expired = true
		}
		if expired {
			exit := p.OptionLTP
			_ = e.store.UpdateEvaluationOutcome(ctx, p.ID, "EXPIRED", exit, 0, now)
			continue
		}

		call := &ActiveCall{
			ID:            p.ID,
			InstrumentKey: p.InstrumentKey,
			Underlying:    p.Underlying,
			Action:        p.Action,
			EntryPrice:    p.OptionLTP,
			TargetPrice:   p.TargetPrice,
			StopLossPrice: p.StopLossPrice,
			Score:         p.Score,
			Confidence:    p.Confidence,
			Source:        p.Source,
			Reasoning:     p.Reasoning,
			CostPct:       settings.costPct,
			CreatedAt:     p.Timestamp,
			LastPrice:     p.OptionLTP,
			LastTickAt:    p.Timestamp,
		}
		e.activeCalls[p.ID] = call
		e.noteSignal(p.Underlying, p.Timestamp)
	}

	atomic.StoreInt64(&e.activeCount, int64(len(e.activeCalls)))
	if len(e.activeCalls) == 0 {
		return
	}

	e.log.Info("restored active ai paper calls", "event", "ai_calls_restored", "count", len(e.activeCalls))
	for _, call := range e.activeCalls {
		e.notifyTracked(call)
	}
}

// Start begins the background periodic evaluation scanner and position monitor if enabled.
func (e *Evaluator) Start(ctx context.Context) {
	if e == nil {
		return
	}
	e.cfgMu.RLock()
	enabled := e.cfg.Enabled && e.cfg.PeriodicScan
	intervalMin := e.cfg.ScanIntervalMinutes
	e.cfgMu.RUnlock()

	if !enabled {
		e.log.Info("ai evaluator periodic scanner is disabled", "event", "ai_evaluator_disabled")
		return
	}
	if intervalMin <= 0 {
		intervalMin = 15
	}

	e.baseMu.Lock()
	if ctx != nil && ctx.Err() == nil {
		e.baseCtx = ctx
	} else if e.baseCtx == nil || e.baseCtx.Err() != nil {
		e.baseCtx = context.Background()
	}
	baseCtx := e.baseCtx
	e.baseMu.Unlock()

	if e.running.Swap(true) {
		return // already running
	}

	scanCtx, cancel := context.WithCancel(baseCtx)
	e.scanCancel = cancel
	done := make(chan struct{})
	e.scanDone = done

	e.scanMu.Lock()
	e.nextScanTime = time.Now().Add(5 * time.Second)
	e.scanMu.Unlock()

	if e.onScanStatus != nil {
		e.onScanStatus(e.ScannerStatus())
	}

	go e.runScanner(scanCtx, time.Duration(intervalMin)*time.Minute, done)
	go e.runPositionMonitor(scanCtx)
}

// scannerStopTimeout bounds how long Stop waits for an in-flight scanner run.
const scannerStopTimeout = 20 * time.Second

// Stop cleanly terminates the periodic evaluation scanner.
func (e *Evaluator) Stop() {
	if e == nil || !e.running.Swap(false) {
		return
	}
	if e.scanCancel != nil {
		e.scanCancel()
	}
	if e.scanDone != nil {
		// Bound the wait so a slow quote provider can never wedge config saves.
		select {
		case <-e.scanDone:
		case <-time.After(scannerStopTimeout):
			e.log.Warn("ai evaluator scanner stop timed out", "event", "ai_scanner_stop_timeout",
				"timeout", scannerStopTimeout)
		}
	}
	e.scanMu.Lock()
	e.nextScanTime = time.Time{}
	e.scanMu.Unlock()

	if e.onScanStatus != nil {
		e.onScanStatus(e.ScannerStatus())
	}
}

// ScannerStatus represents the current state and timing of the background evaluation scanner.
type ScannerStatus struct {
	Enabled           bool      `json:"enabled"`
	Running           bool      `json:"running"`
	Scanning          bool      `json:"scanning"`
	IntervalMinutes   int       `json:"interval_minutes"`
	NextScanTime      time.Time `json:"next_scan_time,omitempty"`
	NextScanUnix      int64     `json:"next_scan_unix"`
	SecondsUntilNext  int       `json:"seconds_until_next"`
	LastScanTime      time.Time `json:"last_scan_time,omitempty"`
	LastScanUnix      int64     `json:"last_scan_unix"`
	LastError         string    `json:"last_error,omitempty"`
	LastCandidateCnt  int       `json:"last_candidate_count"`
	LastCallsTaken    int       `json:"last_calls_taken"`
	AnyMarketOpen     bool      `json:"any_market_open"`
	OpenUnderlyings   []string  `json:"open_underlyings,omitempty"`
	ClosedUnderlyings []string  `json:"closed_underlyings,omitempty"`
}

// ScannerStatus returns a snapshot of the periodic evaluation scanner state and timing.
func (e *Evaluator) ScannerStatus() ScannerStatus {
	if e == nil {
		return ScannerStatus{}
	}
	e.cfgMu.RLock()
	enabled := e.cfg.Enabled && e.cfg.PeriodicScan
	interval := e.cfg.ScanIntervalMinutes
	if interval <= 0 {
		interval = 15
	}
	e.cfgMu.RUnlock()

	running := e.running.Load()
	scanning := e.isScanning.Load()

	e.scanMu.RLock()
	next := e.nextScanTime
	last := e.lastScanTime
	lastErr := e.lastScanError
	lastCandidates := e.lastCandidateCnt
	lastCalls := e.lastCallsTaken
	e.scanMu.RUnlock()

	var secUntil int
	var nextUnix int64
	if running && !next.IsZero() {
		nextUnix = next.Unix()
		diff := int(time.Until(next).Seconds())
		if diff < 0 {
			diff = 0
		}
		secUntil = diff
	}

	var lastUnix int64
	if !last.IsZero() {
		lastUnix = last.Unix()
	}

	var configured []string
	if uProvider, ok := e.quoteProvider.(interface{ GetConfiguredUnderlyings() []string }); ok {
		configured = uProvider.GetConfiguredUnderlyings()
	}
	if len(configured) == 0 {
		configured = []string{"NIFTY", "SENSEX"}
	}

	var openUnderlyings []string
	var closedUnderlyings []string
	anyOpen := false

	if checker, ok := e.quoteProvider.(interface{ IsUnderlyingMarketOpen(string) bool }); ok {
		for _, u := range configured {
			if checker.IsUnderlyingMarketOpen(u) {
				openUnderlyings = append(openUnderlyings, u)
				anyOpen = true
			} else {
				closedUnderlyings = append(closedUnderlyings, u)
			}
		}
	} else {
		openUnderlyings = configured
		anyOpen = true
	}

	return ScannerStatus{
		Enabled:           enabled,
		Running:           running,
		Scanning:          scanning,
		IntervalMinutes:   interval,
		NextScanTime:      next,
		NextScanUnix:      nextUnix,
		SecondsUntilNext:  secUntil,
		LastScanTime:      last,
		LastScanUnix:      lastUnix,
		LastError:         lastErr,
		LastCandidateCnt:  lastCandidates,
		LastCallsTaken:    lastCalls,
		AnyMarketOpen:     anyOpen,
		OpenUnderlyings:   openUnderlyings,
		ClosedUnderlyings: closedUnderlyings,
	}
}

// UpdateConfig hot-reloads configuration (e.g. on settings save) and adjusts the scanner.
func (e *Evaluator) UpdateConfig(ctx context.Context, newCfg config.AI) {
	if e == nil {
		return
	}
	e.cfgMu.Lock()
	oldCfg := e.cfg
	e.cfg = newCfg
	e.cfgMu.Unlock()

	e.baseMu.Lock()
	if ctx != nil && ctx.Err() == nil {
		e.baseCtx = ctx
	}
	baseCtx := e.baseCtx
	e.baseMu.Unlock()

	// Update Jev client if URL, key, or model changed.
	if newCfg.Jev.BaseURL != oldCfg.Jev.BaseURL || newCfg.Jev.APIKey != oldCfg.Jev.APIKey || newCfg.Jev.Model != oldCfg.Jev.Model || newCfg.Jev.TimeoutMS != oldCfg.Jev.TimeoutMS {
		e.clientMu.Lock()
		e.jevClient = jev.NewClient(jev.Config{
			BaseURL: newCfg.Jev.BaseURL,
			APIKey:  newCfg.Jev.APIKey,
			Model:   newCfg.Jev.Model,
			Timeout: time.Duration(newCfg.Jev.TimeoutMS) * time.Millisecond,
		})
		e.clientMu.Unlock()
	}
	// Update Agent client if provider, URL, key, or model changed.
	if newCfg.Agent.BaseURL != oldCfg.Agent.BaseURL || newCfg.Agent.APIKey != oldCfg.Agent.APIKey || newCfg.Agent.Model != oldCfg.Agent.Model || newCfg.Agent.Provider != oldCfg.Agent.Provider || newCfg.Agent.TimeoutMS != oldCfg.Agent.TimeoutMS {
		e.clientMu.Lock()
		e.agentClient = agent.NewClient(agent.Config{
			Provider: newCfg.Agent.Provider,
			BaseURL:  newCfg.Agent.BaseURL,
			APIKey:   newCfg.Agent.APIKey,
			Model:    newCfg.Agent.Model,
			Timeout:  time.Duration(newCfg.Agent.TimeoutMS) * time.Millisecond,
		})
		e.clientMu.Unlock()
	}

	wasEnabled := oldCfg.Enabled && oldCfg.PeriodicScan
	nowEnabled := newCfg.Enabled && newCfg.PeriodicScan

	if !nowEnabled && wasEnabled {
		e.Stop()
	} else if nowEnabled && (!wasEnabled || newCfg.ScanIntervalMinutes != oldCfg.ScanIntervalMinutes) {
		e.Stop()
		e.Start(baseCtx)
	} else if nowEnabled && e.onScanStatus != nil {
		e.onScanStatus(e.ScannerStatus())
	}
}

// OnTick is invoked for every tick received from Kotak WebSocket/HSM feed.
// It features a sub-microsecond atomic early return when there are no active calls.
func (e *Evaluator) OnTick(ctx context.Context, instrumentKey string, ltp float64) {
	if e == nil || ltp <= 0 {
		return
	}

	now := time.Now()
	e.pushTick(instrumentKey, ltp, now)

	if atomic.LoadInt64(&e.activeCount) == 0 {
		return
	}

	e.activeMu.Lock()
	var targets []*ActiveCall
	for _, call := range e.activeCalls {
		if call.InstrumentKey == instrumentKey || strings.EqualFold(call.InstrumentKey, instrumentKey) {
			call.LastPrice = ltp
			call.LastTickAt = now
			targets = append(targets, call)
		}
	}
	e.activeMu.Unlock()

	for _, call := range targets {
		var status string
		exitPrice := ltp

		if call.TargetPrice > 0 && ltp >= call.TargetPrice {
			status = "TARGET_HIT"
			// A resting target order fills at the target, not at the gapped tick.
			exitPrice = call.TargetPrice
		} else if call.StopLossPrice > 0 && ltp <= call.StopLossPrice {
			status = "SL_HIT"
			exitPrice = ltp
		}

		if status != "" {
			e.finalizeCall(ctx, call, status, exitPrice, now)
		}
	}
}

func (e *Evaluator) pushTick(instrumentKey string, ltp float64, now time.Time) {
	e.ringMu.Lock()
	ring, ok := e.tickRings[instrumentKey]
	if !ok {
		// Bound memory: evict an arbitrary old instrument when the ring map grows.
		if len(e.tickRings) >= 1024 {
			for key := range e.tickRings {
				delete(e.tickRings, key)
				break
			}
		}
		ring = features.NewTickRing(240)
		e.tickRings[instrumentKey] = ring
	}
	ring.Push(features.Tick{Price: ltp, Timestamp: now.UnixMilli()})
	e.ringMu.Unlock()
}

// ScanNow triggers an on-demand evaluation of active ATM strikes.
// When an agent is configured it generates candidates first and Jev validates
// them. Without an agent, Jev alone is used as the candidate generator.
func (e *Evaluator) ScanNow(ctx context.Context, underlying string) ([]store.Evaluation, error) {
	if e == nil {
		return nil, errors.New("evaluator: nil instance")
	}
	jevClient := e.JevClient()
	agentClient := e.AgentClient()
	if jevClient == nil && agentClient == nil {
		return nil, errors.New("evaluator: neither jev nor agent client is configured")
	}
	if e.quoteProvider == nil {
		return nil, errors.New("evaluator: quote provider not configured")
	}

	if underlying == "" {
		underlying = "NIFTY"
	}

	e.scanRunMu.Lock()
	defer e.scanRunMu.Unlock()

	e.isScanning.Store(true)
	defer e.isScanning.Store(false)

	e.scanMu.Lock()
	e.lastScanTime = time.Now()
	e.lastScanError = ""
	e.lastCandidateCnt = 0
	e.lastCallsTaken = 0
	e.scanMu.Unlock()

	settings := e.scanSettings()

	e.expireStaleCalls(ctx, settings)

	spot, err := e.quoteProvider.GetSpotPrice(ctx, underlying)
	if err != nil {
		e.noteScanFailure(err)
		return nil, fmt.Errorf("evaluator: get spot price: %w", err)
	}

	var chainCtx features.ChainContext
	candidates := []scanCandidate{}
	if provider, ok := e.quoteProvider.(chainContextProvider); ok {
		if cc, cerr := provider.GetChainContext(ctx, underlying, spot); cerr == nil && len(cc.Candidates) > 0 {
			chainCtx = cc
			candidates = e.buildCandidatesFromInfo(ctx, spot, cc.Candidates)
		}
	}
	if len(candidates) == 0 {
		ceKey, peKey, ceLTP, peLTP, aerr := e.quoteProvider.GetATMStrikes(ctx, underlying)
		if aerr != nil {
			e.noteScanFailure(aerr)
			return nil, fmt.Errorf("evaluator: get atm strikes: %w", aerr)
		}
		candidates = e.buildCandidates(ctx, underlying, spot, ceKey, peKey, ceLTP, peLTP)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	e.scanMu.Lock()
	e.lastCandidateCnt = len(candidates)
	e.scanMu.Unlock()

	pb := &store.Playbook{}
	if e.store != nil {
		if loaded, perr := e.store.GetLatestPlaybook(ctx); perr == nil && loaded != nil {
			pb = loaded
		}
	}
	bias, regime, narrative := pb.Bias, pb.Regime, pb.Narrative

	underlyingCtx := features.UnderlyingContext{SpotPrice: spot}
	if provider, ok := e.quoteProvider.(underlyingContextProvider); ok {
		if uc, uerr := provider.GetUnderlyingContext(ctx, underlying); uerr == nil {
			underlyingCtx = uc
			if underlyingCtx.SpotPrice <= 0 {
				underlyingCtx.SpotPrice = spot
			}
		}
	}

	decisions := make(map[string]agent.CandidateDecision)
	if agentClient != nil {
		req := agent.CandidateRequest{
			Timestamp:         time.Now().Format(time.RFC3339),
			Underlying:        underlying,
			SpotPrice:         spot,
			MacroBias:         bias,
			Regime:            regime,
			Narrative:         narrative,
			UnderlyingContext: underlyingCtx,
			KeySupport:        pb.KeySupport,
			KeyResistance:     pb.KeyResistance,
			MaxPain:           firstNonZeroFloat(pb.MaxPain, chainCtx.MaxPain),
			PCR:               firstNonZeroFloat(pb.PCR, chainCtx.PCR),
			CallOIWall:        chainCtx.CallOIWall,
			PutOIWall:         chainCtx.PutOIWall,
			JevRules:          pb.JevRulesJSON,
			OpenPositions:     e.openPositionsSnapshot(),
			Candidates:        make([]agent.CandidateContract, 0, len(candidates)),
		}
		for _, cand := range candidates {
			req.Candidates = append(req.Candidates, cand.agentInput())
		}

		agentStart := time.Now()
		agentDecisions, aerr := agentClient.EvaluateCandidates(ctx, req)
		if aerr != nil {
			e.log.Warn("agent candidate generation failed", "event", "ai_agent_candidates_failed",
				"underlying", underlying, "error", aerr)
			e.noteScanFailure(aerr)
			return nil, fmt.Errorf("evaluator: agent candidate generation failed: %w", aerr)
		}
		e.log.Info("agent candidate generation finished", "event", "ai_agent_candidates_ok",
			"underlying", underlying, "contracts", len(candidates), "decisions", len(agentDecisions),
			"duration_ms", time.Since(agentStart).Milliseconds())
		decisions = matchAgentDecisions(candidates, agentDecisions)

		anyBuy := false
		for _, d := range decisions {
			if d.Action == "BUY" {
				anyBuy = true
				break
			}
		}
		if !anyBuy {
			e.log.Info("agent passed on all candidates", "event", "ai_agent_no_candidates", "underlying", underlying)
			return nil, nil
		}
	}

	var results []store.Evaluation
	for _, cand := range candidates {
		var agentDecision *agent.CandidateDecision
		if agentClient != nil {
			d, ok := decisions[cand.Key]
			if !ok || d.Action != "BUY" {
				continue
			}
			dc := d
			agentDecision = &dc
		}

		if !e.canOpenCall(underlying, cand.Key, settings.cooldown) {
			e.log.Info("skipping ai candidate: position or cooldown already active",
				"event", "ai_candidate_skipped", "underlying", underlying, "instrument", cand.Key)
			continue
		}

		snapshot := e.buildSnapshot(underlying, spot, bias, regime, cand, agentDecision, settings)
		stateJSON, _ := snapshot.ToJSON()

		var jevRes *jev.EvaluationResult
		if jevClient != nil {
			start := time.Now()
			res, jerr := jevClient.EvaluateSetup(ctx, stateJSON)
			durationMS := int(time.Since(start).Milliseconds())
			if jerr != nil {
				// Jev is the validation layer. If it cannot validate, do not open
				// a paper position and do not silently fall back to the agent.
				e.log.Warn("jev validation failed; skipping candidate", "event", "jev_validation_failed",
					"underlying", underlying, "instrument", cand.Key, "error", jerr)
				continue
			}
			res.DurationMS = durationMS
			jevRes = &res
		}

		approved := false
		if jevRes != nil {
			approved = approveJev(*jevRes, settings)
		} else if agentDecision != nil {
			approved = approveAgent(*agentDecision, settings)
		}
		if !approved {
			e.log.Info("ai candidate rejected by decision layer", "event", "ai_candidate_rejected",
				"underlying", underlying, "instrument", cand.Key,
				"jev_action", jevAction(jevRes), "jev_score", jevScore(jevRes), "jev_conf", jevConfidence(jevRes),
				"agent_score", agentScore(agentDecision), "agent_conf", agentConfidence(agentDecision))
			continue
		}

		source := "jev"
		if agentDecision != nil && jevRes != nil {
			source = "agent+jev"
		} else if agentDecision != nil {
			source = "agent"
		}

		score := jevScore(jevRes)
		confidence := jevConfidence(jevRes)
		if jevRes == nil && agentDecision != nil {
			score = agentDecision.Score
			confidence = agentDecision.Confidence
		}

		reasoning := buildReasoning(agentDecision, jevRes)

		eval, ok, cerr := e.createPaperCall(ctx, cand, underlying, spot, source, reasoning, score, confidence, stateJSON, settings)
		if cerr != nil {
			e.log.Error("failed to persist ai paper call", "event", "ai_paper_call_save_failed",
				"underlying", underlying, "instrument", cand.Key, "error", cerr)
			continue
		}
		if !ok {
			continue
		}
		results = append(results, eval)
	}

	e.scanMu.Lock()
	e.lastCallsTaken = len(results)
	e.scanMu.Unlock()

	if e.onScanStatus != nil {
		e.onScanStatus(e.ScannerStatus())
	}
	return results, nil
}

func (e *Evaluator) buildCandidates(ctx context.Context, _ string, spot float64, ceKey, peKey string, ceLTP, peLTP float64) []scanCandidate {
	raw := []struct {
		Key  string
		Type string
		LTP  float64
	}{
		{Key: ceKey, Type: "CE", LTP: ceLTP},
		{Key: peKey, Type: "PE", LTP: peLTP},
	}

	out := make([]scanCandidate, 0, len(raw))
	for _, item := range raw {
		if strings.TrimSpace(item.Key) == "" {
			continue
		}

		ltp := item.LTP
		if ltp <= 0 {
			if lp, ok := e.quoteProvider.(liveQuoteProvider); ok {
				if p, err := lp.GetOptionLTP(ctx, item.Key); err == nil {
					ltp = p
				}
			}
		}

		depth, info := e.marketSnapshot(ctx, item.Key)
		if info.OptionType == "" {
			info.OptionType = item.Type
		}
		if info.LTP > 0 {
			ltp = info.LTP
		}
		if ltp <= 0 {
			// Without a price there is no meaningful setup to evaluate.
			continue
		}
		strike := info.Strike
		if strike <= 0 {
			strike = parseStrikeFromKey(item.Key)
		}
		strikeLabel := ""
		if strike > 0 {
			strikeLabel = strconv.FormatFloat(strike, 'f', -1, 64)
		}

		vwap, ema9, ema21, rsi14, atr14, hasTicks := e.getTechnicalMetrics(item.Key, ltp)
		// Kotak's avg_cost is the day's volume-weighted average price. Prefer
		// it over the short tick-window VWAP when available.
		if info.AvgPrice > 0 {
			vwap = info.AvgPrice
		}
		distVWAP := 0.0
		if vwap > 0 {
			distVWAP = ((ltp - vwap) / vwap) * 100.0
		}

		moneyness := 0.0
		if spot > 0 && strike > 0 {
			if strings.EqualFold(info.OptionType, "PE") {
				moneyness = ((strike - spot) / spot) * 100.0
			} else {
				moneyness = ((spot - strike) / spot) * 100.0
			}
		}
		indicatorsSource := "placeholder"
		if hasTicks {
			indicatorsSource = "ticks"
		}

		out = append(out, scanCandidate{
			Key:              item.Key,
			Type:             strings.ToUpper(strings.TrimSpace(info.OptionType)),
			Strike:           strikeLabel,
			StrikePrice:      strike,
			LTP:              ltp,
			Expiry:           info.Expiry,
			PreviousClose:    info.PreviousClose,
			ChangePct:        info.ChangePct,
			DayOpen:          info.Open,
			DayHigh:          info.High,
			DayLow:           info.Low,
			AvgPrice:         info.AvgPrice,
			MoneynessPct:     moneyness,
			MoneynessLabel:   moneynessLabelFor(moneyness),
			VWAP:             vwap,
			EMA9:             ema9,
			EMA21:            ema21,
			RSI14:            rsi14,
			ATR14:            atr14,
			DistFromVWAPPct:  distVWAP,
			IndicatorsSource: indicatorsSource,
			Depth:            depth,
			OI:               info.OI,
			Volume:           info.Volume,
		})
	}
	return out
}

// buildCandidatesFromInfo converts a provider-supplied near-ATM ladder into
// evaluator candidates. Quotes and depth come from the chain response, so no
// extra per-contract quote request is needed.
func (e *Evaluator) buildCandidatesFromInfo(ctx context.Context, spot float64, infos []features.OptionInfo) []scanCandidate {
	out := make([]scanCandidate, 0, len(infos))
	seen := make(map[string]bool, len(infos))
	for _, info := range infos {
		key := strings.TrimSpace(info.InstrumentKey)
		if key == "" || info.LTP <= 0 || seen[key] {
			continue
		}
		seen[key] = true

		depth := info.OrderBook
		if depth.BestBid <= 0 && depth.BestAsk <= 0 && e.quoteProvider != nil {
			depth, _ = e.quoteProvider.GetMarketDepth(ctx, key)
		}

		optionType := strings.ToUpper(strings.TrimSpace(info.OptionType))
		if optionType == "" {
			optionType = optionTypeFromKey(key)
		}
		strike := info.Strike
		if strike <= 0 {
			strike = parseStrikeFromKey(key)
		}
		strikeLabel := ""
		if strike > 0 {
			strikeLabel = strconv.FormatFloat(strike, 'f', -1, 64)
		}

		moneyness := 0.0
		if spot > 0 && strike > 0 {
			if optionType == "PE" {
				moneyness = ((strike - spot) / spot) * 100.0
			} else {
				moneyness = ((spot - strike) / spot) * 100.0
			}
		}

		vwap, ema9, ema21, rsi14, atr14, hasTicks := e.getTechnicalMetrics(key, info.LTP)
		if info.AvgPrice > 0 {
			vwap = info.AvgPrice
		}
		distVWAP := 0.0
		if vwap > 0 {
			distVWAP = ((info.LTP - vwap) / vwap) * 100.0
		}
		indicatorsSource := "placeholder"
		if hasTicks {
			indicatorsSource = "ticks"
		}
		label := info.MoneynessLabel
		if label == "" {
			label = moneynessLabelFor(moneyness)
		}

		out = append(out, scanCandidate{
			Key:              key,
			Type:             optionType,
			Strike:           strikeLabel,
			StrikePrice:      strike,
			LTP:              info.LTP,
			Expiry:           info.Expiry,
			PreviousClose:    info.PreviousClose,
			ChangePct:        info.ChangePct,
			DayOpen:          info.Open,
			DayHigh:          info.High,
			DayLow:           info.Low,
			AvgPrice:         info.AvgPrice,
			MoneynessPct:     moneyness,
			MoneynessLabel:   label,
			VWAP:             vwap,
			EMA9:             ema9,
			EMA21:            ema21,
			RSI14:            rsi14,
			ATR14:            atr14,
			DistFromVWAPPct:  distVWAP,
			IndicatorsSource: indicatorsSource,
			Depth:            depth,
			OI:               info.OI,
			Volume:           info.Volume,
		})
	}
	return out
}

func optionTypeFromKey(key string) string {
	upper := strings.ToUpper(strings.TrimSpace(key))
	switch {
	case strings.HasSuffix(upper, "PE"):
		return "PE"
	case strings.HasSuffix(upper, "CE"):
		return "CE"
	default:
		return "CE"
	}
}

func (e *Evaluator) marketSnapshot(ctx context.Context, instrumentKey string) (features.OrderBookMetrics, features.OptionInfo) {
	if provider, ok := e.quoteProvider.(marketSnapshotProvider); ok {
		depth, info, err := provider.GetMarketSnapshot(ctx, instrumentKey)
		if err == nil {
			return depth, info
		}
	}
	if e.quoteProvider == nil {
		return features.OrderBookMetrics{}, features.OptionInfo{}
	}
	depth, _ := e.quoteProvider.GetMarketDepth(ctx, instrumentKey)
	return depth, features.OptionInfo{}
}

func (e *Evaluator) buildSnapshot(underlying string, spot float64, bias, regime string, cand scanCandidate, decision *agent.CandidateDecision, settings scanSettings) features.StateSnapshot {
	daysToExpiry := 0.0
	if cand.Expiry != "" {
		if expiry, err := time.Parse("2006-01-02", cand.Expiry); err == nil {
			daysToExpiry = math.Max(0, time.Until(expiry).Hours()/24.0)
		}
	}

	snapshot := features.StateSnapshot{
		Timestamp:       time.Now().Format(time.RFC3339),
		Underlying:      underlying,
		SpotPrice:       spot,
		Strike:          cand.Strike,
		StrikePrice:     cand.StrikePrice,
		OptionType:      cand.Type,
		OptionLTP:       cand.LTP,
		Expiry:          cand.Expiry,
		DaysToExpiry:    math.Round(daysToExpiry*10) / 10,
		MoneynessPct:    math.Round(cand.MoneynessPct*100) / 100,
		VWAP:            cand.VWAP,
		EMA9:            cand.EMA9,
		EMA21:           cand.EMA21,
		RSI14:           cand.RSI14,
		ATR14:           cand.ATR14,
		DistFromVWAPPct: math.Round(cand.DistFromVWAPPct*100) / 100,
		OrderBook:       cand.Depth,
		OI:              cand.OI,
		Volume:          cand.Volume,
		MacroRegime:     regime,
		MacroBias:       bias,
		TargetPct:       settings.targetPct,
		StopLossPct:     settings.stopLossPct,
		Timeframe:       "5m",
	}
	if decision != nil {
		snapshot.AgentSignal = decision.Action
		snapshot.AgentScore = decision.Score
		snapshot.AgentConfidence = decision.Confidence
		snapshot.AgentReasoning = decision.Reasoning
	}
	return snapshot
}

func (e *Evaluator) createPaperCall(ctx context.Context, cand scanCandidate, underlying string, spot float64, source, reasoning string, score int, confidence float64, stateJSON string, settings scanSettings) (store.Evaluation, bool, error) {
	entry := fillPrice(cand.LTP, cand.Depth)
	if entry <= 0 {
		return store.Evaluation{}, false, nil
	}

	// Re-check under the active lock to avoid two candidates on the same underlying.
	if !e.canOpenCall(underlying, cand.Key, settings.cooldown) {
		return store.Evaluation{}, false, nil
	}

	targetPrice := math.Round(entry*(1.0+settings.targetPct/100.0)*100) / 100
	stopLossPrice := math.Round(entry*(1.0-settings.stopLossPct/100.0)*100) / 100

	eval := store.Evaluation{
		Timestamp:     time.Now(),
		InstrumentKey: cand.Key,
		Underlying:    underlying,
		SpotPrice:     spot,
		OptionLTP:     entry,
		Action:        "BUY_" + strings.ToUpper(cand.Type),
		Confidence:    confidence,
		Score:         score,
		StateSnapshot: stateJSON,
		Shadow:        settings.shadow,
		TargetPrice:   targetPrice,
		StopLossPrice: stopLossPrice,
		Status:        "PENDING",
		Source:        source,
		Reasoning:     reasoning,
	}

	if e.store != nil {
		id, serr := e.store.SaveEvaluation(ctx, eval)
		if serr != nil {
			return store.Evaluation{}, false, serr
		}
		eval.ID = id
	}

	e.TrackEvaluation(eval)

	e.log.Info("new ai paper call opened", "event", "ai_call_detected",
		"id", eval.ID, "action", eval.Action, "instrument", eval.InstrumentKey,
		"entry", eval.OptionLTP, "target", eval.TargetPrice, "stop", eval.StopLossPrice,
		"score", eval.Score, "conf", eval.Confidence, "source", source)

	if e.onNewCall != nil {
		e.onNewCall(eval)
	}
	return eval, true, nil
}

func fillPrice(ltp float64, depth features.OrderBookMetrics) float64 {
	if ltp <= 0 {
		return 0
	}
	// Conservative paper fill at the best ask when it is close to the LTP.
	if depth.BestAsk > 0 && depth.BestAsk >= depth.BestBid && depth.BestAsk <= ltp*1.05 {
		return math.Round(depth.BestAsk*100) / 100
	}
	return ltp
}

func (e *Evaluator) finalizeCall(ctx context.Context, call *ActiveCall, status string, exitPrice float64, now time.Time) {
	if e == nil || call == nil {
		return
	}

	e.activeMu.Lock()
	current, ok := e.activeCalls[call.ID]
	if !ok || current != call {
		e.activeMu.Unlock()
		return
	}
	delete(e.activeCalls, call.ID)
	atomic.StoreInt64(&e.activeCount, int64(len(e.activeCalls)))
	e.activeMu.Unlock()

	if exitPrice <= 0 {
		exitPrice = call.LastPrice
	}
	if exitPrice <= 0 {
		exitPrice = call.EntryPrice
	}

	pnlPct := 0.0
	if call.EntryPrice > 0 {
		pnlPct = ((exitPrice - call.EntryPrice) / call.EntryPrice) * 100.0
		pnlPct -= call.CostPct
	}
	pnlPct = math.Round(pnlPct*100) / 100

	if e.store != nil {
		_ = e.store.UpdateEvaluationOutcome(ctx, call.ID, status, exitPrice, pnlPct, now)
	}

	eval := store.Evaluation{
		ID:            call.ID,
		Timestamp:     call.CreatedAt,
		InstrumentKey: call.InstrumentKey,
		Underlying:    call.Underlying,
		Action:        call.Action,
		OptionLTP:     call.EntryPrice,
		TargetPrice:   call.TargetPrice,
		StopLossPrice: call.StopLossPrice,
		Status:        status,
		ExitPrice:     exitPrice,
		ExitTime:      &now,
		PnLPct:        pnlPct,
		Score:         call.Score,
		Confidence:    call.Confidence,
		Source:        call.Source,
		Reasoning:     call.Reasoning,
	}

	e.log.Info("ai paper call outcome determined", "event", "ai_call_outcome",
		"id", call.ID, "instrument", call.InstrumentKey, "status", status,
		"entry", call.EntryPrice, "exit", exitPrice, "pnl_pct", pnlPct, "source", call.Source)

	if e.onOutcome != nil {
		e.onOutcome(eval)
	}
}

func (e *Evaluator) expireStaleCalls(ctx context.Context, settings scanSettings) {
	if e == nil || atomic.LoadInt64(&e.activeCount) == 0 {
		return
	}

	now := time.Now()
	type expiredCall struct {
		call  *ActiveCall
		price float64
	}
	var expired []expiredCall

	e.activeMu.RLock()
	for _, call := range e.activeCalls {
		shouldExpire := settings.maxHold > 0 && now.Sub(call.CreatedAt) >= settings.maxHold
		if !shouldExpire && settings.maxHold <= 0 && call.CreatedAt.Format("2006-01-02") != now.Format("2006-01-02") {
			shouldExpire = true
		}
		if !shouldExpire {
			if checker, ok := e.quoteProvider.(interface{ IsUnderlyingMarketOpen(string) bool }); ok {
				if !checker.IsUnderlyingMarketOpen(call.Underlying) {
					shouldExpire = true
				}
			}
		}
		if shouldExpire {
			price := call.LastPrice
			if price <= 0 {
				price = call.EntryPrice
			}
			expired = append(expired, expiredCall{call: call, price: price})
		}
	}
	e.activeMu.RUnlock()

	for _, item := range expired {
		e.finalizeCall(ctx, item.call, "EXPIRED", item.price, now)
	}
}

func (e *Evaluator) pollActivePositions(ctx context.Context) {
	if e == nil || atomic.LoadInt64(&e.activeCount) == 0 {
		return
	}
	provider, ok := e.quoteProvider.(liveQuoteProvider)
	if !ok {
		return
	}

	settings := e.scanSettings()
	e.expireStaleCalls(ctx, settings)

	type pendingQuote struct {
		key      string
		lastTick time.Time
	}
	now := time.Now()
	var pending []pendingQuote

	e.activeMu.RLock()
	for _, call := range e.activeCalls {
		if call.LastTickAt.After(now.Add(-20 * time.Second)) {
			continue
		}
		pending = append(pending, pendingQuote{key: call.InstrumentKey, lastTick: call.LastTickAt})
	}
	e.activeMu.RUnlock()

	for _, item := range pending {
		quoteCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		price, err := provider.GetOptionLTP(quoteCtx, item.key)
		cancel()
		if err != nil || price <= 0 {
			continue
		}
		e.OnTick(ctx, item.key, price)
	}
}

func (e *Evaluator) getTechnicalMetrics(instrumentKey string, ltp float64) (vwap, ema9, ema21, rsi14, atr14 float64, hasTicks bool) {
	var ring *features.TickRing
	if e != nil {
		e.ringMu.RLock()
		ring = e.tickRings[instrumentKey]
		e.ringMu.RUnlock()
	}

	if ring != nil && ring.Len() > 0 {
		ticks := ring.Slice()
		prices := make([]float64, len(ticks))
		vols := make([]int64, len(ticks))
		highs := make([]float64, len(ticks))
		lows := make([]float64, len(ticks))
		for i, t := range ticks {
			prices[i] = t.Price
			vols[i] = 1
			highs[i] = t.Price
			lows[i] = t.Price
		}
		vwap = features.CalculateVWAP(prices, vols)
		ema9 = features.CalculateEMA(prices, 9)
		ema21 = features.CalculateEMA(prices, 21)
		rsi14 = features.CalculateRSI(prices, 14)
		atr14 = features.CalculateATR(highs, lows, prices, 14)
		if vwap <= 0 {
			vwap = ltp
		}
		if ema9 <= 0 {
			ema9 = ltp
		}
		if ema21 <= 0 {
			ema21 = ltp
		}
		if atr14 <= 0 {
			atr14 = ltp * 0.05
		}
		if rsi14 <= 0 {
			rsi14 = 50.0
		}
		return vwap, ema9, ema21, rsi14, atr14, true
	}

	highs := []float64{ltp * 1.01, ltp * 1.01}
	lows := []float64{ltp * 0.99, ltp * 0.99}
	closes := []float64{ltp, ltp}
	vwap = features.CalculateVWAP([]float64{ltp}, []int64{1})
	ema9 = features.CalculateEMA([]float64{ltp}, 9)
	ema21 = features.CalculateEMA([]float64{ltp}, 21)
	rsi14 = features.CalculateRSI(closes, 14)
	atr14 = features.CalculateATR(highs, lows, closes, 14)
	return vwap, ema9, ema21, rsi14, atr14, false
}

// ActiveCallsCount returns the number of calls currently monitored for market outcomes.
func (e *Evaluator) ActiveCallsCount() int {
	if e == nil {
		return 0
	}
	return int(atomic.LoadInt64(&e.activeCount))
}

// LivePrices returns the latest known price for each open paper call.
func (e *Evaluator) LivePrices() map[int64]float64 {
	if e == nil {
		return nil
	}
	out := make(map[int64]float64)
	e.activeMu.RLock()
	defer e.activeMu.RUnlock()
	for id, call := range e.activeCalls {
		price := call.LastPrice
		if price <= 0 {
			price = call.EntryPrice
		}
		out[id] = price
	}
	return out
}

// openPositionsSnapshot summarizes open paper calls so the agent knows what is
// already live before proposing another position.
func (e *Evaluator) openPositionsSnapshot() []agent.OpenPosition {
	if e == nil {
		return nil
	}
	now := time.Now()
	e.activeMu.RLock()
	defer e.activeMu.RUnlock()
	out := make([]agent.OpenPosition, 0, len(e.activeCalls))
	for _, call := range e.activeCalls {
		current := call.LastPrice
		if current <= 0 {
			current = call.EntryPrice
		}
		pnl := 0.0
		if call.EntryPrice > 0 {
			pnl = ((current - call.EntryPrice) / call.EntryPrice) * 100.0
			pnl -= call.CostPct
		}
		out = append(out, agent.OpenPosition{
			InstrumentKey: call.InstrumentKey,
			Underlying:    call.Underlying,
			Action:        call.Action,
			Entry:         call.EntryPrice,
			Current:       current,
			PnLPct:        math.Round(pnl*100) / 100,
			AgeMinutes:    int(now.Sub(call.CreatedAt).Minutes()),
		})
	}
	return out
}

func firstNonZeroFloat(a, b float64) float64 {
	if a != 0 {
		return a
	}
	return b
}

// TrackedInstrumentKeys returns the instrument keys currently needed for live tracking.
func (e *Evaluator) TrackedInstrumentKeys() map[string]bool {
	if e == nil {
		return nil
	}
	out := make(map[string]bool)
	e.activeMu.RLock()
	defer e.activeMu.RUnlock()
	for _, call := range e.activeCalls {
		out[strings.ToUpper(call.InstrumentKey)] = true
	}
	return out
}

// FilterUntrackedKeys removes keys that are still needed by an open paper call.
func (e *Evaluator) FilterUntrackedKeys(keys []string) []string {
	tracked := e.TrackedInstrumentKeys()
	if len(tracked) == 0 {
		return keys
	}
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if !tracked[strings.ToUpper(strings.TrimSpace(key))] {
			out = append(out, key)
		}
	}
	return out
}

// Stats returns evaluation win/loss counts and win rate percentage.
func (e *Evaluator) Stats(ctx context.Context) (store.EvaluationStats, error) {
	if e == nil || e.store == nil {
		return store.EvaluationStats{}, nil
	}
	return e.store.GetEvaluationStats(ctx)
}

// ContractSetup holds input details for evaluating a specific contract on-demand.
type ContractSetup struct {
	Underlying    string  `json:"underlying"`
	InstrumentKey string  `json:"instrument_key"`
	Symbol        string  `json:"symbol"`
	Strike        string  `json:"strike"`
	OptionType    string  `json:"option_type"`
	OptionLTP     float64 `json:"option_ltp"`
	Source        string  `json:"source,omitempty"`
}

// EvaluateContract performs an on-demand evaluation of a specified contract using
// Jev or the Agent. It persists and tracks the call only when the action is BUY.
func (e *Evaluator) EvaluateContract(ctx context.Context, setup ContractSetup) (store.Evaluation, error) {
	if e == nil {
		return store.Evaluation{}, errors.New("evaluator: nil instance")
	}
	jevClient := e.JevClient()
	agentClient := e.AgentClient()
	if jevClient == nil && agentClient == nil {
		return store.Evaluation{}, errors.New("evaluator: neither jev nor agent client is configured")
	}
	if setup.OptionLTP <= 0 {
		return store.Evaluation{}, errors.New("evaluator: option LTP must be greater than zero")
	}
	if setup.Underlying == "" {
		setup.Underlying = "NIFTY"
	}
	if setup.OptionType == "" {
		setup.OptionType = "CE"
	}
	setup.OptionType = strings.ToUpper(strings.TrimSpace(setup.OptionType))

	var spot float64
	if e.quoteProvider != nil {
		spot, _ = e.quoteProvider.GetSpotPrice(ctx, setup.Underlying)
	}
	if spot <= 0 {
		spot = setup.OptionLTP
	}

	depth, info := features.OrderBookMetrics{}, features.OptionInfo{}
	if e.quoteProvider != nil && setup.InstrumentKey != "" {
		depth, info = e.marketSnapshot(ctx, setup.InstrumentKey)
	}

	var bias, regime string
	if e.store != nil {
		if pb, perr := e.store.GetLatestPlaybook(ctx); perr == nil && pb != nil {
			bias, regime = pb.Bias, pb.Regime
		}
	}

	settings := e.scanSettings()
	strike := setup.Strike
	strikePrice := info.Strike
	if strikePrice <= 0 {
		strikePrice = parseStrikeFromKey(setup.InstrumentKey)
	}
	if strike == "" && strikePrice > 0 {
		strike = strconv.FormatFloat(strikePrice, 'f', -1, 64)
	}
	expiry := info.Expiry
	moneyness := 0.0
	if spot > 0 && strikePrice > 0 {
		if setup.OptionType == "PE" {
			moneyness = ((strikePrice - spot) / spot) * 100.0
		} else {
			moneyness = ((spot - strikePrice) / spot) * 100.0
		}
	}

	vwap, ema9, ema21, rsi14, atr14, _ := e.getTechnicalMetrics(setup.InstrumentKey, setup.OptionLTP)
	distVWAP := 0.0
	if vwap > 0 {
		distVWAP = ((setup.OptionLTP - vwap) / vwap) * 100.0
	}

	snapshot := features.StateSnapshot{
		Timestamp:       time.Now().Format(time.RFC3339),
		Underlying:      setup.Underlying,
		SpotPrice:       spot,
		Strike:          strike,
		StrikePrice:     strikePrice,
		OptionType:      setup.OptionType,
		OptionLTP:       setup.OptionLTP,
		Expiry:          expiry,
		MoneynessPct:    math.Round(moneyness*100) / 100,
		VWAP:            vwap,
		EMA9:            ema9,
		EMA21:           ema21,
		RSI14:           rsi14,
		ATR14:           atr14,
		DistFromVWAPPct: math.Round(distVWAP*100) / 100,
		OrderBook:       depth,
		OI:              info.OI,
		Volume:          info.Volume,
		MacroRegime:     regime,
		MacroBias:       bias,
		TargetPct:       settings.targetPct,
		StopLossPct:     settings.stopLossPct,
		Timeframe:       "5m",
	}
	stateJSON, _ := snapshot.ToJSON()

	var action string
	var confidence float64
	var score int
	var durationMS int
	var reasoning string
	source := strings.TrimSpace(setup.Source)
	if source == "" {
		source = "manual"
	}

	if jevClient != nil {
		start := time.Now()
		res, jerr := jevClient.EvaluateSetup(ctx, stateJSON)
		durationMS = int(time.Since(start).Milliseconds())
		if jerr != nil {
			e.log.Warn("manual jev evaluation failed, trying agent fallback",
				"event", "jev_eval_fallback", "instrument", setup.InstrumentKey, "error", jerr)
		} else {
			action = strings.ToUpper(strings.TrimSpace(res.Action))
			confidence = res.Confidence
			score = res.Score
		}
	}

	if action == "" && agentClient != nil {
		start := time.Now()
		decision, aerr := agentClient.EvaluateContract(ctx, agent.ContractSetup{
			Underlying:    setup.Underlying,
			SpotPrice:     spot,
			OptionType:    setup.OptionType,
			Strike:        strike,
			OptionLTP:     setup.OptionLTP,
			MacroBias:     bias,
			OrderBookDesc: "real-time market depth",
		})
		durationMS = int(time.Since(start).Milliseconds())
		if aerr != nil {
			return store.Evaluation{}, fmt.Errorf("evaluator: agent evaluation failed: %w", aerr)
		}
		action = strings.ToUpper(strings.TrimSpace(decision.Action))
		confidence = decision.Confidence
		score = decision.Score
		reasoning = decision.Reasoning
		if source == "manual" {
			source = "manual+agent"
		}
	}

	if action == "" {
		return store.Evaluation{}, errors.New("evaluator: no decision returned by AI models")
	}

	action = normalizeOptionAction(action, setup.OptionType)
	if action != "BUY_CE" && action != "BUY_PE" {
		return store.Evaluation{
			Timestamp:     time.Now(),
			InstrumentKey: setup.InstrumentKey,
			Underlying:    setup.Underlying,
			SpotPrice:     spot,
			OptionLTP:     setup.OptionLTP,
			Action:        action,
			Confidence:    confidence,
			Score:         score,
			DurationMS:    durationMS,
			StateSnapshot: stateJSON,
			Shadow:        settings.shadow,
			Status:        "REJECTED",
			Source:        source,
			Reasoning:     reasoning,
		}, nil
	}

	entry := setup.OptionLTP
	targetPrice := math.Round(entry*(1.0+settings.targetPct/100.0)*100) / 100
	stopLossPrice := math.Round(entry*(1.0-settings.stopLossPct/100.0)*100) / 100

	eval := store.Evaluation{
		Timestamp:     time.Now(),
		InstrumentKey: setup.InstrumentKey,
		Underlying:    setup.Underlying,
		SpotPrice:     spot,
		OptionLTP:     entry,
		Action:        action,
		Confidence:    confidence,
		Score:         score,
		DurationMS:    durationMS,
		StateSnapshot: stateJSON,
		Shadow:        settings.shadow,
		TargetPrice:   targetPrice,
		StopLossPrice: stopLossPrice,
		Status:        "PENDING",
		Source:        source,
		Reasoning:     reasoning,
	}

	if e.store != nil {
		id, serr := e.store.SaveEvaluation(ctx, eval)
		if serr != nil {
			return store.Evaluation{}, fmt.Errorf("evaluator: save manual evaluation: %w", serr)
		}
		eval.ID = id
	}

	e.TrackEvaluation(eval)
	if e.onNewCall != nil {
		e.onNewCall(eval)
	}
	return eval, nil
}

// JevClient returns the configured Jev decision client.
func (e *Evaluator) JevClient() *jev.Client {
	if e == nil {
		return nil
	}
	e.clientMu.RLock()
	defer e.clientMu.RUnlock()
	return e.jevClient
}

// AgentClient returns the configured Agent LLM client.
func (e *Evaluator) AgentClient() *agent.Client {
	if e == nil {
		return nil
	}
	e.clientMu.RLock()
	defer e.clientMu.RUnlock()
	return e.agentClient
}

// TrackEvaluation registers an actionable call into the live tick outcome tracker.
func (e *Evaluator) TrackEvaluation(eval store.Evaluation) {
	if e == nil || eval.ID == 0 || !isActionableAction(eval.Action) {
		return
	}

	settings := e.scanSettings()
	now := time.Now()

	e.activeMu.Lock()
	e.activeCalls[eval.ID] = &ActiveCall{
		ID:            eval.ID,
		InstrumentKey: eval.InstrumentKey,
		Underlying:    eval.Underlying,
		Action:        eval.Action,
		EntryPrice:    eval.OptionLTP,
		TargetPrice:   eval.TargetPrice,
		StopLossPrice: eval.StopLossPrice,
		Score:         eval.Score,
		Confidence:    eval.Confidence,
		Source:        eval.Source,
		Reasoning:     eval.Reasoning,
		CostPct:       settings.costPct,
		CreatedAt:     eval.Timestamp,
		LastPrice:     eval.OptionLTP,
		LastTickAt:    now,
	}
	atomic.StoreInt64(&e.activeCount, int64(len(e.activeCalls)))
	call := e.activeCalls[eval.ID]
	e.activeMu.Unlock()

	e.noteSignal(eval.Underlying, now)
	e.notifyTracked(call)
}

// UntrackEvaluation removes an evaluation from the live tick outcome tracker
// so no further Target / Stop-Loss outcomes are recorded for it.
func (e *Evaluator) UntrackEvaluation(id int64) {
	if e == nil || id == 0 {
		return
	}
	e.activeMu.Lock()
	if _, ok := e.activeCalls[id]; ok {
		delete(e.activeCalls, id)
		atomic.StoreInt64(&e.activeCount, int64(len(e.activeCalls)))
	}
	e.activeMu.Unlock()
}

func (e *Evaluator) notifyTracked(call *ActiveCall) {
	if e.onTrack == nil || call == nil {
		return
	}
	e.onTrack(store.Evaluation{
		ID:            call.ID,
		Timestamp:     call.CreatedAt,
		InstrumentKey: call.InstrumentKey,
		Underlying:    call.Underlying,
		OptionLTP:     call.EntryPrice,
		Action:        call.Action,
		TargetPrice:   call.TargetPrice,
		StopLossPrice: call.StopLossPrice,
		Score:         call.Score,
		Confidence:    call.Confidence,
		Source:        call.Source,
		Reasoning:     call.Reasoning,
		Status:        "PENDING",
	})
}

func (e *Evaluator) noteSignal(underlying string, at time.Time) {
	key := strings.ToUpper(strings.TrimSpace(underlying))
	if key == "" {
		return
	}
	e.lastSignalMu.Lock()
	e.lastSignal[key] = at
	e.lastSignalMu.Unlock()
}

func (e *Evaluator) canOpenCall(underlying, instrumentKey string, cooldown time.Duration) bool {
	underlying = strings.ToUpper(strings.TrimSpace(underlying))
	instrumentKey = strings.ToUpper(strings.TrimSpace(instrumentKey))

	e.activeMu.RLock()
	for _, call := range e.activeCalls {
		if strings.EqualFold(call.Underlying, underlying) || strings.EqualFold(call.InstrumentKey, instrumentKey) {
			e.activeMu.RUnlock()
			return false
		}
	}
	e.activeMu.RUnlock()

	if cooldown <= 0 {
		return true
	}
	e.lastSignalMu.Lock()
	last, ok := e.lastSignal[underlying]
	e.lastSignalMu.Unlock()
	return !ok || time.Since(last) >= cooldown
}

func (e *Evaluator) runPositionMonitor(ctx context.Context) {
	for {
		settings := e.scanSettings()
		interval := time.Duration(settings.evalCadence) * time.Second
		if interval < 10*time.Second {
			interval = 10 * time.Second
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			e.pollActivePositions(ctx)
		}
	}
}

type scanCycleResult int

const (
	scanCycleOK scanCycleResult = iota
	scanCycleMarketClosed
	scanCycleFailed
)

func (e *Evaluator) runScanner(ctx context.Context, interval time.Duration, done chan struct{}) {
	defer close(done)

	e.log.Info("ai evaluator periodic scanner started", "event", "ai_scanner_started", "interval", interval)

	retryInterval := 2 * time.Minute
	if retryInterval > interval {
		retryInterval = interval
	}
	if retryInterval < 15*time.Second {
		retryInterval = 15 * time.Second
	}

	// runCycle executes one scan pass. Closed markets are re-checked soon so
	// the scanner starts promptly at the next open; repeated scan failures back
	// off exponentially to avoid hammering a slow or rate-limited agent.
	runCycle := func() scanCycleResult {
		e.scanMu.Lock()
		e.nextScanTime = time.Now().Add(interval)
		e.scanMu.Unlock()

		status := e.ScannerStatus()
		if e.onScanStatus != nil {
			e.onScanStatus(status)
		}

		// Always expire stale positions, even when markets are closed.
		e.expireStaleCalls(ctx, e.scanSettings())

		if !status.AnyMarketOpen {
			e.log.Info("periodic ai scan skipped: all configured markets closed", "event", "ai_periodic_scan_markets_closed", "closed", status.ClosedUnderlyings)
			if e.onScanStatus != nil {
				e.onScanStatus(e.ScannerStatus())
			}
			return scanCycleMarketClosed
		}

		underlyings := status.OpenUnderlyings
		if len(underlyings) == 0 {
			return scanCycleMarketClosed
		}

		var totalEvals []store.Evaluation
		var scanErr error
		for _, u := range underlyings {
			scanCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
			evals, err := e.ScanNow(scanCtx, u)
			cancel()
			if err != nil {
				scanErr = err
			} else {
				totalEvals = append(totalEvals, evals...)
			}
		}

		failed := scanErr != nil && len(totalEvals) == 0

		e.scanMu.Lock()
		e.lastScanTime = time.Now()
		if failed {
			e.lastScanError = scanErr.Error()
		} else {
			e.lastScanError = ""
		}
		e.scanMu.Unlock()

		if failed {
			e.log.Warn("periodic ai scan failed", "event", "ai_periodic_scan_failed", "error", scanErr)
		} else {
			e.log.Info("periodic ai scan finished", "event", "ai_periodic_scan_success",
				"count", len(totalEvals), "scanned", underlyings)
		}

		if e.onScanStatus != nil {
			e.onScanStatus(e.ScannerStatus())
		}
		if failed {
			return scanCycleFailed
		}
		return scanCycleOK
	}

	// Trigger the first scan shortly after startup (after feeds/scrips stabilize).
	delay := 5 * time.Second
	failures := 0
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			e.log.Info("ai evaluator periodic scanner stopped", "event", "ai_scanner_stopped")
			return
		case <-timer.C:
			switch runCycle() {
			case scanCycleOK:
				failures = 0
				delay = interval
			case scanCycleMarketClosed:
				failures = 0
				delay = retryInterval
			case scanCycleFailed:
				failures++
				delay = retryInterval
				for i := 1; i < failures; i++ {
					if delay >= interval {
						break
					}
					delay *= 2
				}
				if delay > interval {
					delay = interval
				}
			}
			e.scanMu.Lock()
			e.nextScanTime = time.Now().Add(delay)
			e.scanMu.Unlock()
			if e.onScanStatus != nil {
				e.onScanStatus(e.ScannerStatus())
			}
		}
	}
}

func (e *Evaluator) noteScanFailure(err error) {
	if err == nil {
		return
	}
	e.scanMu.Lock()
	e.lastScanError = err.Error()
	e.scanMu.Unlock()
}

type scanCandidate struct {
	Key              string
	Type             string
	Strike           string
	StrikePrice      float64
	LTP              float64
	Expiry           string
	PreviousClose    float64
	ChangePct        float64
	DayOpen          float64
	DayHigh          float64
	DayLow           float64
	AvgPrice         float64
	MoneynessPct     float64
	MoneynessLabel   string
	VWAP             float64
	EMA9             float64
	EMA21            float64
	RSI14            float64
	ATR14            float64
	DistFromVWAPPct  float64
	IndicatorsSource string
	Depth            features.OrderBookMetrics
	OI               int64
	Volume           int64
}

func (c scanCandidate) agentInput() agent.CandidateContract {
	daysToExpiry := 0.0
	if c.Expiry != "" {
		if expiry, err := time.Parse("2006-01-02", c.Expiry); err == nil {
			daysToExpiry = math.Max(0, time.Until(expiry).Hours()/24.0)
			daysToExpiry = math.Round(daysToExpiry*10) / 10
		}
	}
	moneynessLabel := c.MoneynessLabel
	if moneynessLabel == "" {
		moneynessLabel = moneynessLabelFor(c.MoneynessPct)
	}
	return agent.CandidateContract{
		InstrumentKey:    c.Key,
		OptionType:       c.Type,
		Strike:           c.Strike,
		StrikePrice:      c.StrikePrice,
		OptionLTP:        c.LTP,
		Expiry:           c.Expiry,
		DaysToExpiry:     daysToExpiry,
		MoneynessPct:     math.Round(c.MoneynessPct*100) / 100,
		MoneynessLabel:   moneynessLabel,
		PreviousClose:    c.PreviousClose,
		ChangePct:        c.ChangePct,
		DayOpen:          c.DayOpen,
		DayHigh:          c.DayHigh,
		DayLow:           c.DayLow,
		VWAP:             c.VWAP,
		AvgPrice:         c.AvgPrice,
		EMA9:             c.EMA9,
		EMA21:            c.EMA21,
		RSI14:            c.RSI14,
		ATR14:            c.ATR14,
		DistFromVWAPPct:  math.Round(c.DistFromVWAPPct*100) / 100,
		IndicatorsSource: c.IndicatorsSource,
		OI:               c.OI,
		Volume:           c.Volume,
		OrderBook:        c.Depth,
	}
}

// moneynessLabelFor classifies a contract as ATM, ITM or OTM. Positive
// moneyness means the option is in-the-money for both calls and puts with the
// convention used by the evaluator: (spot-strike)/spot for CE and
// (strike-spot)/spot for PE.
func moneynessLabelFor(moneynessPct float64) string {
	switch {
	case math.Abs(moneynessPct) < 0.5:
		return "ATM"
	case moneynessPct > 0:
		return "ITM"
	default:
		return "OTM"
	}
}

type scanSettings struct {
	minConfidence float64
	minScore      int
	targetPct     float64
	stopLossPct   float64
	costPct       float64
	maxHold       time.Duration
	cooldown      time.Duration
	evalCadence   int
	shadow        bool
}

func (e *Evaluator) scanSettings() scanSettings {
	e.cfgMu.RLock()
	c := e.cfg
	e.cfgMu.RUnlock()

	if c.TargetPct <= 0 {
		c.TargetPct = 8.0
	}
	if c.StopLossPct <= 0 {
		c.StopLossPct = 5.0
	}
	if c.MinConfidence <= 0 {
		c.MinConfidence = 0.60
	}
	if c.MinScore <= 0 {
		c.MinScore = 3
	}
	if c.RoundTripCostPct < 0 {
		c.RoundTripCostPct = 0
	}
	if c.MaxHoldMinutes < 0 {
		c.MaxHoldMinutes = 0
	}
	if c.CooldownMinutes < 0 {
		c.CooldownMinutes = 0
	}
	if c.EvalCadenceSeconds <= 0 {
		c.EvalCadenceSeconds = 15
	}

	return scanSettings{
		minConfidence: c.MinConfidence,
		minScore:      c.MinScore,
		targetPct:     c.TargetPct,
		stopLossPct:   c.StopLossPct,
		costPct:       c.RoundTripCostPct,
		maxHold:       time.Duration(c.MaxHoldMinutes) * time.Minute,
		cooldown:      time.Duration(c.CooldownMinutes) * time.Minute,
		evalCadence:   c.EvalCadenceSeconds,
		shadow:        c.ShadowMode,
	}
}

func approveJev(res jev.EvaluationResult, settings scanSettings) bool {
	action := strings.ToUpper(strings.TrimSpace(res.Action))
	if action != "BUY" && action != "BUY_CE" && action != "BUY_PE" {
		return false
	}
	return res.Score >= settings.minScore && res.Confidence >= settings.minConfidence
}

func approveAgent(decision agent.CandidateDecision, settings scanSettings) bool {
	return strings.EqualFold(strings.TrimSpace(decision.Action), "BUY") &&
		decision.Score >= settings.minScore &&
		decision.Confidence >= settings.minConfidence
}

func normalizeOptionAction(action, optionType string) string {
	action = strings.ToUpper(strings.TrimSpace(action))
	optionType = strings.ToUpper(strings.TrimSpace(optionType))
	if optionType != "PE" {
		optionType = "CE"
	}
	if action == "BUY" || action == "BUY_CE" || action == "BUY_PE" || strings.HasPrefix(action, "BUY_") {
		return "BUY_" + optionType
	}
	return action
}

func isActionableAction(action string) bool {
	switch strings.ToUpper(strings.TrimSpace(action)) {
	case "BUY", "BUY_CE", "BUY_PE":
		return true
	default:
		return false
	}
}

func matchAgentDecisions(candidates []scanCandidate, decisions []agent.CandidateDecision) map[string]agent.CandidateDecision {
	out := make(map[string]agent.CandidateDecision)
	for _, d := range decisions {
		matched := ""
		for _, cand := range candidates {
			if d.InstrumentKey != "" && strings.EqualFold(strings.TrimSpace(d.InstrumentKey), cand.Key) {
				matched = cand.Key
				break
			}
			if d.OptionType != "" && strings.EqualFold(strings.TrimSpace(d.OptionType), cand.Type) {
				if d.Strike == "" || strikesEqual(d.Strike, cand.Strike) {
					matched = cand.Key
					break
				}
			}
		}
		if matched == "" && len(candidates) == 1 && d.OptionType == "" {
			matched = candidates[0].Key
		}
		if matched != "" {
			out[matched] = d
		}
	}
	return out
}

func strikesEqual(a, b string) bool {
	af, aerr := strconv.ParseFloat(strings.TrimSpace(a), 64)
	bf, berr := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if aerr == nil && berr == nil {
		return math.Abs(af-bf) < 0.001
	}
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func parseStrikeFromKey(instrumentKey string) float64 {
	s := strings.ToUpper(strings.TrimSpace(instrumentKey))
	s = strings.TrimSuffix(s, "CE")
	s = strings.TrimSuffix(s, "PE")

	end := -1
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] >= '0' && s[i] <= '9' {
			end = i
			break
		}
	}
	if end < 0 {
		return 0
	}
	start := end
	for start >= 0 && ((s[start] >= '0' && s[start] <= '9') || s[start] == '.') {
		start--
	}
	value, err := strconv.ParseFloat(s[start+1:end+1], 64)
	if err != nil {
		return 0
	}
	return value
}

func buildReasoning(agentDecision *agent.CandidateDecision, jevRes *jev.EvaluationResult) string {
	parts := make([]string, 0, 2)
	if agentDecision != nil && strings.TrimSpace(agentDecision.Reasoning) != "" {
		parts = append(parts, "Agent: "+strings.TrimSpace(agentDecision.Reasoning))
	}
	if jevRes != nil {
		parts = append(parts, fmt.Sprintf("Jev validation: %s, score %d/5, confidence %.0f%%",
			strings.ToUpper(strings.TrimSpace(jevRes.Action)), jevRes.Score, jevRes.Confidence*100))
	}
	return strings.Join(parts, " | ")
}

func jevAction(res *jev.EvaluationResult) string {
	if res == nil {
		return ""
	}
	return res.Action
}

func jevScore(res *jev.EvaluationResult) int {
	if res == nil {
		return 0
	}
	return res.Score
}

func jevConfidence(res *jev.EvaluationResult) float64 {
	if res == nil {
		return 0
	}
	return res.Confidence
}

func agentScore(decision *agent.CandidateDecision) int {
	if decision == nil {
		return 0
	}
	return decision.Score
}

func agentConfidence(decision *agent.CandidateDecision) float64 {
	if decision == nil {
		return 0
	}
	return decision.Confidence
}
