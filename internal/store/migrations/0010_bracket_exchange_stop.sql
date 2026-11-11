ALTER TABLE brackets ADD COLUMN exchange_stop_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE brackets ADD COLUMN exchange_stop_order_id TEXT NOT NULL DEFAULT '';
ALTER TABLE brackets ADD COLUMN exchange_stop_price REAL NOT NULL DEFAULT 0;
ALTER TABLE brackets ADD COLUMN exchange_stop_status TEXT NOT NULL DEFAULT '';
