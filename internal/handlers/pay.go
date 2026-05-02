package handlers

import (
	crand "crypto/rand"
	"fmt"
	"net/http"
	"net/url"
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

type payMethodData struct {
	Payment          models.PaymentRequest
	CSRFField        any
	Method           string
	EFTAccountName   string
	EFTBankName      string
	EFTAccountNumber string
	EFTBranchCode    string
	EFTConfigured    bool
}

type payResultData struct {
	Payment  models.PaymentRequest
	Message  string
	RetryURL string
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

func (a *App) PayMethodPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	method := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "method")))
	if method != "eft" && method != "card" {
		http.NotFound(w, r)
		return
	}
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

	a.render(w, "pay_method.html", payMethodData{
		Payment:          payment,
		CSRFField:        csrf.TemplateField(r),
		Method:           method,
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
	paymentMethod := strings.ToLower(strings.TrimSpace(r.FormValue("payment_method")))
	bankName := ""
	bankRef := ""
	auditDetail := ""

	switch paymentMethod {
	case "eft":
		payerBankName := strings.TrimSpace(r.FormValue("eft_bank_name"))
		payerAccountHolder := strings.TrimSpace(r.FormValue("eft_account_holder"))
		payerAccountNumber := digitsOnly(strings.TrimSpace(r.FormValue("eft_account_number")))
		payerBranchCode := digitsOnly(strings.TrimSpace(r.FormValue("eft_branch_code")))
		bankRef = strings.TrimSpace(r.FormValue("bank_reference"))
		if payerBankName == "" || payerAccountHolder == "" || payerAccountNumber == "" || payerBranchCode == "" || bankRef == "" {
			http.Error(w, "all EFT details are required", http.StatusBadRequest)
			return
		}
		if len(payerAccountNumber) > 20 || len(payerBranchCode) > 20 {
			http.Error(w, "invalid account number or branch code", http.StatusBadRequest)
			return
		}
		bankName = payerBankName
		auditDetail = fmt.Sprintf(`{"payment_method":"eft","bank":%q,"bank_reference":%q,"account_holder":%q,"account_last4":%q,"branch_code":%q,"remote_addr":%q,"user_agent":%q}`,
			payerBankName,
			bankRef,
			payerAccountHolder,
			last4(payerAccountNumber),
			payerBranchCode,
			r.RemoteAddr,
			r.UserAgent(),
		)
	case "card":
		cardName := strings.TrimSpace(r.FormValue("card_name"))
		cardNumberRaw := strings.TrimSpace(r.FormValue("card_number"))
		cardExpiryMonth := strings.TrimSpace(r.FormValue("card_expiry_month"))
		cardExpiryYear := strings.TrimSpace(r.FormValue("card_expiry_year"))
		cardCVV := strings.TrimSpace(r.FormValue("card_cvv"))
		cardNumber := digitsOnly(cardNumberRaw)
		if cardName == "" || cardNumber == "" || cardExpiryMonth == "" || cardExpiryYear == "" || cardCVV == "" {
			http.Error(w, "all card details are required", http.StatusBadRequest)
			return
		}
		if len(cardNumber) != 16 || !isDigits(cardCVV) || len(cardCVV) < 3 || len(cardCVV) > 4 {
			http.Error(w, "invalid card details", http.StatusBadRequest)
			return
		}
		if !isValidCardExpiry(cardExpiryMonth, cardExpiryYear, time.Now()) {
			http.Error(w, "invalid or expired card expiry date", http.StatusBadRequest)
			return
		}
		bankName = "CARD"
		bankRef = "SIM-CARD-" + last4(cardNumber)
		auditDetail = fmt.Sprintf(`{"payment_method":"card","card_name":%q,"card_last4":%q,"card_expiry_month":%q,"card_expiry_year":%q,"remote_addr":%q,"user_agent":%q}`,
			cardName,
			last4(cardNumber),
			cardExpiryMonth,
			cardExpiryYear,
			r.RemoteAddr,
			r.UserAgent(),
		)
	default:
		http.Error(w, "invalid payment method", http.StatusBadRequest)
		return
	}

	if payment.Status == models.PaymentStatusPending && time.Now().After(payment.DueDate) {
		_ = a.Store.UpdatePaymentStatus(r.Context(), payment.ID, models.PaymentStatusPending, models.PaymentStatusExpired)
		http.Redirect(w, r, "/pay/"+payment.ID+"/status", http.StatusFound)
		return
	}
	if payment.Status != models.PaymentStatusPending {
		http.Redirect(w, r, "/pay/"+payment.ID+"/status", http.StatusFound)
		return
	}

	if !randomPaymentOutcome() {
		msg := "Payment failed this time. Please try again."
		if paymentMethod == "card" {
			msg = "Card payment was declined in simulation. Please try again."
		}
		_ = a.Store.CreateAuditLog(r.Context(), "PAYMENT_SIMULATION_FAILED", payment.PayerEmail, payment.ID, fmt.Sprintf(`{"payment_method":%q,"reason":%q}`, paymentMethod, msg))
		http.Redirect(w, r, "/pay/"+payment.ID+"/failed?method="+url.QueryEscape(paymentMethod)+"&msg="+url.QueryEscape(msg), http.StatusFound)
		return
	}

	if err := a.Store.ConfirmPaymentIntent(r.Context(), payment.ID, bankName, bankRef); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	payment.Status = models.PaymentStatusAwaitingTransfer
	payment.BankName = bankName
	payment.BankReference = bankRef

	_ = a.Store.CreateAuditLog(r.Context(), "PAYMENT_CONFIRMED", payment.PayerEmail, payment.ID, auditDetail)
	_ = a.Store.EnqueueWebhook(r.Context(), "payment_request.confirmed", payment.ID, map[string]any{
		"reference":      payment.Reference,
		"status":         payment.Status,
		"payment_method": paymentMethod,
		"bank_name":      bankName,
		"bank_reference": bankRef,
		"confirmed_at":   time.Now().UTC().Format(time.RFC3339),
	})
	if a.AdminEmail != "" {
		_ = a.Notifier.Send([]string{a.AdminEmail}, "DPG Pay: Payer confirmed payment", fmt.Sprintf("%s confirmed payment %s", payment.PayerName, payment.Reference))
	}
	msg := "Payment initiated successfully. We are processing your transfer."
	if paymentMethod == "card" {
		msg = "Card payment authorized in simulation. Processing has started."
	}
	http.Redirect(w, r, "/pay/"+payment.ID+"/success?msg="+url.QueryEscape(msg), http.StatusFound)
}

func isValidCardExpiry(monthValue, yearValue string, now time.Time) bool {
	month := parseInt64(strings.TrimSpace(monthValue))
	year := parseInt64(strings.TrimSpace(yearValue))
	if month < 1 || month > 12 {
		return false
	}
	if year < 1000 || year > 9999 {
		return false
	}
	if year < int64(now.Year()) {
		return false
	}
	if year == int64(now.Year()) && month < int64(now.Month()) {
		return false
	}
	return true
}

func digitsOnly(v string) string {
	var b strings.Builder
	b.Grow(len(v))
	for _, r := range v {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isDigits(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func last4(v string) string {
	if len(v) <= 4 {
		return v
	}
	return v[len(v)-4:]
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
	msg := strings.TrimSpace(r.URL.Query().Get("msg"))
	if msg == "" {
		msg = fmt.Sprintf("Reference %s payment was accepted.", payment.Reference)
	}
	a.render(w, "pay_success.html", payResultData{Payment: payment, Message: msg})
}

func (a *App) PayFailedPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	payment, err := a.Store.GetPaymentRequestByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	msg := strings.TrimSpace(r.URL.Query().Get("msg"))
	if msg == "" {
		msg = "Payment failed. Please try again."
	}
	method := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("method")))
	retryURL := "/pay/" + payment.ID
	if method == "eft" || method == "card" {
		retryURL = "/pay/" + payment.ID + "/method/" + method
	}
	a.render(w, "pay_failed.html", payResultData{Payment: payment, Message: msg, RetryURL: retryURL})
}

func randomPaymentOutcome() bool {
	var b [1]byte
	if _, err := crand.Read(b[:]); err != nil {
		return time.Now().UnixNano()%2 == 0
	}
	return int(b[0])%2 == 0
}
