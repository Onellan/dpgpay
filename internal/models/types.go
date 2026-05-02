package models

import "time"

const (
	WalletTypeOperating = "OPERATING"
	WalletTypeEscrow    = "ESCROW"
	WalletTypeFee       = "FEE"
)

const (
	DirectionDebit  = "DR"
	DirectionCredit = "CR"
)

const (
	EntryTypePaymentIn  = "PAYMENT_IN"
	EntryTypePaymentOut = "PAYMENT_OUT"
	EntryTypeFee        = "FEE"
	EntryTypeSettlement = "SETTLEMENT"
	EntryTypeReversal   = "REVERSAL"
	EntryTypeSimulation = "SIMULATION"
)

const (
	PaymentStatusPending          = "PENDING"
	PaymentStatusAwaitingTransfer = "AWAITING_TRANSFER"
	PaymentStatusSettled          = "SETTLED"
	PaymentStatusFailed           = "FAILED"
	PaymentStatusCancelled        = "CANCELLED"
	PaymentStatusExpired          = "EXPIRED"
)

type Wallet struct {
	ID           int64
	Type         string
	Currency     string
	BalanceCents int64
	PendingCents int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type LedgerEntry struct {
	ID          int64
	WalletID    int64
	Direction   string
	AmountCents int64
	EntryType   string
	ReferenceID string
	Metadata    string
	CreatedAt   time.Time
}

type PaymentRequest struct {
	ID             string
	Reference      string
	IdempotencyKey string
	PayerName      string
	PayerEmail     string
	AmountCents    int64
	Currency       string
	Description    string
	DueDate        time.Time
	Status         string
	RetryCount     int
	BankName       string
	BankReference  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ProcessedAt    *time.Time
}

type Settlement struct {
	ID          int64
	AmountCents int64
	EntryCount  int64
	Status      string
	TriggeredAt time.Time
	CompletedAt *time.Time
}

type AuditLog struct {
	ID          int64
	EventType   string
	Actor       string
	ReferenceID string
	Detail      string
	CreatedAt   time.Time
}

type DashboardStats struct {
	TotalBalanceCents   int64
	PendingBalanceCents int64
	SettledTodayCount   int64
	FailedTodayCount    int64
}

type ReconciliationLine struct {
	Status      string
	Count       int64
	AmountCents int64
}

type ReconciliationReport struct {
	GeneratedAt            time.Time
	Lines                  []ReconciliationLine
	LedgerBalanceInvariant bool
	NetWalletBalanceCents  int64
	EscrowPendingCents     int64
	LedgerDebitCents       int64
	LedgerCreditCents      int64
}

type WebhookOutboxItem struct {
	ID            int64
	EventType     string
	ReferenceID   string
	Payload       string
	Status        string
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
	SentAt        *time.Time
}

type Operator struct {
	ID           int64
	Username     string
	PasswordHash string
	Email        string
	Role         string
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AdminSettings struct {
	BusinessName               string
	BusinessLogoURL            string
	BusinessContactEmail       string
	BusinessContactPhone       string
	SMTPHost                   string
	SMTPPort                   string
	SMTPUser                   string
	SMTPPass                   string
	SMTPFrom                   string
	FeeFlatCents               string
	FeePercentBps              string
	FeePerTransactionCents     string
	Currency                   string
	Locale                     string
	Timezone                   string
	PaymentDescriptionTemplate string
	PaymentDueDaysDefault      string
	NotifyAdminOnConfirm       bool
	NotifyAdminOnSettle        bool
	NotifyPayerOnSettle        bool
	NotifyPayerOnFail          bool
	EFTAccountName             string
	EFTBankName                string
	EFTAccountNumber           string
	EFTBranchCode              string
}
