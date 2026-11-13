package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/paper"
)

// EnsurePaperAccount creates the single paper account row if it does not exist
// and returns the current ledger.
func (s *Store) EnsurePaperAccount(ctx context.Context, startingBalance float64) (paper.Account, error) {
	if s == nil || s.db == nil {
		return paper.Account{}, errors.New("store: database not open")
	}
	now := s.now().Unix()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO paper_account (id, starting_balance, realized_pnl, updated_at)
		VALUES (1, ?, 0, ?)
		ON CONFLICT(id) DO NOTHING`, startingBalance, now); err != nil {
		return paper.Account{}, fmt.Errorf("store: ensure paper account: %w", err)
	}
	return s.GetPaperAccount(ctx)
}

// GetPaperAccount returns the paper ledger.
func (s *Store) GetPaperAccount(ctx context.Context) (paper.Account, error) {
	if s == nil || s.db == nil {
		return paper.Account{}, errors.New("store: database not open")
	}
	var a paper.Account
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT starting_balance, realized_pnl, updated_at FROM paper_account WHERE id = 1`).
		Scan(&a.StartingBalance, &a.RealizedPnL, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return paper.Account{}, sql.ErrNoRows
		}
		return paper.Account{}, fmt.Errorf("store: get paper account: %w", err)
	}
	a.UpdatedAt = time.Unix(updatedAt, 0)
	return a, nil
}

// SavePaperAccount persists the paper ledger.
func (s *Store) SavePaperAccount(ctx context.Context, a paper.Account) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	updated := a.UpdatedAt
	if updated.IsZero() {
		updated = s.now()
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE paper_account SET starting_balance = ?, realized_pnl = ?, updated_at = ? WHERE id = 1`,
		a.StartingBalance, a.RealizedPnL, updated.Unix())
	if err != nil {
		return fmt.Errorf("store: save paper account: %w", err)
	}
	if rows, rerr := res.RowsAffected(); rerr == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// CreatePaperOrder inserts a simulated order.
func (s *Store) CreatePaperOrder(ctx context.Context, o paper.Order) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if o.ID == "" {
		return errors.New("store: paper order ID is required")
	}
	created := o.CreatedAt
	if created.IsZero() {
		created = s.now()
	}
	updated := o.UpdatedAt
	if updated.IsZero() {
		updated = created
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO paper_orders (
			id, bracket_id, exchange_segment, trading_symbol, instrument_key,
			transaction_type, product, order_type, quantity, filled_quantity,
			price, trigger_price, filled_price, status, validity, tag, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.BracketID, o.ExchangeSegment, o.TradingSymbol, o.InstrumentKey,
		o.TransactionType, o.Product, o.OrderType, o.Quantity, o.FilledQuantity,
		o.Price, o.TriggerPrice, o.FilledPrice, o.Status, orderValidity(o.Validity),
		o.Tag, created.Unix(), updated.Unix())
	if err != nil {
		return fmt.Errorf("store: create paper order %s: %w", o.ID, err)
	}
	return nil
}

// UpdatePaperOrder updates a simulated order.
func (s *Store) UpdatePaperOrder(ctx context.Context, o paper.Order) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if o.ID == "" {
		return errors.New("store: paper order ID is required")
	}
	updated := o.UpdatedAt
	if updated.IsZero() {
		updated = s.now()
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE paper_orders SET
			bracket_id = ?, exchange_segment = ?, trading_symbol = ?, instrument_key = ?,
			transaction_type = ?, product = ?, order_type = ?, quantity = ?, filled_quantity = ?,
			price = ?, trigger_price = ?, filled_price = ?, status = ?, validity = ?, tag = ?, updated_at = ?
		WHERE id = ?`,
		o.BracketID, o.ExchangeSegment, o.TradingSymbol, o.InstrumentKey,
		o.TransactionType, o.Product, o.OrderType, o.Quantity, o.FilledQuantity,
		o.Price, o.TriggerPrice, o.FilledPrice, o.Status, orderValidity(o.Validity),
		o.Tag, updated.Unix(), o.ID)
	if err != nil {
		return fmt.Errorf("store: update paper order %s: %w", o.ID, err)
	}
	if rows, rerr := res.RowsAffected(); rerr == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetPaperOrder fetches one simulated order.
