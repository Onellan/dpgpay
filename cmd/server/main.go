package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"dpg-pay/internal/handlers"
	"dpg-pay/internal/ledger"
	"dpg-pay/internal/middleware"
	"dpg-pay/internal/models"
	"dpg-pay/internal/notify"
	"dpg-pay/internal/transfer"
	"dpg-pay/internal/wallet"
	"dpg-pay/internal/webhook"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/csrf"
	"golang.org/x/time/rate"
	_ "modernc.org/sqlite"
)

type config struct {
	Port              int
	DBPath            string
	AdminUsername     string
	AdminPasswordHash string
	AdminEmail        string
	SMTPHost          string
	SMTPPort          string
	SMTPUser          string
	SMTPPass          string
	SMTPFrom          string
	BaseURL           string
	SimulationMode    bool
	CSRFAuthKey       string
	WebhookEndpoint   string
	WebhookSecret     string
	EFTAccountName    string
	EFTBankName       string
	EFTAccountNumber  string
	EFTBranchCode     string
}

func main() {
	cfg := loadConfig()
	if cfg.AdminUsername == "" || cfg.AdminPasswordHash == "" {
		log.Fatal("ADMIN_USERNAME and ADMIN_PASSWORD_BCRYPT are required")
	}

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		log.Fatalf("failed to create db directory: %v", err)
	}

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		log.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		log.Printf("warning: failed to set WAL mode: %v", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
		log.Printf("warning: failed to set busy timeout: %v", err)
	}

	ctx := context.Background()
	if err := models.RunMigrations(ctx, db, "migrations"); err != nil {
		log.Fatalf("migrations failed: %v", err)
	}

	store := models.NewStore(db)
	if err := store.EnsureDefaultWallets(ctx, "ZAR"); err != nil {
		log.Fatalf("wallet bootstrap failed: %v", err)
	}

	ledgerSvc := ledger.NewService(store)
	walletSvc := wallet.NewService(store)
	notifier := notify.NewService(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)

	var rail transfer.Rail = transfer.SimulationRail{}
	if !cfg.SimulationMode {
		rail = transfer.SimulationRail{}
	}
	transferEngine := transfer.NewEngine(store, ledgerSvc, notifier, rail, cfg.AdminEmail)
	webhookDispatcher := webhook.NewDispatcher(store, cfg.WebhookEndpoint, cfg.WebhookSecret)

	cookieSecure := strings.HasPrefix(strings.ToLower(cfg.BaseURL), "https://")
	app, err := handlers.NewApp(
		store,
		ledgerSvc,
		walletSvc,
		transferEngine,
		notifier,
		cfg.AdminUsername,
		cfg.AdminPasswordHash,
		cfg.AdminEmail,
		cfg.BaseURL,
		cfg.EFTAccountName,
		cfg.EFTBankName,
		cfg.EFTAccountNumber,
		cfg.EFTBranchCode,
		cookieSecure,
	)
	if err != nil {
		log.Fatalf("template setup failed: %v", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestLogger)
	r.Use(middleware.SecurityHeaders)
	r.Use(csrf.Protect(csrfKey(cfg), csrf.Secure(cookieSecure), csrf.Path("/"), csrf.HttpOnly(true), csrf.SameSite(csrf.SameSiteLaxMode)))
	r.Get("/health", app.Health)
	r.Get("/ready", app.Ready)

	payLimiter := middleware.NewRateLimiter(rate.Limit(30), 30)
	loginLimiter := middleware.NewRateLimiter(rate.Every(time.Minute/5), 5)

	r.Get("/portal", app.HomePage)
	r.Post("/portal/open", app.PortalOpenPayment)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	r.With(loginLimiter.Middleware).Get("/admin/login", app.AdminLoginPage)
	r.With(loginLimiter.Middleware).Post("/admin/login", app.AdminLoginSubmit)

	r.Route("/pay", func(pr chi.Router) {
		pr.Use(payLimiter.Middleware)
		pr.Get("/{id}", app.PayPage)
		pr.Post("/{id}/confirm", app.ConfirmPayment)
		pr.Get("/{id}/status", app.PayStatusPage)
		pr.Get("/{id}/status/fragment", app.PayStatusFragment)
		pr.Get("/{id}/success", app.PaySuccessPage)
		pr.Get("/{id}/failed", app.PayFailedPage)
	})

	r.Route("/admin", func(ar chi.Router) {
		ar.Use(middleware.AuthRequired(store))
		ar.Get("/", app.AdminHub)
		ar.Get("/settings", app.AdminSettingsPage)
		ar.Post("/settings", app.AdminSaveSettings)
		ar.Post("/operators", app.AdminCreateOperator)
		ar.Post("/operators/{operatorID}/status", app.AdminToggleOperatorStatus)
		ar.Post("/operators/{operatorID}/password", app.AdminResetOperatorPassword)
		ar.Get("/dashboard", app.AdminDashboard)
		ar.Get("/reconciliation", app.AdminReconciliationPage)
		ar.Get("/webhooks", app.AdminWebhooksPage)
		ar.Get("/feed", app.AdminLiveFeed)
		ar.Post("/payment-requests", app.AdminCreatePaymentRequest)
		ar.Get("/wallet/{walletType}", app.AdminWalletDetail)
		ar.Get("/settlements", app.AdminSettlementsPage)
		ar.Post("/settlements/run", app.AdminTriggerSettlement)
		ar.Get("/audit", app.AdminAuditPage)
		ar.Post("/logout", app.AdminLogout)
	})

	r.With(middleware.AuthRequired(store)).Get("/", app.AdminDashboard)

	routeCtx, cancelRoutes := context.WithCancel(context.Background())
	defer cancelRoutes()
	go transferEngine.Start(routeCtx)
	go webhookDispatcher.Start(routeCtx)
	go cleanupSessions(routeCtx, store)
	go expireOverduePayments(routeCtx, store)
	go validateLedgerInvariant(routeCtx, store)

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("DPG Pay listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Printf("shutdown signal received")
	cancelRoutes()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

func cleanupSessions(ctx context.Context, store *models.Store) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = store.CleanupExpiredSessions(ctx)
		}
	}
}

