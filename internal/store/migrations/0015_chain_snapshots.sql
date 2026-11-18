-- 0015_chain_snapshots.sql — persist option-chain snapshots for backtesting.
--
-- Snapshots are recorded from ladders the server has already fetched while
-- serving /api/chain and /api/chain/stats; recording never makes an extra
-- broker call. The header keeps the chain-level summary (spot, PCR, max pain,
-- CE/PE OI walls) and each row keeps per-strike CE/PE LTP, OI, day ΔOI and
-- volume so a signal backtester can replay the historical chain context.

CREATE TABLE IF NOT EXISTS chain_snapshots (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  trading_day  TEXT NOT NULL,            -- IST 'YYYY-MM-DD' of the capture
  captured_at  INTEGER NOT NULL,         -- unix seconds
  underlying   TEXT NOT NULL,           -- index key, e.g. NIFTY
  expiry       TEXT NOT NULL,            -- 'YYYY-MM-DD'
  spot         REAL NOT NULL DEFAULT 0,  -- 0 when no cached spot was available
  pcr          REAL NOT NULL DEFAULT 0,
  max_pain     REAL NOT NULL DEFAULT 0,
  call_oi_wall REAL NOT NULL DEFAULT 0,
  put_oi_wall  REAL NOT NULL DEFAULT 0,
  strikes      INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_chain_snapshots_lookup ON chain_snapshots (underlying, expiry, captured_at);
CREATE INDEX IF NOT EXISTS idx_chain_snapshots_captured ON chain_snapshots (captured_at);

CREATE TABLE IF NOT EXISTS chain_snapshot_rows (
  snapshot_id  INTEGER NOT NULL REFERENCES chain_snapshots(id) ON DELETE CASCADE,
  strike       REAL NOT NULL,
  ce_ltp       REAL NOT NULL DEFAULT 0,
  ce_oi        INTEGER NOT NULL DEFAULT 0,
  ce_oi_change INTEGER NOT NULL DEFAULT 0,
  ce_volume    INTEGER NOT NULL DEFAULT 0,
  pe_ltp       REAL NOT NULL DEFAULT 0,
  pe_oi        INTEGER NOT NULL DEFAULT 0,
  pe_oi_change INTEGER NOT NULL DEFAULT 0,
  pe_volume    INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_chain_snapshot_rows_sid ON chain_snapshot_rows (snapshot_id);
