package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/akshaykhairmode/scalper/internal/scrip"
)

// ReplaceScripSnapshot atomically replaces the instrument cache and appends the
// sync audit rows. An invalid snapshot leaves the previous cache untouched.
func (s *Store) ReplaceScripSnapshot(
	ctx context.Context,
	options []scrip.Option,
	spots []scrip.Spot,
	syncs []scrip.SyncRecord,
) (err error) {
	seenOptions := make(map[string]bool, len(options))
	for _, opt := range options {
		if err := opt.Validate(); err != nil {
			return fmt.Errorf("store: replace instruments: %w", err)
		}
		if seenOptions[opt.InstrumentKey] {
			return fmt.Errorf("store: replace instruments: duplicate key %s", opt.InstrumentKey)
		}
		seenOptions[opt.InstrumentKey] = true
	}
	seenSpots := make(map[string]bool, len(spots))
	for _, spot := range spots {
		if err := spot.Validate(); err != nil {
			return fmt.Errorf("store: replace spot instruments: %w", err)
		}
		if seenSpots[spot.InstrumentKey] {
			return fmt.Errorf("store: replace spot instruments: duplicate key %s", spot.InstrumentKey)
		}
		seenSpots[spot.InstrumentKey] = true
	}
	for i, sync := range syncs {
		if sync.Segment == "" {
			return fmt.Errorf("store: sync record %d has no segment", i)
		}
		if sync.Status == "" {
			return fmt.Errorf("store: sync record %d has no status", i)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin scrip snapshot: %w", err)
	}
	defer func() {
		if err != nil {
			if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("store: rollback scrip snapshot: %w", rerr))
			}
		}
	}()

	if _, err = tx.ExecContext(ctx, `DELETE FROM instruments`); err != nil {
		return fmt.Errorf("store: clear instruments: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM spot_instruments`); err != nil {
		return fmt.Errorf("store: clear spot instruments: %w", err)
	}

	if len(options) > 0 {
		stmt, perr := tx.PrepareContext(ctx, `
			INSERT INTO instruments (
				instrument_key, exchange_segment, p_symbol, p_trd_symbol, underlying,
				expiry, strike_paise, option_type, lot_size, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if perr != nil {
			return fmt.Errorf("store: prepare instrument insert: %w", perr)
		}
		now := s.now().Unix()
		for _, opt := range options {
			if _, err = stmt.ExecContext(ctx,
				opt.InstrumentKey, opt.ExchangeSegment, opt.PSymbol, opt.PTrdSymbol,
				opt.Underlying, opt.Expiry, opt.StrikePaise, string(opt.OptionType),
				opt.LotSize, now,
			); err != nil {
				return fmt.Errorf("store: insert instrument %s: %w", opt.InstrumentKey, err)
			}
		}
	}

	if len(spots) > 0 {
		stmt, perr := tx.PrepareContext(ctx, `
			INSERT INTO spot_instruments (
				instrument_key, exchange_segment, symbol, display_name, p_symbol, updated_at
			) VALUES (?, ?, ?, ?, ?, ?)`)
		if perr != nil {
			return fmt.Errorf("store: prepare spot insert: %w", perr)
		}
		now := s.now().Unix()
		for _, spot := range spots {
			if _, err = stmt.ExecContext(ctx,
				spot.InstrumentKey, spot.ExchangeSegment, spot.Symbol, spot.DisplayName,
				spot.PSymbol, now,
			); err != nil {
				return fmt.Errorf("store: insert spot %s: %w", spot.InstrumentKey, err)
			}
		}
	}

	if len(syncs) > 0 {
		stmt, perr := tx.PrepareContext(ctx, `
			INSERT INTO scrip_sync (segment, file_url, synced_at, row_count, status)
			VALUES (?, ?, ?, ?, ?)`)
		if perr != nil {
			return fmt.Errorf("store: prepare sync insert: %w", perr)
		}
		for _, sync := range syncs {
			if _, err = stmt.ExecContext(ctx,
				sync.Segment, sync.FileURL, sync.SyncedAt.Unix(), sync.Rows, sync.Status,
			); err != nil {
				return fmt.Errorf("store: insert scrip sync %s: %w", sync.Segment, err)
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("store: commit scrip snapshot: %w", err)
	}
	s.log.Info("scrip snapshot persisted",
		"event", "scrip_persisted", "options", len(options), "spots", len(spots),
		"syncs", len(syncs))
	return nil
}

// LoadScripSnapshot reads the persisted instrument cache. It returns empty
// slices when the cache has never been populated.
func (s *Store) LoadScripSnapshot(ctx context.Context) ([]scrip.Option, []scrip.Spot, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT instrument_key, exchange_segment, p_symbol, p_trd_symbol, underlying,
		       expiry, strike_paise, option_type, lot_size
		FROM instruments
		ORDER BY underlying, expiry, strike_paise, option_type`)
	if err != nil {
		return nil, nil, fmt.Errorf("store: query instruments: %w", err)
	}
	var options []scrip.Option
	var scanErr error
	func() {
		defer func() {
			if cerr := rows.Close(); cerr != nil {
				s.log.Warn("close instrument rows", "event", "db_rows_close_failed", "error", cerr)
			}
		}()
		for rows.Next() {
			var opt scrip.Option
			var optionType string
			if err := rows.Scan(
				&opt.InstrumentKey, &opt.ExchangeSegment, &opt.PSymbol, &opt.PTrdSymbol,
				&opt.Underlying, &opt.Expiry, &opt.StrikePaise, &optionType, &opt.LotSize,
			); err != nil {
				scanErr = err
				return
			}
			opt.OptionType = scrip.OptionType(optionType)
			options = append(options, opt)
		}
	}()
	if scanErr != nil {
		return nil, nil, fmt.Errorf("store: scan instrument row: %w", scanErr)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("store: iterate instruments: %w", err)
	}

	spotRows, err := s.db.QueryContext(ctx, `
		SELECT instrument_key, exchange_segment, symbol, display_name, p_symbol
		FROM spot_instruments
		ORDER BY exchange_segment, instrument_key`)
	if err != nil {
		return nil, nil, fmt.Errorf("store: query spot instruments: %w", err)
	}
	var spots []scrip.Spot
	func() {
		defer func() {
			if cerr := spotRows.Close(); cerr != nil {
				s.log.Warn("close spot rows", "event", "db_rows_close_failed", "error", cerr)
			}
		}()
		for spotRows.Next() {
			var spot scrip.Spot
			if err := spotRows.Scan(
				&spot.InstrumentKey, &spot.ExchangeSegment, &spot.Symbol,
				&spot.DisplayName, &spot.PSymbol,
			); err != nil {
				scanErr = err
				return
			}
			spots = append(spots, spot)
		}
	}()
	if scanErr != nil {
		return nil, nil, fmt.Errorf("store: scan spot instrument: %w", scanErr)
	}
	if err := spotRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("store: iterate spot instruments: %w", err)
	}
	return options, spots, nil
}

