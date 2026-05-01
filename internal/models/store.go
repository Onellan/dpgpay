package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) EnsureDefaultWallets(ctx context.Context, currency string) error {
	for _, walletType := range []string{WalletTypeOperating, WalletTypeEscrow, WalletTypeFee} {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO wallets(type, currency)
			VALUES(?, ?)
			ON CONFLICT(type) DO NOTHING`, walletType, currency); err != nil {
			return fmt.Errorf("insert wallet %s: %w", walletType, err)
		}
	}
	return nil
}

func (s *Store) GetWalletByType(ctx context.Context, walletType string) (Wallet, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, type, currency, balance_cents, pending_cents, created_at, updated_at
		FROM wallets
		WHERE type = ?`, walletType)
	return scanWallet(row)
}

func (s *Store) GetWalletByID(ctx context.Context, walletID int64) (Wallet, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, type, currency, balance_cents, pending_cents, created_at, updated_at
		FROM wallets
		WHERE id = ?`, walletID)
	return scanWallet(row)
}

func (s *Store) ListWallets(ctx context.Context) ([]Wallet, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, type, currency, balance_cents, pending_cents, created_at, updated_at
		FROM wallets
		ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Wallet
	for rows.Next() {
		var w Wallet
		if err := rows.Scan(&w.ID, &w.Type, &w.Currency, &w.BalanceCents, &w.PendingCents, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) UpdateWalletPending(ctx context.Context, walletID int64, deltaCents int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE wallets
		SET pending_cents = pending_cents + ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, deltaCents, walletID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GeneratePaymentReference(ctx context.Context, now time.Time) (string, error) {
	datePart := now.UTC().Format("20060102")
	prefix := "DPG-" + datePart + "-"
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM payment_requests WHERE reference LIKE ?`, prefix+"%").Scan(&count); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%04d", prefix, count+1), nil
}

func (s *Store) CreatePaymentRequest(ctx context.Context, p PaymentRequest) (PaymentRequest, error) {
	idempotencyKey := strings.TrimSpace(p.IdempotencyKey)
	if idempotencyKey != "" {
		existing, err := s.GetPaymentRequestByIdempotencyKey(ctx, idempotencyKey)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return PaymentRequest{}, err
		}
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO payment_requests(
			id, reference, idempotency_key, payer_name, payer_email, amount_cents, currency, description, due_date, status, retry_count, created_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		p.ID, p.Reference, nullableString(idempotencyKey), p.PayerName, p.PayerEmail, p.AmountCents, p.Currency, p.Description, p.DueDate.UTC(), p.Status,
	)
	if err != nil {
		if idempotencyKey != "" && strings.Contains(strings.ToLower(err.Error()), "idempotency") {
			return s.GetPaymentRequestByIdempotencyKey(ctx, idempotencyKey)
		}
		return PaymentRequest{}, err
	}
	return s.GetPaymentRequestByID(ctx, p.ID)
}

func (s *Store) GetPaymentRequestByIdempotencyKey(ctx context.Context, idempotencyKey string) (PaymentRequest, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, reference, COALESCE(idempotency_key, ''), payer_name, payer_email, amount_cents, currency, description, due_date, status, retry_count, COALESCE(bank_name, ''), COALESCE(bank_reference, ''), created_at, updated_at
		FROM payment_requests
		WHERE idempotency_key = ?`, idempotencyKey)
	return scanPaymentRequest(row)
}

func (s *Store) GetPaymentRequestByID(ctx context.Context, id string) (PaymentRequest, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, reference, COALESCE(idempotency_key, ''), payer_name, payer_email, amount_cents, currency, description, due_date, status, retry_count, COALESCE(bank_name, ''), COALESCE(bank_reference, ''), created_at, updated_at
		FROM payment_requests
		WHERE id = ?`, id)
	return scanPaymentRequest(row)
}

