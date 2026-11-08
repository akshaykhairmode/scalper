-- 0006_ai.sql — AI Playbooks and Jev Evaluation Telemetry

CREATE TABLE ai_playbooks (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  playbook_date  TEXT NOT NULL,
  bias           TEXT NOT NULL,
  regime         TEXT NOT NULL,
  key_support    REAL NOT NULL DEFAULT 0.0,
  key_resistance REAL NOT NULL DEFAULT 0.0,
  max_pain       REAL NOT NULL DEFAULT 0.0,
  pcr            REAL NOT NULL DEFAULT 0.0,
  narrative      TEXT NOT NULL DEFAULT '',
  jev_rules_json TEXT NOT NULL DEFAULT '{}',
  source         TEXT NOT NULL DEFAULT 'antigravity',
  created_at     DATETIME NOT NULL
);

CREATE INDEX idx_ai_playbooks_date ON ai_playbooks(playbook_date DESC, created_at DESC);

CREATE TABLE ai_evaluations (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp      DATETIME NOT NULL,
  instrument_key TEXT NOT NULL,
  underlying     TEXT NOT NULL,
  spot_price     REAL NOT NULL DEFAULT 0.0,
  option_ltp     REAL NOT NULL DEFAULT 0.0,
  action         TEXT NOT NULL,
  confidence     REAL NOT NULL DEFAULT 0.0,
  score          INTEGER NOT NULL DEFAULT 0,
  duration_ms    INTEGER NOT NULL DEFAULT 0,
  state_snapshot TEXT NOT NULL DEFAULT '',
  shadow         BOOLEAN NOT NULL DEFAULT 1
);

CREATE INDEX idx_ai_evaluations_time ON ai_evaluations(timestamp DESC);
CREATE INDEX idx_ai_evaluations_inst ON ai_evaluations(instrument_key, timestamp DESC);
