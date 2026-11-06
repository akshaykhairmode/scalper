-- 0003_brackets.sql — Bracket orders and OCO engine state.

CREATE TABLE brackets (
  id               TEXT PRIMARY KEY,
  exchange_segment TEXT NOT NULL,
  trading_symbol   TEXT NOT NULL,
  instrument_key   TEXT NOT NULL,
  transaction_type TEXT NOT NULL,
  quantity         INTEGER NOT NULL,
  product          TEXT NOT NULL,
  entry_price      REAL NOT NULL,
  entry_order_id   TEXT NOT NULL DEFAULT '',
  target_price     REAL NOT NULL DEFAULT 0,
  stoploss_price   REAL NOT NULL DEFAULT 0,
  trailing_stop    REAL NOT NULL DEFAULT 0,
  exit_order_id    TEXT NOT NULL DEFAULT '',
  exit_price       REAL NOT NULL DEFAULT 0,
  status           TEXT NOT NULL DEFAULT 'PENDING_ENTRY',
  pnl              REAL NOT NULL DEFAULT 0,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_brackets_status ON brackets(status);
CREATE INDEX idx_brackets_instrument ON brackets(instrument_key);
CREATE INDEX idx_brackets_created ON brackets(created_at DESC);
