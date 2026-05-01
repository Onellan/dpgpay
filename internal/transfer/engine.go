package transfer

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"dpg-pay/internal/ledger"
	"dpg-pay/internal/models"
	"dpg-pay/internal/notify"
)

type Rail interface {
	Process(ctx context.Context, payment models.PaymentRequest) (bool, error)
}

type SimulationRail struct{}

func (SimulationRail) Process(_ context.Context, _ models.PaymentRequest) (bool, error) {
	return rand.Intn(100) < 90, nil
}

type Engine struct {
	store      *models.Store
	ledger     *ledger.Service
	notify     *notify.Service
	rail       Rail
	adminEmail string

	mu    sync.Mutex
	dueAt map[string]time.Time
}

func NewEngine(store *models.Store, ledgerSvc *ledger.Service, notifier *notify.Service, rail Rail, adminEmail string) *Engine {
	return &Engine{
		store:      store,
		ledger:     ledgerSvc,
		notify:     notifier,
		rail:       rail,
		adminEmail: adminEmail,
		dueAt:      map[string]time.Time{},
	}
}

func (e *Engine) Start(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	log.Printf("[SIMULATED] transfer engine started")
	for {
		select {
		case <-ctx.Done():
			log.Printf("[SIMULATED] transfer engine stopping")
			return
		case <-ticker.C:
			e.processAwaiting(ctx)
		}
	}
}

func (e *Engine) processAwaiting(ctx context.Context) {
	payments, err := e.store.GetAwaitingTransfers(ctx, 200)
	if err != nil {
		log.Printf("[SIMULATED] awaiting query failed: %v", err)
		return
	}
	now := time.Now()
	for _, p := range payments {
		due := e.getOrCreateDue(p.ID, now)
		if now.Before(due) {
			continue
		}
		if err := e.processOne(ctx, p); err != nil {
			log.Printf("[SIMULATED] payment %s process error: %v", p.ID, err)
		}
		e.clearDue(p.ID)
	}
}

func (e *Engine) processOne(ctx context.Context, p models.PaymentRequest) error {
	success, err := e.rail.Process(ctx, p)
	if err != nil {
		return err
	}
	if success {
		if err := e.ledger.ClearPendingEscrowToOperating(ctx, p.ID, p.AmountCents, map[string]any{"event": "simulated_transfer_success"}); err != nil {
			if !isUniqueConstraintError(err) {
				return err
			}
			log.Printf("[SIMULATED] payment %s ledger posting already applied, continuing status update", p.ID)
		}
		if err := e.store.UpdatePaymentStatus(ctx, p.ID, models.PaymentStatusAwaitingTransfer, models.PaymentStatusSettled); err != nil {
			return err
		}
		_ = e.store.CreateAuditLog(ctx, "PAYMENT_SETTLED", "transfer-engine", p.ID, `{"mode":"SIMULATED"}`)
		_ = e.store.EnqueueWebhook(ctx, "payment_request.settled", p.ID, map[string]any{
			"reference":  p.Reference,
			"status":     models.PaymentStatusSettled,
			"settled_at": time.Now().UTC().Format(time.RFC3339),
		})
		log.Printf("[SIMULATED] payment %s settled", p.ID)

		_ = e.notify.Send([]string{e.adminEmail}, "DPG Pay: Payment settled", fmt.Sprintf("Payment %s settled for %d cents", p.Reference, p.AmountCents))
		_ = e.notify.Send([]string{p.PayerEmail}, "Your payment was settled", fmt.Sprintf("Your payment %s has been settled.", p.Reference))
		return nil
	}

	newRetry := p.RetryCount + 1
	if err := e.store.IncrementRetryAndSetStatus(ctx, p.ID, models.PaymentStatusFailed); err != nil {
		return err
	}
	_ = e.store.CreateAuditLog(ctx, "PAYMENT_TRANSFER_FAILED", "transfer-engine", p.ID, fmt.Sprintf(`{"retry":%d}`, newRetry))
	_ = e.store.EnqueueWebhook(ctx, "payment_request.failed", p.ID, map[string]any{
		"reference": p.Reference,
		"status":    models.PaymentStatusFailed,
		"retry":     newRetry,
		"failed_at": time.Now().UTC().Format(time.RFC3339),
	})
	log.Printf("[SIMULATED] payment %s failed on retry %d", p.ID, newRetry)

	_ = e.notify.Send([]string{e.adminEmail}, "DPG Pay: Transfer failed", fmt.Sprintf("Payment %s failed (retry %d)", p.Reference, newRetry))
	_ = e.notify.Send([]string{p.PayerEmail}, "Your payment is delayed", fmt.Sprintf("Payment %s failed to settle on attempt %d.", p.Reference, newRetry))

	if newRetry < 3 {
		if err := e.store.UpdatePaymentStatus(ctx, p.ID, models.PaymentStatusFailed, models.PaymentStatusAwaitingTransfer); err != nil {
			return err
		}
		_ = e.store.CreateAuditLog(ctx, "PAYMENT_RETRY_SCHEDULED", "transfer-engine", p.ID, fmt.Sprintf(`{"next_retry":%d}`, newRetry+1))
		log.Printf("[SIMULATED] payment %s retry scheduled", p.ID)
	}
	return nil
}

