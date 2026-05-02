package handlers

import (
	"context"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dpg-pay/internal/ledger"
	"dpg-pay/internal/models"
	"dpg-pay/internal/notify"
	"dpg-pay/internal/transfer"
	"dpg-pay/internal/wallet"
)

type App struct {
	Store            *models.Store
	Ledger           *ledger.Service
	Wallet           *wallet.Service
	Transfer         *transfer.Engine
	Notifier         *notify.Service
	Templates        *template.Template
	AdminUser        string
	AdminHash        string
	AdminEmail       string
	BaseURL          string
	EFTAccountName   string
	EFTBankName      string
	EFTAccountNumber string
	EFTBranchCode    string
	CookieSecure     bool
}

func NewApp(store *models.Store, ledgerSvc *ledger.Service, walletSvc *wallet.Service, transferEngine *transfer.Engine, notifier *notify.Service, adminUser, adminHash, adminEmail, baseURL, eftAccountName, eftBankName, eftAccountNumber, eftBranchCode string, cookieSecure bool) (*App, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"fmtCents":       wallet.FormatCents,
		"fmtDateTime":    formatDateTime,
		"fmtDateTimePtr": formatDateTimePtr,
		"fmtDate":        formatDate,
		"statusClass":    statusClass,
		"statusHeadline": statusHeadline,
	}).ParseGlob(filepath.Join("internal", "templates", "*.html"))
	if err != nil {
		return nil, err
	}
	return &App{
		Store:            store,
		Ledger:           ledgerSvc,
		Wallet:           walletSvc,
		Transfer:         transferEngine,
		Notifier:         notifier,
		Templates:        tmpl,
		AdminUser:        adminUser,
		AdminHash:        adminHash,
		AdminEmail:       adminEmail,
		BaseURL:          strings.TrimSuffix(baseURL, "/"),
		EFTAccountName:   eftAccountName,
		EFTBankName:      eftBankName,
		EFTAccountNumber: eftAccountNumber,
		EFTBranchCode:    eftBranchCode,
		CookieSecure:     cookieSecure,
	}, nil
}

func (a *App) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.Templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s render failed: %v", name, err)
		http.Error(w, "template rendering error", http.StatusInternalServerError)
	}
}

func (a *App) requestBaseURL(r *http.Request) string {
	if !isLocalBaseURL(a.BaseURL) {
		return a.BaseURL
	}

	scheme := "http"
	if r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		scheme = "https"
	}

	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return a.BaseURL
	}

	return scheme + "://" + host
}

func isLocalBaseURL(baseURL string) bool {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return true
	}
	hostname := strings.ToLower(parsed.Hostname())
	switch hostname {
	case "", "localhost", "127.0.0.1", "0.0.0.0":
		return true
	default:
		return false
	}
}

func parseInt64(input string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(input), 10, 64)
	return v
}

func formatDateTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04:05")
}

func formatDateTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatDateTime(*t)
}

func formatDate(t time.Time) string {
	return t.Local().Format("2006-01-02")
}

func statusClass(status string) string {
	switch status {
	case models.PaymentStatusSettled:
		return "bg-green-100 text-green-700"
	case models.PaymentStatusAwaitingTransfer:
		return "bg-amber-100 text-amber-700"
	case models.PaymentStatusFailed:
		return "bg-red-100 text-red-700"
	case models.PaymentStatusPending:
		return "bg-slate-100 text-slate-700"
	default:
		return "bg-zinc-100 text-zinc-700"
	}
}

func statusHeadline(status string) string {
	switch status {
	case models.PaymentStatusPending:
		return "Awaiting confirmation"
	case models.PaymentStatusAwaitingTransfer:
		return "Transfer in progress"
	case models.PaymentStatusSettled:
		return "Payment settled"
	case models.PaymentStatusFailed:
		return "Transfer failed"
	case models.PaymentStatusCancelled:
		return "Request cancelled"
	case models.PaymentStatusExpired:
		return "Request expired"
	default:
		return status
	}
}

func (a *App) Health(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (a *App) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.Store.DB().PingContext(ctx); err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "reason": "db_unreachable"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func respondJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
