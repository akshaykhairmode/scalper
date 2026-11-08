package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/ai/evaluator"
	"github.com/akshaykhairmode/scalper/internal/ai/jev"
	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/store"
)

type mockAIStore struct {
	playbooks   []store.Playbook
	evaluations []store.Evaluation
}

func (m *mockAIStore) SavePlaybook(_ context.Context, p store.Playbook) (int64, error) {
	id := int64(len(m.playbooks) + 1)
	p.ID = id
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	m.playbooks = append(m.playbooks, p)
	return id, nil
}

func (m *mockAIStore) GetLatestPlaybook(_ context.Context) (*store.Playbook, error) {
	if len(m.playbooks) == 0 {
		return nil, nil
	}
	p := m.playbooks[len(m.playbooks)-1]
	return &p, nil
}

func (m *mockAIStore) GetPlaybookByDate(_ context.Context, date string) (*store.Playbook, error) {
	for i := len(m.playbooks) - 1; i >= 0; i-- {
		if m.playbooks[i].PlaybookDate == date {
			p := m.playbooks[i]
			return &p, nil
		}
	}
	return nil, nil
}

func (m *mockAIStore) SaveEvaluation(_ context.Context, e store.Evaluation) (int64, error) {
	e.ID = int64(len(m.evaluations) + 1)
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	m.evaluations = append(m.evaluations, e)
	return e.ID, nil
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

func (m *mockAIStore) GetEvaluationStats(_ context.Context) (store.EvaluationStats, error) {
	var stats store.EvaluationStats
	stats.TotalCalls = len(m.evaluations)
	for _, e := range m.evaluations {
		switch e.Status {
		case "TARGET_HIT":
			stats.TargetHits++
		case "SL_HIT":
			stats.StopLossHits++
		case "PENDING":
			stats.PendingCalls++
		}
	}
	closed := stats.TargetHits + stats.StopLossHits
	if closed > 0 {
		stats.WinRatePct = float64(stats.TargetHits) / float64(closed) * 100.0
	}
	return stats, nil
}

func (m *mockAIStore) ListEvaluations(_ context.Context, limit int) ([]store.Evaluation, error) {
	if limit <= 0 || limit > len(m.evaluations) {
		limit = len(m.evaluations)
	}
	out := make([]store.Evaluation, limit)
	copy(out, m.evaluations[len(m.evaluations)-limit:])
	return out, nil
}

func TestAIPlaybookAndEvaluationHandlers(t *testing.T) {
	mockStore := &mockAIStore{}
	s := newTestServer(t, serverOpts{db: fakeDB{}})
	defer s.Close()
	s.aiStore = mockStore

	// 1. Initial GET /api/ai/playbook returns 200 with nil playbook
	rec := do(t, s, localRequest(http.MethodGet, "/api/ai/playbook"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var initResp playbookResponse
	if err := json.NewDecoder(rec.Body).Decode(&initResp); err != nil {
		t.Fatalf("decode init playbook: %v", err)
	}
	if initResp.Playbook != nil {
		t.Fatalf("expected nil playbook, got %+v", initResp.Playbook)
	}

	// 2. POST /api/ai/playbook creates a playbook
	payload := []byte(`{
		"playbook_date": "2026-11-09",
		"bias": "BULLISH",
		"regime": "BREAKOUT",
		"key_support": 24450.0,
		"key_resistance": 24650.0,
		"max_pain": 24500.0,
		"pcr": 1.18,
		"narrative": "GIFT Nifty strong, global tech rally.",
		"jev_rules_json": "{\"min_score\": 4}",
		"source": "antigravity"
	}`)

	saveRec := do(t, s, localRequestWithBody(http.MethodPost, "/api/ai/playbook", payload))
	if saveRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", saveRec.Code, saveRec.Body.String())
	}
	var saveResp savePlaybookResponse
	if err := json.NewDecoder(saveRec.Body).Decode(&saveResp); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	if !saveResp.Success || saveResp.ID <= 0 {
		t.Fatalf("unexpected save response: %+v", saveResp)
	}
	if saveResp.Playbook.Bias != "BULLISH" || saveResp.Playbook.KeySupport != 24450.0 {
		t.Fatalf("playbook fields mismatch: %+v", saveResp.Playbook)
	}

	// 3. GET /api/ai/playbook now returns the saved playbook
	getRec := do(t, s, localRequest(http.MethodGet, "/api/ai/playbook"))
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", getRec.Code)
	}
	var getResp playbookResponse
	if err := json.NewDecoder(getRec.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if getResp.Playbook == nil || getResp.Playbook.ID != saveResp.ID {
		t.Fatalf("expected playbook ID %d, got %+v", saveResp.ID, getResp.Playbook)
	}

	// 4. GET /api/ai/playbook?date=2026-11-09 returns the playbook
	dateRec := do(t, s, localRequest(http.MethodGet, "/api/ai/playbook?date=2026-11-09"))
	if dateRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", dateRec.Code)
	}

	// 5. GET /api/ai/evaluations returns evaluations list
	_, _ = mockStore.SaveEvaluation(context.Background(), store.Evaluation{
		InstrumentKey: "NIFTY24500CE",
		Underlying:    "NIFTY",
		SpotPrice:     24520.0,
		Action:        "BUY",
		Confidence:    0.85,
		Score:         4,
	})

	evalRec := do(t, s, localRequest(http.MethodGet, "/api/ai/evaluations?limit=10"))
	if evalRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", evalRec.Code)
	}
	var evalResp evaluationsResponse
	if err := json.NewDecoder(evalRec.Body).Decode(&evalResp); err != nil {
		t.Fatalf("decode evaluations: %v", err)
	}
	if len(evalResp.Evaluations) != 1 {
		t.Fatalf("expected 1 evaluation, got %d", len(evalResp.Evaluations))
	}
	if evalResp.Evaluations[0].Action != "BUY" {
		t.Errorf("action = %q, want BUY", evalResp.Evaluations[0].Action)
	}

	// 6. GET /api/ai/evaluations/stats returns stats
	statsRec := do(t, s, localRequest(http.MethodGet, "/api/ai/evaluations/stats"))
	if statsRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", statsRec.Code)
	}
	var statsResp evaluationStatsResponse
	if err := json.NewDecoder(statsRec.Body).Decode(&statsResp); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if statsResp.Stats.TotalCalls != 1 {
		t.Errorf("total_calls = %d, want 1", statsResp.Stats.TotalCalls)
	}

	// 7. Verify 503 when evaluator has no valid client
	s.evaluator = nil
	nilAskRec := do(t, s, localRequestWithBody(http.MethodPost, "/api/ai/evaluations/ask", []byte(`{
		"underlying": "NIFTY",
		"instrument_key": "NIFTY24500CE",
		"strike": "24500",
		"option_type": "CE",
		"ltp": 120.0
	}`)))
	if nilAskRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable when evaluator is nil, got %d", nilAskRec.Code)
	}

	nilScanRec := do(t, s, localRequest(http.MethodPost, "/api/ai/evaluations/scan"))
	if nilScanRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable when evaluator is nil, got %d", nilScanRec.Code)
	}

	// 8. Setup mock Jev server and test Ask & Scan
	mockJevSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{
				"action":        map[string]any{"choice": "BUY"},
				"breakout_prob": map[string]any{"noul": 0.88},
				"conviction":    map[string]any{"score": 5},
			},
		})
	}))
	defer mockJevSrv.Close()

	jevCli := jev.NewClient(jev.Config{
		BaseURL: mockJevSrv.URL,
		Model:   "jev-test",
		Timeout: time.Second,
	})

	s.evaluator = evaluator.New(evaluator.Options{
		Config: config.AI{
			Enabled:       true,
			TargetPct:     8.0,
			StopLossPct:   5.0,
			MinConfidence: 0.75,
			MinScore:      4,
		},
		Store:         mockStore,
		JevClient:     jevCli,
		QuoteProvider: s,
	})

	askRec := do(t, s, localRequestWithBody(http.MethodPost, "/api/ai/evaluations/ask", []byte(`{
		"underlying": "NIFTY",
		"instrument_key": "NIFTY24500CE",
		"strike": "24500",
		"option_type": "CE",
		"ltp": 100.0
	}`)))
	if askRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from ask endpoint, got %d: %s", askRec.Code, askRec.Body.String())
	}
	var askResp askEvaluationResponse
	if err := json.NewDecoder(askRec.Body).Decode(&askResp); err != nil {
		t.Fatalf("decode ask response: %v", err)
	}
	if !askResp.Success || askResp.Action != "BUY_CE" {
		t.Errorf("expected BUY_CE action, got %q", askResp.Action)
	}
	if askResp.TargetPrice != 108.0 { // 100 * 1.08 = 108
		t.Errorf("expected target price 108.0, got %f", askResp.TargetPrice)
	}
	if askResp.StopLossPrice != 95.0 { // 100 * 0.95 = 95
		t.Errorf("expected stop loss price 95.0, got %f", askResp.StopLossPrice)
	}
}
