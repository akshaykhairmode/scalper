package web

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/akshaykhairmode/scalper/internal/market"
)

// candleWriter is the narrow store contract needed by the aggregator.
type candleWriter interface {
	WriteCandles(ctx context.Context, instrumentKey, timeframe string, candles []market.Candle) error
}

type candlePruner interface {
	PruneCandles(ctx context.Context, before int64) (int64, error)
}

// candleTimeframes are the bars maintained from live ticks. The chart only
// requests configured timeframes; extra bars are harmless.
var candleTimeframes = []struct {
	Name    string
	Seconds int64
}{
	{"1m", 60},
	{"3m", 180},
	{"5m", 300},
	{"15m", 900},
	{"1h", 3600},
	{"4h", 14400},
}

const istOffsetSeconds = 5*60*60 + 30*60

// candleAggregator turns live ticks into session-aligned OHLCV bars and
// persists them. This is what allows MCX charts (which have no Kotak
// historical endpoint) to survive a page refresh.
type candleAggregator struct {
	mu            sync.Mutex
	store         candleWriter
	log           *slog.Logger
	now           func() time.Time
	retentionDays int
	builders      map[string]*candleBuilder // instrument + "\x00" + timeframe
	pending       []pendingCandle
}

type candleBuilder struct {
	instrument  string
	timeframe   string
	bar         market.Candle
	startVolume int64
	hasVolume   bool
	lastTick    time.Time
	dirty       bool
}

type pendingCandle struct {
	instrument string
	timeframe  string
	candle     market.Candle
}

func newCandleAggregator(store candleWriter, log *slog.Logger, retentionDays int) *candleAggregator {
	if log == nil {
		log = slog.Default()
	}
	return &candleAggregator{
		store:         store,
		log:           log,
		now:           time.Now,
		retentionDays: retentionDays,
		builders:      make(map[string]*candleBuilder),
	}
}

// OnTick updates every maintained timeframe for one instrument. It never
// touches the database, keeping the live feed path fast.
func (a *candleAggregator) OnTick(instrumentKey string, ltp float64, volume, oi int64, ts time.Time) {
	if a == nil || a.store == nil {
		return
	}
	instrumentKey = strings.TrimSpace(instrumentKey)
	if instrumentKey == "" || ltp <= 0 {
		return
	}
	if ts.IsZero() {
		ts = a.now()
	}
	mcx := strings.HasPrefix(instrumentKey, "mcx_fo|")

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, tf := range candleTimeframes {
		bucket := sessionBucket(ts.Unix(), tf.Seconds, mcx)
		key := instrumentKey + "\x00" + tf.Name
		b := a.builders[key]
		if b == nil || bucket > b.bar.Time {
			if b != nil {
				a.pending = append(a.pending, pendingCandle{
					instrument: instrumentKey,
					timeframe:  tf.Name,
					candle:     b.bar,
				})
			}
			startVolume := int64(0)
			if volume > 0 {
				startVolume = volume
			}
			b = &candleBuilder{
				instrument:  instrumentKey,
				timeframe:   tf.Name,
				bar:         market.Candle{Time: bucket, Open: ltp, High: ltp, Low: ltp, Close: ltp, OpenInterest: oi},
				startVolume: startVolume,
				hasVolume:   volume > 0,
				lastTick:    ts,
			}
			a.builders[key] = b
		} else if bucket < b.bar.Time {
			// Ignore an out-of-order tick rather than corrupting the bar.
			continue
		} else {
			if ltp > b.bar.High {
				b.bar.High = ltp
			}
			if ltp < b.bar.Low {
				b.bar.Low = ltp
			}
			b.bar.Close = ltp
			b.lastTick = ts
		}

		if oi > 0 {
			b.bar.OpenInterest = oi
		}
		if volume > 0 {
			if !b.hasVolume {
				b.startVolume = volume
				b.hasVolume = true
			}
			delta := volume - b.startVolume
			if delta < 0 {
				delta = 0
			}
			if delta > b.bar.Volume {
				b.bar.Volume = delta
			}
		}
		b.dirty = true
	}
}

// Run periodically flushes dirty bars and closed bars to the store.
func (a *candleAggregator) Run(ctx context.Context) {
	if a == nil || a.store == nil {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	pruneTicker := time.NewTicker(time.Hour)
	defer pruneTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			a.flush(flushCtx)
			cancel()
			return
		case <-ticker.C:
			flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			a.flush(flushCtx)
			cancel()
		case <-pruneTicker.C:
			pruneCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			a.prune(pruneCtx)
			cancel()
		}
	}
}

func (a *candleAggregator) prune(ctx context.Context) {
	if a == nil || a.retentionDays <= 0 {
		return
	}
	pruner, ok := a.store.(candlePruner)
	if !ok {
		return
	}
	before := a.now().AddDate(0, 0, -a.retentionDays).Unix()
	rows, err := pruner.PruneCandles(ctx, before)
	if err != nil {
		a.log.Warn("failed to prune old candles", "event", "candle_prune_failed", "error", err)
		return
	}
	if rows > 0 {
		a.log.Info("pruned old candles", "event", "candle_pruned", "rows", rows, "retention_days", a.retentionDays)
	}
}

// Flush writes any pending bars immediately (used on graceful shutdown).
func (a *candleAggregator) Flush(ctx context.Context) {
	if a == nil {
		return
	}
	a.flush(ctx)
}

func (a *candleAggregator) flush(ctx context.Context) {
	if a == nil || a.store == nil {
		return
	}

	a.mu.Lock()
	if len(a.builders) == 0 && len(a.pending) == 0 {
		a.mu.Unlock()
		return
	}

	type groupKey struct {
		instrument string
		timeframe  string
	}
	groups := make(map[groupKey][]market.Candle)
	add := func(instrument, timeframe string, candle market.Candle) {
		key := groupKey{instrument: instrument, timeframe: timeframe}
		groups[key] = append(groups[key], candle)
	}

	for _, item := range a.pending {
		add(item.instrument, item.timeframe, item.candle)
	}
	a.pending = nil

	cutoff := a.now().Add(-6 * time.Hour)
	for key, b := range a.builders {
		if b.dirty {
			add(b.instrument, b.timeframe, b.bar)
			b.dirty = false
		}
		if b.lastTick.Before(cutoff) && !b.dirty {
			delete(a.builders, key)
		}
	}
	a.mu.Unlock()

	for key, candles := range groups {
		if err := a.store.WriteCandles(ctx, key.instrument, key.timeframe, candles); err != nil {
			a.log.Warn("failed to persist live candles",
				"event", "live_candle_write_failed",
				"instrument", key.instrument, "timeframe", key.timeframe, "error", err)
		}
	}
}

// sessionBucket aligns a timestamp to the same session-based buckets the
// browser uses for live candles: NSE/BSE open at 09:15, MCX at 09:00 IST.
func sessionBucket(unix, intervalSeconds int64, mcx bool) int64 {
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}
	localDayStart := ((unix+istOffsetSeconds)/86400)*86400 - istOffsetSeconds
	openSeconds := int64(9*3600 + 15*60)
	if mcx {
		openSeconds = 9 * 3600
	}
	sessionStart := localDayStart + openSeconds
	if unix < sessionStart {
		return sessionStart - intervalSeconds
	}
	return sessionStart + ((unix-sessionStart)/intervalSeconds)*intervalSeconds
}
