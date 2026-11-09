// Package evaluator coordinates periodic market scanning with TypeSafe Jev (System 1)
// and tracks real-time execution outcomes (Target vs Stop-Loss) against live feed ticks.
package evaluator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
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

// OutcomeCallback is invoked when an active call reaches Target or Stop-Loss.
type OutcomeCallback func(eval store.Evaluation)

// NewCallCallback is invoked when a new high-conviction Jev call is detected.
type NewCallCallback func(eval store.Evaluation)

// ScanStatusCallback is invoked when periodic scanner timing or execution status updates.
type ScanStatusCallback func(status ScannerStatus)

// ActiveCall tracks an open Jev recommendation awaiting market outcome.
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
	CreatedAt     time.Time
}

// QuoteProvider returns current market quote data for an instrument or underlying.
type QuoteProvider interface {
	GetSpotPrice(ctx context.Context, underlying string) (float64, error)
	GetATMStrikes(ctx context.Context, underlying string) (ceKey, peKey string, ceLTP, peLTP float64, err error)
	GetMarketDepth(ctx context.Context, instrumentKey string) (features.OrderBookMetrics, error)
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

// Evaluator manages periodic Jev evaluations and live feed outcome tracking.
type Evaluator struct {
	cfgMu         sync.RWMutex
	cfg           config.AI
	store         AIStore
	jevClient     *jev.Client
	agentClient   *agent.Client
	quoteProvider QuoteProvider
	log           *slog.Logger

	activeMu    sync.RWMutex
	activeCalls map[int64]*ActiveCall
	activeCount int64 // atomic fast-path counter for OnTick

	ringMu    sync.RWMutex
	tickRings map[string]*features.TickRing

	onOutcome    OutcomeCallback
	onNewCall    NewCallCallback
	onScanStatus ScanStatusCallback

	scanCancel context.CancelFunc
	scanDone   chan struct{}
	running    atomic.Bool
	isScanning atomic.Bool
	baseCtx    context.Context

	scanMu       sync.RWMutex
	nextScanTime time.Time
	lastScanTime time.Time
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
		onOutcome:     opts.OnOutcome,
		onNewCall:     opts.OnNewCall,
		onScanStatus:  opts.OnScanStatus,
	}

	// Load existing PENDING calls from store on boot
	if opts.Store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if pending, err := opts.Store.GetActiveEvaluations(ctx); err == nil {
			for _, p := range pending {
				e.activeCalls[p.ID] = &ActiveCall{
					ID:            p.ID,
					InstrumentKey: p.InstrumentKey,
					Underlying:    p.Underlying,
					Action:        p.Action,
					EntryPrice:    p.OptionLTP,
					TargetPrice:   p.TargetPrice,
					StopLossPrice: p.StopLossPrice,
					Score:         p.Score,
					Confidence:    p.Confidence,
					CreatedAt:     p.Timestamp,
				}
			}
			e.activeCount = int64(len(e.activeCalls))
		}
	}

	return e
}

