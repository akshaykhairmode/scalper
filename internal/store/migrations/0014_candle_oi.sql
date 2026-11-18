-- 0014_candle_oi.sql — persist per-bar open interest and daily ΔOI baselines.

ALTER TABLE candles ADD COLUMN oi INTEGER NOT NULL DEFAULT 0;

-- First open interest seen for a contract on an IST trading day. Kotak sends
-- only the current OI, so this row is what makes intraday ΔOI survive a restart.
CREATE TABLE IF NOT EXISTS oi_baselines (
  instrument_key TEXT NOT NULL,
  trading_day    TEXT NOT NULL,
  oi             INTEGER NOT NULL,
  created_at     INTEGER NOT NULL,
  PRIMARY KEY (instrument_key, trading_day)
);

-- Loads filter by day only, and the prune tick deletes by created_at.
CREATE INDEX IF NOT EXISTS idx_oi_baselines_day ON oi_baselines(trading_day);
CREATE INDEX IF NOT EXISTS idx_oi_baselines_created ON oi_baselines(created_at);
