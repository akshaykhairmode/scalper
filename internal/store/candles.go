package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/akshaykhairmode/scalper/internal/market"
)

// ReadCandles returns all cached bars for an instrument and interval, ordered
// from oldest to newest.
func (s *Store) ReadCandles(ctx context.Context, instrumentKey, timeframe string) ([]market.Candle, error) {
	if instrumentKey == "" || timeframe == "" {
		return nil, errors.New("store: candle instrument and timeframe are required")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, open, high, low, close, COALESCE(volume, 0)
		FROM candles WHERE instrument_key = ? AND timeframe = ? ORDER BY ts`, instrumentKey, timeframe)
	if err != nil {
		return nil, fmt.Errorf("store: query candles: %w", err)
	}
	defer rows.Close()

	var candles []market.Candle
	for rows.Next() {
		var candle market.Candle
		if err := rows.Scan(&candle.Time, &candle.Open, &candle.High, &candle.Low, &candle.Close, &candle.Volume); err != nil {
			return nil, fmt.Errorf("store: scan candle: %w", err)
		}
		candles = append(candles, candle)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate candles: %w", err)
	}
	return candles, nil
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
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO candles (instrument_key, timeframe, ts, open, high, low, close, volume, ticks)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(instrument_key, timeframe, ts) DO UPDATE SET
			open = excluded.open, high = excluded.high, low = excluded.low,
			close = excluded.close, volume = excluded.volume`)
	if err != nil {
		return fmt.Errorf("store: prepare candle write: %w", err)
	}
	defer stmt.Close()
	for _, candle := range candles {
		if candle.Time <= 0 || !finite(candle.Open) || !finite(candle.High) ||
			!finite(candle.Low) || !finite(candle.Close) || candle.Volume < 0 {
			return fmt.Errorf("store: invalid candle at timestamp %d", candle.Time)
		}
		if _, err := stmt.ExecContext(ctx, instrumentKey, timeframe, candle.Time,
			candle.Open, candle.High, candle.Low, candle.Close, candle.Volume); err != nil {
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

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
