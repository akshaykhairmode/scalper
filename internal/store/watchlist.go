package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SaveWatchlist replaces the persisted background-collection watchlist.
func (s *Store) SaveWatchlist(ctx context.Context, keys []string) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	seen := make(map[string]bool, len(keys))
	clean := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		clean = append(clean, key)
	}

	now := time.Now()
	if s.now != nil {
		now = s.now()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin watchlist write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM background_watchlist`); err != nil {
		return fmt.Errorf("store: clear watchlist: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO background_watchlist (instrument_key, updated_at) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("store: prepare watchlist insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for _, key := range clean {
		if _, err := stmt.ExecContext(ctx, key, now); err != nil {
			return fmt.Errorf("store: insert watchlist %q: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit watchlist: %w", err)
	}
	return nil
}

// LoadWatchlist returns the persisted background-collection watchlist.
func (s *Store) LoadWatchlist(ctx context.Context) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT instrument_key FROM background_watchlist
		ORDER BY updated_at DESC, instrument_key ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: query watchlist: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close watchlist rows", "error", cerr)
		}
	}()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("store: scan watchlist: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate watchlist: %w", err)
	}
	return keys, nil
}
