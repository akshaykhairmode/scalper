package store

import (
	"context"
	"testing"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/paper"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

func TestPaperOrderUsesCashAndRealisesPnL(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	cfg := config.Default().Trading
	service, err := paper.NewService(ctx, db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	instrument := scrip.Option{
		InstrumentKey:   "nse_fo|NIFTY|2026-10-29|25000|CE",
		ExchangeSegment: "nse_fo",
		PSymbol:         "12345",
		PTrdSymbol:      "NIFTY29OCT25000CE",
		Underlying:      "NIFTY",
		Expiry:          "2026-10-29",
		StrikePaise:     2_500_000,
		OptionType:      scrip.Call,
		LotSize:         25,
	}
	place := func(side, intent string, quantity int64, price float64) {
		t.Helper()
		order, err := service.PlaceOrder(ctx, paper.OrderRequest{
			Instrument: instrument,
			Side:       side,
			Intent:     intent,
			Product:    "MIS",
			Quantity:   quantity,
			FillPrice:  price,
		})
		if err != nil {
			t.Fatalf("PlaceOrder(%s %s): %v", side, intent, err)
		}
		if order.Status != "EXECUTED" || order.Mode != "paper" || order.Price != price {
			t.Fatalf("paper order = %+v", order)
		}
	}

	place("B", "ENTRY", 50, 100)
	place("S", "EXIT", 25, 120)
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Account.Cash != 998_000 || snapshot.Account.RealisedPnL != 500 {
		t.Fatalf("account = %+v", snapshot.Account)
	}
	if len(snapshot.Positions) != 1 || snapshot.Positions[0].Quantity != 25 || snapshot.Positions[0].AveragePrice != 100 {
		t.Fatalf("positions = %+v", snapshot.Positions)
	}
	if len(snapshot.Orders) != 2 || len(snapshot.Trades) != 2 {
		t.Fatalf("orders=%d trades=%d", len(snapshot.Orders), len(snapshot.Trades))
	}

	place("S", "EXIT", 25, 120)
	snapshot, err = service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Account.Cash != 1_001_000 || snapshot.Account.RealisedPnL != 1_000 {
		t.Fatalf("closed account = %+v", snapshot.Account)
	}
	if len(snapshot.Positions) != 0 {
		t.Fatalf("closed positions = %+v", snapshot.Positions)
	}
}

func TestPaperOrderRejectsInvalidOrUnaffordableOrders(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	cfg := config.Default().Trading
	cfg.Paper.StartingCash = 1000
	service, err := paper.NewService(ctx, db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	instrument := scrip.Option{
		InstrumentKey: "nse_fo|NIFTY|2026-10-29|25000|CE",
		PSymbol:       "12345",
		PTrdSymbol:    "NIFTY29OCT25000CE",
		Underlying:    "NIFTY",
		Expiry:        "2026-10-29",
		StrikePaise:   2_500_000,
		OptionType:    scrip.Call,
		LotSize:       25,
	}
	if _, err := service.PlaceOrder(ctx, paper.OrderRequest{
		Instrument: instrument, Side: "B", Quantity: 50, Product: "MIS", FillPrice: 100,
	}); err == nil {
		t.Fatal("expected insufficient paper funds error")
	}
	if _, err := service.PlaceOrder(ctx, paper.OrderRequest{
		Instrument: instrument, Side: "B", Quantity: 26, Product: "MIS", FillPrice: 1,
	}); err == nil {
		t.Fatal("expected non-lot multiple to be rejected")
	}
	if _, err := service.PlaceOrder(ctx, paper.OrderRequest{
		Instrument: instrument, Side: "S", Intent: "EXIT", Quantity: 25, Product: "MIS", FillPrice: 1,
	}); err == nil {
		t.Fatal("expected exit without a position to be rejected")
	}
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Account.Cash != cfg.Paper.StartingCash || len(snapshot.Orders) != 0 {
		t.Fatalf("failed order changed account: %+v", snapshot)
	}
}

func TestPaperAccountStartingCashIsNotReset(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	if err := db.EnsurePaperAccount(ctx, 10_000); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE paper_account SET cash = 5000 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePaperAccount(ctx, 20_000); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.PaperSnapshot(ctx, 15)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Account.StartingCash != 10_000 || snapshot.Account.Cash != 5000 {
		t.Fatalf("paper account reset unexpectedly: %+v", snapshot.Account)
	}
}

func TestPaperRepositoryRequiresAccount(t *testing.T) {
	db := openTest(t)
	_, err := db.PaperSnapshot(context.Background(), 15)
	if err == nil {
		t.Fatalf("PaperSnapshot error = %v", err)
	}
}