// Start begins the background periodic evaluation scanner if enabled.
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

	if ctx != nil && ctx.Err() == nil {
		e.baseCtx = ctx
	} else if e.baseCtx == nil || e.baseCtx.Err() != nil {
		e.baseCtx = context.Background()
	}

	if e.running.Swap(true) {
		return // already running
	}

	scanCtx, cancel := context.WithCancel(e.baseCtx)
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

	if ctx != nil && ctx.Err() == nil {
		e.baseCtx = ctx
	}

	// Update Jev client if URL or key changed
	if newCfg.Jev.BaseURL != oldCfg.Jev.BaseURL || newCfg.Jev.APIKey != oldCfg.Jev.APIKey || newCfg.Jev.Model != oldCfg.Jev.Model {
		e.jevClient = jev.NewClient(jev.Config{
			BaseURL: newCfg.Jev.BaseURL,
			APIKey:  newCfg.Jev.APIKey,
			Model:   newCfg.Jev.Model,
			Timeout: time.Duration(newCfg.Jev.TimeoutMS) * time.Millisecond,
		})
	}
	// Update Agent client if provider, URL, key, or model changed
	if newCfg.Agent.BaseURL != oldCfg.Agent.BaseURL || newCfg.Agent.APIKey != oldCfg.Agent.APIKey || newCfg.Agent.Model != oldCfg.Agent.Model || newCfg.Agent.Provider != oldCfg.Agent.Provider {
		e.agentClient = agent.NewClient(agent.Config{
			Provider: newCfg.Agent.Provider,
			BaseURL:  newCfg.Agent.BaseURL,
			APIKey:   newCfg.Agent.APIKey,
			Model:    newCfg.Agent.Model,
			Timeout:  time.Duration(newCfg.Agent.TimeoutMS) * time.Millisecond,
		})
	}

	wasEnabled := oldCfg.Enabled && oldCfg.PeriodicScan
	nowEnabled := newCfg.Enabled && newCfg.PeriodicScan

	if !nowEnabled && wasEnabled {
		e.Stop()
	} else if nowEnabled && (!wasEnabled || newCfg.ScanIntervalMinutes != oldCfg.ScanIntervalMinutes) {
		e.Stop()
		e.Start(e.baseCtx)
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
	e.ringMu.Lock()
	ring, ok := e.tickRings[instrumentKey]
	if !ok {
		ring = features.NewTickRing(100)
		e.tickRings[instrumentKey] = ring
	}
	ring.Push(features.Tick{Price: ltp, Timestamp: now.UnixMilli()})
	e.ringMu.Unlock()

	if atomic.LoadInt64(&e.activeCount) == 0 {
		return
	}

	e.activeMu.RLock()
	var targets []*ActiveCall
	for _, call := range e.activeCalls {
		if call.InstrumentKey == instrumentKey || strings.EqualFold(call.InstrumentKey, instrumentKey) {
			targets = append(targets, call)
		}
	}
	e.activeMu.RUnlock()

	if len(targets) == 0 {
		return
	}

	now = time.Now()
	for _, call := range targets {
		var status string
		var pnlPct float64

		if call.TargetPrice > 0 && ltp >= call.TargetPrice {
			status = "TARGET_HIT"
			pnlPct = ((ltp - call.EntryPrice) / call.EntryPrice) * 100.0
		} else if call.StopLossPrice > 0 && ltp <= call.StopLossPrice {
			status = "SL_HIT"
			pnlPct = ((ltp - call.EntryPrice) / call.EntryPrice) * 100.0
		}

		if status != "" {
			e.activeMu.Lock()
			delete(e.activeCalls, call.ID)
			atomic.StoreInt64(&e.activeCount, int64(len(e.activeCalls)))
			e.activeMu.Unlock()

			// Round PnL % to 2 decimals
			pnlPct = math.Round(pnlPct*100) / 100

			if e.store != nil {
				_ = e.store.UpdateEvaluationOutcome(ctx, call.ID, status, ltp, pnlPct, now)
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
				ExitPrice:     ltp,
				ExitTime:      &now,
				PnLPct:        pnlPct,
				Score:         call.Score,
				Confidence:    call.Confidence,
			}

			e.log.Info("ai call outcome determined", "event", "ai_call_outcome",
				"id", call.ID, "instrument", call.InstrumentKey, "status", status,
				"entry", call.EntryPrice, "exit", ltp, "pnl_pct", pnlPct)

			if e.onOutcome != nil {
				e.onOutcome(eval)
			}
		}
	}
}

