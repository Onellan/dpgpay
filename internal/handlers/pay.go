package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"dpg-pay/internal/models"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/csrf"
)

type payPageData struct {
	Payment          models.PaymentRequest
	CSRFField        any
	BaseURL          string
	EFTAccountName   string
	EFTBankName      string
	EFTAccountNumber string
	EFTBranchCode    string
	EFTConfigured    bool
}

type payStatusData struct {
	Payment models.PaymentRequest
}

func (a *App) PayPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	payment, err := a.Store.GetPaymentRequestByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if payment.Status == models.PaymentStatusPending && time.Now().After(payment.DueDate) {
		_ = a.Store.UpdatePaymentStatus(r.Context(), payment.ID, models.PaymentStatusPending, models.PaymentStatusExpired)
		payment.Status = models.PaymentStatusExpired
	}
	settings, _ := a.Store.GetSettings(r.Context(), []string{"eft_account_name", "eft_bank_name", "eft_account_number", "eft_branch_code"})
	eftAccountName := strings.TrimSpace(settings["eft_account_name"])
	eftBankName := strings.TrimSpace(settings["eft_bank_name"])
	eftAccountNumber := strings.TrimSpace(settings["eft_account_number"])
	eftBranchCode := strings.TrimSpace(settings["eft_branch_code"])
	if eftAccountName == "" {
		eftAccountName = a.EFTAccountName
	}
	if eftBankName == "" {
		eftBankName = a.EFTBankName
	}
	if eftAccountNumber == "" {
		eftAccountNumber = a.EFTAccountNumber
	}
	if eftBranchCode == "" {
		eftBranchCode = a.EFTBranchCode
	}
	eftConfigured := eftAccountName != "" && eftBankName != "" && eftAccountNumber != ""
	a.render(w, "pay_page.html", payPageData{
		Payment:          payment,
		CSRFField:        csrf.TemplateField(r),
		BaseURL:          a.BaseURL,
		EFTAccountName:   eftAccountName,
		EFTBankName:      eftBankName,
		EFTAccountNumber: eftAccountNumber,
		EFTBranchCode:    eftBranchCode,
		EFTConfigured:    eftConfigured,
	})
}

func (a *App) ConfirmPayment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	payment, err := a.Store.GetPaymentRequestByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	bankName := strings.TrimSpace(r.FormValue("bank_name"))
	bankRef := strings.TrimSpace(r.FormValue("bank_reference"))
	if bankName == "" || bankRef == "" {
		http.Error(w, "bank details are required", http.StatusBadRequest)
		return
	}
	if r.FormValue("eft_confirmed") != "on" {
		http.Error(w, "please confirm that you completed the EFT transfer", http.StatusBadRequest)
		return
	}

	if time.Now().After(payment.DueDate) {
		_ = a.Store.UpdatePaymentStatus(r.Context(), payment.ID, payment.Status, models.PaymentStatusExpired)
		http.Redirect(w, r, "/pay/"+payment.ID+"/status", http.StatusFound)
		return
	}

	if err := a.Store.ConfirmPaymentIntent(r.Context(), payment.ID, bankName, bankRef); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	payment.Status = models.PaymentStatusAwaitingTransfer
	payment.BankName = bankName
	payment.BankReference = bankRef

	_ = a.Store.CreateAuditLog(r.Context(), "PAYMENT_CONFIRMED", payment.PayerEmail, payment.ID, fmt.Sprintf(`{"bank":%q,"bank_reference":%q,"remote_addr":%q,"user_agent":%q}`,
		bankName,
		bankRef,
		r.RemoteAddr,
		r.UserAgent(),
	))
	_ = a.Store.EnqueueWebhook(r.Context(), "payment_request.confirmed", payment.ID, map[string]any{
		"reference":      payment.Reference,
		"status":         payment.Status,
		"bank_name":      bankName,
		"bank_reference": bankRef,
		"confirmed_at":   time.Now().UTC().Format(time.RFC3339),
	})
	if a.AdminEmail != "" {
		_ = a.Notifier.Send([]string{a.AdminEmail}, "DPG Pay: Payer confirmed payment", fmt.Sprintf("%s confirmed payment %s", payment.PayerName, payment.Reference))
	}

	http.Redirect(w, r, "/pay/"+payment.ID+"/status", http.StatusFound)
}

func (a *App) PayStatusPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	payment, err := a.Store.GetPaymentRequestByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.render(w, "pay_status.html", payStatusData{Payment: payment})
}

func (a *App) PayStatusFragment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	payment, err := a.Store.GetPaymentRequestByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.render(w, "partials_pay_status.html", payStatusData{Payment: payment})
}

func (a *App) PaySuccessPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	payment, err := a.Store.GetPaymentRequestByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.render(w, "pay_success.html", payStatusData{Payment: payment})
}

func (a *App) PayFailedPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	payment, err := a.Store.GetPaymentRequestByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.render(w, "pay_failed.html", payStatusData{Payment: payment})
}
