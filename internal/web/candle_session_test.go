package web

import (
	"testing"
	"time"

	"github.com/akshaykhairmode/scalper/internal/market"
)

func istTime(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, istLocation)
}

func TestCandleSessionAllowsNSE(t *testing.T) {
	holidays := []string{"2026-11-10"}
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"pre-open", istTime(2026, time.November, 11, 9, 10), false},
		{"open", istTime(2026, time.November, 11, 9, 15), true},
		{"cas window", istTime(2026, time.November, 11, 15, 35), true},
		{"derivatives close", istTime(2026, time.November, 11, 15, 40), true},
		{"after close", istTime(2026, time.November, 11, 15, 41), false},
		{"holiday", istTime(2026, time.November, 10, 11, 0), false},
		{"saturday", istTime(2026, time.November, 7, 11, 0), false},
		{"sunday", istTime(2026, time.November, 8, 14, 30), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := candleSessionAllows("nse_cm|NIFTY", tc.at, holidays); got != tc.want {
				t.Fatalf("candleSessionAllows(%s) = %v, want %v", tc.at.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

func TestCandleSessionAllowsMCX(t *testing.T) {
	if !candleSessionAllows("mcx_fo|CRUDEOIL", istTime(2026, time.November, 11, 23, 50), nil) {
		t.Fatal("MCX should allow 23:50")
	}
	if candleSessionAllows("mcx_fo|CRUDEOIL", istTime(2026, time.November, 11, 23, 58), nil) {
		t.Fatal("MCX should reject 23:58")
	}
	if candleSessionAllows("mcx_fo|CRUDEOIL", istTime(2026, time.November, 11, 8, 50), nil) {
		t.Fatal("MCX should reject 08:50")
	}
}

func TestFilterSessionCandlesDropsHolidayReplay(t *testing.T) {
	day := func(y int, m time.Month, d int) []market.Candle {
		var out []market.Candle
		for minute := 9*60 + 15; minute <= 15*60+25; minute += 5 {
			ts := istTime(y, m, d, minute/60, minute%60).Unix()
			out = append(out, market.Candle{Time: ts, Open: 100, High: 100, Low: 100, Close: 100})
		}
		return out
	}
	previous := day(2026, time.November, 9)
	previous[0] = market.Candle{Time: previous[0].Time, Open: 100, High: 105, Low: 99, Close: 104}
	holiday := day(2026, time.November, 10) // flat replay of the previous close
	next := day(2026, time.November, 11)
	next[0] = market.Candle{Time: next[0].Time, Open: 104, High: 106, Low: 103, Close: 105}

	candles := append(append(append([]market.Candle{}, previous...), holiday...), next...)
	got := filterSessionCandles(candles, "nse_cm|NIFTY", []string{"2026-11-10"})
	for _, candle := range got {
		at := time.Unix(candle.Time, 0).In(istLocation)
		if at.Day() == 10 {
			t.Fatalf("holiday bar survived: %s", at.Format(time.RFC3339))
		}
	}
	if len(got) != len(previous)+len(next) {
		t.Fatalf("kept %d bars, want %d", len(got), len(previous)+len(next))
	}
}

func TestFilterSessionCandlesDropsWeekendAndAfterHours(t *testing.T) {
	candles := []market.Candle{
		{Time: istTime(2026, time.November, 8, 14, 30).Unix(), Open: 100, High: 101, Low: 99, Close: 100},
		{Time: istTime(2026, time.November, 11, 9, 10).Unix(), Open: 100, High: 101, Low: 99, Close: 100},
		{Time: istTime(2026, time.November, 11, 15, 50).Unix(), Open: 100, High: 101, Low: 99, Close: 100},
	}
	if got := filterSessionCandles(candles, "nse_cm|NIFTY", nil); len(got) != 0 {
		t.Fatalf("kept %d out-of-session bars, want 0", len(got))
	}
}

func TestDropFlatNonTradingDaysKeepsRealFlatDays(t *testing.T) {
	// A flat day that does not match the previous close is still a real day
	// (for example a locked circuit or an illiquid option).
	prev := []market.Candle{{Time: istTime(2026, time.November, 9, 15, 25).Unix(), Open: 100, High: 101, Low: 99, Close: 100}}
	flat := []market.Candle{
		{Time: istTime(2026, time.November, 11, 9, 15).Unix(), Open: 120, High: 120, Low: 120, Close: 120},
		{Time: istTime(2026, time.November, 11, 9, 20).Unix(), Open: 120, High: 120, Low: 120, Close: 120},
	}
	got := dropFlatNonTradingDays(append(prev, flat...))
	if len(got) != 3 {
		t.Fatalf("kept %d bars, want 3", len(got))
	}
}
