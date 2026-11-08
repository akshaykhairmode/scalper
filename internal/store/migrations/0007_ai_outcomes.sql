-- 0007_ai_outcomes.sql — Add target, stop loss, and outcome tracking to ai_evaluations

ALTER TABLE ai_evaluations ADD COLUMN target_price REAL NOT NULL DEFAULT 0.0;
ALTER TABLE ai_evaluations ADD COLUMN stop_loss_price REAL NOT NULL DEFAULT 0.0;
ALTER TABLE ai_evaluations ADD COLUMN status TEXT NOT NULL DEFAULT 'PENDING';
ALTER TABLE ai_evaluations ADD COLUMN exit_price REAL NOT NULL DEFAULT 0.0;
ALTER TABLE ai_evaluations ADD COLUMN exit_time DATETIME;
ALTER TABLE ai_evaluations ADD COLUMN pnl_pct REAL NOT NULL DEFAULT 0.0;

CREATE INDEX IF NOT EXISTS idx_ai_evaluations_status ON ai_evaluations(status, timestamp DESC);
