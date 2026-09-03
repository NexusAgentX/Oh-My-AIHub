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
)

type Account struct {
	ID                 string
	Username           string
	DisplayName        string
	IsAdmin            bool
	Status             Status
	MustChangePassword bool
	PasswordVersion    int64
	Version            int64
	CreditLimit        money.Amount
	CreditFrozen       bool
	PostedBalance      money.Amount
	AssetReserved      money.Amount
	SpendAuthorized    money.Amount
	CreatedAt          time.Time
	UpdatedAt          time.Time
	PasswordChangedAt  *time.Time
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
	Status             Status
	MustChangePassword bool
	CreditLimit        money.Amount
}

type AccountUpdate struct {
	ExpectedVersion int64
	Status          *Status
	CreditLimit     *money.Amount
	CreditFrozen    *bool
	IsAdmin         *bool
}

type Store interface {
	FindAccountByUsername(context.Context, string) (AccountWithPassword, error)
	FindAccountByID(context.Context, string) (AccountWithPassword, error)
	FindAccountBySession(context.Context, []byte, time.Time) (Account, error)
	CreateSession(context.Context, Session) error
	DeleteSession(context.Context, []byte) error
	ReplacePasswordAndSessions(context.Context, string, int64, string, Session, time.Time) error
	CreateAccount(context.Context, NewAccount) (Account, error)
	CreateBootstrapAdmin(context.Context, NewAccount) (Account, error)
	HasAdministrator(context.Context) (bool, error)
	ListAccounts(context.Context, string) ([]Account, error)
	UpdateAccount(context.Context, string, string, AccountUpdate) (Account, error)
}

type LoginResult struct {
	Account      Account
	SessionToken string
}

type CreatedAccount struct {
	Account         Account
	InitialPassword string
}
