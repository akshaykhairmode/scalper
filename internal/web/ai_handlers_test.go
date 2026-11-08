package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

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

func (m *mockAIStore) SaveEvaluation(_ context.Context, e store.Evaluation) error {
	e.ID = int64(len(m.evaluations) + 1)
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	m.evaluations = append(m.evaluations, e)
	return nil
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
	_ = mockStore.SaveEvaluation(context.Background(), store.Evaluation{
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
}
