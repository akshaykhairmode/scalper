package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// MaxReadChainSnapshots bounds one history query. Reads are ascending, so a
// window holding more than this many snapshots returns the earliest portion and
// the caller is expected to surface the truncation instead of paging silently.
const MaxReadChainSnapshots = 2000

// chainSnapshotRowChunk caps the parameter count of the per-snapshot row
// lookup so every statement stays well below SQLite's variable limit.
const chainSnapshotRowChunk = 900

// ChainSnapshot is one recorded option-chain capture: a header with the
// chain-level summary plus one row per strike. Spot is 0 when the server had
// no cached index price at capture time; the strike rows are the durable
// per-strike record for replay.
type ChainSnapshot struct {
	ID         int64              `json:"id"`
	TradingDay string             `json:"trading_day"` // IST YYYY-MM-DD
	CapturedAt int64              `json:"captured_at"` // unix seconds
	Underlying string             `json:"underlying"`
	Expiry     string             `json:"expiry"`
	Spot       float64            `json:"spot"`
	PCR        float64            `json:"pcr"`
	MaxPain    float64            `json:"max_pain"`
	CallOIWall float64            `json:"call_oi_wall"`
	PutOIWall  float64            `json:"put_oi_wall"`
	Strikes    int                `json:"strikes"`
	Rows       []ChainSnapshotRow `json:"rows"`
}

// ChainSnapshotRow is one strike of a ChainSnapshot. The CE or PE fields are
// zero when that side had no contract in the captured ladder.
type ChainSnapshotRow struct {
	Strike       float64 `json:"strike"`
	CallLTP      float64 `json:"ce_ltp"`
	CallOI       int64   `json:"ce_oi"`
	CallOIChange int64   `json:"ce_oi_change"`
	CallVolume   int64   `json:"ce_volume"`
	PutLTP       float64 `json:"pe_ltp"`
	PutOI        int64   `json:"pe_oi"`
	PutOIChange  int64   `json:"pe_oi_change"`
	PutVolume    int64   `json:"pe_volume"`
}

