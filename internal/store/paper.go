package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/akshaykhairmode/scalper/internal/paper"
)

// EnsurePaperAccount initializes the persisted paper account if absent.
func (s *Store) EnsurePaperAccount(ctx context.Context, startingCash float64) error {
	if startingCash <= 0 || math.IsNaN(startingCash) || math.IsInf(startingCash, 0) {
		return errors.New("store: paper starting cash must be positive and finite")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO paper_account (id, starting_cash, cash, realised_pnl, updated_at)
		VALUES (1, ?, ?, 0, ?)
		ON CONFLICT(id) DO NOTHING`, startingCash, startingCash, s.now().Unix())
	if err != nil {
		return fmt.Errorf("store: initialize paper account: %w", err)
	}
	return nil
}

// PaperSnapshot reads the paper account and its current books.
func (s *Store) PaperSnapshot(ctx context.Context, shortMarginPct float64) (paper.Snapshot, error) {
	var snapshot paper.Snapshot
	err := s.db.QueryRowContext(ctx, `
		SELECT starting_cash, cash, realised_pnl
		FROM paper_account WHERE id = 1`).Scan(
		&snapshot.Account.StartingCash, &snapshot.Account.Cash, &snapshot.Account.RealisedPnL)
	if err != nil {
		return paper.Snapshot{}, fmt.Errorf("store: read paper account: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT instrument_key, symbol, net_qty, avg_price, updated_at
		FROM paper_positions WHERE net_qty != 0 ORDER BY instrument_key`)
	if err != nil {
		return paper.Snapshot{}, fmt.Errorf("store: query paper positions: %w", err)
	}
	for rows.Next() {
		var position paper.Position
		if err := rows.Scan(
			&position.InstrumentKey, &position.Symbol, &position.Quantity,
			&position.AveragePrice, new(int64)); err != nil {
			_ = rows.Close()
			return paper.Snapshot{}, fmt.Errorf("store: scan paper position: %w", err)
		}
		snapshot.Positions = append(snapshot.Positions, position)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return paper.Snapshot{}, fmt.Errorf("store: iterate paper positions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return paper.Snapshot{}, fmt.Errorf("store: close paper positions: %w", err)
	}
	for _, position := range snapshot.Positions {
		if position.Quantity < 0 {
			snapshot.Account.Used += math.Abs(float64(position.Quantity)) *
				position.AveragePrice * shortMarginPct / 100
		}
	}
	snapshot.Account.Available = snapshot.Account.Cash - snapshot.Account.Used

	orderRows, err := s.db.QueryContext(ctx, `
		SELECT client_order_id, instrument_key, symbol, side, qty, price, status,
		       reject_reason, created_at, product, mode
		FROM orders WHERE mode = 'paper' ORDER BY id DESC LIMIT 500`)
	if err != nil {
		return paper.Snapshot{}, fmt.Errorf("store: query paper orders: %w", err)
	}
	for orderRows.Next() {
		var order paper.Order
		var reason sql.NullString
		var price sql.NullFloat64
		if err := orderRows.Scan(
			&order.ID, &order.InstrumentKey, &order.Symbol, &order.Side,
			&order.Quantity, &price, &order.Status, &reason,
			&order.Timestamp, &order.Product, &order.Mode); err != nil {
			_ = orderRows.Close()
			return paper.Snapshot{}, fmt.Errorf("store: scan paper order: %w", err)
		}
		if price.Valid {
			order.Price = price.Float64
		}
		if reason.Valid {
			order.Reason = reason.String
		}
		snapshot.Orders = append(snapshot.Orders, order)
	}
	if err := orderRows.Err(); err != nil {
		_ = orderRows.Close()
		return paper.Snapshot{}, fmt.Errorf("store: iterate paper orders: %w", err)
	}
	if err := orderRows.Close(); err != nil {
		return paper.Snapshot{}, fmt.Errorf("store: close paper orders: %w", err)
	}

	tradeRows, err := s.db.QueryContext(ctx, `
		SELECT id, instrument_key, symbol, side, qty, price, executed_at
		FROM trades WHERE mode = 'paper' ORDER BY id DESC LIMIT 500`)
	if err != nil {
		return paper.Snapshot{}, fmt.Errorf("store: query paper trades: %w", err)
	}
	for tradeRows.Next() {
		var trade paper.Trade
		if err := tradeRows.Scan(
			&trade.ID, &trade.InstrumentKey, &trade.Symbol, &trade.Side,
			&trade.Quantity, &trade.Price, &trade.ExecutedAt); err != nil {
			_ = tradeRows.Close()
			return paper.Snapshot{}, fmt.Errorf("store: scan paper trade: %w", err)
		}
		snapshot.Trades = append(snapshot.Trades, trade)
	}
	if err := tradeRows.Err(); err != nil {
		_ = tradeRows.Close()
		return paper.Snapshot{}, fmt.Errorf("store: iterate paper trades: %w", err)
	}
	if err := tradeRows.Close(); err != nil {
		return paper.Snapshot{}, fmt.Errorf("store: close paper trades: %w", err)
	}
	return snapshot, nil
}

// ApplyPaperTrade atomically applies a paper order to the account and books.
func (s *Store) ApplyPaperTrade(ctx context.Context, request paper.OrderRequest, shortMarginPct float64) (result paper.Order, err error) {
	if request.ClientOrderID == "" || request.Instrument.InstrumentKey == "" ||
		request.Quantity <= 0 || request.FillPrice <= 0 ||
		(request.Side != "B" && request.Side != "S") {
		return paper.Order{}, errors.New("store: invalid paper trade")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return paper.Order{}, fmt.Errorf("store: begin paper trade: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("store: rollback paper trade: %w", rollbackErr))
			}
		}
	}()
	now := request.Timestamp
	if now <= 0 {
		return paper.Order{}, errors.New("store: paper trade timestamp is invalid")
	}

	var cash, realised float64
	if err = tx.QueryRowContext(ctx, `
		SELECT cash, realised_pnl FROM paper_account WHERE id = 1`).Scan(&cash, &realised); err != nil {
		return paper.Order{}, fmt.Errorf("store: read paper balance: %w", err)
	}

	var oldQty int64
	var oldAverage float64
	err = tx.QueryRowContext(ctx, `
		SELECT net_qty, avg_price FROM paper_positions WHERE instrument_key = ?`,
		request.Instrument.InstrumentKey).Scan(&oldQty, &oldAverage)
	if errors.Is(err, sql.ErrNoRows) {
		oldQty, oldAverage, err = 0, 0, nil
	}
	if err != nil {
		return paper.Order{}, fmt.Errorf("store: read paper position: %w", err)
	}
	sideSign := int64(1)
	if request.Side == "S" {
		sideSign = -1
	}
	if request.Intent == "EXIT" {
		if oldQty == 0 || oldQty*sideSign >= 0 || request.Quantity > absInt64(oldQty) {
			return paper.Order{}, errors.New("store: exit order must reduce an existing position")
		}
	}
	newQty := oldQty + sideSign*request.Quantity
	price := request.FillPrice
	newAverage := oldAverage
	realisedDelta := 0.0
	if oldQty == 0 || oldQty*sideSign > 0 {
		newAverage = (math.Abs(float64(oldQty))*oldAverage +
			float64(request.Quantity)*price) / math.Abs(float64(newQty))
	} else {
		closed := min(absInt64(oldQty), request.Quantity)
		direction := 1.0
		if oldQty < 0 {
			direction = -1
		}
		realisedDelta = float64(closed) * (price - oldAverage) * direction
		if newQty == 0 {
			newAverage = 0
		} else if oldQty*newQty < 0 {
			newAverage = price
		}
	}

	newCash := cash - float64(sideSign*request.Quantity)*price
	usedMargin := 0.0
	if newQty < 0 {
		usedMargin = math.Abs(float64(newQty)) * newAverage * shortMarginPct / 100
	}
	if newCash-usedMargin < -0.000001 {
		return paper.Order{}, errors.New("insufficient paper account cash/margin")
	}

	orderResult, err := tx.ExecContext(ctx, `
		INSERT INTO orders (
			client_order_id, mode, intent, underlying, instrument_key, symbol,
			side, qty, price, order_type, product, status, created_at, updated_at
		) VALUES (?, 'paper', ?, ?, ?, ?, ?, ?, ?, 'MARKET', ?, 'EXECUTED', ?, ?)`,
		request.ClientOrderID, request.Intent, request.Instrument.Underlying,
		request.Instrument.InstrumentKey, request.Instrument.PTrdSymbol, request.Side,
		request.Quantity, price, request.Product, now, now)
	if err != nil {
		return paper.Order{}, fmt.Errorf("store: insert paper order: %w", err)
	}
	rowID, err := orderResult.LastInsertId()
	if err != nil {
		return paper.Order{}, fmt.Errorf("store: read paper order ID: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO trades (order_id, mode, instrument_key, symbol, side, qty, price, executed_at)
		VALUES (?, 'paper', ?, ?, ?, ?, ?, ?)`,
		rowID, request.Instrument.InstrumentKey, request.Instrument.PTrdSymbol,
		request.Side, request.Quantity, price, now); err != nil {
		return paper.Order{}, fmt.Errorf("store: insert paper trade: %w", err)
	}
	if newQty == 0 {
		if _, err = tx.ExecContext(ctx, `DELETE FROM paper_positions WHERE instrument_key = ?`,
			request.Instrument.InstrumentKey); err != nil {
			return paper.Order{}, fmt.Errorf("store: delete paper position: %w", err)
		}
	} else {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO paper_positions (instrument_key, symbol, net_qty, avg_price, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(instrument_key) DO UPDATE SET
				symbol = excluded.symbol, net_qty = excluded.net_qty,
				avg_price = excluded.avg_price, updated_at = excluded.updated_at`,
			request.Instrument.InstrumentKey, request.Instrument.PTrdSymbol, newQty, newAverage, now); err != nil {
			return paper.Order{}, fmt.Errorf("store: update paper position: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE paper_account SET cash = ?, realised_pnl = realised_pnl + ?, updated_at = ?
		WHERE id = 1`, newCash, realisedDelta, now); err != nil {
		return paper.Order{}, fmt.Errorf("store: update paper account: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return paper.Order{}, fmt.Errorf("store: commit paper trade: %w", err)
	}
	return paper.Order{
		ID:              request.ClientOrderID,
		InstrumentKey:   request.Instrument.InstrumentKey,
		ExchangeSegment: request.Instrument.ExchangeSegment,
		Underlying:      request.Instrument.Underlying,
		Expiry:          request.Instrument.Expiry,
		Strike:          float64(request.Instrument.StrikePaise) / 100,
		OptionType:      string(request.Instrument.OptionType),
		Symbol:          request.Instrument.PTrdSymbol,
		Side:            request.Side,
		Quantity:        request.Quantity,
		Price:           price,
		Status:          "EXECUTED",
		Timestamp:       now,
		Product:         request.Product,
		Mode:            "paper",
	}, nil
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

// PaperOrder looks up a paper order by its client order ID.
func (s *Store) PaperOrder(ctx context.Context, id string) (paper.Order, bool, error) {
	if strings.TrimSpace(id) == "" {
		return paper.Order{}, false, errors.New("store: paper order ID is empty")
	}
	var order paper.Order
	err := s.db.QueryRowContext(ctx, `
		SELECT client_order_id, instrument_key, symbol, side, qty, price, status,
		       created_at, product, mode
		FROM orders WHERE mode = 'paper' AND client_order_id = ?`, id).Scan(
		&order.ID, &order.InstrumentKey, &order.Symbol, &order.Side, &order.Quantity,
		&order.Price, &order.Status, &order.Timestamp, &order.Product, &order.Mode)
	if errors.Is(err, sql.ErrNoRows) {
		return paper.Order{}, false, nil
	}
	if err != nil {
		return paper.Order{}, false, fmt.Errorf("store: read paper order: %w", err)
	}
	return order, true, nil
}