func loadConfig() config {
	return config{
		Port:              envInt("PORT", 18231),
		DBPath:            env("DB_PATH", "./data/dpgpay.db"),
		AdminUsername:     os.Getenv("ADMIN_USERNAME"),
		AdminPasswordHash: os.Getenv("ADMIN_PASSWORD_BCRYPT"),
		AdminEmail:        env("ADMIN_EMAIL", env("SMTP_FROM", "")),
		SMTPHost:          os.Getenv("SMTP_HOST"),
		SMTPPort:          os.Getenv("SMTP_PORT"),
		SMTPUser:          os.Getenv("SMTP_USER"),
		SMTPPass:          os.Getenv("SMTP_PASS"),
		SMTPFrom:          os.Getenv("SMTP_FROM"),
		BaseURL:           env("BASE_URL", "http://localhost:18231"),
		SimulationMode:    envBool("EFT_SIMULATION_MODE", true),
		CSRFAuthKey:       os.Getenv("CSRF_AUTH_KEY"),
		WebhookEndpoint:   strings.TrimSpace(os.Getenv("WEBHOOK_ENDPOINT_URL")),
		WebhookSecret:     os.Getenv("WEBHOOK_SIGNING_SECRET"),
		EFTAccountName:    strings.TrimSpace(os.Getenv("EFT_ACCOUNT_NAME")),
		EFTBankName:       strings.TrimSpace(os.Getenv("EFT_BANK_NAME")),
		EFTAccountNumber:  strings.TrimSpace(os.Getenv("EFT_ACCOUNT_NUMBER")),
		EFTBranchCode:     strings.TrimSpace(os.Getenv("EFT_BRANCH_CODE")),
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.Atoi(value)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if value := strings.TrimSpace(strings.ToLower(os.Getenv(key))); value != "" {
		return value == "1" || value == "true" || value == "yes" || value == "on"
	}
	return fallback
}

func csrfKey(cfg config) []byte {
	if cfg.CSRFAuthKey != "" {
		key := []byte(cfg.CSRFAuthKey)
		if len(key) >= 32 {
			return key[:32]
		}
		out := make([]byte, 32)
		copy(out, key)
		return out
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err == nil {
		log.Printf("warning: CSRF_AUTH_KEY is not set; using ephemeral key for this process")
		return b
	}
	h := sha256.Sum256([]byte(cfg.AdminPasswordHash + cfg.AdminUsername + cfg.BaseURL))
	decoded := make([]byte, hex.EncodedLen(len(h)))
	hex.Encode(decoded, h[:])
	return decoded[:32]
}

func expireOverduePayments(ctx context.Context, store *models.Store) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids, err := store.ExpireOverduePendingPayments(ctx, 200)
			if err != nil {
				log.Printf("payment expiry sweep failed: %v", err)
				continue
			}
			for _, id := range ids {
				_ = store.CreateAuditLog(ctx, "PAYMENT_EXPIRED", "system", id, `{"source":"expiry_sweep"}`)
				_ = store.EnqueueWebhook(ctx, "payment_request.expired", id, map[string]any{
					"payment_id": id,
					"expired_at": time.Now().UTC().Format(time.RFC3339),
				})
			}
		}
	}
}

func validateLedgerInvariant(ctx context.Context, store *models.Store) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report, err := store.ReconciliationReport(ctx)
			if err != nil {
				log.Printf("ledger invariant check failed: %v", err)
				continue
			}
			if !report.LedgerBalanceInvariant {
				detail := fmt.Sprintf(`{"net_wallet_balance_cents":%d}`, report.NetWalletBalanceCents)
				_ = store.CreateAuditLog(ctx, "LEDGER_INVARIANT_BROKEN", "system", "", detail)
			}
		}
	}
}
