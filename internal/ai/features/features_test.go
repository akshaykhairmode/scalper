// Package features implements real-time quantitative indicator and order book feature extraction.
package features

import (
	"math"
	"testing"

	"github.com/akshaykhairmode/scalper/internal/market"
)

func TestTickRing(t *testing.T) {
	ring := NewTickRing(3)
	if ring.Len() != 0 {
		t.Fatalf("expected empty ring, got len %d", ring.Len())
	}
	if _, ok := ring.Latest(); ok {
		t.Fatal("expected no latest tick from empty ring")
	}

	ring.Push(Tick{Price: 100, Volume: 10, Timestamp: 1})
	ring.Push(Tick{Price: 101, Volume: 20, Timestamp: 2})
	ring.Push(Tick{Price: 102, Volume: 30, Timestamp: 3})

	if ring.Len() != 3 {
		t.Errorf("len = %d, want 3", ring.Len())
	}
	latest, ok := ring.Latest()
	if !ok || latest.Price != 102 {
		t.Errorf("latest = %+v, want price 102", latest)
	}

	// Push 4th element: overwrites oldest (100)
	ring.Push(Tick{Price: 103, Volume: 40, Timestamp: 4})
	if ring.Len() != 3 {
		t.Errorf("len after overflow = %d, want 3", ring.Len())
	}

	slice := ring.Slice()
	if len(slice) != 3 {
		t.Fatalf("slice len = %d, want 3", len(slice))
	}
	expectedPrices := []float64{101, 102, 103}
	for i, want := range expectedPrices {
		if slice[i].Price != want {
			t.Errorf("slice[%d] = %f, want %f", i, slice[i].Price, want)
		}
	}
}

func TestTickRingNilSafe(t *testing.T) {
	var ring *TickRing
	ring.Push(Tick{Price: 100})
	if ring.Len() != 0 {
		t.Errorf("Len on nil ring = %d, want 0", ring.Len())
	}
	if _, ok := ring.Latest(); ok {
		t.Error("Latest on nil ring returned true, want false")
	}
	if s := ring.Slice(); s != nil {
		t.Errorf("Slice on nil ring = %+v, want nil", s)
	}
}

func TestCalculateVWAP(t *testing.T) {
	prices := []float64{100.0, 102.0, 101.0}
	volumes := []int64{10, 20, 30}
	// Total PV = (100*10) + (102*20) + (101*30) = 1000 + 2040 + 3030 = 6070
	// Total Vol = 60
	// Expected VWAP = 6070 / 60 = 101.166666...
	got := CalculateVWAP(prices, volumes)
	want := 6070.0 / 60.0
	if math.Abs(got-want) > 0.0001 {
		t.Errorf("CalculateVWAP = %f, want %f", got, want)
	}
}

func TestCalculateEMA(t *testing.T) {
	prices := []float64{10, 11, 12, 13, 14, 15, 16, 17, 18, 19}
	ema := CalculateEMA(prices, 5)
	if ema <= 0 {
		t.Errorf("CalculateEMA returned invalid value %f", ema)
	}
	// For steadily rising prices, EMA should track close to recent prices
	if ema < 16.0 || ema > 19.0 {
		t.Errorf("CalculateEMA = %f, expected between 16 and 19", ema)
	}
}

func TestCalculateRSI(t *testing.T) {
	// Strictly increasing prices: RSI should be 100
	rising := []float64{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25}
	rsiRising := CalculateRSI(rising, 14)
	if rsiRising != 100.0 {
		t.Errorf("RSI of rising prices = %f, want 100.0", rsiRising)
	}

	// Strictly falling prices: RSI should be 0
	falling := []float64{25, 24, 23, 22, 21, 20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10}
	rsiFalling := CalculateRSI(falling, 14)
	if rsiFalling != 0.0 {
		t.Errorf("RSI of falling prices = %f, want 0.0", rsiFalling)
	}
}

func TestCalculateATR(t *testing.T) {
	highs := []float64{105, 106, 107, 108, 109, 110}
	lows := []float64{95, 96, 97, 98, 99, 100}
	closes := []float64{100, 101, 102, 103, 104, 105}

	atr := CalculateATR(highs, lows, closes, 5)
	if atr <= 0 {
		t.Errorf("ATR = %f, want > 0", atr)
	}
	// Daily range is consistently 10
	if math.Abs(atr-10.0) > 0.5 {
		t.Errorf("ATR = %f, want approx 10.0", atr)
	}
}

func TestCalculateOrderBookMetrics(t *testing.T) {
	bids := []market.DepthLevel{
		{Price: 100.0, Quantity: 200},
		{Price: 99.5, Quantity: 100},
	}
	asks := []market.DepthLevel{
		{Price: 100.5, Quantity: 100},
		{Price: 101.0, Quantity: 50},
	}

	m := CalculateOrderBookMetrics(100.0, bids, asks)
	if m.TotalBidQty != 300 {
		t.Errorf("TotalBidQty = %d, want 300", m.TotalBidQty)
	}
	if m.TotalAskQty != 150 {
		t.Errorf("TotalAskQty = %d, want 150", m.TotalAskQty)
	}
	// Imbalance = (300 - 150) / 450 = 150 / 450 = 0.33333
	expectedImbalance := 150.0 / 450.0
	if math.Abs(m.ImbalanceRatio-expectedImbalance) > 0.001 {
		t.Errorf("ImbalanceRatio = %f, want %f", m.ImbalanceRatio, expectedImbalance)
	}
	if m.Spread != 0.5 {
		t.Errorf("Spread = %f, want 0.5", m.Spread)
	}
	if math.Abs(m.SpreadPct-0.5) > 0.001 {
		t.Errorf("SpreadPct = %f, want 0.5", m.SpreadPct)
	}
}

func BenchmarkTechnicalCalculations(b *testing.B) {
	closes := make([]float64, 100)
	for i := range closes {
		closes[i] = 100.0 + float64(i%10)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CalculateEMA(closes, 9)
		_ = CalculateEMA(closes, 21)
		_ = CalculateRSI(closes, 14)
	}
}
