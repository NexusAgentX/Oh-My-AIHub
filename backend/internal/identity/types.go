package identity

import (
	"context"
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrNotFound           = errors.New("not found")
	ErrConflict           = errors.New("conflict")
	ErrForbidden          = errors.New("forbidden")
	ErrInvalidInput       = errors.New("invalid input")
	// ErrLastAdministrator protects the instance from losing its last active administrator.
	ErrLastAdministrator = errors.New("last active administrator")
	// ErrSelfModification rejects administrators disabling, demoting or resetting themselves.
	ErrSelfModification = errors.New("cannot modify own administrative state")
)

type Account struct {
	ID                 string
	Username           string
	DisplayName        string
	IsAdmin            bool
	Status             Status
	MustChangePassword bool
	PasswordVersion    int64
	CreditLimit        money.Amount
	CreatedAt          time.Time
	UpdatedAt          time.Time
	PasswordChangedAt  *time.Time
}

// AdminAccount is the administrator view of an account with its ledger balance.
type AdminAccount struct {
	Account
	Balance money.Amount
}

type AccountWithPassword struct {
	Account
	PasswordHash string
}

type Session struct {
	TokenHash       []byte
	AccountID       string
	PasswordVersion int64
	ExpiresAt       time.Time
}

type NewAccount struct {
	ActorID            string
	Username           string
	DisplayName        string
	PasswordHash       string
	IsAdmin            bool
	MustChangePassword bool
	// CreditLimit nil means the platform default (settings.default_credit_limit_nano).
	CreditLimit *money.Amount
}

// AccountUpdate changes the given fields; nil fields stay unchanged.
type AccountUpdate struct {
	DisplayName *string
	Status      *Status
	CreditLimit *money.Amount
	IsAdmin     *bool
}

type AccountFilter struct {
	Query       string
	Status      Status
	AfterCursor string // username of the last row of the previous page
	Limit       int
}

type Store interface {
	FindAccountByUsername(context.Context, string) (AccountWithPassword, error)
	FindAccountByID(context.Context, string) (AccountWithPassword, error)
	FindAccountBySession(context.Context, []byte, time.Time) (Account, error)
	CreateSession(context.Context, Session) error
	DeleteSession(context.Context, []byte) error
	ReplacePasswordAndSessions(ctx context.Context, accountID string, expectedPasswordVersion int64, passwordHash string, session Session, changedAt time.Time) error
	// CreateAccount creates the account, its user ledger account and an audit row in one transaction.
	CreateAccount(context.Context, NewAccount) (AdminAccount, error)
	// CreateBootstrapAdmin creates the first administrator when none exists.
	CreateBootstrapAdmin(context.Context, NewAccount) (Account, error)
	HasAdministrator(context.Context) (bool, error)
	ListAccounts(context.Context, AccountFilter) ([]AdminAccount, error)
	GetAccount(context.Context, string) (AdminAccount, error)
	// UpdateAccount applies the update with audit; disabling deletes all sessions of the account.
	UpdateAccount(ctx context.Context, actorID, accountID string, update AccountUpdate) (AdminAccount, error)
	// ResetPassword sets a new initial password, forces a change at next login and deletes all sessions.
	ResetPassword(ctx context.Context, actorID, accountID, passwordHash string, changedAt time.Time) (AdminAccount, error)
}

type LoginResult struct {
	Account      Account
	SessionToken string
}

type CreatedAccount struct {
	Account         AdminAccount
	InitialPassword string
}
