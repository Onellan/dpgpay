package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"dpg-pay/internal/models"
)

type Posting struct {
	WalletType  string
	Direction   string
	AmountCents int64
	EntryType   string
	ReferenceID string
	Metadata    map[string]any
}

type Service struct {
	store *models.Store
}

func NewService(store *models.Store) *Service {
	return &Service{store: store}
}

func (s *Service) Postings(ctx context.Context, postings []Posting) error {
	if len(postings) < 2 {
		return fmt.Errorf("double-entry requires at least two postings")
	}
	var drTotal, crTotal int64
	for _, p := range postings {
		switch p.Direction {
		case models.DirectionDebit:
			drTotal += p.AmountCents
		case models.DirectionCredit:
			crTotal += p.AmountCents
		default:
			return fmt.Errorf("invalid direction: %s", p.Direction)
		}
	}
	if drTotal != crTotal {
		return fmt.Errorf("unbalanced posting: dr=%d cr=%d", drTotal, crTotal)
	}

	return models.WithWriteRetry(ctx, 5, func() error {
		tx, err := s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return err
		}
		defer tx.Rollback()

		if err := postInTx(ctx, tx, postings); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (s *Service) ClearPendingEscrowToOperating(ctx context.Context, referenceID string, amountCents int64, metadata map[string]any) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["processed_at"] = time.Now().UTC().Format(time.RFC3339)

	return models.WithWriteRetry(ctx, 5, func() error {
		tx, err := s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return err
		}
		defer tx.Rollback()

		escrowID, err := walletIDByTypeTx(ctx, tx, models.WalletTypeEscrow)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE wallets
			SET pending_cents = CASE WHEN pending_cents >= ? THEN pending_cents - ? ELSE 0 END, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`, amountCents, amountCents, escrowID); err != nil {
			return err
		}

		if err := postInTx(ctx, tx, []Posting{
			{
				WalletType:  models.WalletTypeOperating,
				Direction:   models.DirectionDebit,
				AmountCents: amountCents,
				EntryType:   models.EntryTypePaymentIn,
				ReferenceID: referenceID,
				Metadata:    metadata,
			},
			{
				WalletType:  models.WalletTypeEscrow,
				Direction:   models.DirectionCredit,
				AmountCents: amountCents,
				EntryType:   models.EntryTypeSimulation,
				ReferenceID: referenceID,
				Metadata:    metadata,
			},
		}); err != nil {
			return err
		}

		return tx.Commit()
	})
}

func (s *Service) RunSettlement(ctx context.Context, amountCents int64, settlementID int64) error {
	meta := map[string]any{"settlement_id": settlementID}
	return s.Postings(ctx, []Posting{
		{
			WalletType:  models.WalletTypeEscrow,
			Direction:   models.DirectionDebit,
			AmountCents: amountCents,
			EntryType:   models.EntryTypeSettlement,
			ReferenceID: fmt.Sprintf("settlement:%d", settlementID),
			Metadata:    meta,
		},
		{
			WalletType:  models.WalletTypeOperating,
			Direction:   models.DirectionCredit,
			AmountCents: amountCents,
			EntryType:   models.EntryTypeSettlement,
			ReferenceID: fmt.Sprintf("settlement:%d", settlementID),
			Metadata:    meta,
		},
	})
}

func (s *Service) ValidateIntegrity(ctx context.Context) error {
	debit, credit, err := s.store.LedgerTotals(ctx)
	if err != nil {
		return err
	}
	if debit != credit {
		return fmt.Errorf("ledger imbalance detected: debit=%d credit=%d", debit, credit)
	}
	return nil
}

func (s *Service) RefundSettledPayment(ctx context.Context, paymentID string, amountCents int64) error {
	if amountCents <= 0 {
		return fmt.Errorf("invalid refund amount")
	}

	return models.WithWriteRetry(ctx, 5, func() error {
		tx, err := s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return err
		}
		defer tx.Rollback()

		if err := postInTx(ctx, tx, []Posting{
			{
				WalletType:  models.WalletTypeEscrow,
				Direction:   models.DirectionDebit,
				AmountCents: amountCents,
				EntryType:   models.EntryTypeReversal,
				ReferenceID: paymentID,
				Metadata:    map[string]any{"reason": "admin_refund"},
			},
			{
				WalletType:  models.WalletTypeOperating,
				Direction:   models.DirectionCredit,
				AmountCents: amountCents,
				EntryType:   models.EntryTypeReversal,
				ReferenceID: paymentID,
				Metadata:    map[string]any{"reason": "admin_refund"},
			},
		}); err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx, `
			UPDATE payment_requests
			SET status = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND status = ?`, models.PaymentStatusCancelled, paymentID, models.PaymentStatusSettled)
		if err != nil {
			return err
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			return sql.ErrNoRows
		}

		return tx.Commit()
	})
}

func walletIDByTypeTx(ctx context.Context, tx *sql.Tx, walletType string) (int64, error) {
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM wallets WHERE type = ?`, walletType).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func postInTx(ctx context.Context, tx *sql.Tx, postings []Posting) error {
	var drTotal, crTotal int64
	for _, p := range postings {
		switch p.Direction {
		case models.DirectionDebit:
			drTotal += p.AmountCents
		case models.DirectionCredit:
			crTotal += p.AmountCents
		default:
			return fmt.Errorf("invalid direction: %s", p.Direction)
		}
	}
	if drTotal != crTotal {
		return fmt.Errorf("unbalanced posting: dr=%d cr=%d", drTotal, crTotal)
	}

	for _, p := range postings {
		walletID, err := walletIDByTypeTx(ctx, tx, p.WalletType)
		if err != nil {
			return err
		}
		metaBytes, _ := json.Marshal(p.Metadata)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ledger_entries(wallet_id, direction, amount_cents, entry_type, reference_id, metadata, created_at)
			VALUES(?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
			walletID, p.Direction, p.AmountCents, p.EntryType, p.ReferenceID, string(metaBytes)); err != nil {
			return err
		}
		delta := p.AmountCents
		if p.Direction == models.DirectionCredit {
			delta = -delta
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE wallets
			SET balance_cents = balance_cents + ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`, delta, walletID); err != nil {
			return err
		}
	}
	return nil
}
