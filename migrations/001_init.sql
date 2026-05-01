PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS wallets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL UNIQUE,
    currency TEXT NOT NULL,
    balance_cents INTEGER NOT NULL DEFAULT 0,
    pending_cents INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ledger_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    wallet_id INTEGER NOT NULL,
    direction TEXT NOT NULL CHECK(direction IN ('DR', 'CR')),
    amount_cents INTEGER NOT NULL CHECK(amount_cents > 0),
    entry_type TEXT NOT NULL CHECK(entry_type IN ('PAYMENT_IN', 'PAYMENT_OUT', 'FEE', 'SETTLEMENT', 'REVERSAL', 'SIMULATION')),
    reference_id TEXT NOT NULL,
    metadata TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(wallet_id) REFERENCES wallets(id)
);

CREATE INDEX IF NOT EXISTS idx_ledger_wallet_created ON ledger_entries(wallet_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ledger_reference ON ledger_entries(reference_id);

CREATE TABLE IF NOT EXISTS payment_requests (
    id TEXT PRIMARY KEY,
    reference TEXT NOT NULL UNIQUE,
    payer_name TEXT NOT NULL,
    payer_email TEXT NOT NULL,
    amount_cents INTEGER NOT NULL CHECK(amount_cents > 0),
    currency TEXT NOT NULL DEFAULT 'ZAR',
    description TEXT,
    due_date DATETIME NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('PENDING', 'AWAITING_TRANSFER', 'SETTLED', 'FAILED', 'CANCELLED', 'EXPIRED')),
    retry_count INTEGER NOT NULL DEFAULT 0,
    bank_name TEXT,
    bank_reference TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_payment_status_updated ON payment_requests(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_payment_due_date ON payment_requests(due_date);

CREATE TABLE IF NOT EXISTS settlements (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    amount_cents INTEGER NOT NULL CHECK(amount_cents >= 0),
    entry_count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK(status IN ('RUNNING', 'SUCCESS', 'FAILED')),
    triggered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);

CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    actor TEXT NOT NULL,
    reference_id TEXT,
    detail TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_reference ON audit_log(reference_id);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    admin_user TEXT NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
