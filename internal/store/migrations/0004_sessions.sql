-- 0004_sessions.sql — Persistent sessions for broker authentication.

CREATE TABLE sessions (
  token_id     TEXT PRIMARY KEY,
  broker_token TEXT NOT NULL,
  broker_sid   TEXT NOT NULL,
  base_url     TEXT NOT NULL,
  feed_url     TEXT NOT NULL,
  login_method TEXT NOT NULL,
  created_at   DATETIME NOT NULL,
  expires_at   DATETIME NOT NULL
);

CREATE INDEX idx_sessions_created ON sessions(created_at DESC);