func (s *Store) ListPaymentRequests(ctx context.Context, status string, limit int) ([]PaymentRequest, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT id, reference, COALESCE(idempotency_key, ''), payer_name, payer_email, amount_cents, currency, description, due_date, status, retry_count, COALESCE(bank_name, ''), COALESCE(bank_reference, ''), created_at, updated_at
		FROM payment_requests`
	var args []any
	status = strings.TrimSpace(strings.ToUpper(status))
	if status == "ACTIVE" {
		query += " WHERE status IN (?, ?)"
		args = append(args, PaymentStatusPending, PaymentStatusAwaitingTransfer)
	} else if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PaymentRequest
	for rows.Next() {
		var p PaymentRequest
		if err := rows.Scan(
			&p.ID, &p.Reference, &p.IdempotencyKey, &p.PayerName, &p.PayerEmail, &p.AmountCents, &p.Currency, &p.Description,
			&p.DueDate, &p.Status, &p.RetryCount, &p.BankName, &p.BankReference, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) SetPaymentAwaitingTransfer(ctx context.Context, id, bankName, bankReference string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE payment_requests
		SET status = ?, bank_name = ?, bank_reference = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = ?`,
		PaymentStatusAwaitingTransfer, bankName, bankReference, id, PaymentStatusPending,
	)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return errors.New("payment request not in PENDING state")
	}
	return nil
}

func (s *Store) ConfirmPaymentIntent(ctx context.Context, id, bankName, bankReference string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var amountCents int64
	if err := tx.QueryRowContext(ctx, `
		SELECT amount_cents
		FROM payment_requests
		WHERE id = ? AND status = ?`, id, PaymentStatusPending).Scan(&amountCents); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("payment request not in PENDING state")
		}
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE payment_requests
		SET status = ?, bank_name = ?, bank_reference = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = ?`,
		PaymentStatusAwaitingTransfer, bankName, bankReference, id, PaymentStatusPending,
	); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE wallets
		SET pending_cents = pending_cents + ?, updated_at = CURRENT_TIMESTAMP
		WHERE type = ?`, amountCents, WalletTypeEscrow); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) UpdatePaymentStatus(ctx context.Context, id, fromStatus, toStatus string) error {
	query := `
		UPDATE payment_requests
		SET status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`
	args := []any{toStatus, id}
	if fromStatus != "" {
		query += " AND status = ?"
		args = append(args, fromStatus)
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) IncrementRetryAndSetStatus(ctx context.Context, id, status string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE payment_requests
		SET retry_count = retry_count + 1, status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetAwaitingTransfers(ctx context.Context, limit int) ([]PaymentRequest, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, reference, COALESCE(idempotency_key, ''), payer_name, payer_email, amount_cents, currency, description, due_date, status, retry_count, COALESCE(bank_name, ''), COALESCE(bank_reference, ''), created_at, updated_at
		FROM payment_requests
		WHERE status = ?
		ORDER BY updated_at ASC
		LIMIT ?`, PaymentStatusAwaitingTransfer, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PaymentRequest
	for rows.Next() {
		var p PaymentRequest
		if err := rows.Scan(
			&p.ID, &p.Reference, &p.IdempotencyKey, &p.PayerName, &p.PayerEmail, &p.AmountCents, &p.Currency, &p.Description,
			&p.DueDate, &p.Status, &p.RetryCount, &p.BankName, &p.BankReference, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListLedgerEntriesByWallet(ctx context.Context, walletID int64, limit, offset int) ([]LedgerEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, wallet_id, direction, amount_cents, entry_type, reference_id, COALESCE(metadata, ''), created_at
		FROM ledger_entries
		WHERE wallet_id = ?
		ORDER BY id DESC
		LIMIT ? OFFSET ?`, walletID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.ID, &e.WalletID, &e.Direction, &e.AmountCents, &e.EntryType, &e.ReferenceID, &e.Metadata, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) ListRecentLedgerEntries(ctx context.Context, limit int) ([]LedgerEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, wallet_id, direction, amount_cents, entry_type, reference_id, COALESCE(metadata, ''), created_at
		FROM ledger_entries
		ORDER BY id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.ID, &e.WalletID, &e.Direction, &e.AmountCents, &e.EntryType, &e.ReferenceID, &e.Metadata, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) CreateSettlement(ctx context.Context, status string) (Settlement, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO settlements(amount_cents, entry_count, status, triggered_at)
		VALUES(0, 0, ?, CURRENT_TIMESTAMP)`, status)
	if err != nil {
		return Settlement{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetSettlementByID(ctx, id)
}

func (s *Store) GetSettlementByID(ctx context.Context, id int64) (Settlement, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, amount_cents, entry_count, status, triggered_at, completed_at
		FROM settlements
		WHERE id = ?`, id)
	var sRow Settlement
	if err := row.Scan(&sRow.ID, &sRow.AmountCents, &sRow.EntryCount, &sRow.Status, &sRow.TriggeredAt, &sRow.CompletedAt); err != nil {
		return Settlement{}, err
	}
	return sRow, nil
}

func (s *Store) CompleteSettlement(ctx context.Context, id int64, amountCents, entryCount int64, status string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE settlements
		SET amount_cents = ?, entry_count = ?, status = ?, completed_at = CURRENT_TIMESTAMP
		WHERE id = ?`, amountCents, entryCount, status, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ListSettlements(ctx context.Context, limit int) ([]Settlement, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, amount_cents, entry_count, status, triggered_at, completed_at
		FROM settlements
		ORDER BY id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Settlement
	for rows.Next() {
		var sRow Settlement
		if err := rows.Scan(&sRow.ID, &sRow.AmountCents, &sRow.EntryCount, &sRow.Status, &sRow.TriggeredAt, &sRow.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, sRow)
	}
	return out, rows.Err()
}

func (s *Store) CreateAuditLog(ctx context.Context, eventType, actor, referenceID, detail string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log(event_type, actor, reference_id, detail, created_at)
		VALUES(?, ?, ?, ?, CURRENT_TIMESTAMP)`, eventType, actor, referenceID, detail)
	return err
}

