package scrip

import (
	"fmt"
	"testing"
	"time"
)

func makeBenchmarkOptions(count int) []Option {
	underlyings := []string{"NIFTY", "BANKNIFTY", "FINNIFTY"}
	expiries := []string{"2026-11-12", "2026-11-19", "2026-11-26", "2026-12-31"}
	opts := make([]Option, 0, count)
	for i := 0; i < count; i++ {
		u := underlyings[i%len(underlyings)]
		e := expiries[(i/len(underlyings))%len(expiries)]
		opts = append(opts, Option{
			InstrumentKey:   fmt.Sprintf("nse_fo|%d", 10000+i),
			ExchangeSegment: "nse_fo",
			PSymbol:         fmt.Sprintf("%s%s%dCE", u, e, 24000+i*50),
			PTrdSymbol:      fmt.Sprintf("%s-%s-%d-CE", u, e, 24000+i*50),
			Underlying:      u,
			Expiry:          e,
			StrikePaise:     int64(24000+i*50) * 100,
			OptionType:      Call,
		})
	}
	return opts
}

func BenchmarkScripManager_Options(b *testing.B) {
	mgr, err := NewManager(ManagerConfig{
		Segments:    []string{"nse_fo"},
		Underlyings: []Underlying{{Key: "NIFTY", DisplayName: "Nifty 50", ScripName: "NIFTY", SpotSymbol: "Nifty 50", ExchangeSegment: "nse_fo"}},
	})
	if err != nil {
		b.Fatal(err)
	}
	opts := makeBenchmarkOptions(50000)
	mgr.apply(opts, nil, time.Now(), "test")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		res := mgr.Options("NIFTY", "2026-11-12")
		if len(res) == 0 {
			b.Fatal("expected options")
		}
	}
}

// BenchmarkScripManager_Options_LegacyLinearScan measures the former 50k linear scan.
func BenchmarkScripManager_Options_LegacyLinearScan(b *testing.B) {
	opts := makeBenchmarkOptions(50000)
	underlying := "NIFTY"
	expiry := "2026-11-12"

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		out := make([]Option, 0, 64)
		for _, opt := range opts {
			if opt.Underlying == underlying && opt.Expiry == expiry {
				out = append(out, opt)
			}
		}
		if len(out) == 0 {
			b.Fatal("expected options")
		}
	}
}

func BenchmarkScripManager_Expiries(b *testing.B) {
	mgr, err := NewManager(ManagerConfig{
		Segments:    []string{"nse_fo"},
		Underlyings: []Underlying{{Key: "NIFTY", DisplayName: "Nifty 50", ScripName: "NIFTY", SpotSymbol: "Nifty 50", ExchangeSegment: "nse_fo"}},
	})
	if err != nil {
		b.Fatal(err)
	}
	opts := makeBenchmarkOptions(50000)
	mgr.apply(opts, nil, time.Now(), "test")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		res := mgr.Expiries("NIFTY")
		if len(res) == 0 {
			b.Fatal("expected expiries")
		}
	}
}
