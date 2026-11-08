package store

import (
	"context"
	"testing"
	"time"
)

func TestSaveAndGetPlaybook(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	baseTime := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return baseTime }

	p := Playbook{
		PlaybookDate:  "2026-11-09",
		Bias:          "BULLISH",
		Regime:        "BREAKOUT",
		KeySupport:    24450.0,
		KeyResistance: 24650.0,
		MaxPain:       24500.0,
		PCR:           1.18,
		Narrative:     "GIFT Nifty strong, global tech rally.",
		JevRulesJSON:  `{"min_score": 4, "min_confidence": 0.8}`,
		Source:        "antigravity",
		CreatedAt:     baseTime,
	}

	id, err := s.SavePlaybook(ctx, p)
	if err != nil {
		t.Fatalf("SavePlaybook: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive playbook ID, got %d", id)
	}

	latest, err := s.GetLatestPlaybook(ctx)
	if err != nil {
		t.Fatalf("GetLatestPlaybook: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest playbook, got nil")
	}

	if latest.PlaybookDate != p.PlaybookDate {
		t.Errorf("PlaybookDate = %q, want %q", latest.PlaybookDate, p.PlaybookDate)
	}
	if latest.Bias != p.Bias {
		t.Errorf("Bias = %q, want %q", latest.Bias, p.Bias)
	}
	if latest.KeySupport != p.KeySupport {
		t.Errorf("KeySupport = %f, want %f", latest.KeySupport, p.KeySupport)
	}
	if latest.KeyResistance != p.KeyResistance {
		t.Errorf("KeyResistance = %f, want %f", latest.KeyResistance, p.KeyResistance)
	}
	if latest.Source != p.Source {
		t.Errorf("Source = %q, want %q", latest.Source, p.Source)
	}

	byDate, err := s.GetPlaybookByDate(ctx, "2026-11-09")
	if err != nil {
		t.Fatalf("GetPlaybookByDate: %v", err)
	}
	if byDate == nil || byDate.ID != id {
		t.Fatalf("expected playbook with ID %d, got %+v", id, byDate)
	}

	notFound, err := s.GetPlaybookByDate(ctx, "2020-01-01")
	if err != nil {
		t.Fatalf("GetPlaybookByDate nonexistent: %v", err)
	}
	if notFound != nil {
		t.Fatalf("expected nil for nonexistent date, got %+v", notFound)
	}
}

func TestSaveAndListEvaluations(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)

	for i := 0; i < 5; i++ {
		e := Evaluation{
			Timestamp:     now.Add(time.Duration(i) * time.Second),
			InstrumentKey: "NIFTY24500CE",
			Underlying:    "NIFTY",
			SpotPrice:     24520.0 + float64(i),
			OptionLTP:     140.0 + float64(i),
			Action:        "BUY",
			Confidence:    0.82 + float64(i)*0.01,
			Score:         4,
			DurationMS:    180,
			StateSnapshot: `{"vwap": 138.5}`,
			Shadow:        true,
		}
		if err := s.SaveEvaluation(ctx, e); err != nil {
			t.Fatalf("SaveEvaluation [%d]: %v", i, err)
		}
	}

	evals, err := s.ListEvaluations(ctx, 3)
	if err != nil {
		t.Fatalf("ListEvaluations: %v", err)
	}
	if len(evals) != 3 {
		t.Fatalf("expected 3 evaluations, got %d", len(evals))
	}
	// Verify descending order (most recent first)
	if evals[0].SpotPrice < evals[1].SpotPrice {
		t.Errorf("expected descending timestamp order, got %f then %f", evals[0].SpotPrice, evals[1].SpotPrice)
	}
}