func (s *Store) ListAuditLogs(ctx context.Context, limit int) ([]AuditLog, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_type, actor, COALESCE(reference_id, ''), COALESCE(detail, ''), created_at
		FROM audit_log
		ORDER BY id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuditLog
	for rows.Next() {
		var a AuditLog
		if err := rows.Scan(&a.ID, &a.EventType, &a.Actor, &a.ReferenceID, &a.Detail, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) CreateSession(ctx context.Context, sessionID, adminUser string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions(id, admin_user, expires_at, created_at)
		VALUES(?, ?, ?, CURRENT_TIMESTAMP)`, sessionID, adminUser, expiresAt.UTC())
	return err
}

func (s *Store) GetSession(ctx context.Context, sessionID string) (string, time.Time, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT admin_user, expires_at
		FROM sessions
		WHERE id = ?`, sessionID)
	var adminUser string
	var expiresAt time.Time
	if err := row.Scan(&adminUser, &expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return adminUser, expiresAt, nil
}

func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

func (s *Store) CleanupExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < CURRENT_TIMESTAMP`)
	return err
}

func (s *Store) DashboardStats(ctx context.Context) (DashboardStats, error) {
	var stats DashboardStats
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(balance_cents), 0) FROM wallets`).Scan(&stats.TotalBalanceCents); err != nil {
		return stats, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(pending_cents), 0) FROM wallets`).Scan(&stats.PendingBalanceCents); err != nil {
		return stats, err
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1)
		FROM payment_requests
		WHERE status = ? AND DATE(updated_at) = DATE('now')`, PaymentStatusSettled).Scan(&stats.SettledTodayCount); err != nil {
		return stats, err
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1)
		FROM payment_requests
		WHERE status = ? AND DATE(updated_at) = DATE('now')`, PaymentStatusFailed).Scan(&stats.FailedTodayCount); err != nil {
		return stats, err
	}
	return stats, nil
}

func (s *Store) ExpireOverduePendingPayments(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 200
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM payment_requests
		WHERE status = ? AND due_date < CURRENT_TIMESTAMP
		ORDER BY due_date ASC
		LIMIT ?`, PaymentStatusPending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `
			UPDATE payment_requests
			SET status = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND status = ?`, PaymentStatusExpired, id, PaymentStatusPending); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) ReconciliationReport(ctx context.Context) (ReconciliationReport, error) {
	report := ReconciliationReport{GeneratedAt: time.Now().UTC()}

	rows, err := s.db.QueryContext(ctx, `
		SELECT status, COUNT(1), COALESCE(SUM(amount_cents), 0)
		FROM payment_requests
		GROUP BY status
		ORDER BY status ASC`)
	if err != nil {
		return report, err
	}
	defer rows.Close()

	for rows.Next() {
		var line ReconciliationLine
		if err := rows.Scan(&line.Status, &line.Count, &line.AmountCents); err != nil {
			return report, err
		}
		report.Lines = append(report.Lines, line)
	}
	if err := rows.Err(); err != nil {
		return report, err
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(balance_cents), 0) FROM wallets`).Scan(&report.NetWalletBalanceCents); err != nil {
		return report, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(pending_cents, 0) FROM wallets WHERE type = ?`, WalletTypeEscrow).Scan(&report.EscrowPendingCents); err != nil {
		return report, err
	}
	report.LedgerBalanceInvariant = report.NetWalletBalanceCents == 0
	return report, nil
}

