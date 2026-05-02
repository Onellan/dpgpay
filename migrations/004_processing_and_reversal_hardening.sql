PRAGMA foreign_keys = ON;

ALTER TABLE payment_requests ADD COLUMN processed_at DATETIME;

CREATE INDEX IF NOT EXISTS idx_payment_status_processed
ON payment_requests(status, processed_at, updated_at DESC);