// SaveChainSnapshot persists one snapshot header and its strike rows in a
// single transaction and returns the new snapshot id.
func (s *Store) SaveChainSnapshot(ctx context.Context, snap ChainSnapshot) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("store: not open")
	}
	if strings.TrimSpace(snap.Underlying) == "" || strings.TrimSpace(snap.Expiry) == "" {
		return 0, errors.New("store: chain snapshot underlying and expiry are required")
	}
	if snap.CapturedAt <= 0 {
		return 0, errors.New("store: chain snapshot captured_at must be set")
	}
	if len(snap.Rows) == 0 {
		return 0, errors.New("store: chain snapshot needs at least one strike row")
	}
	for i, row := range snap.Rows {
		if !finite(row.Strike) || row.Strike <= 0 {
			return 0, fmt.Errorf("store: chain snapshot row %d has invalid strike %v", i, row.Strike)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin chain snapshot write: %w", err)
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) && s.log != nil {
			s.log.Debug("failed to rollback chain snapshot write tx", "error", rerr)
		}
	}()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO chain_snapshots
			(trading_day, captured_at, underlying, expiry, spot, pcr, max_pain, call_oi_wall, put_oi_wall, strikes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snap.TradingDay, snap.CapturedAt, strings.TrimSpace(snap.Underlying), strings.TrimSpace(snap.Expiry),
		snap.Spot, snap.PCR, snap.MaxPain, snap.CallOIWall, snap.PutOIWall, len(snap.Rows))
	if err != nil {
		return 0, fmt.Errorf("store: write chain snapshot header (%s %s): %w", snap.Underlying, snap.Expiry, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: get chain snapshot id (%s %s): %w", snap.Underlying, snap.Expiry, err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chain_snapshot_rows
			(snapshot_id, strike, ce_ltp, ce_oi, ce_oi_change, ce_volume, pe_ltp, pe_oi, pe_oi_change, pe_volume)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("store: prepare chain snapshot rows: %w", err)
	}
	defer func() {
		if cerr := stmt.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close chain snapshot statement", "error", cerr)
		}
	}()
	for _, row := range snap.Rows {
		if _, err := stmt.ExecContext(ctx, id, row.Strike,
			row.CallLTP, row.CallOI, row.CallOIChange, row.CallVolume,
			row.PutLTP, row.PutOI, row.PutOIChange, row.PutVolume); err != nil {
			return 0, fmt.Errorf("store: write chain snapshot row %.2f: %w", row.Strike, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit chain snapshot write: %w", err)
	}
	return id, nil
}

// ReadChainSnapshots returns snapshots for one underlying and expiry with
// captured_at in [from, to], oldest first, with their strike rows attached in
// ascending strike order. At most MaxReadChainSnapshots are returned; when the
// window holds more, the earliest portion comes back, so a caller needing the
// tail must narrow the range.
func (s *Store) ReadChainSnapshots(ctx context.Context, underlying, expiry string, from, to int64) ([]ChainSnapshot, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: not open")
	}
	underlying = strings.TrimSpace(underlying)
	expiry = strings.TrimSpace(expiry)
	if underlying == "" || expiry == "" {
		return nil, errors.New("store: chain snapshot underlying and expiry are required")
	}
	if from < 0 || to < from {
		return nil, fmt.Errorf("store: chain snapshot range from=%d to=%d is invalid", from, to)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, trading_day, captured_at, underlying, expiry, spot, pcr, max_pain, call_oi_wall, put_oi_wall, strikes
		FROM chain_snapshots
		WHERE underlying = ? AND expiry = ? AND captured_at >= ? AND captured_at <= ?
		ORDER BY captured_at ASC, id ASC
		LIMIT ?`, underlying, expiry, from, to, MaxReadChainSnapshots)
	if err != nil {
		return nil, fmt.Errorf("store: query chain snapshots (%s %s): %w", underlying, expiry, err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close chain snapshot rows", "error", cerr)
		}
	}()

	var snaps []ChainSnapshot
	index := make(map[int64]int, MaxReadChainSnapshots)
	for rows.Next() {
		var snap ChainSnapshot
		if err := rows.Scan(&snap.ID, &snap.TradingDay, &snap.CapturedAt, &snap.Underlying, &snap.Expiry,
			&snap.Spot, &snap.PCR, &snap.MaxPain, &snap.CallOIWall, &snap.PutOIWall, &snap.Strikes); err != nil {
			return nil, fmt.Errorf("store: scan chain snapshot: %w", err)
		}
		index[snap.ID] = len(snaps)
		snaps = append(snaps, snap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate chain snapshots: %w", err)
	}
	if len(snaps) == 0 {
		return snaps, nil
	}

	// Attach strike rows in bounded id chunks so the IN clause never exceeds
	// SQLite's variable limit, even at the read cap. Positions in index refer
	// to the full snaps slice, so the chunk passes only its ids.
	for start := 0; start < len(snaps); start += chainSnapshotRowChunk {
		end := min(start+chainSnapshotRowChunk, len(snaps))
		ids := make([]any, 0, end-start)
		for _, snap := range snaps[start:end] {
			ids = append(ids, snap.ID)
		}
		if err := s.readChainSnapshotRows(ctx, snaps, ids, index); err != nil {
			return nil, err
		}
	}
	return snaps, nil
}

func (s *Store) readChainSnapshotRows(ctx context.Context, snaps []ChainSnapshot, ids []any, index map[int64]int) error {
	var query strings.Builder
	query.WriteString(`
		SELECT snapshot_id, strike, ce_ltp, ce_oi, ce_oi_change, ce_volume, pe_ltp, pe_oi, pe_oi_change, pe_volume
		FROM chain_snapshot_rows
		WHERE snapshot_id IN (`)
	for i := range ids {
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteByte('?')
	}
	query.WriteString(`) ORDER BY snapshot_id ASC, strike ASC`)

	rows, err := s.db.QueryContext(ctx, query.String(), ids...)
	if err != nil {
		return fmt.Errorf("store: query chain snapshot strike rows: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close chain snapshot strike rows", "error", cerr)
		}
	}()
	for rows.Next() {
		var id int64
		var row ChainSnapshotRow
		if err := rows.Scan(&id, &row.Strike,
			&row.CallLTP, &row.CallOI, &row.CallOIChange, &row.CallVolume,
			&row.PutLTP, &row.PutOI, &row.PutOIChange, &row.PutVolume); err != nil {
			return fmt.Errorf("store: scan chain snapshot strike row: %w", err)
		}
		pos, ok := index[id]
		if !ok {
			return fmt.Errorf("store: chain snapshot strike row %d has no header", id)
		}
		snaps[pos].Rows = append(snaps[pos].Rows, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: iterate chain snapshot strike rows: %w", err)
	}
	return nil
}

// PruneChainSnapshots deletes snapshots captured before the given Unix
// timestamp and returns the number of snapshot headers removed. Strike rows
// are deleted explicitly in the same transaction so the prune stays correct
// even for a connection opened without the foreign_keys pragma.
func (s *Store) PruneChainSnapshots(ctx context.Context, before int64) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("store: not open")
	}
	if before <= 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin chain snapshot prune: %w", err)
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) && s.log != nil {
			s.log.Debug("failed to rollback chain snapshot prune tx", "error", rerr)
		}
	}()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM chain_snapshot_rows WHERE snapshot_id IN (SELECT id FROM chain_snapshots WHERE captured_at < ?)`,
		before); err != nil {
		return 0, fmt.Errorf("store: prune chain snapshot rows: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM chain_snapshots WHERE captured_at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("store: prune chain snapshots: %w", err)
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: count pruned chain snapshots: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit chain snapshot prune: %w", err)
	}
	return removed, nil
}
