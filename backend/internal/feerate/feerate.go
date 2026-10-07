// Package feerate manages the global platform fee rate applied to new API
// call snapshots. Every change appends a new immutable version; existing
// call snapshots keep the version they captured.
package feerate

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

const (
	// MaxRate is 100%, matching ledger.FixedPointScale in pricing formulas.
	MaxRate money.Amount = money.Amount(money.Scale)

	MaxReasonRunes   = 256
	DefaultListLimit = 20
	MaxListLimit     = 100
)

var (
	ErrInvalidInput = errors.New("invalid fee rate")
	// ErrConflict means the expected version is no longer the current one.
	ErrConflict = errors.New("fee rate version conflict")
	// ErrUnchanged means the requested rate equals the current rate.
	ErrUnchanged = errors.New("fee rate unchanged")
)

// Version is one immutable fee rate version. CreatedBy and Reason are empty
// for the migration-seeded default.
type Version struct {
	Version           int64
	Rate              money.Amount
	CreatedByID       string
	CreatedByUsername string
	Reason            string
	CreatedAt         time.Time
}

type Store interface {
	// ListFeeRates returns the newest versions first; the first item is current.
	ListFeeRates(ctx context.Context, limit int) ([]Version, error)
	// AppendFeeRate inserts a new version only when expectedVersion is still
	// the latest one, and records an audit event in the same transaction.
	AppendFeeRate(ctx context.Context, actorID string, expectedVersion int64, rate money.Amount, reason string) (Version, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// List returns the current version followed by older versions.
func (s *Service) List(ctx context.Context, actor identity.Account, limit int) ([]Version, error) {
	if !actor.IsAdmin {
		return nil, identity.ErrForbidden
	}
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		return nil, ErrInvalidInput
	}
	versions, err := s.store.ListFeeRates(ctx, limit)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, errors.New("fee rate store has no versions")
	}
	return versions, nil
}

func (s *Service) Set(ctx context.Context, actor identity.Account, expectedVersion int64, rate money.Amount, reason string) (Version, error) {
	if !actor.IsAdmin {
		return Version{}, identity.ErrForbidden
	}
	reason = strings.TrimSpace(reason)
	if err := Validate(expectedVersion, rate, reason); err != nil {
		return Version{}, err
	}
	return s.store.AppendFeeRate(ctx, actor.ID, expectedVersion, rate, reason)
}

func Validate(expectedVersion int64, rate money.Amount, reason string) error {
	if expectedVersion <= 0 || rate < 0 || rate > MaxRate {
		return ErrInvalidInput
	}
	if reason == "" || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > MaxReasonRunes {
		return ErrInvalidInput
	}
	return nil
}
