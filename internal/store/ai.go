package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Playbook represents a daily strategic market plan created by an AI reasoning agent.
type Playbook struct {
	ID            int64     `json:"id"`
	PlaybookDate  string    `json:"playbook_date"` // YYYY-MM-DD
	Bias          string    `json:"bias"`          // BULLISH, BEARISH, NEUTRAL, CHOPPY
	Regime        string    `json:"regime"`        // TRENDING, BREAKOUT, RANGE_BOUND
	KeySupport    float64   `json:"key_support"`
	KeyResistance float64   `json:"key_resistance"`
	MaxPain       float64   `json:"max_pain"`
	PCR           float64   `json:"pcr"`
	Narrative     string    `json:"narrative"`
	JevRulesJSON  string    `json:"jev_rules_json"`
	Source        string    `json:"source"` // antigravity, agent, manual
	CreatedAt     time.Time `json:"created_at"`
}

// Evaluation represents a tactical decision made by a System 1 decision model (Jev).
type Evaluation struct {
	ID            int64      `json:"id"`
	Timestamp     time.Time  `json:"timestamp"`
	InstrumentKey string     `json:"instrument_key"`
	Underlying    string     `json:"underlying"`
	SpotPrice     float64    `json:"spot_price"`
	OptionLTP     float64    `json:"option_ltp"`
	Action        string     `json:"action"` // BUY, SELL, HOLD, EXIT, BUY_CE, BUY_PE
	Confidence    float64    `json:"confidence"`
	Score         int        `json:"score"`
	DurationMS    int        `json:"duration_ms"`
	StateSnapshot string     `json:"state_snapshot"`
	Shadow        bool       `json:"shadow"`
	TargetPrice   float64    `json:"target_price"`
	StopLossPrice float64    `json:"stop_loss_price"`
	Status        string     `json:"status"` // PENDING, TARGET_HIT, SL_HIT, EXPIRED
	ExitPrice     float64    `json:"exit_price"`
	ExitTime      *time.Time `json:"exit_time,omitempty"`
	PnLPct        float64    `json:"pnl_pct"`
	Source        string     `json:"source"`    // agent+jev, jev, agent, manual
	Reasoning     string     `json:"reasoning"` // concise model rationale

	// Runtime-only enrichment; never persisted.
	CurrentPrice       float64 `json:"current_price,omitempty"`
	TimeInTradeSeconds int     `json:"time_in_trade_seconds,omitempty"`
}

// EvaluationStats holds summary metrics for Jev calls and win rates.
type EvaluationStats struct {
	TotalCalls    int     `json:"total_calls"`
	TargetHits    int     `json:"target_hits"`
	StopLossHits  int     `json:"stop_loss_hits"`
	ExpiredCalls  int     `json:"expired_calls"`
	PendingCalls  int     `json:"pending_calls"`
	ResolvedCalls int     `json:"resolved_calls"`
	WinRatePct    float64 `json:"win_rate_pct"`
	AvgPnLPct     float64 `json:"avg_pnl_pct"`
}