func (s *Store) GetPaperOrder(ctx context.Context, id string) (*paper.Order, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, bracket_id, exchange_segment, trading_symbol, instrument_key,
		       transaction_type, product, order_type, quantity, filled_quantity,
		       price, trigger_price, filled_price, status, validity, tag, created_at, updated_at
		FROM paper_orders WHERE id = ?`, id)
	return scanPaperOrder(row)
}

// ListPaperOrders returns recent simulated orders, newest first.
func (s *Store) ListPaperOrders(ctx context.Context, limit int) ([]paper.Order, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, bracket_id, exchange_segment, trading_symbol, instrument_key,
		       transaction_type, product, order_type, quantity, filled_quantity,
		       price, trigger_price, filled_price, status, validity, tag, created_at, updated_at
		FROM paper_orders ORDER BY created_at DESC, updated_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list paper orders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []paper.Order
	for rows.Next() {
		o, err := scanPaperOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan paper order: %w", err)
		}
		out = append(out, *o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate paper orders: %w", err)
	}
	return out, nil
}

// ListOpenPaperOrders returns every order that can still fill, without a row
// cap, so restart recovery never drops a working order.
func (s *Store) ListOpenPaperOrders(ctx context.Context) ([]paper.Order, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, bracket_id, exchange_segment, trading_symbol, instrument_key,
		       transaction_type, product, order_type, quantity, filled_quantity,
		       price, trigger_price, filled_price, status, validity, tag, created_at, updated_at
		FROM paper_orders WHERE status IN ('OPEN', 'TRIGGER_PENDING')
		ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: list open paper orders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []paper.Order
	for rows.Next() {
		o, err := scanPaperOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan open paper order: %w", err)
		}
		out = append(out, *o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate open paper orders: %w", err)
	}
	return out, nil
}

// RecordPaperFill writes the order, position, account and trade in one
// transaction so a crash or cancelled request cannot persist a partial fill.
func (s *Store) RecordPaperFill(ctx context.Context, o paper.Order, p paper.Position, a paper.Account, t paper.Trade) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if o.ID == "" {
		return errors.New("store: paper order ID is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin paper fill: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	updated := o.UpdatedAt
	if updated.IsZero() {
		updated = s.now()
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE paper_orders SET
			bracket_id = ?, exchange_segment = ?, trading_symbol = ?, instrument_key = ?,
			transaction_type = ?, product = ?, order_type = ?, quantity = ?, filled_quantity = ?,
			price = ?, trigger_price = ?, filled_price = ?, status = ?, validity = ?, tag = ?, updated_at = ?
		WHERE id = ?`,
		o.BracketID, o.ExchangeSegment, o.TradingSymbol, o.InstrumentKey,
		o.TransactionType, o.Product, o.OrderType, o.Quantity, o.FilledQuantity,
		o.Price, o.TriggerPrice, o.FilledPrice, o.Status, orderValidity(o.Validity),
		o.Tag, updated.Unix(), o.ID)
	if err != nil {
		return fmt.Errorf("store: fill paper order %s: %w", o.ID, err)
	}
	if rows, rerr := res.RowsAffected(); rerr == nil && rows == 0 {
		return fmt.Errorf("store: fill paper order %s: %w", o.ID, sql.ErrNoRows)
	}

	if err := writePaperFillChildren(ctx, tx, p, a, t, updated); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit paper fill: %w", err)
	}
	return nil
}

