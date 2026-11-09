package evaluator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/ai/features"
	"github.com/akshaykhairmode/scalper/internal/ai/jev"
	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/store"
)

type mockAIStore struct {
	evaluations []store.Evaluation
}

func (m *mockAIStore) SaveEvaluation(_ context.Context, e store.Evaluation) (int64, error) {
	e.ID = int64(len(m.evaluations) + 1)
	m.evaluations = append(m.evaluations, e)
	return e.ID, nil
}

func (m *mockAIStore) UpdateEvaluationOutcome(_ context.Context, id int64, status string, exitPrice, pnlPct float64, exitTime time.Time) error {
	for i := range m.evaluations {
		if m.evaluations[i].ID == id {
			m.evaluations[i].Status = status
			m.evaluations[i].ExitPrice = exitPrice
			m.evaluations[i].PnLPct = pnlPct
			m.evaluations[i].ExitTime = &exitTime
			return nil
		}
	}
	return nil
}

func (m *mockAIStore) GetActiveEvaluations(_ context.Context) ([]store.Evaluation, error) {
	var active []store.Evaluation
	for _, e := range m.evaluations {
		if e.Status == "PENDING" {
			active = append(active, e)
		}
	}
	return active, nil
}

func (m *mockAIStore) GetEvaluationStats(_ context.Context) (store.EvaluationStats, error) {
	var s store.EvaluationStats
	s.TotalCalls = len(m.evaluations)
	for _, e := range m.evaluations {
		switch e.Status {
		case "TARGET_HIT":
			s.TargetHits++
		case "SL_HIT":
			s.StopLossHits++
		case "PENDING":
			s.PendingCalls++
		}
	}
	closed := s.TargetHits + s.StopLossHits
	if closed > 0 {
		s.WinRatePct = float64(s.TargetHits) / float64(closed) * 100.0
	}
	return s, nil
}

func (m *mockAIStore) ListEvaluations(_ context.Context, limit int) ([]store.Evaluation, error) {
	if limit <= 0 || limit > len(m.evaluations) {
		limit = len(m.evaluations)
	}
	return m.evaluations[:limit], nil
}

func (m *mockAIStore) GetLatestPlaybook(_ context.Context) (*store.Playbook, error) {
	return &store.Playbook{Bias: "BULLISH", Regime: "TRENDING"}, nil
}

type mockQuoteProvider struct {
	spot  float64
	ceLTP float64
	peLTP float64
}

func (m *mockQuoteProvider) GetSpotPrice(_ context.Context, _ string) (float64, error) {
	return m.spot, nil
}

func (m *mockQuoteProvider) GetATMStrikes(_ context.Context, _ string) (string, string, float64, float64, error) {
	return "NIFTY24500CE", "NIFTY24500PE", m.ceLTP, m.peLTP, nil
}

func (m *mockQuoteProvider) GetMarketDepth(_ context.Context, _ string) (features.OrderBookMetrics, error) {
	return features.OrderBookMetrics{ImbalanceRatio: 0.5, Spread: 0.1}, nil
}