// SavePlaybook persists an AI playbook in the database and returns its assigned ID.
func (s *Store) SavePlaybook(ctx context.Context, p Playbook) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("store: database not open")
	}
	date := strings.TrimSpace(p.PlaybookDate)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	bias := strings.TrimSpace(p.Bias)
	if bias == "" {
		bias = "NEUTRAL"
	}
	regime := strings.TrimSpace(p.Regime)
	if regime == "" {
		regime = "RANGE_BOUND"
	}
	source := strings.TrimSpace(p.Source)
	if source == "" {
		source = "antigravity"
	}
	rulesJSON := strings.TrimSpace(p.JevRulesJSON)
	if rulesJSON == "" {
		rulesJSON = "{}"
	}
	createdAt := p.CreatedAt
	if createdAt.IsZero() {
		if s.now != nil {
			createdAt = s.now()
		} else {
			createdAt = time.Now()
		}
	}

	query := `
		INSERT INTO ai_playbooks (
			playbook_date, bias, regime, key_support, key_resistance, max_pain,
			pcr, narrative, jev_rules_json, source, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	res, err := s.db.ExecContext(ctx, query,
		date, bias, regime, p.KeySupport, p.KeyResistance, p.MaxPain,
		pcrOrZero(p.PCR), p.Narrative, rulesJSON, source, createdAt,
	)
	if err != nil {
		return 0, fmt.Errorf("store: save playbook: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: get playbook id: %w", err)
	}
	return id, nil
}

// GetLatestPlaybook retrieves the most recently created playbook.
func (s *Store) GetLatestPlaybook(ctx context.Context) (*Playbook, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}

	query := `
		SELECT id, playbook_date, bias, regime, key_support, key_resistance,
		       max_pain, pcr, narrative, jev_rules_json, source, created_at
		FROM ai_playbooks
		ORDER BY created_at DESC
		LIMIT 1`

	row := s.db.QueryRowContext(ctx, query)
	var p Playbook
	if err := row.Scan(
		&p.ID, &p.PlaybookDate, &p.Bias, &p.Regime, &p.KeySupport, &p.KeyResistance,
		&p.MaxPain, &p.PCR, &p.Narrative, &p.JevRulesJSON, &p.Source, &p.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: query latest playbook: %w", err)
	}
	return &p, nil
}

// GetPlaybookByDate retrieves the latest playbook for a specific YYYY-MM-DD date.
func (s *Store) GetPlaybookByDate(ctx context.Context, date string) (*Playbook, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}

	query := `
		SELECT id, playbook_date, bias, regime, key_support, key_resistance,
		       max_pain, pcr, narrative, jev_rules_json, source, created_at
		FROM ai_playbooks
		WHERE playbook_date = ?
		ORDER BY created_at DESC
		LIMIT 1`

	row := s.db.QueryRowContext(ctx, query, date)
	var p Playbook
	if err := row.Scan(
		&p.ID, &p.PlaybookDate, &p.Bias, &p.Regime, &p.KeySupport, &p.KeyResistance,
		&p.MaxPain, &p.PCR, &p.Narrative, &p.JevRulesJSON, &p.Source, &p.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: query playbook by date: %w", err)
	}
	return &p, nil
}

// SaveEvaluation records a Jev decision or shadow evaluation.
func (s *Store) SaveEvaluation(ctx context.Context, e Evaluation) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("store: database not open")
	}
	ts := e.Timestamp
	if ts.IsZero() {
		if s.now != nil {
			ts = s.now()
		} else {
			ts = time.Now()
		}
	}
	status := strings.TrimSpace(e.Status)
	if status == "" {
		status = "PENDING"
	}

	source := strings.TrimSpace(e.Source)
	if source == "" {
		source = "jev"
	}

	query := `
		INSERT INTO ai_evaluations (
			timestamp, instrument_key, underlying, spot_price, option_ltp,
			action, confidence, score, duration_ms, state_snapshot, shadow,
			target_price, stop_loss_price, status, exit_price, exit_time, pnl_pct,
			source, reasoning
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	res, err := s.db.ExecContext(ctx, query,
		ts, e.InstrumentKey, e.Underlying, e.SpotPrice, e.OptionLTP,
		e.Action, e.Confidence, e.Score, e.DurationMS, e.StateSnapshot, e.Shadow,
		e.TargetPrice, e.StopLossPrice, status, e.ExitPrice, e.ExitTime, e.PnLPct,
		source, strings.TrimSpace(e.Reasoning),
	)
	if err != nil {
		return 0, fmt.Errorf("store: save evaluation: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: get evaluation id: %w", err)
	}
	return id, nil
}

// UpdateEvaluationOutcome updates the execution outcome of an evaluation.
func (s *Store) UpdateEvaluationOutcome(ctx context.Context, id int64, status string, exitPrice, pnlPct float64, exitTime time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if exitTime.IsZero() {
		if s.now != nil {
			exitTime = s.now()
		} else {
			exitTime = time.Now()
		}
	}

	query := `
		UPDATE ai_evaluations
		SET status = ?, exit_price = ?, pnl_pct = ?, exit_time = ?
		WHERE id = ?`

	_, err := s.db.ExecContext(ctx, query, status, exitPrice, pnlPct, exitTime, id)
	if err != nil {
		return fmt.Errorf("store: update evaluation outcome: %w", err)
	}
	return nil
}

