// Package audit reads the append-only audit log. Writes happen inside each
// domain's own transaction through auditpg.Record.
package audit

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidInput = errors.New("invalid audit query")

// Actions recorded by Feature A; later features add their own.
const (
	ActionAccountCreated        = "account.created"
	ActionAccountUpdated        = "account.updated"
	ActionAccountPasswordReset  = "account.password_reset"
	ActionAccountPasswordChange = "account.password_changed"
	ActionLedgerAdjust          = "ledger.adjust"
	ActionLedgerWriteOff        = "ledger.write_off"
	ActionModelCreated          = "model.created"
	ActionModelUpdated          = "model.updated"
	ActionSettingsUpdated       = "settings.updated"
	ActionInstanceInitialized   = "instance.initialized"
	ActionChannelSuspended      = "channel.suspended"
	ActionChannelUnsuspended    = "channel.unsuspended"
	ActionAPIKeyReveal          = "api_key.reveal"
)

type Actor struct {
	ID          string
	Username    string
	DisplayName string
}

type Entry struct {
	ID         int64
	Actor      *Actor
	Action     string
	TargetType string
	TargetID   string
	Reason     string
	Detail     []byte // JSON object
	CreatedAt  time.Time
}

type Filter struct {
	Action     string
	TargetType string
	TargetID   string
	ActorID    string
	BeforeID   int64
	Limit      int
}

type Store interface {
	ListAudit(ctx context.Context, filter Filter) ([]Entry, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) List(ctx context.Context, filter Filter) ([]Entry, error) {
	if filter.Limit < 1 || filter.Limit > 100 || filter.BeforeID < 0 {
		return nil, ErrInvalidInput
	}
	return s.store.ListAudit(ctx, filter)
}
