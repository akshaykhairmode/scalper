-- 0008_ai_paper_trades.sql — Track paper-call provenance and reasoning

ALTER TABLE ai_evaluations ADD COLUMN source TEXT NOT NULL DEFAULT 'jev';
ALTER TABLE ai_evaluations ADD COLUMN reasoning TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_ai_evaluations_action_status ON ai_evaluations(action, status, timestamp DESC);
