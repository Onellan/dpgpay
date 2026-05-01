PRAGMA foreign_keys = ON;

ALTER TABLE payment_requests ADD COLUMN idempotency_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_idempotency_key
ON payment_requests(idempotency_key)
WHERE idempotency_key IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_idempotency
ON ledger_entries(wallet_id, direction, entry_type, reference_id);

CREATE TABLE IF NOT EXISTS webhook_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    reference_id TEXT NOT NULL,
    payload TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('PENDING', 'SENT', 'FAILED')) DEFAULT 'PENDING',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    sent_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_webhook_outbox_pending
ON webhook_outbox(status, next_attempt_at);
