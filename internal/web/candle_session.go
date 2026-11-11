package web

import (
	"math"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/market"
)

// Candle sessions are wider than the order window on purpose. Equity
// derivatives now trade until 15:40 IST, and the cash closing auction (CAS)
// keeps the underlying index printing until roughly 15:35. Anything stamped
// outside these windows is a stale broker snapshot, not a real bar.
const (
	nseCandleOpenMinute  = 9*60 + 15
	nseCandleCloseMinute = 15*60 + 40
	mcxCandleOpenMinute  = 9 * 60
	mcxCandleCloseMinute = 23*60 + 55
)

var istLocation = time.FixedZone("IST", istOffsetSeconds)

func isMCXInstrumentKey(key string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(key)), "MCX_FO|")
}

// candleSessionAllows reports whether a tick timestamp belongs to a real
// trading session for the instrument.
func candleSessionAllows(instrumentKey string, at time.Time, holidays []string) bool {
	if at.IsZero() {
		at = time.Now()
	}
	ist := at.In(istLocation)
	if ist.Weekday() == time.Saturday || ist.Weekday() == time.Sunday {
		return false
	}
	day := ist.Format("2006-01-02")
	for _, holiday := range holidays {
		if strings.TrimSpace(holiday) == day {
			return false
		}
	}
	minute := ist.Hour()*60 + ist.Minute()
	if isMCXInstrumentKey(instrumentKey) {
		return minute >= mcxCandleOpenMinute && minute <= mcxCandleCloseMinute
	}
	return minute >= nseCandleOpenMinute && minute <= nseCandleCloseMinute
}

// filterSessionCandles drops bars from weekends, configured holidays and
// outside-session timestamps, then removes whole days that are flat at the
// previous close. Brokers replay the last close as a snapshot on non-trading
// days; those bars used to render as a long flat gap on the chart.
func filterSessionCandles(candles []market.Candle, instrumentKey string, holidays []string) []market.Candle {
	if len(candles) == 0 {
		return candles
	}
	kept := make([]market.Candle, 0, len(candles))
	for _, candle := range candles {
		if candle.Time <= 0 {
			continue
		}
		if !candleSessionAllows(instrumentKey, time.Unix(candle.Time, 0), holidays) {
			continue
		}
		kept = append(kept, candle)
	}
	return dropFlatNonTradingDays(kept)
}

func dropFlatNonTradingDays(candles []market.Candle) []market.Candle {
	if len(candles) < 2 {
		return candles
	}
	out := make([]market.Candle, 0, len(candles))
	var prevClose float64
	var day string
	var group []market.Candle

	flush := func() {
		if len(group) == 0 {
			return
		}
		flat := true
		for _, candle := range group {
			if candle.Open != candle.High || candle.High != candle.Low || candle.Low != candle.Close {
				flat = false
				break
			}
		}
		if flat && prevClose > 0 && math.Abs(group[0].Close-prevClose) < 1e-6 {
			group = nil
			return
		}
		out = append(out, group...)
		prevClose = group[len(group)-1].Close
		group = nil
	}

	for _, candle := range candles {
		candleDay := time.Unix(candle.Time, 0).In(istLocation).Format("2006-01-02")
		if day != "" && candleDay != day {
			flush()
		}
		day = candleDay
		group = append(group, candle)
	}
	flush()
	return out
}

func (s *Server) candleHolidays() []string {
	if s == nil {
		return nil
	}
	return s.configSnapshot().Market.Holidays
}

func (s *Server) candleSessionAllows(instrumentKey string, at time.Time) bool {
	return candleSessionAllows(instrumentKey, at, s.candleHolidays())
}

func (s *Server) filterCandles(instrumentKey string, candles []market.Candle) []market.Candle {
	return filterSessionCandles(candles, instrumentKey, s.candleHolidays())
}
