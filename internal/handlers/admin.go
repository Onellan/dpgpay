package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dpg-pay/internal/middleware"
	"dpg-pay/internal/models"
	"dpg-pay/internal/observability"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/csrf"
	"golang.org/x/crypto/bcrypt"
)

type adminDashboardData struct {
	Stats           models.DashboardStats
	Wallets         []models.Wallet
	Payments        []models.PaymentRequest
	FilterStatus    string
	LiveEntries     []models.LedgerEntry
	WalletTypeByID  map[int64]string
	CSRFField       any
	ShareURLBuilder string
}

type paymentRowData struct {
	Payment models.PaymentRequest
}

type walletPageData struct {
	Wallet    models.Wallet
	Entries   []models.LedgerEntry
	Page      int
	PrevPage  int
	NextPage  int
	WalletKey string
}

type settlementsPageData struct {
	Settlements []models.Settlement
	CSRFField   any
}

type auditPageData struct {
	Logs []models.AuditLog
}

type reconciliationPageData struct {
	Report    models.ReconciliationReport
	CSRFField any
}

type webhookPageData struct {
	Rows []models.WebhookOutboxItem
}

type adminSettingsPageData struct {
	Settings  models.AdminSettings
	Operators []models.Operator
	CSRFField any
	Message   string
	Error     string
}

type adminRefundPageData struct {
	Payment   models.PaymentRequest
	CSRFField any
}

func (a *App) AdminHub(w http.ResponseWriter, r *http.Request) {
	a.render(w, "admin_hub.html", struct {
		CSRFField any
	}{
		CSRFField: csrf.TemplateField(r),
	})
}

