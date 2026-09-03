package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

const (
	MaxRequestBytes           = 32 << 20
	MaxNonStreamingBytes      = 64 << 20
	MaxUpstreamErrorBytes     = 1 << 20
	MaxSSEEventBytes          = 2 << 20
	MaxPrecommitBytes         = 2 << 20
	MaxStreamingBytes         = 128 << 20
	MaxTerminalFrames         = 3
	MaxTerminalBytes          = 6 << 20
	MaxSSECredentialStreams   = 2048
	MaxChatToolCallIndex      = 127
	MaxStoredRawErrorBytes    = 4096
	FormulaVersion            = "formula-v1"
	FormulaVersionV2          = "formula-v2"
	DefaultLeaseDuration      = 2 * time.Minute
	NonStreamingTotalTimeout  = 10 * time.Minute
	StreamingTotalTimeout     = 30 * time.Minute
	PrecommitTimeout          = 60 * time.Second
	StreamingIdleTimeout      = 90 * time.Second
	NonStreamingWriteTimeout  = 90 * time.Second
	ProtocolErrorWriteTimeout = 30 * time.Second
	PersistenceTimeout        = 5 * time.Second
)

var (
	ErrNotFound       = errors.New("gateway resource not found")
	ErrConflict       = errors.New("gateway version conflict")
	ErrInvalidInput   = errors.New("invalid gateway input")
	ErrForbidden      = errors.New("gateway access forbidden")
	ErrInvalidAPIKey  = errors.New("invalid platform api key")
	ErrRejected       = errors.New("gateway call rejected")
	ErrNoUsage        = errors.New("upstream response has no unambiguous usage")
	ErrResponseTooBig = errors.New("upstream response exceeds resource limit")
	ErrSnapshotRetry  = errors.New("gateway snapshot serialization retry")
)

type KeyStatus string

const (
	KeyActive   KeyStatus = "active"
	KeyDisabled KeyStatus = "disabled"
	KeyDeleted  KeyStatus = "deleted"
)