// CountInstruments reports cached option and spot row counts.
func (s *Store) CountInstruments(ctx context.Context) (options, spots int, err error) {
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM instruments`).Scan(&options); err != nil {
		return 0, 0, fmt.Errorf("store: count instruments: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM spot_instruments`).Scan(&spots); err != nil {
		return 0, 0, fmt.Errorf("store: count spot instruments: %w", err)
	}
	return options, spots, nil
}

// LatestScripSync returns the most recent audit rows, newest first.
func (s *Store) LatestScripSync(ctx context.Context, limit int) ([]scrip.SyncRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT segment, COALESCE(file_url, ''), row_count, synced_at, status
		FROM scrip_sync ORDER BY synced_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: query scrip sync: %w", err)
	}
	var out []scrip.SyncRecord
	var scanErr error
	func() {
		defer func() {
			if cerr := rows.Close(); cerr != nil {
				s.log.Warn("close sync rows", "event", "db_rows_close_failed", "error", cerr)
			}
		}()
		for rows.Next() {
			var rec scrip.SyncRecord
			var rowsCount sql.NullInt64
			var syncedAt int64
			if err := rows.Scan(&rec.Segment, &rec.FileURL, &rowsCount, &syncedAt, &rec.Status); err != nil {
				scanErr = err
				return
			}
			if rowsCount.Valid {
				rec.Rows = int(rowsCount.Int64)
			}
			rec.SyncedAt = time.Unix(syncedAt, 0)
			out = append(out, rec)
		}
	}()
	if scanErr != nil {
		return nil, fmt.Errorf("store: scan scrip sync: %w", scanErr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate scrip sync: %w", err)
	}
	return out, nil
}
