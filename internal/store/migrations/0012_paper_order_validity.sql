-- 0012_paper_order_validity.sql — expire paper DAY orders across sessions.

ALTER TABLE paper_orders ADD COLUMN validity TEXT NOT NULL DEFAULT 'DAY';