type APIKey struct {
	ID             string
	OwnerAccountID string
	DisplayName    string
	Prefix         string
	Generation     int64
	Status         KeyStatus
	Version        int64
	Pools          []ModelPool
	LastUsedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CreatedAPIKey struct {
	APIKey APIKey
	Secret string
}

type ModelPool struct {
	ID               string
	CanonicalModelID string
	ModelName        string
	Protocol         channel.Protocol
	Version          int64
	Members          []PoolMember
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type PoolMember struct {
	Priority                 int
	OfferID                  string
	ChannelID                string
	ChannelDisplayName       string
	OwnerDisplayName         string
	AddedValidationVersion   int64
	CurrentValidationVersion int64
	Eligible                 bool
	IneligibleReason         string
	InputPrice               money.Amount
	OutputPrice              money.Amount
	CacheWritePrice          money.Amount
	CacheReadPrice           money.Amount
	// ModelID, Multiplier and PriceTiers carry the benchmark facts behind the
	// precomputed display prices above; the API layer derives per-tier display
	// prices from them.
	ModelID         string
	Multiplier      money.Amount
	PriceTiers      []ledger.PriceTier
	CallSuccessRate *string
	TTFTMilliseconds *int64
	TokensPerSecond *string
}

type PoolInput struct {
	CanonicalModelID string
	Protocol         channel.Protocol
	OfferIDs         []string
}

type KeyConfigInput struct {
	DisplayName string
	Pools       []PoolInput
}

type AuthenticatedKey struct {
	ID             string
	OwnerAccountID string
	Generation     int64
	Hash           [32]byte
}

type CallStatus string

const (
	CallRejected        CallStatus = "rejected"
	CallInProgress      CallStatus = "in_progress"
	CallPendingDelivery CallStatus = "pending_delivery"
	CallSucceeded       CallStatus = "succeeded"
	CallFailed          CallStatus = "failed"
	CallIncomplete      CallStatus = "incomplete"
	CallCancelled       CallStatus = "cancelled"
)

type Call struct {
	ID                   string
	ConsumerAccountID    string
	APIKeyID             string
	KeyPrefix            string
	KeyGeneration        int64
	PoolID               string
	PoolVersion          int64
	CanonicalModelID     string
	Protocol             channel.Protocol
	Status               CallStatus
	DecisionCode         string
	CandidateCount       int
	UpstreamAttemptCount int
	HoldID               string
	Preauthorized        money.Amount
	ZeroHoldReason       string
	FeeRateVersion       int64
	FeeRateNano          int64
	LeaseGeneration      int64
	FinalOfferID         string
	FinalChannelName     string
	CompletionReason     string
	Usage                *ledger.UsageV1
	ProviderCharge       money.Amount
	PlatformFee          money.Amount
	SettledPriceTierSeq  int
	FinalHTTPStatus      int
	Attempts             []Attempt
	CreatedAt            time.Time
	CompletedAt          *time.Time
}

type Candidate struct {
	Priority        int
	Lease           channel.RoutingLease
	SelfChannel     bool
	NetDebitUpper   money.Amount
	LeaseGeneration int64
}

type CallPlan struct {
	Call       Call
	Candidates []Candidate
}

type BeginCallRequest struct {
	Authenticated    AuthenticatedKey
	Protocol         channel.Protocol
	CanonicalModelID string
	LeaseDuration    time.Duration
}

type LeaseResolver func(context.Context, channel.RoutingStore, []string) ([]channel.PoolOfferStatus, []channel.RoutingLease, error)

type AttemptStatus string

const (
	AttemptInProgress      AttemptStatus = "in_progress"
	AttemptPendingDelivery AttemptStatus = "pending_delivery"
	AttemptSucceeded       AttemptStatus = "succeeded"
	AttemptFailed          AttemptStatus = "failed"
	AttemptCancelled       AttemptStatus = "cancelled"
	AttemptIncomplete      AttemptStatus = "incomplete"
)

type Attempt struct {
	ID                  string
	CallID              string
	Sequence            int
	OfferID             string
	ChannelDisplayName  string
	ProviderAccountID   string
	LeaseGeneration     int64
	Status              AttemptStatus
	HTTPStatus          int
	ErrorCode           string
	RawError            string
	RawErrorTruncated   bool
	SemanticCommitted   bool
	TTFT                *time.Duration
	Duration            *time.Duration
	Usage               *ledger.UsageV1
	TokensPerSecondNano *int64
	StartedAt           time.Time
	CompletedAt         *time.Time
}

type AttemptResult struct {
	LeaseGeneration   int64
	Status            AttemptStatus
	HTTPStatus        int
	ErrorCode         string
	RawError          string
	SemanticCommitted bool
	MeasureTPS        bool
	TTFTObserved      bool
	TTFT              time.Duration
	Duration          time.Duration
	Usage             *ledger.UsageV1
}

type AttemptCommitObservation struct {
	LeaseGeneration int64
	TTFT            time.Duration
	Duration        time.Duration
	MeasureTPS      bool
}

type FinalizeOutcome struct {
	LeaseGeneration  int64
	Status           CallStatus
	CompletionReason string
	FinalOfferID     string
	HTTPStatus       int
	Usage            *ledger.UsageV1
	// SuccessAttemptID and SuccessAttempt make a successful upstream attempt,
	// its charge, the settlement fact, and the terminal call one database
	// transaction. Failure outcomes never carry these fields.
	SuccessAttemptID string
	SuccessAttempt   *AttemptResult
}

type Dashboard struct {
	ConsumerSpent               money.Amount
	ProviderIncome              money.Amount
	TodaySpent                  money.Amount
	TodaySucceededCalls         int64
	TodayExternalProviderIncome money.Amount
	ActiveKeyCount              int64
	PoolCount                   int64
	HealthyOfferCount           int64
	UnhealthyOfferCount         int64
	PendingItems                int64
	RecentCalls                 []Call
}

type Store interface {
	CreateAPIKey(context.Context, string, string, string, [32]byte, []PoolInput) (APIKey, error)
	ListAPIKeys(context.Context, string) ([]APIKey, error)
	GetAPIKey(context.Context, string, string) (APIKey, error)
	UpdateAPIKey(context.Context, string, string, int64, KeyConfigInput) (APIKey, error)
	RotateAPIKey(context.Context, string, string, int64, string, [32]byte) (APIKey, error)
	SetAPIKeyStatus(context.Context, string, string, int64, KeyStatus) (APIKey, error)
	AuthenticateAPIKey(context.Context, [32]byte) (AuthenticatedKey, error)
	BeginCall(context.Context, BeginCallRequest, LeaseResolver) (CallPlan, error)
	StartAttempt(context.Context, string, Candidate) (Attempt, error)
	CompleteAttempt(context.Context, string, AttemptResult) (Attempt, error)
	MarkAttemptCommitted(context.Context, string, AttemptCommitObservation) error
	HeartbeatCall(context.Context, string, int64) error
	FinalizeCall(context.Context, string, FinalizeOutcome) (Call, error)
	ConfirmCallDelivery(context.Context, string, int64) (Call, error)
	CompensateCallDelivery(context.Context, string, int64, string) (Call, error)
	RecoverOrphanCalls(context.Context, time.Time, int) (int, error)
	ListCalls(context.Context, identity.Account, int) ([]Call, error)
	GetCall(context.Context, identity.Account, string) (Call, error)
	Dashboard(context.Context, string) (Dashboard, error)
}

type OutboundFactory interface {
	ResolveRoutingLeasesWithStore(context.Context, channel.RoutingStore, []string) ([]channel.PoolOfferStatus, []channel.RoutingLease, error)
	ProxyTarget(context.Context, channel.RoutingLease, bool, time.Duration) (*http.Client, string, error)
}