// DeleteEvaluation removes an AI evaluation by ID so it is no longer listed,
// counted in win-rate statistics, or tracked against live ticks.
func (s *Store) DeleteEvaluation(ctx context.Context, id int64) error {
	if s == nil || s.db == nil {
		return errors.New("store: database not open")
	}
	if id <= 0 {
		return errors.New("store: invalid evaluation id")
	}

	_, err := s.db.ExecContext(ctx, `DELETE FROM ai_evaluations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete evaluation: %w", err)
	}
	return nil
}

// GetActiveEvaluations returns all evaluations currently in PENDING status.
func (s *Store) GetActiveEvaluations(ctx context.Context) ([]Evaluation, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}

	query := `
		SELECT id, timestamp, instrument_key, underlying, spot_price, option_ltp,
		       action, confidence, score, duration_ms, state_snapshot, shadow,
		       target_price, stop_loss_price, status, exit_price, exit_time, pnl_pct,
		       source, reasoning
		FROM ai_evaluations
		WHERE status = 'PENDING'
		ORDER BY timestamp DESC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: query active evaluations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var evals []Evaluation
	for rows.Next() {
		var e Evaluation
		var exitTime sql.NullTime
		if err := rows.Scan(
			&e.ID, &e.Timestamp, &e.InstrumentKey, &e.Underlying, &e.SpotPrice, &e.OptionLTP,
			&e.Action, &e.Confidence, &e.Score, &e.DurationMS, &e.StateSnapshot, &e.Shadow,
			&e.TargetPrice, &e.StopLossPrice, &e.Status, &e.ExitPrice, &exitTime, &e.PnLPct,
			&e.Source, &e.Reasoning,
		); err != nil {
			return nil, fmt.Errorf("store: scan active evaluation: %w", err)
		}
		if exitTime.Valid {
			e.ExitTime = &exitTime.Time
		}
		evals = append(evals, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate active evaluations: %w", err)
	}
	return evals, nil
}

// GetEvaluationStats returns aggregate win/loss statistics for AI calls.
func (s *Store) GetEvaluationStats(ctx context.Context) (EvaluationStats, error) {
	if s == nil || s.db == nil {
		return EvaluationStats{}, errors.New("store: database not open")
	}

	query := `
		SELECT 
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'TARGET_HIT' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'SL_HIT' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'EXPIRED' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'PENDING' THEN 1 ELSE 0 END), 0),
			COALESCE(AVG(CASE WHEN status IN ('TARGET_HIT', 'SL_HIT', 'EXPIRED') THEN pnl_pct END), 0.0)
		FROM ai_evaluations
		WHERE action IN ('BUY_CE', 'BUY_PE', 'BUY')`

	var stats EvaluationStats
	err := s.db.QueryRowContext(ctx, query).Scan(
		&stats.TotalCalls,
		&stats.TargetHits,
		&stats.StopLossHits,
		&stats.ExpiredCalls,
		&stats.PendingCalls,
		&stats.AvgPnLPct,
	)
	if err != nil {
		return stats, fmt.Errorf("store: get evaluation stats: %w", err)
	}

	stats.ResolvedCalls = stats.TargetHits + stats.StopLossHits
	if stats.ResolvedCalls > 0 {
		stats.WinRatePct = float64(stats.TargetHits) / float64(stats.ResolvedCalls) * 100.0
	}
	return stats, nil
}

// ListEvaluations returns the most recent evaluations up to limit.
func (s *Store) ListEvaluations(ctx context.Context, limit int) ([]Evaluation, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: database not open")
	}
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, timestamp, instrument_key, underlying, spot_price, option_ltp,
		       action, confidence, score, duration_ms, state_snapshot, shadow,
		       target_price, stop_loss_price, status, exit_price, exit_time, pnl_pct,
		       source, reasoning
		FROM ai_evaluations
		WHERE action IN ('BUY_CE', 'BUY_PE', 'BUY')
		ORDER BY timestamp DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("store: query evaluations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var evals []Evaluation
	for rows.Next() {
		var e Evaluation
		var exitTime sql.NullTime
		if err := rows.Scan(
			&e.ID, &e.Timestamp, &e.InstrumentKey, &e.Underlying, &e.SpotPrice, &e.OptionLTP,
			&e.Action, &e.Confidence, &e.Score, &e.DurationMS, &e.StateSnapshot, &e.Shadow,
			&e.TargetPrice, &e.StopLossPrice, &e.Status, &e.ExitPrice, &exitTime, &e.PnLPct,
			&e.Source, &e.Reasoning,
		); err != nil {
			return nil, fmt.Errorf("store: scan evaluation: %w", err)
		}
		if exitTime.Valid {
			e.ExitTime = &exitTime.Time
		}
		evals = append(evals, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate evaluations: %w", err)
	}
	return evals, nil
}

func pcrOrZero(val float64) float64 {
	if val < 0 {
		return 0
	}
	return val
}