func (s *Store) EnqueueWebhook(ctx context.Context, eventType, referenceID string, payload map[string]any) error {
	if strings.TrimSpace(eventType) == "" || strings.TrimSpace(referenceID) == "" {
		return errors.New("event_type and reference_id are required")
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO webhook_outbox(event_type, reference_id, payload, status, attempts, next_attempt_at, created_at)
		VALUES(?, ?, ?, 'PENDING', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, eventType, referenceID, string(payloadBytes))
	return err
}

func (s *Store) GetSettings(ctx context.Context, keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}
	placeholders := make([]string, len(keys))
	args := make([]any, len(keys))
	for i, key := range keys {
		placeholders[i] = "?"
		args[i] = key
	}
	query := `
		SELECT key, value
		FROM app_settings
		WHERE key IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string, len(keys))
	for rows.Next() {
		var key string
		var value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

func (s *Store) UpsertSettings(ctx context.Context, settings map[string]string) error {
	if len(settings) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for key, value := range settings {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO app_settings(key, value, updated_at)
			VALUES(?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, key, strings.TrimSpace(value)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) ListOperators(ctx context.Context, limit int) ([]Operator, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, password_hash, COALESCE(email, ''), role, is_active, created_at, updated_at
		FROM operators
		ORDER BY id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Operator, 0, limit)
	for rows.Next() {
		var op Operator
		var isActive int
		if err := rows.Scan(&op.ID, &op.Username, &op.PasswordHash, &op.Email, &op.Role, &isActive, &op.CreatedAt, &op.UpdatedAt); err != nil {
			return nil, err
		}
		op.IsActive = isActive == 1
		out = append(out, op)
	}
	return out, rows.Err()
}

func (s *Store) CreateOperator(ctx context.Context, username, passwordHash, email, role string) error {
	username = strings.TrimSpace(strings.ToLower(username))
	if username == "" || passwordHash == "" {
		return errors.New("username and password are required")
	}
	if role == "" {
		role = "operator"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO operators(username, password_hash, email, role, is_active, created_at, updated_at)
		VALUES(?, ?, ?, ?, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, username, passwordHash, strings.TrimSpace(email), role)
	return err
}

func (s *Store) SetOperatorActive(ctx context.Context, operatorID int64, isActive bool) error {
	active := 0
	if isActive {
		active = 1
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE operators
		SET is_active = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, active, operatorID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpdateOperatorPasswordHash(ctx context.Context, operatorID int64, passwordHash string) error {
	if strings.TrimSpace(passwordHash) == "" {
		return errors.New("password hash is required")
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE operators
		SET password_hash = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, passwordHash, operatorID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetActiveOperatorByUsername(ctx context.Context, username string) (Operator, error) {
	username = strings.TrimSpace(strings.ToLower(username))
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, COALESCE(email, ''), role, is_active, created_at, updated_at
		FROM operators
		WHERE username = ? AND is_active = 1`, username)
	var op Operator
	var isActive int
	if err := row.Scan(&op.ID, &op.Username, &op.PasswordHash, &op.Email, &op.Role, &isActive, &op.CreatedAt, &op.UpdatedAt); err != nil {
		return Operator{}, err
	}
	op.IsActive = isActive == 1
	return op, nil
}

