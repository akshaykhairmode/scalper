-- 0011_paper_trading.sql — Server-side paper trading account and books.

ALTER TABLE brackets ADD COLUMN mode TEXT NOT NULL DEFAULT 'live';

CREATE INDEX idx_brackets_mode_status ON brackets(mode, status);

CREATE TABLE IF NOT EXISTS paper_account (
  id               INTEGER PRIMARY KEY CHECK (id = 1),
  starting_balance REAL NOT NULL,
  realized_pnl     REAL NOT NULL DEFAULT 0,
  updated_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS paper_orders (
  id               TEXT PRIMARY KEY,
  bracket_id       TEXT NOT NULL DEFAULT '',
  exchange_segment TEXT NOT NULL,
  trading_symbol   TEXT NOT NULL,
  instrument_key   TEXT NOT NULL,
  transaction_type TEXT NOT NULL,
  product          TEXT NOT NULL,
  order_type       TEXT NOT NULL,
  quantity         INTEGER NOT NULL,
  filled_quantity  INTEGER NOT NULL DEFAULT 0,
  price            REAL NOT NULL DEFAULT 0,
  trigger_price    REAL NOT NULL DEFAULT 0,
  filled_price     REAL NOT NULL DEFAULT 0,
  status           TEXT NOT NULL,
  tag              TEXT NOT NULL DEFAULT '',
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_paper_orders_status ON paper_orders(status);
CREATE INDEX IF NOT EXISTS idx_paper_orders_instrument ON paper_orders(instrument_key);

CREATE TABLE IF NOT EXISTS paper_positions (
  instrument_key   TEXT PRIMARY KEY,
  exchange_segment TEXT NOT NULL,
  trading_symbol   TEXT NOT NULL,
  product          TEXT NOT NULL,
  net_quantity     INTEGER NOT NULL DEFAULT 0,
  average_price    REAL NOT NULL DEFAULT 0,
  realized_pnl     REAL NOT NULL DEFAULT 0,
  updated_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS paper_trades (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  order_id         TEXT NOT NULL,
  instrument_key   TEXT NOT NULL,
  trading_symbol   TEXT NOT NULL,
  transaction_type TEXT NOT NULL,
  quantity         INTEGER NOT NULL,
  price            REAL NOT NULL,
  executed_at      INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_paper_trades_time ON paper_trades(executed_at);
