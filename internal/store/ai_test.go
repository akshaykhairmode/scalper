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

	var lastID int64
	for i := 0; i < 5; i++ {
		e := Evaluation{
			Timestamp:     now.Add(time.Duration(i) * time.Second),
			InstrumentKey: "NIFTY24500CE",
			Underlying:    "NIFTY",
			SpotPrice:     24520.0 + float64(i),
			OptionLTP:     140.0 + float64(i),
			Action:        "BUY_CE",
			Confidence:    0.82 + float64(i)*0.01,
			Score:         4,
			DurationMS:    180,
			StateSnapshot: `{"vwap": 138.5}`,
			Shadow:        true,
			TargetPrice:   151.2,
			StopLossPrice: 133.0,
			Status:        "PENDING",
		}
		id, err := s.SaveEvaluation(ctx, e)
		if err != nil {
			t.Fatalf("SaveEvaluation [%d]: %v", i, err)
		}
		lastID = id
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

	// Test Active Evaluations
	active, err := s.GetActiveEvaluations(ctx)
	if err != nil {
		t.Fatalf("GetActiveEvaluations: %v", err)
	}
	if len(active) != 5 {
		t.Fatalf("expected 5 active evaluations, got %d", len(active))
	}

	// Update last evaluation outcome to TARGET_HIT
	if err := s.UpdateEvaluationOutcome(ctx, lastID, "TARGET_HIT", 152.0, 8.57, time.Now()); err != nil {
		t.Fatalf("UpdateEvaluationOutcome: %v", err)
	}

	// Now active count should be 4
	activeAfter, err := s.GetActiveEvaluations(ctx)
	if err != nil {
		t.Fatalf("GetActiveEvaluations after update: %v", err)
	}
	if len(activeAfter) != 4 {
		t.Fatalf("expected 4 active evaluations, got %d", len(activeAfter))
	}

	// Test stats
	stats, err := s.GetEvaluationStats(ctx)
	if err != nil {
		t.Fatalf("GetEvaluationStats: %v", err)
	}
	if stats.TotalCalls != 5 || stats.TargetHits != 1 || stats.PendingCalls != 4 || stats.WinRatePct != 100.0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestDeleteEvaluation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	newEval := func(key, action string) int64 {
		id, err := s.SaveEvaluation(ctx, Evaluation{
			Timestamp:     now,
			InstrumentKey: key,
			Underlying:    "NIFTY",
			SpotPrice:     24520.0,
			OptionLTP:     140.0,
			Action:        action,
			TargetPrice:   151.2,
			StopLossPrice: 133.0,
			Status:        "PENDING",
		})
		if err != nil {
			t.Fatalf("SaveEvaluation(%s): %v", key, err)
		}
		return id
	}

	deletedID := newEval("NIFTY24500CE", "BUY_CE")
	keptID := newEval("NIFTY24500PE", "BUY_PE")

	if err := s.DeleteEvaluation(ctx, deletedID); err != nil {
		t.Fatalf("DeleteEvaluation: %v", err)
	}

	evals, err := s.ListEvaluations(ctx, 10)
	if err != nil {
		t.Fatalf("ListEvaluations: %v", err)
	}
	if len(evals) != 1 || evals[0].ID != keptID {
		t.Fatalf("expected only evaluation %d to remain, got %+v", keptID, evals)
	}

	active, err := s.GetActiveEvaluations(ctx)
	if err != nil {
		t.Fatalf("GetActiveEvaluations: %v", err)
	}
	if len(active) != 1 || active[0].ID != keptID {
		t.Fatalf("expected deleted evaluation to be untracked, got %+v", active)
	}

	stats, err := s.GetEvaluationStats(ctx)
	if err != nil {
		t.Fatalf("GetEvaluationStats: %v", err)
	}
	if stats.TotalCalls != 1 || stats.PendingCalls != 1 {
		t.Fatalf("deleted evaluation still counted in stats: %+v", stats)
	}

	// Deleting an unknown ID is a no-op and invalid IDs are rejected.
	if err := s.DeleteEvaluation(ctx, 999999); err != nil {
		t.Fatalf("DeleteEvaluation unknown id: %v", err)
	}
	if err := s.DeleteEvaluation(ctx, 0); err == nil {
		t.Fatal("expected error for invalid evaluation id")
	}
}

