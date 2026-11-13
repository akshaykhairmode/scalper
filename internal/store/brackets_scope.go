package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/akshaykhairmode/scalper/internal/bracket"
)

// ScopedBrackets exposes one mode's bracket rows through the bracket store
// interfaces, so live and paper engines never see each other's state.
type ScopedBrackets struct {
	store *Store
	mode  string
}

// BracketsFor returns a store scoped to the live or paper bracket namespace.
func (s *Store) BracketsFor(mode string) *ScopedBrackets {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "paper" {
		mode = "live"
	}
	return &ScopedBrackets{store: s, mode: mode}
}

// CreateBracket inserts a bracket in this scope.
func (b *ScopedBrackets) CreateBracket(ctx context.Context, br bracket.Bracket) error {
	br.Mode = b.mode
	return b.store.CreateBracket(ctx, br)
}

// UpdateBracket updates a bracket in this scope.
func (b *ScopedBrackets) UpdateBracket(ctx context.Context, br bracket.Bracket) error {
	br.Mode = b.mode
	return b.store.UpdateBracket(ctx, br)
}

// GetBracket fetches a bracket that belongs to this scope.
func (b *ScopedBrackets) GetBracket(ctx context.Context, id string) (*bracket.Bracket, error) {
	if b.store == nil || b.store.db == nil {
		return nil, errors.New("store: database not open")
	}
	row := b.store.db.QueryRowContext(ctx, `
		SELECT id, mode, exchange_segment, trading_symbol, instrument_key,
		       transaction_type, quantity, product, entry_price, entry_order_id,
		       target_price, stoploss_price, trailing_stop, exit_order_id, exit_price,
		       status, pnl, exchange_stop_mode, exchange_stop_order_id,
		       exchange_stop_price, exchange_stop_status, created_at, updated_at
		FROM brackets WHERE id = ? AND mode = ?`, id, b.mode)
	got, err := scanBracket(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("store: get %s bracket %s: %w", b.mode, id, err)
	}
	return got, nil
}

// ListBrackets returns recent brackets in this scope.
func (b *ScopedBrackets) ListBrackets(ctx context.Context, limit int) ([]bracket.Bracket, error) {
	if b.store == nil || b.store.db == nil {
		return nil, errors.New("store: database not open")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := b.store.db.QueryContext(ctx, `
		SELECT id, mode, exchange_segment, trading_symbol, instrument_key,
		       transaction_type, quantity, product, entry_price, entry_order_id,
		       target_price, stoploss_price, trailing_stop, exit_order_id, exit_price,
		       status, pnl, exchange_stop_mode, exchange_stop_order_id,
		       exchange_stop_price, exchange_stop_status, created_at, updated_at
		FROM brackets WHERE mode = ? ORDER BY created_at DESC LIMIT ?`, b.mode, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list %s brackets: %w", b.mode, err)
	}
	defer func() { _ = rows.Close() }()

	var brackets []bracket.Bracket
	for rows.Next() {
		br, err := scanBracket(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan %s bracket: %w", b.mode, err)
		}
		brackets = append(brackets, *br)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate %s brackets: %w", b.mode, err)
	}
	return brackets, nil
}

// GetActiveBrackets returns PENDING_ENTRY and IN_POSITION brackets in this scope.
func (b *ScopedBrackets) GetActiveBrackets(ctx context.Context) ([]bracket.Bracket, error) {
	if b.store == nil || b.store.db == nil {
		return nil, errors.New("store: database not open")
	}
	rows, err := b.store.db.QueryContext(ctx, `
		SELECT id, mode, exchange_segment, trading_symbol, instrument_key,
		       transaction_type, quantity, product, entry_price, entry_order_id,
		       target_price, stoploss_price, trailing_stop, exit_order_id, exit_price,
		       status, pnl, exchange_stop_mode, exchange_stop_order_id,
		       exchange_stop_price, exchange_stop_status, created_at, updated_at
		FROM brackets WHERE mode = ? AND status IN ('PENDING_ENTRY', 'IN_POSITION', 'EXIT_FAILED')
		ORDER BY created_at ASC`, b.mode)
	if err != nil {
		return nil, fmt.Errorf("store: get active %s brackets: %w", b.mode, err)
	}
	defer func() { _ = rows.Close() }()

	var brackets []bracket.Bracket
	for rows.Next() {
		br, err := scanBracket(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan active %s bracket: %w", b.mode, err)
		}
		brackets = append(brackets, *br)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate active %s brackets: %w", b.mode, err)
	}
	return brackets, nil
}
