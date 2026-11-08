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

	"github.com/akshaykhairmode/scalper/internal/ai/features"
	"github.com/akshaykhairmode/scalper/internal/ai/jev"
	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/store"
)

// OutcomeCallback is invoked when an active call reaches Target or Stop-Loss.
type OutcomeCallback func(eval store.Evaluation)

// NewCallCallback is invoked when a new high-conviction Jev call is detected.
type NewCallCallback func(eval store.Evaluation)

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
	quoteProvider QuoteProvider
	log           *slog.Logger

	activeMu    sync.RWMutex
	activeCalls map[int64]*ActiveCall
	activeCount int64 // atomic fast-path counter for OnTick

	ringMu    sync.RWMutex
	tickRings map[string]*features.TickRing

	onOutcome OutcomeCallback
	onNewCall NewCallCallback

	scanCancel context.CancelFunc
	scanDone   chan struct{}
	running    atomic.Bool
}

// Options configures the Evaluator instance.
type Options struct {
	Config        config.AI
	Store         AIStore
	JevClient     *jev.Client
	QuoteProvider QuoteProvider
	Logger        *slog.Logger
	OnOutcome     OutcomeCallback
	OnNewCall     NewCallCallback
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
		quoteProvider: opts.QuoteProvider,
		log:           logger,
		activeCalls:   make(map[int64]*ActiveCall),
		tickRings:     make(map[string]*features.TickRing),
		onOutcome:     opts.OnOutcome,
		onNewCall:     opts.OnNewCall,
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

	if e.running.Swap(true) {
		return // already running
	}

	scanCtx, cancel := context.WithCancel(ctx)
	e.scanCancel = cancel
	e.scanDone = make(chan struct{})

	go e.runScanner(scanCtx, time.Duration(intervalMin)*time.Minute)
}