func TestEvaluationPaperTradeProvenanceAndStats(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Second)

	// Actionable call proposed by the agent and validated by Jev.
	id, err := s.SaveEvaluation(ctx, Evaluation{
		Timestamp:     base,
		InstrumentKey: "NIFTY24500CE",
		Underlying:    "NIFTY",
		OptionLTP:     150,
		Action:        "BUY_CE",
		Confidence:    0.82,
		Score:         4,
		TargetPrice:   162,
		StopLossPrice: 142.5,
		Status:        "PENDING",
		Source:        "agent+jev",
		Reasoning:     "Agent: breakout | Jev validation: BUY, score 4/5",
	})
	if err != nil {
		t.Fatalf("SaveEvaluation BUY_CE: %v", err)
	}

	// Non-actionable verdicts must never appear in paper stats or the calls list.
	if _, err := s.SaveEvaluation(ctx, Evaluation{
		Timestamp:     base.Add(time.Second),
		InstrumentKey: "NIFTY24500CE",
		Underlying:    "NIFTY",
		Action:        "HOLD",
		Status:        "PENDING",
		Source:        "jev",
	}); err != nil {
		t.Fatalf("SaveEvaluation HOLD: %v", err)
	}

	// Expired paper call.
	if _, err := s.SaveEvaluation(ctx, Evaluation{
		Timestamp:     base.Add(2 * time.Second),
		InstrumentKey: "NIFTY24500PE",
		Underlying:    "NIFTY",
		OptionLTP:     120,
		Action:        "BUY_PE",
		Confidence:    0.7,
		Score:         3,
		TargetPrice:   129.6,
		StopLossPrice: 114,
		Status:        "EXPIRED",
		PnLPct:        -0.1,
		Source:        "agent+jev",
	}); err != nil {
		t.Fatalf("SaveEvaluation EXPIRED: %v", err)
	}

	// Default source is recorded for legacy writers.
	if _, err := s.SaveEvaluation(ctx, Evaluation{
		Timestamp:     base.Add(3 * time.Second),
		InstrumentKey: "NIFTY24600CE",
		Underlying:    "NIFTY",
		OptionLTP:     90,
		Action:        "BUY_CE",
		Status:        "PENDING",
	}); err != nil {
		t.Fatalf("SaveEvaluation default source: %v", err)
	}

	evals, err := s.ListEvaluations(ctx, 50)
	if err != nil {
		t.Fatalf("ListEvaluations: %v", err)
	}
	if len(evals) != 3 {
		t.Fatalf("expected 3 actionable evaluations, got %d", len(evals))
	}
	var found *Evaluation
	for i := range evals {
		if evals[i].ID == id {
			found = &evals[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected to find the agent+jev evaluation")
	}
	if found.Source != "agent+jev" {
		t.Errorf("source = %q, want agent+jev", found.Source)
	}
	if found.Reasoning == "" {
		t.Error("expected reasoning to round-trip")
	}
	if found.CurrentPrice != 0 {
		t.Errorf("runtime-only current price should not persist, got %f", found.CurrentPrice)
	}

	var defaultSource string
	for _, e := range evals {
		if e.InstrumentKey == "NIFTY24600CE" {
			defaultSource = e.Source
		}
	}
	if defaultSource != "jev" {
		t.Errorf("default source = %q, want jev", defaultSource)
	}

	stats, err := s.GetEvaluationStats(ctx)
	if err != nil {
		t.Fatalf("GetEvaluationStats: %v", err)
	}
	if stats.TotalCalls != 3 {
		t.Errorf("total_calls = %d, want 3 (HOLD excluded)", stats.TotalCalls)
	}
	if stats.ExpiredCalls != 1 {
		t.Errorf("expired_calls = %d, want 1", stats.ExpiredCalls)
	}
	if stats.PendingCalls != 2 {
		t.Errorf("pending_calls = %d, want 2", stats.PendingCalls)
	}
	if stats.ResolvedCalls != 0 || stats.WinRatePct != 0 {
		t.Errorf("resolved/win rate = %d/%f, want 0/0", stats.ResolvedCalls, stats.WinRatePct)
	}

	// Resolve one call; win rate is target hits over resolved (target + stop) calls.
	if err := s.UpdateEvaluationOutcome(ctx, id, "TARGET_HIT", 162, 7.9, time.Now()); err != nil {
		t.Fatalf("UpdateEvaluationOutcome: %v", err)
	}
	stats, err = s.GetEvaluationStats(ctx)
	if err != nil {
		t.Fatalf("GetEvaluationStats after update: %v", err)
	}
	if stats.TargetHits != 1 || stats.ResolvedCalls != 1 || stats.WinRatePct != 100.0 {
		t.Errorf("unexpected stats after target hit: %+v", stats)
	}
	if stats.ExpiredCalls != 1 {
		t.Errorf("expired_calls = %d, want 1", stats.ExpiredCalls)
	}
}
