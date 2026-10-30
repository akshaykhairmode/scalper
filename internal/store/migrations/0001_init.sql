-- 0001_init.sql — initial schema. Quantities are always shares.

CREATE TABLE instruments (
  instrument_key   TEXT PRIMARY KEY,
  exchange_segment TEXT NOT NULL,
  p_symbol         TEXT NOT NULL,
  p_trd_symbol     TEXT NOT NULL,
  underlying       TEXT NOT NULL,
  expiry           TEXT NOT NULL,
  strike_paise     INTEGER NOT NULL,
  option_type      TEXT NOT NULL CHECK (option_type IN ('CE','PE')),
  lot_size         INTEGER NOT NULL CHECK (lot_size > 0),
  updated_at       INTEGER NOT NULL
);

CREATE INDEX idx_instruments_chain
  ON instruments(underlying, expiry, option_type, strike_paise);

CREATE INDEX idx_instruments_psymbol
  ON instruments(exchange_segment, p_symbol);

CREATE TABLE spot_instruments (
  instrument_key   TEXT PRIMARY KEY,
  exchange_segment TEXT NOT NULL,
  symbol           TEXT NOT NULL,
  display_name     TEXT NOT NULL,
  updated_at       INTEGER NOT NULL
);

CREATE TABLE candles (
  instrument_key TEXT NOT NULL,
  timeframe      TEXT NOT NULL,
  ts             INTEGER NOT NULL,
  open           REAL,
  high           REAL,
  low            REAL,
  close          REAL,
  volume         INTEGER,
  ticks          INTEGER,
  PRIMARY KEY (instrument_key, timeframe, ts)
);

CREATE INDEX idx_candles_ts ON candles(ts);

CREATE TABLE orders (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  client_order_id TEXT UNIQUE NOT NULL,
  broker_order_id TEXT,
  mode            TEXT NOT NULL DEFAULT 'live',
  intent          TEXT NOT NULL CHECK (intent IN ('ENTRY','EXIT')),
  underlying      TEXT,
  instrument_key  TEXT,
  symbol          TEXT,
  side            TEXT NOT NULL,
  qty             INTEGER NOT NULL CHECK (qty > 0),
  price           REAL,
  order_type      TEXT NOT NULL,
  product         TEXT NOT NULL,
  status          TEXT NOT NULL,
  reject_reason   TEXT,
  raw_json        TEXT,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);

CREATE INDEX idx_orders_created ON orders(created_at);
CREATE INDEX idx_orders_status ON orders(status);

CREATE TABLE trades (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  order_id        INTEGER REFERENCES orders(id),
  broker_trade_id TEXT,
  mode            TEXT NOT NULL DEFAULT 'live',
  instrument_key  TEXT,
  symbol          TEXT NOT NULL,
  side            TEXT NOT NULL,
  qty             INTEGER NOT NULL CHECK (qty > 0),
  price           REAL NOT NULL,
  executed_at     INTEGER NOT NULL
);

CREATE INDEX idx_trades_executed ON trades(executed_at);

CREATE TABLE audit_log (
  id     INTEGER PRIMARY KEY AUTOINCREMENT,
  ts     INTEGER NOT NULL,
  event  TEXT NOT NULL,
  detail TEXT NOT NULL
);

CREATE INDEX idx_audit_ts ON audit_log(ts);

CREATE TABLE scrip_sync (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  segment    TEXT NOT NULL,
  file_url   TEXT,
  synced_at  INTEGER NOT NULL,
  row_count  INTEGER,
  status     TEXT NOT NULL
);

CREATE TABLE meta (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
