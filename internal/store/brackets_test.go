package store

import (
	"context"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/bracket"
)

func TestStoreBracketsCRUD(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)

	b := bracket.Bracket{
		ID:                  "brk-test-1",
		ExchangeSegment:     "NSE",
		TradingSymbol:       "RELIANCE-EQ",
		InstrumentKey:       "2885",
		TransactionType:     "BUY",
		Quantity:            50,
		Product:             "MIS",
		EntryPrice:          2500.50,
		EntryOrderID:        "ord-entry-1",
		TargetPrice:         2550.00,
		StopLossPrice:       2450.00,
		TrailingStop:        10.00,
		ExitOrderID:         "",
		ExitPrice:           0,
		Status:              bracket.StatusPendingEntry,
		PnL:                 0,
		ExchangeStopMode:    bracket.ExchangeStopSLM,
		ExchangeStopOrderID: "xstop-1",
		ExchangeStopPrice:   2450.00,
		ExchangeStopStatus:  bracket.ExchangeStopWorking,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	// 1. Create
	if err := s.CreateBracket(ctx, b); err != nil {
		t.Fatalf("CreateBracket failed: %v", err)
	}

	// 2. Get
	got, err := s.GetBracket(ctx, b.ID)
	if err != nil || got == nil {
		t.Fatalf("GetBracket failed: %v, got=%+v", err, got)
	}
	if got.ID != b.ID || got.TradingSymbol != b.TradingSymbol || got.Status != bracket.StatusPendingEntry {
		t.Fatalf("unexpected fetched bracket: %+v", got)
	}
	if got.EntryPrice != 2500.50 || got.Quantity != 50 {
		t.Fatalf("unexpected prices or qty: %+v", got)
	}
	if got.ExchangeStopMode != bracket.ExchangeStopSLM ||
		got.ExchangeStopOrderID != "xstop-1" ||
		got.ExchangeStopPrice != 2450.00 ||
		got.ExchangeStopStatus != bracket.ExchangeStopWorking {
		t.Fatalf("exchange stop fields did not round-trip: %+v", got)
	}

	// 3. Update
	b.Status = bracket.StatusInPosition
	b.EntryPrice = 2501.00
	b.UpdatedAt = now.Add(time.Minute)
	if err := s.UpdateBracket(ctx, b); err != nil {
		t.Fatalf("UpdateBracket failed: %v", err)
	}

	gotUpdated, err := s.GetBracket(ctx, b.ID)
	if err != nil || gotUpdated == nil {
		t.Fatalf("GetBracket after update failed: %v", err)
	}
	if gotUpdated.Status != bracket.StatusInPosition || gotUpdated.EntryPrice != 2501.00 {
		t.Fatalf("expected updated bracket, got %+v", gotUpdated)
	}

	// 4. List
	list, err := s.ListBrackets(ctx, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListBrackets failed: %v, len=%d", err, len(list))
	}

	// 5. Active Brackets
	active, err := s.GetActiveBrackets(ctx)
	if err != nil || len(active) != 1 {
		t.Fatalf("GetActiveBrackets failed: %v, len=%d", err, len(active))
	}

	// 6. Complete bracket and verify active returns empty
	b.Status = bracket.StatusTargetHit
	b.ExitPrice = 2550.00
	b.PnL = (2550.00 - 2501.00) * 50
	if err := s.UpdateBracket(ctx, b); err != nil {
		t.Fatalf("UpdateBracket to TARGET_HIT failed: %v", err)
	}

	activeAfter, err := s.GetActiveBrackets(ctx)
	if err != nil || len(activeAfter) != 0 {
		t.Fatalf("expected 0 active brackets after target hit, got %d", len(activeAfter))
	}
}
