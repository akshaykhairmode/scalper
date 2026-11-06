package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/bracket"
)

// CreateBracket inserts a new bracket into the database.
func (s *Store) CreateBracket(ctx context.Context, b bracket.Bracket) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if b.ID == "" {
		return errors.New("store: bracket ID is required")
	}

	createdAt := b.CreatedAt
	if createdAt.IsZero() {
		createdAt = s.now()
	}
	updatedAt := b.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO brackets (
			id, exchange_segment, trading_symbol, instrument_key,
			transaction_type, quantity, product, entry_price, entry_order_id,
			target_price, stoploss_price, trailing_stop, exit_order_id, exit_price,
			status, pnl, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.ExchangeSegment, b.TradingSymbol, b.InstrumentKey,
		b.TransactionType, b.Quantity, b.Product, b.EntryPrice, b.EntryOrderID,
		b.TargetPrice, b.StopLossPrice, b.TrailingStop, b.ExitOrderID, b.ExitPrice,
		b.Status, b.PnL,
		createdAt.UTC().Format("2006-01-02 15:04:05"),
		updatedAt.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return fmt.Errorf("store: create bracket %s: %w", b.ID, err)
	}
	return nil
}

// UpdateBracket updates an existing bracket record in the database.
func (s *Store) UpdateBracket(ctx context.Context, b bracket.Bracket) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if b.ID == "" {
		return errors.New("store: bracket ID is required")
	}

	updatedAt := b.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = s.now()
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE brackets SET
			exchange_segment = ?,
			trading_symbol = ?,
			instrument_key = ?,
			transaction_type = ?,
			quantity = ?,
			product = ?,
			entry_price = ?,
			entry_order_id = ?,
			target_price = ?,
			stoploss_price = ?,
			trailing_stop = ?,
			exit_order_id = ?,
			exit_price = ?,
			status = ?,
			pnl = ?,
			updated_at = ?
		WHERE id = ?`,
		b.ExchangeSegment, b.TradingSymbol, b.InstrumentKey,
		b.TransactionType, b.Quantity, b.Product, b.EntryPrice, b.EntryOrderID,
		b.TargetPrice, b.StopLossPrice, b.TrailingStop, b.ExitOrderID, b.ExitPrice,
		b.Status, b.PnL,
		updatedAt.UTC().Format("2006-01-02 15:04:05"),
		b.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update bracket %s: %w", b.ID, err)
	}
	rows, err := res.RowsAffected()
	if err == nil && rows == 0 {
		// If not updated, try insert or return ErrNoRows
		return sql.ErrNoRows
	}
	return nil
}

// GetBracket fetches a bracket by ID.
func (s *Store) GetBracket(ctx context.Context, id string) (*bracket.Bracket, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, exchange_segment, trading_symbol, instrument_key,
		       transaction_type, quantity, product, entry_price, entry_order_id,
		       target_price, stoploss_price, trailing_stop, exit_order_id, exit_price,
		       status, pnl, created_at, updated_at
		FROM brackets WHERE id = ?`, id)

	b, err := scanBracket(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("store: get bracket %s: %w", id, err)
	}
	return b, nil
}

// ListBrackets returns recent brackets ordered newest first.
func (s *Store) ListBrackets(ctx context.Context, limit int) ([]bracket.Bracket, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, exchange_segment, trading_symbol, instrument_key,
		       transaction_type, quantity, product, entry_price, entry_order_id,
		       target_price, stoploss_price, trailing_stop, exit_order_id, exit_price,
		       status, pnl, created_at, updated_at
		FROM brackets ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list brackets: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var brackets []bracket.Bracket
	for rows.Next() {
		b, err := scanBracket(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan bracket: %w", err)
		}
		brackets = append(brackets, *b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate brackets: %w", err)
	}
	return brackets, nil
}

// GetActiveBrackets returns brackets currently in PENDING_ENTRY or IN_POSITION.
func (s *Store) GetActiveBrackets(ctx context.Context) ([]bracket.Bracket, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, exchange_segment, trading_symbol, instrument_key,
		       transaction_type, quantity, product, entry_price, entry_order_id,
		       target_price, stoploss_price, trailing_stop, exit_order_id, exit_price,
		       status, pnl, created_at, updated_at
		FROM brackets WHERE status IN ('PENDING_ENTRY', 'IN_POSITION')
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: get active brackets: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var brackets []bracket.Bracket
	for rows.Next() {
		b, err := scanBracket(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan active bracket: %w", err)
		}
		brackets = append(brackets, *b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate active brackets: %w", err)
	}
	return brackets, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBracket(s rowScanner) (*bracket.Bracket, error) {
	var b bracket.Bracket
	var createdAtStr, updatedAtStr string
	err := s.Scan(
		&b.ID, &b.ExchangeSegment, &b.TradingSymbol, &b.InstrumentKey,
		&b.TransactionType, &b.Quantity, &b.Product, &b.EntryPrice, &b.EntryOrderID,
		&b.TargetPrice, &b.StopLossPrice, &b.TrailingStop, &b.ExitOrderID, &b.ExitPrice,
		&b.Status, &b.PnL, &createdAtStr, &updatedAtStr,
	)
	if err != nil {
		return nil, err
	}
	b.CreatedAt = parseDBTime(createdAtStr)
	b.UpdatedAt = parseDBTime(updatedAtStr)
	return &b, nil
}

func parseDBTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.999999999",
		time.RFC3339,
		time.RFC3339Nano,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
