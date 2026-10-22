// Package contribution owns private, revisioned market proposals. Publication
// and report checks share one transaction; ordinary members never write live
// suppliers, groups, offers or governance directly.
package contribution

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

var (
	ErrInvalid         = errors.New("invalid market contribution")
	ErrNotFound        = errors.New("contribution not found")
	ErrConflict        = errors.New("contribution revision or published base changed")
	ErrPending         = errors.New("another pending contribution targets the same entity")
	ErrPublication     = errors.New("publication committed; runtime refresh pending")
	ErrCommitUncertain = errors.New("publication commit outcome not yet confirmed")
)

type Actor struct {
	ID    string
	Admin bool
}
type Draft struct {
	ID            string              `json:"id"`
	OwnerID       string              `json:"owner_id"`
	Title         string              `json:"title"`
	Kind          string              `json:"kind"`
	TargetID      string              `json:"target_id"`
	State         string              `json:"state"`
	Revision      int64               `json:"revision"`
	Changes       market.Changes      `json:"changes"`
	Base          market.Changes      `json:"base"`
	PriceBlanks   map[string][]string `json:"price_blanks"`
	PendingPrices []string            `json:"pending_prices"`
	ReviewReason  string              `json:"review_reason"`
	ReviewedBy    *string             `json:"reviewed_by"`
	SubmittedAt   *time.Time          `json:"submitted_at"`
	PublishedAt   *time.Time          `json:"published_at"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
}
type Cursor struct {
	At time.Time
	ID string
}
type Summary struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Kind         string    `json:"kind"`
	TargetID     string    `json:"target_id"`
	State        string    `json:"state"`
	Revision     int64     `json:"revision"`
	ReviewReason string    `json:"review_reason"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UnitOfWork owns the locked draft and the same database transaction used to
// check bases, publish market.Apply, save review state and append audit.
type UnitOfWork interface {
	Directory(context.Context) (market.Directory, error)
	Publish(context.Context, string, market.Changes) error
	Audit(context.Context, string, string, map[string]any) error
}
type Store interface {
	Create(context.Context, Draft) error
	Get(context.Context, string) (Draft, error)
	Pending(context.Context, string, string) (string, error)
	List(context.Context, Actor, bool, *Cursor, int) ([]Summary, error)
	Edit(context.Context, string, func(*Draft, UnitOfWork) error) (Draft, error)
}

// Runtime publication is serialized by Service. Block precedes database
// commit; Restore only follows rollback. Refresh atomically installs the new
// runtime state or leaves it fail-closed for a visible retry.
type Publisher interface {
	Block()
	Restore()
	Refresh(context.Context) error
}
type AddressPolicy interface {
	ValidateBaseURL(context.Context, string) (string, error)
}
type Service struct {
	publishMu sync.Mutex
	Store     Store
	Live      market.Store
	Publisher Publisher
	Outbound  AddressPolicy
}
