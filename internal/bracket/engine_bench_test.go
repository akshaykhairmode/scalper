package bracket

import (
	"context"
	"fmt"
	"testing"

	"github.com/akshaykhairmode/scalper/internal/kotakapi"
)

func BenchmarkEngine_OnTick_Eval(b *testing.B) {
	ctx := context.Background()
	engine := NewEngine(nil, nil, nil, testLogger())
	engine.SetAsync(true)

	for i := 0; i < 10; i++ {
		_, _ = engine.Create(ctx, Bracket{
			ID:              fmt.Sprintf("brk-%d", i),
			ExchangeSegment: "NSE",
			TradingSymbol:   "RELIANCE",
			InstrumentKey:   "nse_cm|2885",
			TransactionType: "BUY",
			Quantity:        10,
			EntryPrice:      100,
			TargetPrice:     110,
			StopLossPrice:   90,
			Status:          StatusInPosition,
		})
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		engine.OnTick(ctx, "nse_cm|2885", 105)
	}
}

func BenchmarkEngine_OnTick_Trigger_Async(b *testing.B) {
	ctx := context.Background()
	placeOrder := func(_ context.Context, _ kotakapi.Order) (string, error) {
		return "ord-exit", nil
	}
	engine := NewEngine(nil, placeOrder, nil, testLogger())
	engine.SetAsync(true)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		bID := fmt.Sprintf("brk-trg-%d", i)
		_, _ = engine.Create(ctx, Bracket{
			ID:              bID,
			ExchangeSegment: "NSE",
			TradingSymbol:   "RELIANCE",
			InstrumentKey:   "nse_cm|2885",
			TransactionType: "BUY",
			Quantity:        10,
			EntryPrice:      100,
			TargetPrice:     110,
			StopLossPrice:   90,
			Status:          StatusInPosition,
		})
		b.StartTimer()

		engine.OnTick(ctx, "nse_cm|2885", 112)
	}
}

func BenchmarkEngine_OnTick_Trigger_Sync(b *testing.B) {
	ctx := context.Background()
	placeOrder := func(_ context.Context, _ kotakapi.Order) (string, error) {
		return "ord-exit", nil
	}
	engine := NewEngine(nil, placeOrder, nil, testLogger())
	engine.SetAsync(false)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		bID := fmt.Sprintf("brk-trg-%d", i)
		_, _ = engine.Create(ctx, Bracket{
			ID:              bID,
			ExchangeSegment: "NSE",
			TradingSymbol:   "RELIANCE",
			InstrumentKey:   "nse_cm|2885",
			TransactionType: "BUY",
			Quantity:        10,
			EntryPrice:      100,
			TargetPrice:     110,
			StopLossPrice:   90,
			Status:          StatusInPosition,
		})
		b.StartTimer()

		engine.OnTick(ctx, "nse_cm|2885", 112)
	}
}
