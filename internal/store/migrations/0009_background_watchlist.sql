-- 0009_background_watchlist.sql — Persist the last selected instruments for
-- the TOTP-free background REST poller.

CREATE TABLE background_watchlist (
  instrument_key TEXT PRIMARY KEY,
  updated_at     DATETIME NOT NULL
);

CREATE INDEX idx_background_watchlist_updated ON background_watchlist(updated_at DESC);
