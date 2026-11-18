package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LoadOIBaselines returns the first open interest recorded per instrument for
// one IST trading day (formatted 2006-01-02). Instruments without a baseline
// are omitted, which lets the caller seed them from the next live quote.
func (s *Store) LoadOIBaselines(ctx context.Context, tradingDay string) (map[string]int64, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: not open")
	}
	out := make(map[string]int64)
	if tradingDay == "" {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT instrument_key, oi FROM oi_baselines WHERE trading_day = ?`, tradingDay)
	if err != nil {
		return nil, fmt.Errorf("store: query oi baselines: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close oi baseline rows", "error", cerr)
		}
	}()
	for rows.Next() {
		var key string
		var oi int64
		if err := rows.Scan(&key, &oi); err != nil {
			return nil, fmt.Errorf("store: scan oi baseline: %w", err)
		}
		if oi > 0 {
			out[key] = oi
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate oi baselines: %w", err)
	}
	return out, nil
}

// SaveOIBaselines records first-seen open interest for a trading day. Existing
// rows are left untouched: the baseline is by definition the earliest reading,
// so a later call must never raise or lower it.
func (s *Store) SaveOIBaselines(ctx context.Context, tradingDay string, baselines map[string]int64) error {
	if s == nil || s.db == nil {
		return errors.New("store: not open")
	}
	if tradingDay == "" || len(baselines) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin oi baseline write: %w", err)
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) && s.log != nil {
			s.log.Debug("failed to rollback oi baseline write", "error", rerr)
		}
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO oi_baselines (instrument_key, trading_day, oi, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(instrument_key, trading_day) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("store: prepare oi baseline write: %w", err)
	}
	defer func() {
		if cerr := stmt.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close oi baseline statement", "error", cerr)
		}
	}()

	now := s.now().Unix()
	for key, oi := range baselines {
		if key == "" || oi <= 0 {
			continue
		}
		if _, err := stmt.ExecContext(ctx, key, tradingDay, oi, now); err != nil {
			return fmt.Errorf("store: write oi baseline for %s: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit oi baseline write: %w", err)
	}
	return nil
}