func (e *Engine) TriggerSettlement(ctx context.Context) (models.Settlement, error) {
	run, err := e.store.CreateSettlement(ctx, "RUNNING")
	if err != nil {
		return models.Settlement{}, err
	}
	operating, err := e.store.GetWalletByType(ctx, models.WalletTypeOperating)
	if err != nil {
		_ = e.store.CompleteSettlement(ctx, run.ID, 0, 0, "FAILED")
		return models.Settlement{}, err
	}
	if operating.BalanceCents <= 0 {
		if err := e.store.CompleteSettlement(ctx, run.ID, 0, 0, "SUCCESS"); err != nil {
			return models.Settlement{}, err
		}
		_ = e.store.CreateAuditLog(ctx, "SETTLEMENT_COMPLETED", "admin", fmt.Sprintf("%d", run.ID), `{"amount_cents":0}`)
		settlement, _ := e.store.GetSettlementByID(ctx, run.ID)
		return settlement, nil
	}

	if err := e.ledger.RunSettlement(ctx, operating.BalanceCents, run.ID); err != nil {
		_ = e.store.CompleteSettlement(ctx, run.ID, 0, 0, "FAILED")
		_ = e.store.CreateAuditLog(ctx, "SETTLEMENT_FAILED", "admin", fmt.Sprintf("%d", run.ID), fmt.Sprintf(`{"error":%q}`, err.Error()))
		return models.Settlement{}, err
	}
	if err := e.store.CompleteSettlement(ctx, run.ID, operating.BalanceCents, 2, "SUCCESS"); err != nil {
		return models.Settlement{}, err
	}
	_ = e.store.CreateAuditLog(ctx, "SETTLEMENT_COMPLETED", "admin", fmt.Sprintf("%d", run.ID), fmt.Sprintf(`{"amount_cents":%d}`, operating.BalanceCents))
	_ = e.store.EnqueueWebhook(ctx, "settlement.completed", fmt.Sprintf("%d", run.ID), map[string]any{
		"settlement_id": run.ID,
		"amount_cents":  operating.BalanceCents,
		"status":        "SUCCESS",
		"completed_at":  time.Now().UTC().Format(time.RFC3339),
	})
	_ = e.notify.Send([]string{e.adminEmail}, "DPG Pay: Settlement complete", fmt.Sprintf("Settlement %d completed for %d cents", run.ID, operating.BalanceCents))

	settlement, err := e.store.GetSettlementByID(ctx, run.ID)
	if err != nil {
		return models.Settlement{}, err
	}
	return settlement, nil
}

func (e *Engine) getOrCreateDue(paymentID string, now time.Time) time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	due, ok := e.dueAt[paymentID]
	if ok {
		return due
	}
	delay := time.Duration(30+rand.Intn(91)) * time.Second
	due = now.Add(delay)
	e.dueAt[paymentID] = due
	log.Printf("[SIMULATED] payment %s queued with delay %s", paymentID, delay)
	return due
}

func (e *Engine) clearDue(paymentID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.dueAt, paymentID)
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique")
}
