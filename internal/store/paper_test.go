package store

import (
	"context"
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/paper"
)

func TestPaperAccountAndBooks(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	account, err := s.EnsurePaperAccount(ctx, 250000)
	if err != nil {
		t.Fatalf("EnsurePaperAccount: %v", err)
	}
	if account.StartingBalance != 250000 {
		t.Fatalf("starting balance = %v", account.StartingBalance)
	}
	again, err := s.EnsurePaperAccount(ctx, 999999)
	if err != nil {
		t.Fatalf("EnsurePaperAccount again: %v", err)
	}
	if again.StartingBalance != 250000 {
		t.Fatalf("starting balance changed to %v", again.StartingBalance)
	}

	now := time.Unix(1700000000, 0)
	order := paper.Order{
		ID:              "paper_order_1",
		BracketID:       "brk_1",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType: "BUY",
		Product:         "MIS",
		OrderType:       "L",
		Quantity:        75,
		Price:           100,
		Status:          paper.StatusOpen,
		Tag:             "brk_1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.CreatePaperOrder(ctx, order); err != nil {
		t.Fatalf("CreatePaperOrder: %v", err)
	}
	order.Status = paper.StatusComplete
	order.FilledQuantity = 75
	order.FilledPrice = 100.5
	if err := s.UpdatePaperOrder(ctx, order); err != nil {
		t.Fatalf("UpdatePaperOrder: %v", err)
	}
	got, err := s.GetPaperOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("GetPaperOrder: %v", err)
	}
	if got.Status != paper.StatusComplete || got.FilledPrice != 100.5 {
		t.Fatalf("unexpected order: %+v", got)
	}
	orders, err := s.ListPaperOrders(ctx, 10)
	if err != nil || len(orders) != 1 {
		t.Fatalf("ListPaperOrders = %d, %v", len(orders), err)
	}

	trade := paper.Trade{
		OrderID:         order.ID,
		InstrumentKey:   order.InstrumentKey,
		TradingSymbol:   order.TradingSymbol,
		TransactionType: "BUY",
		Quantity:        75,
		Price:           100.5,
		ExecutedAt:      now,
	}
	if err := s.AddPaperTrade(ctx, trade); err != nil {
		t.Fatalf("AddPaperTrade: %v", err)
	}
	trades, err := s.ListPaperTrades(ctx, 10)
	if err != nil || len(trades) != 1 || trades[0].Price != 100.5 {
		t.Fatalf("ListPaperTrades = %+v, %v", trades, err)
	}

	position := paper.Position{
		InstrumentKey:   order.InstrumentKey,
		ExchangeSegment: "nse_fo",
		TradingSymbol:   order.TradingSymbol,
		Product:         "MIS",
		NetQuantity:     75,
		AveragePrice:    100.5,
		UpdatedAt:       now,
	}
	if err := s.UpsertPaperPosition(ctx, position); err != nil {
		t.Fatalf("UpsertPaperPosition: %v", err)
	}
	position.NetQuantity = 25
	if err := s.UpsertPaperPosition(ctx, position); err != nil {
		t.Fatalf("UpsertPaperPosition update: %v", err)
	}
	positions, err := s.ListPaperPositions(ctx)
	if err != nil || len(positions) != 1 || positions[0].NetQuantity != 25 {
		t.Fatalf("ListPaperPositions = %+v, %v", positions, err)
	}
}

