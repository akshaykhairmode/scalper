-- 0002_spot_psymbol.sql — keep the cash-segment token alongside the feed name.

ALTER TABLE spot_instruments ADD COLUMN p_symbol TEXT NOT NULL DEFAULT '';