func (a *App) AdminDashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := a.Store.DashboardStats(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	wallets, err := a.Store.ListWallets(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := a.Store.ExpireOverduePendingPayments(r.Context(), 2000); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status := r.URL.Query().Get("status")
	if strings.TrimSpace(status) == "" {
		status = "ACTIVE"
	}
	payments, err := a.Store.ListPaymentRequests(r.Context(), status, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	live, err := a.Store.ListRecentLedgerEntries(r.Context(), 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	walletTypeByID := make(map[int64]string, len(wallets))
	for _, wallet := range wallets {
		walletTypeByID[wallet.ID] = wallet.Type
	}

	a.render(w, "admin_dashboard.html", adminDashboardData{
		Stats:           stats,
		Wallets:         wallets,
		Payments:        payments,
		FilterStatus:    status,
		LiveEntries:     live,
		WalletTypeByID:  walletTypeByID,
		CSRFField:       csrf.TemplateField(r),
		ShareURLBuilder: a.BaseURL,
	})
}

func (a *App) AdminLiveFeed(w http.ResponseWriter, r *http.Request) {
	entries, err := a.Store.ListRecentLedgerEntries(r.Context(), 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	wallets, err := a.Store.ListWallets(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	walletTypeByID := make(map[int64]string, len(wallets))
	for _, wallet := range wallets {
		walletTypeByID[wallet.ID] = wallet.Type
	}
	a.render(w, "partials_live_feed.html", struct {
		LiveEntries    []models.LedgerEntry
		WalletTypeByID map[int64]string
	}{
		LiveEntries:    entries,
		WalletTypeByID: walletTypeByID,
	})
}

func (a *App) AdminCreatePaymentRequest(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	amountCents := parseInt64(r.FormValue("amount_cents"))
	if amountCents <= 0 {
		http.Error(w, "amount must be greater than zero", http.StatusBadRequest)
		return
	}
	dueDate, err := time.Parse("2006-01-02", strings.TrimSpace(r.FormValue("due_date")))
	if err != nil {
		http.Error(w, "invalid due date", http.StatusBadRequest)
		return
	}
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if dueDate.Before(todayStart) {
		http.Error(w, "due date cannot be in the past", http.StatusBadRequest)
		return
	}
	// Treat due date as inclusive for the full day.
	dueDate = time.Date(dueDate.Year(), dueDate.Month(), dueDate.Day(), 23, 59, 59, 0, now.Location())
	reference, err := a.Store.GeneratePaymentReference(r.Context(), time.Now())
	if err != nil {
		http.Error(w, "failed to generate reference", http.StatusInternalServerError)
		return
	}

	p, err := a.Store.CreatePaymentRequest(r.Context(), models.PaymentRequest{
		ID:          uuid.NewString(),
		Reference:   reference,
		PayerName:   strings.TrimSpace(r.FormValue("payer_name")),
		PayerEmail:  strings.TrimSpace(r.FormValue("payer_email")),
		AmountCents: amountCents,
		Currency:    strings.ToUpper(strings.TrimSpace(r.FormValue("currency"))),
		Description: strings.TrimSpace(r.FormValue("description")),
		DueDate:     dueDate,
		Status:      models.PaymentStatusPending,
	})
	if p.Currency == "" {
		p.Currency = "ZAR"
	}
	if err != nil {
		http.Error(w, "failed to create payment", http.StatusInternalServerError)
		return
	}
	_ = a.Store.EnqueueWebhook(r.Context(), "payment_request.created", p.ID, map[string]any{
		"reference":    p.Reference,
		"amount_cents": p.AmountCents,
		"currency":     p.Currency,
		"due_date":     p.DueDate.UTC().Format(time.RFC3339),
		"created_at":   p.CreatedAt.UTC().Format(time.RFC3339),
	})
	adminActor := middleware.AdminUserFromContext(r.Context())
	if adminActor == "" {
		adminActor = "admin"
	}
	_ = a.Store.CreateAuditLog(r.Context(), "PAYMENT_REQUEST_CREATED", adminActor, p.ID, fmt.Sprintf(`{"reference":%q,"remote_addr":%q,"user_agent":%q}`,
		p.Reference,
		r.RemoteAddr,
		r.UserAgent(),
	))
	observability.LogEvent("payment_created", map[string]any{
		"payment_id":       p.ID,
		"reference":        p.Reference,
		"amount_cents":     p.AmountCents,
		"currency":         p.Currency,
		"created_by_admin": adminActor,
	})

	if r.Header.Get("HX-Request") == "true" {
		a.render(w, "partials_payment_row.html", p)
		return
	}
	http.Redirect(w, r, "/admin/dashboard", http.StatusFound)
}

func (a *App) AdminWalletDetail(w http.ResponseWriter, r *http.Request) {
	walletType := chi.URLParam(r, "walletType")
	if walletType == "" {
		walletType = models.WalletTypeOperating
	}
	walletType = strings.ToUpper(walletType)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	wlt, entries, err := a.Wallet.Details(r.Context(), walletType, page, 30)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if page < 1 {
		page = 1
	}
	prevPage := page - 1
	if prevPage < 1 {
		prevPage = 1
	}
	a.render(w, "admin_wallet.html", walletPageData{
		Wallet:    wlt,
		Entries:   entries,
		Page:      page,
		PrevPage:  prevPage,
		NextPage:  page + 1,
		WalletKey: walletType,
	})
}

func (a *App) AdminSettlementsPage(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Store.ListSettlements(r.Context(), 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "admin_settlements.html", settlementsPageData{Settlements: rows, CSRFField: csrf.TemplateField(r)})
}

func (a *App) AdminTriggerSettlement(w http.ResponseWriter, r *http.Request) {
	if _, err := a.Transfer.TriggerSettlement(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		a.AdminSettlementsPage(w, r)
		return
	}
	http.Redirect(w, r, "/admin/settlements", http.StatusFound)
}

func (a *App) AdminAuditPage(w http.ResponseWriter, r *http.Request) {
	logs, err := a.Store.ListAuditLogs(r.Context(), 500)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "admin_audit.html", auditPageData{Logs: logs})
}

func (a *App) AdminReconciliationPage(w http.ResponseWriter, r *http.Request) {
	report, err := a.Store.ReconciliationReport(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "admin_reconciliation.html", reconciliationPageData{Report: report, CSRFField: csrf.TemplateField(r)})
}

func (a *App) AdminValidateLedger(w http.ResponseWriter, r *http.Request) {
	if err := a.Ledger.ValidateIntegrity(r.Context()); err != nil {
		_ = a.Store.CreateAuditLog(r.Context(), "LEDGER_VALIDATION_FAILED", middleware.AdminUserFromContext(r.Context()), "", fmt.Sprintf(`{"error":%q}`, err.Error()))
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	_ = a.Store.CreateAuditLog(r.Context(), "LEDGER_VALIDATION_OK", middleware.AdminUserFromContext(r.Context()), "", `{"source":"admin_endpoint"}`)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/admin/reconciliation")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/admin/reconciliation", http.StatusFound)
}

func (a *App) AdminRefundPaymentRequest(w http.ResponseWriter, r *http.Request) {
	paymentID := strings.TrimSpace(chi.URLParam(r, "paymentID"))
	if paymentID == "" {
		http.Error(w, "payment id is required", http.StatusBadRequest)
		return
	}

	payment, err := a.Store.GetPaymentRequestByID(r.Context(), paymentID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "payment not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if payment.Status != models.PaymentStatusSettled {
		http.Error(w, "only settled payments can be refunded", http.StatusConflict)
		return
	}

	if err := a.Ledger.RefundSettledPayment(r.Context(), payment.ID, payment.AmountCents); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	actor := middleware.AdminUserFromContext(r.Context())
	if actor == "" {
		actor = "admin"
	}
	_ = a.Store.CreateAuditLog(r.Context(), "PAYMENT_REFUNDED", actor, payment.ID, fmt.Sprintf(`{"reference":%q,"amount_cents":%d}`, payment.Reference, payment.AmountCents))
	_ = a.Store.EnqueueWebhook(r.Context(), "payment_request.refunded", payment.ID, map[string]any{
		"reference":    payment.Reference,
		"status":       models.PaymentStatusCancelled,
		"amount_cents": payment.AmountCents,
		"refunded_at":  time.Now().UTC().Format(time.RFC3339),
	})
	observability.LogEvent("payment_refunded", map[string]any{
		"payment_id":   payment.ID,
		"reference":    payment.Reference,
		"amount_cents": payment.AmountCents,
		"actor":        actor,
	})

	http.Redirect(w, r, "/admin/dashboard", http.StatusFound)
}

func (a *App) AdminRefundPaymentPage(w http.ResponseWriter, r *http.Request) {
	paymentID := strings.TrimSpace(chi.URLParam(r, "paymentID"))
	if paymentID == "" {
		http.Error(w, "payment id is required", http.StatusBadRequest)
		return
	}
	payment, err := a.Store.GetPaymentRequestByID(r.Context(), paymentID)
	if err != nil {
		http.Error(w, "payment not found", http.StatusNotFound)
		return
	}
	a.render(w, "admin_refund.html", adminRefundPageData{Payment: payment, CSRFField: csrf.TemplateField(r)})
}

func (a *App) AdminWebhooksPage(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Store.ListWebhookOutbox(r.Context(), 300)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "admin_webhooks.html", webhookPageData{Rows: rows})
}

func (a *App) AdminSettingsPage(w http.ResponseWriter, r *http.Request) {
	a.renderAdminSettingsPage(w, r, "", "")
}

func (a *App) AdminSaveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	settings := map[string]string{
		"business_name":                strings.TrimSpace(r.FormValue("business_name")),
		"business_logo_url":            strings.TrimSpace(r.FormValue("business_logo_url")),
		"business_contact_email":       strings.TrimSpace(r.FormValue("business_contact_email")),
		"business_contact_phone":       strings.TrimSpace(r.FormValue("business_contact_phone")),
		"smtp_host":                    strings.TrimSpace(r.FormValue("smtp_host")),
		"smtp_port":                    strings.TrimSpace(r.FormValue("smtp_port")),
		"smtp_user":                    strings.TrimSpace(r.FormValue("smtp_user")),
		"smtp_pass":                    strings.TrimSpace(r.FormValue("smtp_pass")),
		"smtp_from":                    strings.TrimSpace(r.FormValue("smtp_from")),
		"fee_flat_cents":               strings.TrimSpace(r.FormValue("fee_flat_cents")),
		"fee_percent_bps":              strings.TrimSpace(r.FormValue("fee_percent_bps")),
		"fee_per_transaction_cents":    strings.TrimSpace(r.FormValue("fee_per_transaction_cents")),
		"currency":                     strings.TrimSpace(strings.ToUpper(r.FormValue("currency"))),
		"locale":                       strings.TrimSpace(r.FormValue("locale")),
		"timezone":                     strings.TrimSpace(r.FormValue("timezone")),
		"payment_description_template": strings.TrimSpace(r.FormValue("payment_description_template")),
		"payment_due_days_default":     strings.TrimSpace(r.FormValue("payment_due_days_default")),
		"notify_admin_on_confirm":      boolToSetting(r.FormValue("notify_admin_on_confirm") == "on"),
		"notify_admin_on_settle":       boolToSetting(r.FormValue("notify_admin_on_settle") == "on"),
		"notify_payer_on_settle":       boolToSetting(r.FormValue("notify_payer_on_settle") == "on"),
		"notify_payer_on_fail":         boolToSetting(r.FormValue("notify_payer_on_fail") == "on"),
		"eft_account_name":             strings.TrimSpace(r.FormValue("eft_account_name")),
		"eft_bank_name":                strings.TrimSpace(r.FormValue("eft_bank_name")),
		"eft_account_number":           strings.TrimSpace(r.FormValue("eft_account_number")),
		"eft_branch_code":              strings.TrimSpace(r.FormValue("eft_branch_code")),
	}
	if err := a.Store.UpsertSettings(r.Context(), settings); err != nil {
		a.renderAdminSettingsPage(w, r, "", err.Error())
		return
	}
	actor := middleware.AdminUserFromContext(r.Context())
	if actor == "" {
		actor = "admin"
	}
	_ = a.Store.CreateAuditLog(r.Context(), "ADMIN_SETTINGS_UPDATED", actor, "", `{"source":"admin_settings"}`)
	a.renderAdminSettingsPage(w, r, "Settings saved", "")
}

func (a *App) AdminCreateOperator(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	email := strings.TrimSpace(r.FormValue("email"))
	role := strings.TrimSpace(r.FormValue("role"))
	password := r.FormValue("password")
	if username == "" || password == "" {
		a.renderAdminSettingsPage(w, r, "", "username and password are required")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		a.renderAdminSettingsPage(w, r, "", "failed to generate password hash")
		return
	}
	if err := a.Store.CreateOperator(r.Context(), username, string(hash), email, role); err != nil {
		a.renderAdminSettingsPage(w, r, "", err.Error())
		return
	}
	_ = a.Store.CreateAuditLog(r.Context(), "OPERATOR_CREATED", middleware.AdminUserFromContext(r.Context()), username, fmt.Sprintf(`{"role":%q}`, role))
	a.renderAdminSettingsPage(w, r, "Operator created", "")
}

func (a *App) AdminToggleOperatorStatus(w http.ResponseWriter, r *http.Request) {
	operatorID, _ := strconv.ParseInt(chi.URLParam(r, "operatorID"), 10, 64)
	if operatorID <= 0 {
		http.Error(w, "invalid operator id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	isActive := r.FormValue("is_active") == "1"
	if err := a.Store.SetOperatorActive(r.Context(), operatorID, isActive); err != nil {
		a.renderAdminSettingsPage(w, r, "", err.Error())
		return
	}
	_ = a.Store.CreateAuditLog(r.Context(), "OPERATOR_STATUS_UPDATED", middleware.AdminUserFromContext(r.Context()), fmt.Sprintf("%d", operatorID), fmt.Sprintf(`{"is_active":%t}`, isActive))
	a.renderAdminSettingsPage(w, r, "Operator status updated", "")
}

func (a *App) AdminResetOperatorPassword(w http.ResponseWriter, r *http.Request) {
	operatorID, _ := strconv.ParseInt(chi.URLParam(r, "operatorID"), 10, 64)
	if operatorID <= 0 {
		http.Error(w, "invalid operator id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	password := r.FormValue("password")
	if strings.TrimSpace(password) == "" {
		a.renderAdminSettingsPage(w, r, "", "password is required")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		a.renderAdminSettingsPage(w, r, "", "failed to generate password hash")
		return
	}
	if err := a.Store.UpdateOperatorPasswordHash(r.Context(), operatorID, string(hash)); err != nil {
		a.renderAdminSettingsPage(w, r, "", err.Error())
		return
	}
	_ = a.Store.CreateAuditLog(r.Context(), "OPERATOR_PASSWORD_RESET", middleware.AdminUserFromContext(r.Context()), fmt.Sprintf("%d", operatorID), `{"source":"admin_settings"}`)
	a.renderAdminSettingsPage(w, r, "Operator password updated", "")
}

func (a *App) renderAdminSettingsPage(w http.ResponseWriter, r *http.Request, message, renderErr string) {
	settingsKeys := []string{
		"business_name", "business_logo_url", "business_contact_email", "business_contact_phone",
		"smtp_host", "smtp_port", "smtp_user", "smtp_pass", "smtp_from",
		"fee_flat_cents", "fee_percent_bps", "fee_per_transaction_cents",
		"currency", "locale", "timezone",
		"payment_description_template", "payment_due_days_default",
		"notify_admin_on_confirm", "notify_admin_on_settle", "notify_payer_on_settle", "notify_payer_on_fail",
		"eft_account_name", "eft_bank_name", "eft_account_number", "eft_branch_code",
	}
	kv, err := a.Store.GetSettings(r.Context(), settingsKeys)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	operators, err := a.Store.ListOperators(r.Context(), 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := adminSettingsPageData{
		Settings: models.AdminSettings{
			BusinessName:               settingWithDefault(kv, "business_name", "DPG Pay"),
			BusinessLogoURL:            kv["business_logo_url"],
			BusinessContactEmail:       kv["business_contact_email"],
			BusinessContactPhone:       kv["business_contact_phone"],
			SMTPHost:                   kv["smtp_host"],
			SMTPPort:                   kv["smtp_port"],
			SMTPUser:                   kv["smtp_user"],
			SMTPPass:                   kv["smtp_pass"],
			SMTPFrom:                   kv["smtp_from"],
			FeeFlatCents:               kv["fee_flat_cents"],
			FeePercentBps:              kv["fee_percent_bps"],
			FeePerTransactionCents:     kv["fee_per_transaction_cents"],
			Currency:                   settingWithDefault(kv, "currency", "ZAR"),
			Locale:                     settingWithDefault(kv, "locale", "en-ZA"),
			Timezone:                   settingWithDefault(kv, "timezone", "Africa/Johannesburg"),
			PaymentDescriptionTemplate: kv["payment_description_template"],
			PaymentDueDaysDefault:      settingWithDefault(kv, "payment_due_days_default", "7"),
			NotifyAdminOnConfirm:       settingToBool(kv["notify_admin_on_confirm"], true),
			NotifyAdminOnSettle:        settingToBool(kv["notify_admin_on_settle"], true),
			NotifyPayerOnSettle:        settingToBool(kv["notify_payer_on_settle"], true),
			NotifyPayerOnFail:          settingToBool(kv["notify_payer_on_fail"], true),
			EFTAccountName:             settingWithDefault(kv, "eft_account_name", a.EFTAccountName),
			EFTBankName:                settingWithDefault(kv, "eft_bank_name", a.EFTBankName),
			EFTAccountNumber:           settingWithDefault(kv, "eft_account_number", a.EFTAccountNumber),
			EFTBranchCode:              settingWithDefault(kv, "eft_branch_code", a.EFTBranchCode),
		},
		Operators: operators,
		CSRFField: csrf.TemplateField(r),
		Message:   message,
		Error:     renderErr,
	}
	a.render(w, "admin_settings.html", data)
}

func boolToSetting(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func settingToBool(value string, fallback bool) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func settingWithDefault(kv map[string]string, key, fallback string) string {
	if v := strings.TrimSpace(kv[key]); v != "" {
		return v
	}
	return fallback
}