// ScanNow triggers an on-demand evaluation of active ATM strikes against Jev or Agent.
func (e *Evaluator) ScanNow(ctx context.Context, underlying string) ([]store.Evaluation, error) {
	if e == nil {
		return nil, errors.New("evaluator: nil instance")
	}
	if e.jevClient == nil && e.agentClient == nil {
		return nil, errors.New("evaluator: neither jev nor agent client is configured")
	}
	if e.quoteProvider == nil {
		return nil, errors.New("evaluator: quote provider not configured")
	}

	if underlying == "" {
		underlying = "NIFTY"
	}

	e.isScanning.Store(true)
	defer e.isScanning.Store(false)

	e.scanMu.Lock()
	e.lastScanTime = time.Now()
	e.scanMu.Unlock()

	e.cfgMu.RLock()
	minConfidence := e.cfg.MinConfidence
	minScore := e.cfg.MinScore
	targetPct := e.cfg.TargetPct
	stopLossPct := e.cfg.StopLossPct
	shadowMode := e.cfg.ShadowMode
	agentModel := e.cfg.Agent.Model
	e.cfgMu.RUnlock()

	if targetPct <= 0 {
		targetPct = 8.0
	}
	if stopLossPct <= 0 {
		stopLossPct = 5.0
	}
	if minConfidence <= 0 {
		minConfidence = 0.75
	}
	if minScore <= 0 {
		minScore = 4
	}

	spot, err := e.quoteProvider.GetSpotPrice(ctx, underlying)
	if err != nil {
		return nil, fmt.Errorf("evaluator: get spot price: %w", err)
	}

	ceKey, peKey, ceLTP, peLTP, err := e.quoteProvider.GetATMStrikes(ctx, underlying)
	if err != nil {
		return nil, fmt.Errorf("evaluator: get atm strikes: %w", err)
	}

	var results []store.Evaluation
	candidates := []struct {
		Key  string
		Type string
		LTP  float64
	}{
		{Key: ceKey, Type: "CE", LTP: ceLTP},
		{Key: peKey, Type: "PE", LTP: peLTP},
	}

	// Retrieve active playbook bias if available
	var agentBias string
	if e.store != nil {
		if pb, perr := e.store.GetLatestPlaybook(ctx); perr == nil && pb != nil {
			agentBias = pb.Bias
		}
	}

	for _, cand := range candidates {
		if cand.Key == "" || cand.LTP <= 0 {
			continue
		}

		depth, _ := e.quoteProvider.GetMarketDepth(ctx, cand.Key)
		vwap, ema9, ema21, rsi14, atr14 := e.getTechnicalMetrics(cand.Key, cand.LTP)
		distVWAP := 0.0
		if vwap > 0 {
			distVWAP = ((cand.LTP - vwap) / vwap) * 100.0
		}

		snapshot := features.StateSnapshot{
			Timestamp:       time.Now().Format(time.RFC3339),
			Underlying:      underlying,
			SpotPrice:       spot,
			Strike:          cand.Key,
			OptionType:      cand.Type,
			OptionLTP:       cand.LTP,
			VWAP:            vwap,
			EMA9:            ema9,
			EMA21:           ema21,
			RSI14:           rsi14,
			ATR14:           atr14,
			DistFromVWAPPct: distVWAP,
			OrderBook:       depth,
			MacroBias:       agentBias,
		}
		stateJSON, _ := snapshot.ToJSON()

		start := time.Now()
		var action string
		var confidence float64
		var score int
		var durationMS int
		source := "jev"

		if e.jevClient != nil {
			jevRes, jerr := e.jevClient.EvaluateSetup(ctx, stateJSON)
			durationMS = int(time.Since(start).Milliseconds())
			if jerr != nil {
				e.log.Warn("jev evaluation failed", "event", "jev_eval_failed", "instrument", cand.Key, "error", jerr)
			} else {
				action = strings.ToUpper(strings.TrimSpace(jevRes.Action))
				confidence = jevRes.Confidence
				score = jevRes.Score
			}
		}

		if action == "" && e.agentClient != nil {
			startAgent := time.Now()
			decision, aerr := e.agentClient.EvaluateContract(ctx, agent.ContractSetup{
				Underlying:    underlying,
				SpotPrice:     spot,
				OptionType:    cand.Type,
				Strike:        cand.Key,
				OptionLTP:     cand.LTP,
				MacroBias:     agentBias,
				OrderBookDesc: "real-time market depth",
			})
			durationMS = int(time.Since(startAgent).Milliseconds())
			if aerr != nil {
				e.log.Warn("agent evaluation failed", "event", "agent_eval_failed", "instrument", cand.Key, "error", aerr)
				continue
			}
			action = strings.ToUpper(strings.TrimSpace(decision.Action))
			confidence = decision.Confidence
			score = decision.Score
			source = "agent"
		}

		if action == "" {
			action = "HOLD"
		}

		// Normalize to BUY_CE or BUY_PE
		if action == "BUY" {
			if cand.Type == "CE" {
				action = "BUY_CE"
			} else {
				action = "BUY_PE"
			}
		}

		targetPrice := math.Round(cand.LTP*(1.0+targetPct/100.0)*100) / 100
		stopLossPrice := math.Round(cand.LTP*(1.0-stopLossPct/100.0)*100) / 100

		stateSnap := stateJSON
		if source == "agent" {
			stateSnap = fmt.Sprintf(`{"agent_model":"%s","source":"agent"}`, agentModel)
		}

		eval := store.Evaluation{
			Timestamp:     time.Now(),
			InstrumentKey: cand.Key,
			Underlying:    underlying,
			SpotPrice:     spot,
			OptionLTP:     cand.LTP,
			Action:        action,
			Confidence:    confidence,
			Score:         score,
			DurationMS:    durationMS,
			StateSnapshot: stateSnap,
			Shadow:        shadowMode,
			TargetPrice:   targetPrice,
			StopLossPrice: stopLossPrice,
			Status:        "PENDING",
		}

		// Always persist evaluations in the store so recent JEV setups show in the UI table
		if e.store != nil {
			id, serr := e.store.SaveEvaluation(ctx, eval)
			if serr == nil {
				eval.ID = id
			}
		}

		// Filter for high-conviction setups only to track win/loss against live ticks
		if eval.Score >= minScore && eval.Confidence >= minConfidence && strings.HasPrefix(eval.Action, "BUY") {
			// Add to in-memory active call tracker
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
				CreatedAt:     eval.Timestamp,
			}
			atomic.StoreInt64(&e.activeCount, int64(len(e.activeCalls)))
			e.activeMu.Unlock()

			e.log.Info("new high conviction call tracked", "event", "ai_call_detected",
				"id", eval.ID, "action", eval.Action, "instrument", eval.InstrumentKey,
				"entry", eval.OptionLTP, "target", eval.TargetPrice, "stop", eval.StopLossPrice,
				"score", eval.Score, "conf", eval.Confidence, "source", source)
		}

		if e.onNewCall != nil {
			e.onNewCall(eval)
		}

		results = append(results, eval)
	}

	return results, nil
}

