package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/akshaykhairmode/scalper/internal/market"
)

// istOffsetSeconds is IST's fixed offset from UTC. India has no DST, so a
// fixed zone is enough to find each trading day's boundary.
const istOffsetSeconds = 5*60*60 + 30*60

// maxReadCandles bounds how much stored history a single chart request loads.
// Charts only display a few hundred bars; reading the entire table makes
// polling cost grow forever as history accumulates.
const maxReadCandles = 5000

// ReadCandles returns the most recent cached bars for an instrument and
// interval, ordered from oldest to newest.
func (s *Store) ReadCandles(ctx context.Context, instrumentKey, timeframe string) ([]market.Candle, error) {
	if instrumentKey == "" || timeframe == "" {
		return nil, errors.New("store: candle instrument and timeframe are required")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, open, high, low, close, COALESCE(volume, 0), COALESCE(oi, 0)
		FROM candles 
		WHERE instrument_key = ? AND timeframe = ? AND open > 0 AND high > 0 AND low > 0 AND close > 0 AND high >= low 
		ORDER BY ts DESC
		LIMIT ?`, instrumentKey, timeframe, maxReadCandles)
	if err != nil {
		return nil, fmt.Errorf("store: query candles: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close candle query rows", "error", cerr)
		}
	}()

	var candles []market.Candle
	for rows.Next() {
		var candle market.Candle
		if err := rows.Scan(&candle.Time, &candle.Open, &candle.High, &candle.Low, &candle.Close, &candle.Volume, &candle.OpenInterest); err != nil {
			return nil, fmt.Errorf("store: scan candle: %w", err)
		}
		candles = append(candles, candle)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate candles: %w", err)
	}
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}
	return candles, nil
}

// PruneCandles deletes bars older than the given Unix timestamp and returns
// the number of rows removed.
func (s *Store) PruneCandles(ctx context.Context, before int64) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("store: database not open")
	}
	if before <= 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM candles WHERE ts < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("store: prune candles: %w", err)
	}
	// Baselines are tiny but unbounded; keep them on the same retention window
	// as the candles they summarise. A prune failure must not fail candle
	// maintenance, so it is only logged.
	if _, berr := s.db.ExecContext(ctx, `DELETE FROM oi_baselines WHERE created_at < ?`, before); berr != nil && s.log != nil {
		s.log.Warn("prune oi baselines", "event", "oi_baseline_prune_failed", "error", berr)
	}
	return res.RowsAffected()
}

// WriteCandles inserts or replaces historical bars without creating duplicate
// rows when a provider refreshes an overlapping date range.
func (s *Store) WriteCandles(ctx context.Context, instrumentKey, timeframe string, candles []market.Candle) error {
	if instrumentKey == "" || timeframe == "" {
		return errors.New("store: candle instrument and timeframe are required")
	}
	if len(candles) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin candle write: %w", err)
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) && s.log != nil {
			s.log.Debug("failed to rollback candle write tx", "error", rerr)
		}
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO candles (instrument_key, timeframe, ts, open, high, low, close, volume, ticks, oi)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?)
		ON CONFLICT(instrument_key, timeframe, ts) DO UPDATE SET
			open = excluded.open, high = excluded.high, low = excluded.low,
			close = excluded.close, volume = excluded.volume,
			oi = CASE WHEN excluded.oi > 0 THEN excluded.oi ELSE candles.oi END`)
	if err != nil {
		return fmt.Errorf("store: prepare candle write: %w", err)
	}
	defer func() {
		if cerr := stmt.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close candle statement", "error", cerr)
		}
	}()
	for _, candle := range candles {
		if candle.Time <= 0 || !finite(candle.Open) || !finite(candle.High) ||
			!finite(candle.Low) || !finite(candle.Close) || candle.Volume < 0 {
			return fmt.Errorf("store: invalid candle at timestamp %d", candle.Time)
		}
		if candle.Open <= 0 || candle.High <= 0 || candle.Low <= 0 || candle.Close <= 0 {
			// Skip corrupt candle with non-positive prices from upstream anomalies
			continue
		}
		if _, err := stmt.ExecContext(ctx, instrumentKey, timeframe, candle.Time,
			candle.Open, candle.High, candle.Low, candle.Close, candle.Volume, candle.OpenInterest); err != nil {
			return fmt.Errorf("store: write candle at %d: %w", candle.Time, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit candle write: %w", err)
	}
	return nil
}

// CandleSyncAt returns the time a provider refresh completed for a session.
func (s *Store) CandleSyncAt(ctx context.Context, instrumentKey, timeframe, sessionDate string) (int64, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, candleSyncKey(instrumentKey, timeframe, sessionDate)).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("store: read candle sync marker: %w", err)
	}
	syncedAt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("store: parse candle sync marker: %w", err)
	}
	return syncedAt, true, nil
}