func TestEvaluatorOnTick(t *testing.T) {
	mockStore := &mockAIStore{}
	var outcomeCalled bool
	var outcomeEval store.Evaluation

	ev := New(Options{
		Config: config.AI{
			Enabled:      true,
			PeriodicScan: false,
		},
		Store: mockStore,
		OnOutcome: func(eval store.Evaluation) {
			outcomeCalled = true
			outcomeEval = eval
		},
	})

	// Manually add an active call
	ev.activeMu.Lock()
	ev.activeCalls[1] = &ActiveCall{
		ID:            1,
		InstrumentKey: "NIFTY24500CE",
		Action:        "BUY_CE",
		EntryPrice:    100.0,
		TargetPrice:   108.0, // +8%
		StopLossPrice: 95.0,  // -5%
		Score:         5,
		Confidence:    0.85,
		CreatedAt:     time.Now(),
	}
	ev.activeCount = 1
	ev.activeMu.Unlock()

	ctx := context.Background()

	// 1. Tick within range: neither target nor stop hit
	ev.OnTick(ctx, "NIFTY24500CE", 104.0)
	if outcomeCalled {
		t.Fatal("outcome should not be called for intermediate price")
	}
	if ev.ActiveCallsCount() != 1 {
		t.Fatalf("expected 1 active call, got %d", ev.ActiveCallsCount())
	}

	// 2. Tick hits Target (108.5 >= 108.0)
	ev.OnTick(ctx, "NIFTY24500CE", 108.5)
	if !outcomeCalled {
		t.Fatal("expected outcome callback to be called on target hit")
	}
	if outcomeEval.Status != "TARGET_HIT" {
		t.Fatalf("expected TARGET_HIT, got %s", outcomeEval.Status)
	}
	if outcomeEval.PnLPct != 8.5 {
		t.Fatalf("expected PnLPct = 8.5, got %f", outcomeEval.PnLPct)
	}
	if ev.ActiveCallsCount() != 0 {
		t.Fatalf("active call count should be 0, got %d", ev.ActiveCallsCount())
	}
}

func TestEvaluatorScanNow(t *testing.T) {
	// Setup mock Jev server
	jevServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		choice := "BUY_CE"
		conf := 0.88
		score := 5
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{
				"action":        map[string]any{"choice": choice},
				"breakout_prob": map[string]any{"noul": conf},
				"conviction":    map[string]any{"score": score},
			},
		})
	}))
	defer jevServer.Close()

	mockStore := &mockAIStore{}
	mockQuotes := &mockQuoteProvider{
		spot:  24520.0,
		ceLTP: 150.0,
		peLTP: 120.0,
	}

	jevClient := jev.NewClient(jev.Config{
		BaseURL: jevServer.URL,
		Model:   "jev-test",
	})

	var newCallDetected bool
	ev := New(Options{
		Config: config.AI{
			Enabled:       true,
			PeriodicScan:  true,
			TargetPct:     8.0,
			StopLossPct:   5.0,
			MinConfidence: 0.75,
			MinScore:      4,
		},
		Store:         mockStore,
		JevClient:     jevClient,
		QuoteProvider: mockQuotes,
		OnNewCall: func(_ store.Evaluation) {
			newCallDetected = true
		},
	})

	evals, err := ev.ScanNow(context.Background(), "NIFTY")
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	if len(evals) == 0 {
		t.Fatal("expected evaluations from ScanNow")
	}
	if !newCallDetected {
		t.Fatal("expected new call callback to be triggered")
	}
	if ev.ActiveCallsCount() == 0 {
		t.Fatal("expected active calls to be populated")
	}

	// Verify target and stop loss calculations
	last := evals[0]
	if last.TargetPrice != 162.0 { // 150 * 1.08 = 162
		t.Fatalf("expected target 162.0, got %f", last.TargetPrice)
	}
	if last.StopLossPrice != 142.5 { // 150 * 0.95 = 142.5
		t.Fatalf("expected stop loss 142.5, got %f", last.StopLossPrice)
	}

	// Test EvaluateContract
	singleEval, err := ev.EvaluateContract(context.Background(), ContractSetup{
		Underlying:    "NIFTY",
		InstrumentKey: "NIFTY24500CE",
		Symbol:        "NIFTY24500CE",
		Strike:        "24500",
		OptionType:    "CE",
		OptionLTP:     200.0,
	})
	if err != nil {
		t.Fatalf("EvaluateContract failed: %v", err)
	}
	if singleEval.OptionLTP != 200.0 {
		t.Fatalf("expected LTP 200.0, got %f", singleEval.OptionLTP)
	}
	if singleEval.TargetPrice != 216.0 { // 200 * 1.08 = 216
		t.Fatalf("expected target 216.0, got %f", singleEval.TargetPrice)
	}
	if singleEval.StopLossPrice != 190.0 { // 200 * 0.95 = 190
		t.Fatalf("expected stop loss 190.0, got %f", singleEval.StopLossPrice)
	}

	stats, err := ev.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.TotalCalls == 0 {
		t.Fatal("expected positive TotalCalls in stats")
	}
}