// Stop cleanly terminates the periodic evaluation scanner.
func (e *Evaluator) Stop() {
	if e == nil || !e.running.Swap(false) {
		return
	}
	if e.scanCancel != nil {
		e.scanCancel()
	}
	if e.scanDone != nil {
		<-e.scanDone
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

	// Update Jev client if URL or key changed
	if newCfg.Jev.BaseURL != oldCfg.Jev.BaseURL || newCfg.Jev.APIKey != oldCfg.Jev.APIKey || newCfg.Jev.Model != oldCfg.Jev.Model {
		e.jevClient = jev.NewClient(jev.Config{
			BaseURL: newCfg.Jev.BaseURL,
			APIKey:  newCfg.Jev.APIKey,
			Model:   newCfg.Jev.Model,
			Timeout: time.Duration(newCfg.Jev.TimeoutMS) * time.Millisecond,
		})
	}

	wasEnabled := oldCfg.Enabled && oldCfg.PeriodicScan
	nowEnabled := newCfg.Enabled && newCfg.PeriodicScan

	if !nowEnabled && wasEnabled {
		e.Stop()
	} else if nowEnabled && (!wasEnabled || newCfg.ScanIntervalMinutes != oldCfg.ScanIntervalMinutes) {
		e.Stop()
		e.Start(ctx)
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

		if call.Action == "BUY_CE" || call.Action == "BUY_PE" || call.Action == "BUY" {
			if call.TargetPrice > 0 && ltp >= call.TargetPrice {
				status = "TARGET_HIT"
				pnlPct = ((ltp - call.EntryPrice) / call.EntryPrice) * 100.0
			} else if call.StopLossPrice > 0 && ltp <= call.StopLossPrice {
				status = "SL_HIT"
				pnlPct = ((ltp - call.EntryPrice) / call.EntryPrice) * 100.0
			}
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

// ScanNow triggers an on-demand evaluation of active ATM strikes against Jev.
func (e *Evaluator) ScanNow(ctx context.Context, underlying string) ([]store.Evaluation, error) {
	if e == nil {
		return nil, errors.New("evaluator: nil instance")
	}
	if e.jevClient == nil {
		return nil, errors.New("evaluator: jev client not configured")
	}
	if e.quoteProvider == nil {
		return nil, errors.New("evaluator: quote provider not configured")
	}

	if underlying == "" {
		underlying = "NIFTY"
	}

	e.cfgMu.RLock()
	minConfidence := e.cfg.MinConfidence
	minScore := e.cfg.MinScore
	targetPct := e.cfg.TargetPct
	stopLossPct := e.cfg.StopLossPct
	shadowMode := e.cfg.ShadowMode
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
		jevRes, jerr := e.jevClient.EvaluateSetup(ctx, stateJSON)
		durationMS := int(time.Since(start).Milliseconds())

		if jerr != nil {
			e.log.Warn("jev evaluation failed", "event", "jev_eval_failed", "instrument", cand.Key, "error", jerr)
			continue
		}

		action := strings.ToUpper(strings.TrimSpace(jevRes.Action))
		if action == "PASS" || action == "HOLD" || action == "" {
			continue
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

		eval := store.Evaluation{
			Timestamp:     time.Now(),
			InstrumentKey: cand.Key,
			Underlying:    underlying,
			SpotPrice:     spot,
			OptionLTP:     cand.LTP,
			Action:        action,
			Confidence:    jevRes.Confidence,
			Score:         jevRes.Score,
			DurationMS:    durationMS,
			StateSnapshot: stateJSON,
			Shadow:        shadowMode,
			TargetPrice:   targetPrice,
			StopLossPrice: stopLossPrice,
			Status:        "PENDING",
		}

		// Filter for high-conviction setups only
		if eval.Score >= minScore && eval.Confidence >= minConfidence {
			if e.store != nil {
				id, serr := e.store.SaveEvaluation(ctx, eval)
				if serr == nil {
					eval.ID = id
				}
			}

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

			e.log.Info("new high conviction jev call tracked", "event", "ai_call_detected",
				"id", eval.ID, "action", eval.Action, "instrument", eval.InstrumentKey,
				"entry", eval.OptionLTP, "target", eval.TargetPrice, "stop", eval.StopLossPrice,
				"score", eval.Score, "conf", eval.Confidence)

			if e.onNewCall != nil {
				e.onNewCall(eval)
			}
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

// EvaluateContract performs on-demand evaluation of a specified contract using Jev and tracks it.
func (e *Evaluator) EvaluateContract(ctx context.Context, setup ContractSetup) (store.Evaluation, error) {
	if e == nil {
		return store.Evaluation{}, errors.New("evaluator: nil instance")
	}
	if e.jevClient == nil {
		return store.Evaluation{}, errors.New("evaluator: jev client not configured")
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
	jevRes, jerr := e.jevClient.EvaluateSetup(ctx, stateJSON)
	durationMS := int(time.Since(start).Milliseconds())
	if jerr != nil {
		return store.Evaluation{}, fmt.Errorf("evaluator: jev evaluation failed: %w", jerr)
	}

	e.cfgMu.RLock()
	targetPct := e.cfg.TargetPct
	stopLossPct := e.cfg.StopLossPct
	shadowMode := e.cfg.ShadowMode
	e.cfgMu.RUnlock()

	if targetPct <= 0 {
		targetPct = 8.0
	}
	if stopLossPct <= 0 {
		stopLossPct = 5.0
	}

	action := strings.ToUpper(strings.TrimSpace(jevRes.Action))
	if action == "BUY" {
		if setup.OptionType == "CE" {
			action = "BUY_CE"
		} else {
			action = "BUY_PE"
		}
	}

	targetPrice := math.Round(setup.OptionLTP*(1.0+targetPct/100.0)*100) / 100
	stopLossPrice := math.Round(setup.OptionLTP*(1.0-stopLossPct/100.0)*100) / 100

	eval := store.Evaluation{
		Timestamp:     time.Now(),
		InstrumentKey: setup.InstrumentKey,
		Underlying:    setup.Underlying,
		SpotPrice:     spot,
		OptionLTP:     setup.OptionLTP,
		Action:        action,
		Confidence:    jevRes.Confidence,
		Score:         jevRes.Score,
		DurationMS:    durationMS,
		StateSnapshot: stateJSON,
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

	if action == "BUY_CE" || action == "BUY_PE" || action == "BUY" {
		e.TrackEvaluation(eval)
		if e.onNewCall != nil {
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

func (e *Evaluator) runScanner(ctx context.Context, interval time.Duration) {
	defer close(e.scanDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	e.log.Info("ai evaluator periodic scanner started", "event", "ai_scanner_started", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			e.log.Info("ai evaluator periodic scanner stopped", "event", "ai_scanner_stopped")
			return
		case <-ticker.C:
			scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, _ = e.ScanNow(scanCtx, "NIFTY")
			cancel()
		}
	}
}