// MarkCandlesSynced records a successful provider refresh for one session.
func (s *Store) MarkCandlesSynced(ctx context.Context, instrumentKey, timeframe, sessionDate string, syncedAt int64) error {
	key := candleSyncKey(instrumentKey, timeframe, sessionDate)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meta (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, strconv.FormatInt(syncedAt, 10), syncedAt)
	if err != nil {
		return fmt.Errorf("store: mark candles synced: %w", err)
	}
	return nil
}

func candleSyncKey(instrumentKey, timeframe, sessionDate string) string {
	hash := sha256.Sum256([]byte(instrumentKey + "\x00" + timeframe + "\x00" + sessionDate))
	return fmt.Sprintf("candle_sync:%x", hash[:])
}

// AggregateCandlesFrom1m reads stored 1-minute bars for an instrument and
// aggregates them into target timeframe bars (3m, 5m, 15m, 1h).
//
// Buckets are session-aligned: each IST day is anchored at the exchange session
// open (09:15 NSE, 09:00 MCX), and a bar at ts belongs to
// dayOpen + floor((ts-dayOpen)/interval)*interval. This keeps 1h bars starting
// at 09:15/10:15 instead of the UTC-epoch offsets (:30 in IST) that would
// otherwise miss the first 15 minutes and overlap Kotak's own hourly bars. The
// anchor comes from the instrument's segment rather than the day's first stored
// bar, so a store that started collecting mid-session still produces bars on the
// same grid as the live aggregator and the chart client.
func (s *Store) AggregateCandlesFrom1m(ctx context.Context, instrumentKey, targetTf string) ([]market.Candle, error) {
	intervalSeconds := timeframeSeconds(targetTf)
	if intervalSeconds <= 60 {
		return nil, fmt.Errorf("store: cannot aggregate to timeframe %q", targetTf)
	}
	openHour, openMinute := 9, 15
	if strings.HasPrefix(instrumentKey, "mcx") {
		openHour, openMinute = 9, 0
	}
	sessionOpenOffset := int64(openHour*3600 + openMinute*60)

	// Stream the 1m rows oldest-first and fold them into at most one open
	// bucket, so memory stays proportional to the number of output bars rather
	// than the raw row count.
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, open, high, low, close, COALESCE(volume, 0), COALESCE(oi, 0)
		FROM candles
		WHERE instrument_key = ? AND timeframe = '1m'
			AND open > 0 AND high > 0 AND low > 0 AND close > 0
		ORDER BY ts ASC`, instrumentKey)
	if err != nil {
		return nil, fmt.Errorf("store: aggregate candles: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close aggregate candle rows", "error", cerr)
		}
	}()

	var (
		candles     []market.Candle
		current     market.Candle
		haveDay     bool
		dayStart    int64
		dayOpen     int64
		haveBucket  bool
		bucketStart int64
		scanned     int
	)
	flush := func() {
		if haveBucket {
			candles = append(candles, current)
			haveBucket = false
		}
	}

	for rows.Next() {
		scanned++
		if scanned%512 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("store: aggregate candles: %w", err)
			}
		}
		var bar market.Candle
		if err := rows.Scan(&bar.Time, &bar.Open, &bar.High, &bar.Low, &bar.Close, &bar.Volume, &bar.OpenInterest); err != nil {
			return nil, fmt.Errorf("store: scan aggregate candle: %w", err)
		}

		start := istDayStart(bar.Time)
		if !haveDay || start != dayStart {
			flush()
			haveDay = true
			dayStart = start
			dayOpen = start + sessionOpenOffset
		}
		if bar.Time < dayOpen {
			continue
		}
		bucket := dayOpen + ((bar.Time-dayOpen)/intervalSeconds)*intervalSeconds
		if !haveBucket || bucket != bucketStart {
			flush()
			haveBucket = true
			bucketStart = bucket
			current = market.Candle{
				Time:         bucket,
				Open:         bar.Open,
				High:         bar.High,
				Low:          bar.Low,
				Close:        bar.Close,
				Volume:       bar.Volume,
				OpenInterest: bar.OpenInterest,
			}
			continue
		}
		if bar.High > current.High {
			current.High = bar.High
		}
		if bar.Low < current.Low {
			current.Low = bar.Low
		}
		current.Close = bar.Close
		current.Volume += bar.Volume
		// Open interest is a level, not a flow: the bucket keeps the latest
		// non-zero reading instead of summing the 1m values.
		if bar.OpenInterest > 0 {
			current.OpenInterest = bar.OpenInterest
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate aggregate candles: %w", err)
	}
	flush()
	return candles, nil
}

// istDayStart returns the Unix timestamp of 00:00 IST on the calendar day that
// contains ts.
func istDayStart(ts int64) int64 {
	shifted := ts + istOffsetSeconds
	remainder := shifted % 86400
	if remainder < 0 {
		remainder += 86400
	}
	return shifted - remainder - istOffsetSeconds
}

func timeframeSeconds(tf string) int64 {
	switch tf {
	case "3m":
		return 180
	case "5m":
		return 300
	case "15m":
		return 900
	case "1h":
		return 3600
	default:
		return 0
	}
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