func (e *Evaluator) getTechnicalMetrics(instrumentKey string, ltp float64) (vwap, ema9, ema21, rsi14, atr14 float64) {
	var ring *features.TickRing
	if e != nil {
		e.ringMu.RLock()
		ring = e.tickRings[instrumentKey]
		e.ringMu.RUnlock()
	}

	if ring != nil && ring.Len() > 0 {
		_, _ = ring.Latest()
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
		return
	}

	highs := []float64{ltp * 1.01, ltp * 1.01}
	lows := []float64{ltp * 0.99, ltp * 0.99}
	closes := []float64{ltp, ltp}
	vwap = features.CalculateVWAP([]float64{ltp}, []int64{1})
	ema9 = features.CalculateEMA([]float64{ltp}, 9)
	ema21 = features.CalculateEMA([]float64{ltp}, 21)
	rsi14 = features.CalculateRSI(closes, 14)
	atr14 = features.CalculateATR(highs, lows, closes, 14)
	return
}

// ActiveCallsCount returns the number of calls currently monitored for market outcomes.
func (e *Evaluator) ActiveCallsCount() int {
	if e == nil {
		return 0
	}
	return int(atomic.LoadInt64(&e.activeCount))
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
}

// EvaluateContract performs on-demand evaluation of a specified contract using Jev or Agent and tracks it.
func (e *Evaluator) EvaluateContract(ctx context.Context, setup ContractSetup) (store.Evaluation, error) {
	if e == nil {
		return store.Evaluation{}, errors.New("evaluator: nil instance")
	}
	if e.jevClient == nil && e.agentClient == nil {
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

	var spot float64
	if e.quoteProvider != nil {
		spot, _ = e.quoteProvider.GetSpotPrice(ctx, setup.Underlying)
	}
	if spot <= 0 {
		spot = setup.OptionLTP
	}

	var depth features.OrderBookMetrics
	if e.quoteProvider != nil && setup.InstrumentKey != "" {
		depth, _ = e.quoteProvider.GetMarketDepth(ctx, setup.InstrumentKey)
	}

	var agentBias string
	if e.store != nil {
		if pb, perr := e.store.GetLatestPlaybook(ctx); perr == nil && pb != nil {
			agentBias = pb.Bias
		}
	}

	vwap, ema9, ema21, rsi14, atr14 := e.getTechnicalMetrics(setup.InstrumentKey, setup.OptionLTP)
	distVWAP := 0.0
	if vwap > 0 {
		distVWAP = ((setup.OptionLTP - vwap) / vwap) * 100.0
	}

	snapshot := features.StateSnapshot{
		Timestamp:       time.Now().Format(time.RFC3339),
		Underlying:      setup.Underlying,
		SpotPrice:       spot,
		Strike:          setup.Strike,
		OptionType:      setup.OptionType,
		OptionLTP:       setup.OptionLTP,
		VWAP:            vwap,
		EMA9:            ema9,
		EMA21:           ema21,
		RSI14:           rsi14,
		ATR14:           atr14,
		DistFromVWAPPct: distVWAP,
		OrderBook:       depth,
		MacroBias:       agentBias,
	}
	stateJSON, _ := snapshot.ToJSON()

	start := time.Now()
	var action string
	var confidence float64
	var score int
	var durationMS int
	source := "jev"

	if e.jevClient != nil {
		jevRes, jerr := e.jevClient.EvaluateSetup(ctx, stateJSON)
		durationMS = int(time.Since(start).Milliseconds())
		if jerr != nil {
			e.log.Warn("jev evaluation failed, trying agent fallback", "event", "jev_eval_fallback", "instrument", setup.InstrumentKey, "error", jerr)
		} else {
			action = strings.ToUpper(strings.TrimSpace(jevRes.Action))
			confidence = jevRes.Confidence
			score = jevRes.Score
		}
	}

	if action == "" && e.agentClient != nil {
		startAgent := time.Now()
		decision, aerr := e.agentClient.EvaluateContract(ctx, agent.ContractSetup{
			Underlying:    setup.Underlying,
			SpotPrice:     spot,
			OptionType:    setup.OptionType,
			Strike:        setup.Strike,
			OptionLTP:     setup.OptionLTP,
			MacroBias:     agentBias,
			OrderBookDesc: "real-time market depth",
		})
		durationMS = int(time.Since(startAgent).Milliseconds())
		if aerr != nil {
			return store.Evaluation{}, fmt.Errorf("evaluator: agent evaluation failed: %w", aerr)
		}
		action = strings.ToUpper(strings.TrimSpace(decision.Action))
		confidence = decision.Confidence
		score = decision.Score
		source = "agent"
	}

	if action == "" {
		return store.Evaluation{}, errors.New("evaluator: no decision returned by AI models")
	}

	e.cfgMu.RLock()
	targetPct := e.cfg.TargetPct
	stopLossPct := e.cfg.StopLossPct
	shadowMode := e.cfg.ShadowMode
	agentModel := e.cfg.Agent.Model
	e.cfgMu.RUnlock()

	if targetPct <= 0 {
		targetPct = 8.0
	}
	if stopLossPct <= 0 {
		stopLossPct = 5.0
	}

	if action == "BUY" {
		if setup.OptionType == "CE" {
			action = "BUY_CE"
		} else {
			action = "BUY_PE"
		}
	}

	targetPrice := math.Round(setup.OptionLTP*(1.0+targetPct/100.0)*100) / 100
	stopLossPrice := math.Round(setup.OptionLTP*(1.0-stopLossPct/100.0)*100) / 100

	stateSnap := stateJSON
	if source == "agent" {
		stateSnap = fmt.Sprintf(`{"agent_model":"%s","source":"agent"}`, agentModel)
	}

	eval := store.Evaluation{
		Timestamp:     time.Now(),
		InstrumentKey: setup.InstrumentKey,
		Underlying:    setup.Underlying,
		SpotPrice:     spot,
		OptionLTP:     setup.OptionLTP,
		Action:        action,
		Confidence:    confidence,
		Score:         score,
		DurationMS:    durationMS,
		StateSnapshot: stateSnap,
		Shadow:        shadowMode,
		TargetPrice:   targetPrice,
		StopLossPrice: stopLossPrice,
		Status:        "PENDING",
	}

	if e.store != nil {
		id, serr := e.store.SaveEvaluation(ctx, eval)
		if serr == nil {
			eval.ID = id
		}
	}

	if eval.TargetPrice > 0 {
		e.TrackEvaluation(eval)
		if e.onNewCall != nil && (action == "BUY_CE" || action == "BUY_PE" || action == "BUY") {
			e.onNewCall(eval)
		}
	}

	return eval, nil
}

// JevClient returns the configured Jev decision client.
func (e *Evaluator) JevClient() *jev.Client {
	if e == nil {
		return nil
	}
	return e.jevClient
}

// AgentClient returns the configured Agent LLM client.
func (e *Evaluator) AgentClient() *agent.Client {
	if e == nil {
		return nil
	}
	return e.agentClient
}

// TrackEvaluation registers an evaluation into the live tick outcome tracker.
func (e *Evaluator) TrackEvaluation(eval store.Evaluation) {
	if e == nil || eval.ID == 0 {
		return
	}
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
		CreatedAt:     eval.Timestamp,
	}
	atomic.StoreInt64(&e.activeCount, int64(len(e.activeCalls)))
	e.activeMu.Unlock()
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

func (e *Evaluator) runScanner(ctx context.Context, interval time.Duration, done chan struct{}) {
	defer close(done)

	e.log.Info("ai evaluator periodic scanner started", "event", "ai_scanner_started", "interval", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	doScan := func() {
		e.scanMu.Lock()
		e.nextScanTime = time.Now().Add(interval)
		e.scanMu.Unlock()

		status := e.ScannerStatus()
		if e.onScanStatus != nil {
			e.onScanStatus(status)
		}

		if !status.AnyMarketOpen {
			e.log.Info("periodic ai scan skipped: all configured markets closed", "event", "ai_periodic_scan_markets_closed", "closed", status.ClosedUnderlyings)
			return
		}

		underlyings := status.OpenUnderlyings
		if len(underlyings) == 0 {
			return
		}

		var totalEvals []store.Evaluation
		var scanErr error
		for _, u := range underlyings {
			scanCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			evals, err := e.ScanNow(scanCtx, u)
			cancel()
			if err != nil {
				scanErr = err
			} else {
				totalEvals = append(totalEvals, evals...)
			}
		}

		e.scanMu.Lock()
		e.lastScanTime = time.Now()
		e.scanMu.Unlock()

		if scanErr != nil && len(totalEvals) == 0 {
			e.log.Warn("periodic ai scan failed", "event", "ai_periodic_scan_failed", "error", scanErr)
		} else {
			e.log.Info("periodic ai scan finished", "event", "ai_periodic_scan_success", "count", len(totalEvals), "scanned", underlyings)
		}

		if e.onScanStatus != nil {
			e.onScanStatus(e.ScannerStatus())
		}
	}

	// Trigger initial scan shortly after startup (after feeds/scrips stabilize)
	go func() {
		select {
		case <-time.After(5 * time.Second):
			doScan()
		case <-ctx.Done():
		}
	}()

	for {
		select {
		case <-ctx.Done():
			e.log.Info("ai evaluator periodic scanner stopped", "event", "ai_scanner_stopped")
			return
		case <-ticker.C:
			doScan()
		}
	}
}