// CreatePaperFill inserts a complete market order and its fill atomically.
func (s *Store) CreatePaperFill(ctx context.Context, o paper.Order, p paper.Position, a paper.Account, t paper.Trade) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if o.ID == "" {
		return errors.New("store: paper order ID is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin paper fill: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	created := o.CreatedAt
	if created.IsZero() {
		created = s.now()
	}
	updated := o.UpdatedAt
	if updated.IsZero() {
		updated = created
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO paper_orders (
			id, bracket_id, exchange_segment, trading_symbol, instrument_key,
			transaction_type, product, order_type, quantity, filled_quantity,
			price, trigger_price, filled_price, status, validity, tag, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.BracketID, o.ExchangeSegment, o.TradingSymbol, o.InstrumentKey,
		o.TransactionType, o.Product, o.OrderType, o.Quantity, o.FilledQuantity,
		o.Price, o.TriggerPrice, o.FilledPrice, o.Status, orderValidity(o.Validity),
		o.Tag, created.Unix(), updated.Unix()); err != nil {
		return fmt.Errorf("store: create filled paper order %s: %w", o.ID, err)
	}
	if err := writePaperFillChildren(ctx, tx, p, a, t, updated); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit paper fill: %w", err)
	}
	return nil
}

// writePaperFillChildren writes the position, account and trade rows for one
// fill inside an existing transaction.
func writePaperFillChildren(ctx context.Context, tx *sql.Tx, p paper.Position, a paper.Account, t paper.Trade, updated time.Time) error {
	posUpdated := p.UpdatedAt
	if posUpdated.IsZero() {
		posUpdated = updated
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO paper_positions (
			instrument_key, product, exchange_segment, trading_symbol,
			net_quantity, average_price, realized_pnl, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(instrument_key, product) DO UPDATE SET
			exchange_segment = excluded.exchange_segment,
			trading_symbol = excluded.trading_symbol,
			net_quantity = excluded.net_quantity,
			average_price = excluded.average_price,
			realized_pnl = excluded.realized_pnl,
			updated_at = excluded.updated_at`,
		p.InstrumentKey, p.Product, p.ExchangeSegment, p.TradingSymbol,
		p.NetQuantity, p.AveragePrice, p.RealizedPnL, posUpdated.Unix()); err != nil {
		return fmt.Errorf("store: fill paper position %s: %w", p.InstrumentKey, err)
	}

	acctUpdated := a.UpdatedAt
	if acctUpdated.IsZero() {
		acctUpdated = updated
	}
	acctRes, err := tx.ExecContext(ctx, `
		UPDATE paper_account SET starting_balance = ?, realized_pnl = ?, updated_at = ? WHERE id = 1`,
		a.StartingBalance, a.RealizedPnL, acctUpdated.Unix())
	if err != nil {
		return fmt.Errorf("store: fill paper account: %w", err)
	}
	if rows, rerr := acctRes.RowsAffected(); rerr == nil && rows == 0 {
		return fmt.Errorf("store: fill paper account: %w", sql.ErrNoRows)
	}

	executed := t.ExecutedAt
	if executed.IsZero() {
		executed = updated
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO paper_trades (
			order_id, instrument_key, trading_symbol, transaction_type, quantity, price, executed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.OrderID, t.InstrumentKey, t.TradingSymbol, t.TransactionType, t.Quantity, t.Price, executed.Unix()); err != nil {
		return fmt.Errorf("store: fill paper trade: %w", err)
	}
	return nil
}

// AddPaperTrade appends one simulated execution.
func (s *Store) AddPaperTrade(ctx context.Context, t paper.Trade) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	executed := t.ExecutedAt
	if executed.IsZero() {
		executed = s.now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO paper_trades (
			order_id, instrument_key, trading_symbol, transaction_type, quantity, price, executed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.OrderID, t.InstrumentKey, t.TradingSymbol, t.TransactionType, t.Quantity, t.Price, executed.Unix())
	if err != nil {
		return fmt.Errorf("store: add paper trade: %w", err)
	}
	return nil
}

// ListPaperTrades returns recent simulated executions, newest first.
func (s *Store) ListPaperTrades(ctx context.Context, limit int) ([]paper.Trade, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, instrument_key, trading_symbol, transaction_type, quantity, price, executed_at
		FROM paper_trades ORDER BY executed_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list paper trades: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []paper.Trade
	for rows.Next() {
		var t paper.Trade
		var executedAt int64
		if err := rows.Scan(&t.ID, &t.OrderID, &t.InstrumentKey, &t.TradingSymbol,
			&t.TransactionType, &t.Quantity, &t.Price, &executedAt); err != nil {
			return nil, fmt.Errorf("store: scan paper trade: %w", err)
		}
		t.ExecutedAt = time.Unix(executedAt, 0)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate paper trades: %w", err)
	}
	return out, nil
}

// UpsertPaperPosition stores the latest net position for one instrument.
func (s *Store) UpsertPaperPosition(ctx context.Context, p paper.Position) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if p.InstrumentKey == "" {
		return errors.New("store: paper position instrument key is required")
	}
	updated := p.UpdatedAt
	if updated.IsZero() {
		updated = s.now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO paper_positions (
			instrument_key, product, exchange_segment, trading_symbol,
			net_quantity, average_price, realized_pnl, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(instrument_key, product) DO UPDATE SET
			exchange_segment = excluded.exchange_segment,
			trading_symbol = excluded.trading_symbol,
			net_quantity = excluded.net_quantity,
			average_price = excluded.average_price,
			realized_pnl = excluded.realized_pnl,
			updated_at = excluded.updated_at`,
		p.InstrumentKey, p.Product, p.ExchangeSegment, p.TradingSymbol,
		p.NetQuantity, p.AveragePrice, p.RealizedPnL, updated.Unix())
	if err != nil {
		return fmt.Errorf("store: upsert paper position %s: %w", p.InstrumentKey, err)
	}
	return nil
}

// ListPaperPositions returns all paper positions.
func (s *Store) ListPaperPositions(ctx context.Context) ([]paper.Position, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT instrument_key, exchange_segment, trading_symbol, product,
		       net_quantity, average_price, realized_pnl, updated_at
		FROM paper_positions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list paper positions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []paper.Position
	for rows.Next() {
		var p paper.Position
		var updatedAt int64
		if err := rows.Scan(&p.InstrumentKey, &p.ExchangeSegment, &p.TradingSymbol, &p.Product,
			&p.NetQuantity, &p.AveragePrice, &p.RealizedPnL, &updatedAt); err != nil {
			return nil, fmt.Errorf("store: scan paper position: %w", err)
		}
		p.UpdatedAt = time.Unix(updatedAt, 0)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate paper positions: %w", err)
	}
	return out, nil
}

type paperOrderScanner interface {
	Scan(dest ...any) error
}

func scanPaperOrder(s paperOrderScanner) (*paper.Order, error) {
	var o paper.Order
	var createdAt, updatedAt int64
	if err := s.Scan(&o.ID, &o.BracketID, &o.ExchangeSegment, &o.TradingSymbol, &o.InstrumentKey,
		&o.TransactionType, &o.Product, &o.OrderType, &o.Quantity, &o.FilledQuantity,
		&o.Price, &o.TriggerPrice, &o.FilledPrice, &o.Status, &o.Validity, &o.Tag, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	o.CreatedAt = time.Unix(createdAt, 0)
	o.UpdatedAt = time.Unix(updatedAt, 0)
	return &o, nil
}

func orderValidity(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "" {
		return "DAY"
	}
	return v
}
