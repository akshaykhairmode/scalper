-- 0013_paper_positions_product.sql — keep MIS and NRML paper positions separate.

CREATE TABLE paper_positions_new (
  instrument_key   TEXT NOT NULL,
  product          TEXT NOT NULL,
  exchange_segment TEXT NOT NULL,
  trading_symbol   TEXT NOT NULL,
  net_quantity     INTEGER NOT NULL DEFAULT 0,
  average_price    REAL NOT NULL DEFAULT 0,
  realized_pnl     REAL NOT NULL DEFAULT 0,
  updated_at       INTEGER NOT NULL,
  PRIMARY KEY (instrument_key, product)
);

INSERT INTO paper_positions_new (
  instrument_key, product, exchange_segment, trading_symbol,
  net_quantity, average_price, realized_pnl, updated_at
)
SELECT instrument_key, product, exchange_segment, trading_symbol,
       net_quantity, average_price, realized_pnl, updated_at
FROM paper_positions;

DROP TABLE paper_positions;
ALTER TABLE paper_positions_new RENAME TO paper_positions;