func TestRecordPaperFillTransaction(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.EnsurePaperAccount(ctx, 100000); err != nil {
		t.Fatalf("EnsurePaperAccount: %v", err)
	}
	now := time.Unix(1700000000, 0)
	order := paper.Order{
		ID:              "paper_tx_1",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType: "BUY",
		Product:         "MIS",
		OrderType:       "L",
		Quantity:        75,
		Price:           100,
		Status:          paper.StatusOpen,
		Validity:        "DAY",
		Tag:             "brk_tx_1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.CreatePaperOrder(ctx, order); err != nil {
		t.Fatalf("CreatePaperOrder: %v", err)
	}
	filled := order
	filled.Status = paper.StatusComplete
	filled.FilledQuantity = 75
	filled.FilledPrice = 100.5
	position := paper.Position{
		InstrumentKey:   order.InstrumentKey,
		ExchangeSegment: order.ExchangeSegment,
		TradingSymbol:   order.TradingSymbol,
		Product:         order.Product,
		NetQuantity:     75,
		AveragePrice:    100.5,
		UpdatedAt:       now,
	}
	account := paper.Account{StartingBalance: 100000, RealizedPnL: 0, UpdatedAt: now}
	trade := paper.Trade{
		OrderID:         order.ID,
		InstrumentKey:   order.InstrumentKey,
		TradingSymbol:   order.TradingSymbol,
		TransactionType: "BUY",
		Quantity:        75,
		Price:           100.5,
		ExecutedAt:      now,
	}
	if err := s.RecordPaperFill(ctx, filled, position, account, trade); err != nil {
		t.Fatalf("RecordPaperFill: %v", err)
	}
	got, err := s.GetPaperOrder(ctx, order.ID)
	if err != nil || got.Status != paper.StatusComplete || got.FilledPrice != 100.5 {
		t.Fatalf("order after fill = %+v, %v", got, err)
	}
	positions, err := s.ListPaperPositions(ctx)
	if err != nil || len(positions) != 1 || positions[0].NetQuantity != 75 {
		t.Fatalf("positions after fill = %+v, %v", positions, err)
	}
	trades, err := s.ListPaperTrades(ctx, 10)
	if err != nil || len(trades) != 1 {
		t.Fatalf("trades after fill = %+v, %v", trades, err)
	}

	// An unknown order must abort the whole fill, not write position/trade rows.
	missing := filled
	missing.ID = "does-not-exist"
	missing.TradingSymbol = "OTHER"
	badPosition := position
	badPosition.InstrumentKey = "other"
	badTrade := trade
	badTrade.OrderID = missing.ID
	if err := s.RecordPaperFill(ctx, missing, badPosition, account, badTrade); err == nil {
		t.Fatal("expected unknown order fill to fail")
	}
	positions, _ = s.ListPaperPositions(ctx)
	if len(positions) != 1 {
		t.Fatalf("rollback leaked a position: %+v", positions)
	}
	trades, _ = s.ListPaperTrades(ctx, 10)
	if len(trades) != 1 {
		t.Fatalf("rollback leaked a trade: %+v", trades)
	}

	// Market fills insert the order and its children atomically.
	market := filled
	market.ID = "paper_tx_market"
	market.OrderType = "MKT"
	market.CreatedAt = now
	market.UpdatedAt = now
	if err := s.CreatePaperFill(ctx, market, badPosition, account, badTrade); err != nil {
		t.Fatalf("CreatePaperFill: %v", err)
	}
	if _, err := s.GetPaperOrder(ctx, market.ID); err != nil {
		t.Fatalf("market order not inserted: %v", err)
	}
	// A duplicate insert must roll back cleanly.
	dupTrade := badTrade
	dupTrade.OrderID = market.ID
	if err := s.CreatePaperFill(ctx, market, badPosition, account, dupTrade); err == nil {
		t.Fatal("expected duplicate market fill to fail")
	}
	trades, _ = s.ListPaperTrades(ctx, 10)
	if len(trades) != 2 {
		t.Fatalf("duplicate insert leaked a trade: %+v", trades)
	}
}

func TestActiveBracketsIncludeExitFailed(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	live := s.BracketsFor("live")
	b := bracket.Bracket{
		ID:              "exit-failed-1",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      100,
		Status:          bracket.StatusExitFailed,
	}
	if err := live.CreateBracket(ctx, b); err != nil {
		t.Fatalf("create: %v", err)
	}
	active, err := live.GetActiveBrackets(ctx)
	if err != nil {
		t.Fatalf("GetActiveBrackets: %v", err)
	}
	for _, got := range active {
		if got.ID == b.ID && got.Status == bracket.StatusExitFailed {
			return
		}
	}
	t.Fatalf("EXIT_FAILED bracket missing from active set: %+v", active)
}

func TestScopedUpdateCannotCrossModes(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	live := s.BracketsFor("live")
	scopedPaper := s.BracketsFor("paper")

	b := bracket.Bracket{
		ID:              "shared-bracket-1",
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      100,
	}
	if err := live.CreateBracket(ctx, b); err != nil {
		t.Fatalf("create live: %v", err)
	}

	paperB := b
	paperB.Status = bracket.StatusCancelled
	if err := scopedPaper.UpdateBracket(ctx, paperB); err == nil {
		t.Fatal("cross-mode update should fail")
	}

	got, err := live.GetBracket(ctx, b.ID)
	if err != nil {
		t.Fatalf("get live: %v", err)
	}
	if got.Mode != "live" {
		t.Fatalf("mode = %q, want live", got.Mode)
	}
	if got.Status == bracket.StatusCancelled {
		t.Fatal("live row was modified by the paper scope")
	}
}

func TestScopedBracketsIsolateModes(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	live := s.BracketsFor("live")
	scopedPaper := s.BracketsFor("paper")

	base := bracket.Bracket{
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26NOV25000CE",
		InstrumentKey:   "nse_fo|NIFTY|2026-11-26|25000|CE",
		TransactionType: "BUY",
		Quantity:        75,
		Product:         "MIS",
		EntryPrice:      100,
	}
	liveBracket := base
	liveBracket.ID = "live-1"
	if err := live.CreateBracket(ctx, liveBracket); err != nil {
		t.Fatalf("create live: %v", err)
	}
	paperBracket := base
	paperBracket.ID = "paper-1"
	if err := scopedPaper.CreateBracket(ctx, paperBracket); err != nil {
		t.Fatalf("create paper: %v", err)
	}

	liveList, err := live.ListBrackets(ctx, 10)
	if err != nil || len(liveList) != 1 || liveList[0].ID != "live-1" {
		t.Fatalf("live list = %+v, %v", liveList, err)
	}
	paperList, err := scopedPaper.ListBrackets(ctx, 10)
	if err != nil || len(paperList) != 1 || paperList[0].ID != "paper-1" {
		t.Fatalf("paper list = %+v, %v", paperList, err)
	}
	if _, err := live.GetBracket(ctx, "paper-1"); err == nil {
		t.Fatal("live scope must not see paper brackets")
	}
}