func TestEvaluatorScannerStatus(t *testing.T) {
	cfg := config.AI{
		Enabled:             true,
		PeriodicScan:        true,
		ScanIntervalMinutes: 10,
	}
	ev := New(Options{
		Config: cfg,
	})

	// Before Start, running is false
	status := ev.ScannerStatus()
	if !status.Enabled {
		t.Error("expected scanner to be enabled")
	}
	if status.Running {
		t.Error("expected scanner not to be running before Start")
	}
	if status.IntervalMinutes != 10 {
		t.Errorf("expected interval 10, got %d", status.IntervalMinutes)
	}

	// Start scanner with context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ev.Start(ctx)

	status = ev.ScannerStatus()
	if !status.Running {
		t.Error("expected scanner to be running after Start")
	}
	if status.NextScanUnix <= 0 {
		t.Error("expected NextScanUnix > 0")
	}
	if status.SecondsUntilNext < 0 {
		t.Errorf("expected non-negative SecondsUntilNext, got %d", status.SecondsUntilNext)
	}

	// Stop scanner
	ev.Stop()
	status = ev.ScannerStatus()
	if status.Running {
		t.Error("expected scanner not running after Stop")
	}
	if status.NextScanUnix != 0 {
		t.Errorf("expected NextScanUnix 0 after stop, got %d", status.NextScanUnix)
	}

	// Test UpdateConfig with cancelled context doesn't kill background scanner
	ev.Start(context.Background())
	reqCtx, reqCancel := context.WithCancel(context.Background())
	reqCancel() // simulate HTTP request context being cancelled on request finish

	cfg.ScanIntervalMinutes = 5
	ev.UpdateConfig(reqCtx, cfg)

	status = ev.ScannerStatus()
	if !status.Running {
		t.Error("expected scanner to remain running after UpdateConfig with cancelled context")
	}
	if status.IntervalMinutes != 5 {
		t.Errorf("expected interval 5, got %d", status.IntervalMinutes)
	}
	ev.Stop()
}

type mockQuoteProviderWithHours struct {
	mockQuoteProvider
	openMap map[string]bool
}

func (m *mockQuoteProviderWithHours) IsUnderlyingMarketOpen(u string) bool {
	return m.openMap[u]
}

func (m *mockQuoteProviderWithHours) GetConfiguredUnderlyings() []string {
	return []string{"NIFTY", "SENSEX", "NATURALGAS"}
}

func TestScannerStatusMarketHours(t *testing.T) {
	qp := &mockQuoteProviderWithHours{
		openMap: map[string]bool{
			"NIFTY":      false,
			"SENSEX":     false,
			"NATURALGAS": true,
		},
	}

	ev := New(Options{
		Config: config.AI{
			Enabled:             true,
			PeriodicScan:        true,
			ScanIntervalMinutes: 15,
		},
		QuoteProvider: qp,
	})

	status := ev.ScannerStatus()
	if !status.AnyMarketOpen {
		t.Error("expected AnyMarketOpen to be true since NATURALGAS is open")
	}
	if len(status.OpenUnderlyings) != 1 || status.OpenUnderlyings[0] != "NATURALGAS" {
		t.Errorf("expected OpenUnderlyings [NATURALGAS], got %v", status.OpenUnderlyings)
	}
	if len(status.ClosedUnderlyings) != 2 {
		t.Errorf("expected 2 closed underlyings, got %v", status.ClosedUnderlyings)
	}

	// Now close all
	qp.openMap["NATURALGAS"] = false
	status = ev.ScannerStatus()
	if status.AnyMarketOpen {
		t.Error("expected AnyMarketOpen to be false when all closed")
	}
	if len(status.OpenUnderlyings) != 0 {
		t.Errorf("expected 0 open underlyings, got %v", status.OpenUnderlyings)
	}
	if len(status.ClosedUnderlyings) != 3 {
		t.Errorf("expected 3 closed underlyings, got %v", status.ClosedUnderlyings)
	}
}
