// Package ledger is the zero-sum points ledger (ADR-0005, ADR-0025).
//
// Every change of a balance is one balanced transaction: its entries sum to
// zero, which the database also enforces with a single deferred constraint
// trigger. There are no holds or freezes: a balance is one column updated in
// the same database transaction that posts the entries. Business rules about
// how far a balance may go (credit limit for API calls, positive balance for
// C2C listings) are checked by the caller inside its own transaction, with the
// Balance and CreditLimit helpers of the persistence package.
package ledger

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var (
	ErrInvalidInput   = errors.New("invalid ledger input")
	ErrUnbalanced     = errors.New("unbalanced ledger transaction")
	ErrAmountOverflow = errors.New("ledger amount overflow")
	ErrNotFound       = errors.New("ledger resource not found")
	// ErrConflict reports an idempotency key already used by a different
	// transaction type or related object.
	ErrConflict = errors.New("ledger idempotency conflict")
	// ErrNothingToWriteOff reports a write-off for a non-negative balance.
	ErrNothingToWriteOff = errors.New("balance is not negative")
)

type TransactionType string

const (
	TypeAPICall         TransactionType = "api_call"
	TypeC2CList         TransactionType = "c2c_list"
	TypeC2CRelease      TransactionType = "c2c_release"
	TypeC2CReturn       TransactionType = "c2c_return"
	TypeAdminAdjust     TransactionType = "admin_adjust"
	TypeBadDebtWriteOff TransactionType = "bad_debt_writeoff"
)

func (t TransactionType) Valid() bool {
	switch t {
	case TypeAPICall, TypeC2CList, TypeC2CRelease, TypeC2CReturn, TypeAdminAdjust, TypeBadDebtWriteOff:
		return true
	}
	return false
}

// SystemCode names one of the three platform-owned ledger accounts.
type SystemCode string

const (
	SystemPlatformRevenue SystemCode = "platform_revenue"
	SystemC2CEscrow       SystemCode = "c2c_escrow"
	SystemBadDebt         SystemCode = "bad_debt"
)

func (c SystemCode) Valid() bool {
	return c == SystemPlatformRevenue || c == SystemC2CEscrow || c == SystemBadDebt
}

// AccountRef names a ledger account: a user's (by identity account ID) or a
// system account. Exactly one field is set.
type AccountRef struct {
	UserID string
	System SystemCode
}

func User(accountID string) AccountRef  { return AccountRef{UserID: accountID} }
func System(code SystemCode) AccountRef { return AccountRef{System: code} }

func (r AccountRef) valid() bool {
	return (r.UserID != "") != (r.System != "") && (r.System == "" || r.System.Valid())
}

// Related links a transaction to the business object that caused it.
type Related struct {
	Type string // call, c2c_order, c2c_trade, account
	ID   string
}

// Line is one requested entry. Callers net their amounts: each account
// appears at most once per transaction.
type Line struct {
	Account AccountRef
	Amount  money.Amount
}

type Transaction struct {
	Type           TransactionType
	IdempotencyKey string
	Related        *Related
	ActorID        string
	Reason         string
	Entries        []Line
}

// Validate checks the shape of a transaction before it reaches the database:
// at least two non-zero entries on distinct accounts that sum to exactly zero.
func (t Transaction) Validate() error {
	if !t.Type.Valid() || strings.TrimSpace(t.IdempotencyKey) == "" || len(t.IdempotencyKey) > 200 || len(t.Entries) < 2 {
		return ErrInvalidInput
	}
	if t.Related != nil {
		switch t.Related.Type {
		case "call", "c2c_order", "c2c_trade", "account":
		default:
			return ErrInvalidInput
		}
		if t.Related.ID == "" {
			return ErrInvalidInput
		}
	}
	seen := make(map[AccountRef]bool, len(t.Entries))
	var sum int64
	for _, line := range t.Entries {
		if !line.Account.valid() || line.Amount == 0 || seen[line.Account] {
			return ErrInvalidInput
		}
		seen[line.Account] = true
		var overflow bool
		sum, overflow = addChecked(sum, line.Amount.Nano())
		if overflow {
			return ErrAmountOverflow
		}
	}
	if sum != 0 {
		return ErrUnbalanced
	}
	return nil
}

