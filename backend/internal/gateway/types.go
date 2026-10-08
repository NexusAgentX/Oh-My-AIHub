package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var (
	ErrKeyNotFound = errors.New("api key not found")
	ErrNoUsage     = errors.New("no usage")
)

// Outcomes of a call (calls.outcome).
const (
	OutcomeRejectedBalance   = "rejected_balance"
	OutcomeRejectedBudget    = "rejected_budget"
	OutcomeRejectedKey       = "rejected_key"
	OutcomeRejectedModel     = "rejected_model"
	OutcomeRejectedFormat    = "rejected_format"
	OutcomeRejectedNoChannel = "rejected_no_channel"
	OutcomeUpstreamFailed    = "upstream_failed"
	OutcomeInterrupted       = "interrupted"
	OutcomeClientDisconnect  = "client_disconnected"
	OutcomeSucceeded         = "succeeded"
	OutcomeUnbilled          = "succeeded_unbilled"
	OutcomeInProgress        = "in_progress"
)

// KeyAuth is a platform API key with its owner, as the gateway needs it.
type KeyAuth struct {
	KeyID         string
	OwnerID       string
	Name          string
	Prefix        string
	Enabled       bool
	ExpiresAt     *time.Time
	AllowedModels []string
	BudgetDaily   *money.Amount
	BudgetMonthly *money.Amount
	BudgetTotal   *money.Amount
	Aliases       map[string]string
	AccountActive bool
}

// Candidate is one channel offering the requested model in the requested
// format.
type Candidate struct {
	ChannelID      string
	OwnerID        string
	Name           string
	BaseURL        string
	UpstreamModel  string
	MultiplierNano int64
	Advanced       channel.Advanced
	Credential     channel.EncryptedCredential
}

// ChannelStats is the 24h health of a channel computed from call attempts.
type ChannelStats struct {
	Attempts  int64
	Successes int64
	TTFTP50MS *float64
}

func (s ChannelStats) SuccessRate() float64 {
	if s.Attempts == 0 {
		return 0
	}
	return float64(s.Successes) / float64(s.Attempts)
}

// CallInsert opens a call record. An immediately rejected call is inserted
// with its final Outcome already set.
type CallInsert struct {
	AccountID      string
	KeyID          string
	RequestedModel string
	ModelID        string
	Format         channel.Format
	Stream         bool
	Tag            string
	UserAgent      string
	Outcome        string
}

// Attempt is one try on one channel (calls.attempts).
type Attempt struct {
	Channel      *AttemptChannel `json:"channel"`
	StatusCode   *int            `json:"status_code"`
	ErrorCode    *string         `json:"error_code"`
	ErrorMessage *string         `json:"error_message"`
	ConnectMS    *int            `json:"connect_ms"`
	TTFTMS       *int            `json:"ttft_ms"`
	DurationMS   *int            `json:"duration_ms"`
	ResponseByte *int64          `json:"response_bytes"`
	EndReason    string          `json:"end_reason"`
}

type AttemptChannel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Attempt end reasons.
const (
	EndCompleted          = "completed"
	EndUpstreamError      = "upstream_error"
	EndConnectError       = "connect_error"
	EndTimeoutTTFT        = "timeout_ttft"
	EndTimeoutTotal       = "timeout_total"
	EndClientError        = "client_error"
	EndClientDisconnected = "client_disconnected"
	EndInterrupted        = "interrupted"
)

// Billing is the ledger posting of a call: the consumer pays Cost+Fee, the
// channel owner receives Cost and the platform Fee.
type Billing struct {
	ConsumerID string
	ProviderID string
	Cost       money.Amount
	Fee        money.Amount
}

// CallFinish closes a call record and, when Bill is set, books the ledger
// transaction "call:<id>" in the same database transaction.
type CallFinish struct {
	ID                 string
	AccountID          string
	Outcome            string
	ModelID            string
	ChannelID          string
	RoutingMode        string
	RoutingSource      string
	Attempts           []Attempt
	Usage              ledger.Usage
	PriceSnapshot      []byte
	Cost               money.Amount
	Fee                money.Amount
	TTFTMS             *int
	DurationMS         int
	TokensPerSecond    *float64
	IntervalP50MS      *int
	IntervalP95MS      *int
	ResponseBytes      int64
	UpstreamResponseID string
	Bill               *Billing
}

// FinishResult tells whether this call ended the record (false when it had
// already ended, in which case nothing was booked again).
type FinishResult struct {
	Finished bool
	TxID     string
}

// Store is the persistence the gateway needs.
type Store interface {
	LookupKey(ctx context.Context, hash []byte) (KeyAuth, error)
	TouchKey(ctx context.Context, keyID string, at time.Time) error
	Points(ctx context.Context, accountID string) (ledger.Points, error)
	KeySpend(ctx context.Context, keyID string, now time.Time) (Spend, error)
	ChannelRevenue(ctx context.Context, channelID string, since time.Time) (money.Amount, error)
	Candidates(ctx context.Context, modelID string, format channel.Format) ([]Candidate, error)
	ChannelStats(ctx context.Context) (map[string]ChannelStats, error)
	ResponseChannel(ctx context.Context, accountID, responseID string) (string, error)
	InsertCall(ctx context.Context, call CallInsert) (string, error)
	FinishCall(ctx context.Context, finish CallFinish) (FinishResult, error)
	// SweepStaleCalls marks calls still in progress since before the cut-off
	// as interrupted (never billed) and returns how many it changed.
	SweepStaleCalls(ctx context.Context, before time.Time) (int64, error)
	RecordChannelEvent(ctx context.Context, channelID, kind, reason string) error
	// OnlineModels lists, for GET /v1/models, the models that have at least
	// one listed channel.
	OnlineModels(ctx context.Context) ([]string, error)
}

// OnlineChannel is one listed channel offering one model, for browsing.
type OnlineChannel struct {
	ModelID        string
	ChannelID      string
	ChannelName    string
	OwnerID        string
	OwnerName      string
	Formats        []channel.Format
	MultiplierNano int64
	Advanced       channel.Advanced
}

// HomeStats are the counters on the home page.
type HomeStats struct {
	TodaySpend     money.Amount
	TodayCalls     int64
	TodaySucceeded int64
	RevenueToday   money.Amount
	ChannelsOnline int64
	ChannelsTotal  int64
	PendingTrades  int64
}

// CallSummary is a call as listed on the home page.
type CallSummary struct {
	ID             string
	CreatedAt      time.Time
	CompletedAt    *time.Time
	ModelID        *string
	RequestedModel string
	Format         channel.Format
	Stream         bool
	Tag            *string
	KeyID          *string
	KeyName        *string
	Outcome        string
	ChannelID      *string
	ChannelName    *string
	Usage          ledger.Usage
	Cost           money.Amount
	Fee            money.Amount
	TTFTMS         *int32
	DurationMS     *int32
}

// Browse is the read side behind the model pages and the home page.
type Browse interface {
	OnlineChannels(ctx context.Context) ([]OnlineChannel, error)
	Home(ctx context.Context, accountID string, dayStart time.Time) (HomeStats, error)
	RecentCalls(ctx context.Context, accountID string, limit int) ([]CallSummary, error)
}
