package channel

import (
	"context"
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type Status string

const (
	StatusDraft     Status = "draft"
	StatusPublished Status = "published"
	StatusPaused    Status = "paused"
	StatusDeleted   Status = "deleted"
)

type OfferStatus string

const (
	OfferActive   OfferStatus = "active"
	OfferDisabled OfferStatus = "disabled"
	OfferDeleted  OfferStatus = "deleted"
)

type Protocol string

const (
	ProtocolOpenAIChat     Protocol = "openai_chat_completions"
	ProtocolOpenAIResponse Protocol = "openai_responses"
	ProtocolAnthropic      Protocol = "anthropic_messages"
	ProtocolGemini         Protocol = "google_gemini_generate_content"
)

type ValidationStatus string

const (
	ValidationInProgress ValidationStatus = "in_progress"
	ValidationPassed     ValidationStatus = "passed"
	ValidationFailed     ValidationStatus = "failed"
)

type ErrorCategory string

const (
	ErrorAuth          ErrorCategory = "auth_failure"
	ErrorUpstream      ErrorCategory = "upstream_error"
	ErrorTransport     ErrorCategory = "transport_error"
	ErrorTimeout       ErrorCategory = "timeout"
	ErrorTooLarge      ErrorCategory = "response_too_large"
	ErrorInvalid       ErrorCategory = "invalid_response"
	ErrorConfiguration ErrorCategory = "configuration_error"
)

var (
	ErrNotFound     = errors.New("channel not found")
	ErrConflict     = errors.New("channel version conflict")
	ErrInvalidInput = errors.New("invalid channel input")
	ErrForbidden    = errors.New("channel access forbidden")
	ErrUnavailable  = errors.New("channel offer unavailable")
)

type EncryptedCredential struct {
	Version    int64
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
}

type ValidationAttempt struct {
	ID                string
	OfferID           string
	ValidationVersion int64
	AttemptSeq        int64
	ActorAccountID    string
	Status            ValidationStatus
	ErrorCategory     ErrorCategory
	HTTPStatus        int
	RawError          string
	RawErrorTruncated bool
	Duration          time.Duration
	StartedAt         time.Time
	CompletedAt       *time.Time
}

type Offer struct {
	ID                string
	ChannelID         string
	ModelID           string
	ModelName         string
	ModelProvider     string
	Protocol          Protocol
	UpstreamModelID   string
	Multiplier        money.Amount
	Status            OfferStatus
	ValidationVersion int64
	Version           int64
	ModelStatus       catalog.Status
	InputPrice        money.Amount
	OutputPrice       money.Amount
	CacheWritePrice   money.Amount
	CacheReadPrice    money.Amount
	LatestValidation  *ValidationAttempt
	Eligible          bool
	IneligibleReason  string
	CallSuccessRate   *string
	TTFTMilliseconds  *int64
	TokensPerSecond   *string
	CallCount         *int64
	ProviderIncome    *money.Amount
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Channel struct {
	ID                      string
	OwnerAccountID          string
	OwnerDisplayName        string
	OwnerStatus             identity.Status
	OwnerMustChangePassword bool
	DisplayName             string
	NormalizedBaseURL       string
	CredentialConfigured    bool
	CredentialVersion       int64
	CredentialUpdatedAt     *time.Time
	Status                  Status
	Version                 int64
	Offers                  []Offer
	AverageRating           *string
	RatingCount             int64
	CurrentUserRating       *int
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type OfferInput struct {
	ModelID         string
	Protocol        Protocol
	UpstreamModelID string
	Multiplier      money.Amount
}

type CreateCommand struct {
	ChannelID         string
	OwnerAccountID    string
	DisplayName       string
	NormalizedBaseURL string
	Credential        EncryptedCredential
	Offers            []NewOffer
}

type NewOffer struct {
	ID string
	OfferInput
}

type AddOfferCommand struct {
	ActorAccountID         string
	ChannelID              string
	ExpectedChannelVersion int64
	Offer                  NewOffer
}

type UpdateCommand struct {
	ActorAccountID    string
	ChannelID         string
	ExpectedVersion   int64
	DisplayName       string
	NormalizedBaseURL string
	BaseURLChanged    bool
	Credential        *EncryptedCredential
}

type StatusCommand struct {
	ActorAccountID  string
	ChannelID       string
	ExpectedVersion int64
	Status          Status
	Reason          string
	Administrator   bool
}

type OfferUpdateCommand struct {
	ActorAccountID    string
	OfferID           string
	ExpectedVersion   int64
	UpstreamModelID   string
	Multiplier        money.Amount
	UpstreamIDChanged bool
}

type OfferStatusCommand struct {
	ActorAccountID  string
	OfferID         string
	ExpectedVersion int64
	Status          OfferStatus
}

type MarketQuery struct {
	ModelID    string
	Protocol   Protocol
	OwnerQuery string
	Sort       string
	Cursor     string
	Limit      int
}

type MarketOffer struct {
	OfferID            string
	ChannelID          string
	ChannelDisplayName string
	OwnerAccountID     string
	OwnerDisplayName   string
	ModelID            string
	ModelName          string
	ModelProvider      string
	Protocol           Protocol
	Multiplier         money.Amount
	InputPrice         money.Amount
	OutputPrice        money.Amount
	CacheWritePrice    money.Amount
	CacheReadPrice     money.Amount
	ValidationStatus   ValidationStatus
	LastTestedAt       *time.Time
	AverageRating      *string
	RatingCount        int64
	CallSuccessRate    *string
	TTFTMilliseconds   *int64
	TokensPerSecond    *string
	CallCount          *int64
}

// PoolOfferStatus is the safe, non-sensitive projection that #20 can persist in
// a user's model pool even when an offer later becomes ineligible.
type PoolOfferStatus struct {
	MarketOffer
	Eligible         bool
	IneligibleReason string
}

// RoutingLease is server-only material for one outbound decision. It must never
// be JSON-encoded, logged, or exposed through a generic administration DTO.
type RoutingLease struct {
	OfferID           string
	ChannelID         string
	ProviderAccountID string
	ModelID           string
	Protocol          Protocol
	Multiplier        money.Amount
	ValidationVersion int64
	CredentialVersion int64
	ContextWindow     int64
	InputPrice        money.Amount
	OutputPrice       money.Amount
	CacheWritePrice   money.Amount
	CacheReadPrice    money.Amount
	NormalizedBaseURL string `json:"-"`
	UpstreamModelID   string `json:"-"`
	// Credential is intentionally excluded from every JSON representation.
	// It exists only for the internal outbound request that consumes this lease.
	Credential string `json:"-"`
}

type ValidationTarget struct {
	Attempt           ValidationAttempt
	ChannelID         string
	OwnerAccountID    string
	NormalizedBaseURL string
	Protocol          Protocol
	UpstreamModelID   string
	Credential        EncryptedCredential
}

type ReencryptTarget struct {
	ChannelID  string
	Credential EncryptedCredential
}

type RoutingTarget struct {
	Lease      RoutingLease
	Credential EncryptedCredential
}

type RoutingStore interface {
	ResolveRoutingTargets(context.Context, []string) ([]PoolOfferStatus, []RoutingTarget, error)
}

type Store interface {
	CreateChannel(context.Context, CreateCommand) (Channel, error)
	ListOwnerChannels(context.Context, string) ([]Channel, error)
	GetOwnerChannel(context.Context, string, string) (Channel, error)
	UpdateChannel(context.Context, UpdateCommand) (Channel, error)
	SetChannelStatus(context.Context, StatusCommand) (Channel, error)
	RevokeCredential(context.Context, string, string, int64) (Channel, error)
	AddOffer(context.Context, AddOfferCommand) (Offer, error)
	UpdateOffer(context.Context, OfferUpdateCommand) (Offer, error)
	SetOfferStatus(context.Context, OfferStatusCommand) (Offer, error)
	StartValidation(context.Context, identity.Account, string) (ValidationTarget, error)
	CompleteValidation(context.Context, ValidationAttempt) error
	ExpireValidationAttempts(context.Context, time.Time) (int64, error)
	ListValidationAttempts(context.Context, identity.Account, string, int) ([]ValidationAttempt, error)
	ListMarketOffers(context.Context, string, MarketQuery) ([]MarketOffer, string, error)
	GetMarketChannel(context.Context, string, string) (Channel, error)
	UpsertRating(context.Context, string, string, int) (Channel, error)
	ListAdminChannels(context.Context) ([]Channel, error)
	GetAdminChannel(context.Context, string) (Channel, error)
	CredentialInventory(context.Context) ([]ReencryptTarget, error)
	CredentialTargetsForReencrypt(context.Context, string, int) ([]ReencryptTarget, error)
	StoreReencryptedCredential(context.Context, ReencryptTarget, EncryptedCredential, string) error
	RoutingStore
}