// AddBalance applies an entry to a balance, rejecting int64 overflow.
func AddBalance(balance, amount money.Amount) (money.Amount, error) {
	sum, overflow := addChecked(balance.Nano(), amount.Nano())
	if overflow {
		return 0, ErrAmountOverflow
	}
	return money.FromNano(sum), nil
}

func addChecked(a, b int64) (int64, bool) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, true
	}
	return a + b, false
}

type PostedEntry struct {
	LedgerAccountID string
	Account         AccountRef
	Amount          money.Amount
	BalanceAfter    money.Amount
}

// Posted is the stored transaction. Replayed is true when the idempotency key
// already existed and nothing was booked again.
type Posted struct {
	ID        string
	Type      TransactionType
	CreatedAt time.Time
	Replayed  bool
	Entries   []PostedEntry
}

// Points is a user's balance and credit limit. The balance may go down to
// -CreditLimit before API calls are refused.
type Points struct {
	Balance     money.Amount
	CreditLimit money.Amount
	UpdatedAt   time.Time
}

// Available is how much more the user may spend before reaching the credit
// limit (balance + credit limit); it is negative once overdrawn past it.
func (p Points) Available() money.Amount {
	sum, overflow := addChecked(p.Balance.Nano(), p.CreditLimit.Nano())
	if overflow {
		return money.FromNano(math.MaxInt64)
	}
	return money.FromNano(sum)
}

// EntryView is one ledger entry of a user, joined with its transaction and,
// for API calls, the API key that made the call.
type EntryView struct {
	ID            int64
	TransactionID string
	Type          TransactionType
	Reason        string
	RelatedType   string
	RelatedID     string
	Amount        money.Amount
	BalanceAfter  money.Amount
	APIKeyID      *string
	APIKeyName    *string
	CreatedAt     time.Time
}

type EntryFilter struct {
	AccountID string
	Type      TransactionType
	APIKeyID  string
	From      *time.Time
	To        *time.Time
	BeforeID  int64
	Limit     int
}

// Adjustment is an administrator's manual credit (+) or debit (-) of a user
// against the platform revenue account.
type Adjustment struct {
	ActorID        string
	AccountID      string
	Amount         money.Amount
	Reason         string
	IdempotencyKey string
}

// WriteOff moves a user's whole negative balance into the bad debt account.
type WriteOff struct {
	ActorID        string
	AccountID      string
	Reason         string
	IdempotencyKey string
}

type Store interface {
	Points(ctx context.Context, accountID string) (Points, error)
	ListEntries(ctx context.Context, filter EntryFilter) ([]EntryView, error)
	Adjust(ctx context.Context, adjustment Adjustment) (Posted, error)
	WriteOff(ctx context.Context, writeOff WriteOff) (Posted, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) Points(ctx context.Context, accountID string) (Points, error) {
	return s.store.Points(ctx, accountID)
}

func (s *Service) Entries(ctx context.Context, filter EntryFilter) ([]EntryView, error) {
	if filter.AccountID == "" || filter.Limit < 1 || filter.Limit > 100 || (filter.Type != "" && !filter.Type.Valid()) {
		return nil, ErrInvalidInput
	}
	return s.store.ListEntries(ctx, filter)
}

func (s *Service) Adjust(ctx context.Context, adjustment Adjustment) (Posted, error) {
	adjustment.Reason = strings.TrimSpace(adjustment.Reason)
	if adjustment.ActorID == "" || adjustment.AccountID == "" || adjustment.Amount == 0 || adjustment.Amount.Nano() == math.MinInt64 ||
		adjustment.Reason == "" || len([]rune(adjustment.Reason)) > 500 || adjustment.IdempotencyKey == "" {
		return Posted{}, ErrInvalidInput
	}
	return s.store.Adjust(ctx, adjustment)
}

func (s *Service) WriteOff(ctx context.Context, writeOff WriteOff) (Posted, error) {
	writeOff.Reason = strings.TrimSpace(writeOff.Reason)
	if writeOff.ActorID == "" || writeOff.AccountID == "" || writeOff.Reason == "" || len([]rune(writeOff.Reason)) > 500 || writeOff.IdempotencyKey == "" {
		return Posted{}, ErrInvalidInput
	}
	return s.store.WriteOff(ctx, writeOff)
}