func (s *Store) ListDueWebhookOutbox(ctx context.Context, limit int) ([]WebhookOutboxItem, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_type, reference_id, payload, status, attempts, next_attempt_at, COALESCE(last_error, ''), created_at, sent_at
		FROM webhook_outbox
		WHERE status IN ('PENDING', 'FAILED') AND next_attempt_at <= CURRENT_TIMESTAMP
		ORDER BY id ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]WebhookOutboxItem, 0, limit)
	for rows.Next() {
		var item WebhookOutboxItem
		if err := rows.Scan(
			&item.ID,
			&item.EventType,
			&item.ReferenceID,
			&item.Payload,
			&item.Status,
			&item.Attempts,
			&item.NextAttemptAt,
			&item.LastError,
			&item.CreatedAt,
			&item.SentAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListWebhookOutbox(ctx context.Context, limit int) ([]WebhookOutboxItem, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_type, reference_id, payload, status, attempts, next_attempt_at, COALESCE(last_error, ''), created_at, sent_at
		FROM webhook_outbox
		ORDER BY id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]WebhookOutboxItem, 0, limit)
	for rows.Next() {
		var item WebhookOutboxItem
		if err := rows.Scan(
			&item.ID,
			&item.EventType,
			&item.ReferenceID,
			&item.Payload,
			&item.Status,
			&item.Attempts,
			&item.NextAttemptAt,
			&item.LastError,
			&item.CreatedAt,
			&item.SentAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) MarkWebhookSent(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE webhook_outbox
		SET status = 'SENT', attempts = attempts + 1, sent_at = CURRENT_TIMESTAMP, last_error = NULL
		WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) MarkWebhookRetry(ctx context.Context, id int64, nextAttemptAt time.Time, lastError string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE webhook_outbox
		SET status = 'FAILED', attempts = attempts + 1, next_attempt_at = ?, last_error = ?, sent_at = NULL
		WHERE id = ?`, nextAttemptAt.UTC(), lastError, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func nullableString(v string) any {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return v
}

type scanner interface {
	Scan(dest ...any) error
}

func scanWallet(row scanner) (Wallet, error) {
	var w Wallet
	if err := row.Scan(&w.ID, &w.Type, &w.Currency, &w.BalanceCents, &w.PendingCents, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return Wallet{}, err
	}
	return w, nil
}

func scanPaymentRequest(row scanner) (PaymentRequest, error) {
	var p PaymentRequest
	if err := row.Scan(
		&p.ID, &p.Reference, &p.IdempotencyKey, &p.PayerName, &p.PayerEmail, &p.AmountCents, &p.Currency, &p.Description,
		&p.DueDate, &p.Status, &p.RetryCount, &p.BankName, &p.BankReference, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return PaymentRequest{}, err
	}
	return p, nil
}
