// Package typical derives comparison estimates from reliable business usage.
// It neither settles calls nor retains prompts, opaque state or account keys.
package typical

import (
	"context"
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
)

var (
	ErrInvalid   = errors.New("invalid typical cost settings")
	ErrConflict  = errors.New("typical cost settings changed")
	ErrBusy      = errors.New("typical cost update already running")
	ErrSaturated = errors.New("typical cost work limit reached")
	ErrStale     = errors.New("typical cost input changed")
)

const (
	PageSize        = 250
	ScanPages       = 4
	MaxSamples      = 20_000
	MaxBytes        = 32 << 20
	MaxCalculations = 100_000
)

type Config struct {
	Version         int64 `json:"version"`
	WindowDays      int   `json:"window_days"`
	MinSamples      int   `json:"min_samples"`
	IntervalSeconds int   `json:"interval_seconds"`
	TypicalTokens   int64 `json:"typical_tokens"`
	TypicalRequests int64 `json:"typical_requests"`
	TypicalImages   int64 `json:"typical_images"`
}

func DefaultConfig() Config {
	return Config{Version: 1, WindowDays: 30, MinSamples: 20, IntervalSeconds: 300, TypicalTokens: 1_000_000, TypicalRequests: 100, TypicalImages: 100}
}
func Validate(c Config) error {
	if c.Version < 1 || c.WindowDays < 1 || c.WindowDays > 90 || c.MinSamples < 1 || c.MinSamples > MaxSamples || c.IntervalSeconds < 30 || c.IntervalSeconds > 86400 || c.TypicalTokens < 1 || c.TypicalTokens > 1_000_000_000 || c.TypicalRequests < 1 || c.TypicalRequests > 1_000_000 || c.TypicalImages < 1 || c.TypicalImages > 1_000_000 {
		return ErrInvalid
	}
	return nil
}

type Cursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}
type Scan struct {
	After          Cursor
	Floor, Ceiling time.Time
}
type RawCall struct {
	ID, ModelID, Format, Outcome, UsageStatus string
	StartedAt, CompletedAt                    time.Time
	Usage                                     ledger.Usage
	Snapshot                                  []byte
}
type Sample struct {
	ID          string                   `json:"id"`
	GroupID     string                   `json:"group_id"`
	ModelID     string                   `json:"model_id"`
	At          time.Time                `json:"at"`
	CompletedAt time.Time                `json:"completed_at"`
	References  []quotepricing.Reference `json:"references"`
	Facts       quotepricing.Facts       `json:"facts"`
}
type Checkpoint struct {
	Generation    int64     `json:"generation"`
	WindowDays    int       `json:"window_days"`
	Delta         Cursor    `json:"delta"`
	Repair        Cursor    `json:"repair"`
	RepairFloor   time.Time `json:"repair_floor"`
	RepairCeiling time.Time `json:"repair_ceiling"`
	RepairedAt    time.Time `json:"repaired_at"`
}
type Channel struct {
	ID, GroupID, ModelID, Name string
	DiscountNano               int64
}
type Input struct {
	Directory               market.Directory
	Channels                []Channel
	USDCNYNano, FeeRateNano int64
}
type Source interface {
	Current(context.Context) (Input, error)
}
type Repository interface {
	Config(context.Context) (Config, error)
	SetConfig(context.Context, string, Config) (Config, error)
	Checkpoint(context.Context) (Checkpoint, error)
	Calls(context.Context, Scan, int) ([]RawCall, error)
	Ingest(context.Context, Checkpoint, []Sample) error
	Samples(context.Context, time.Time, Cursor, int) ([]Sample, error)
	Prune(context.Context, time.Time, int) error
	LoadSnapshot(context.Context) (*Snapshot, error)
	SaveSnapshot(context.Context, *Snapshot) error
}
type Estimate struct {
	Level                 string   `json:"level"`
	GroupName             string   `json:"group_name,omitempty"`
	SupplierName          string   `json:"supplier_name,omitempty"`
	ChannelName           string   `json:"channel_name,omitempty"`
	ModelID               string   `json:"model_id"`
	GroupID               string   `json:"group_id,omitempty"`
	ChannelID             string   `json:"channel_id,omitempty"`
	GroupModelID          string   `json:"group_model_id,omitempty"`
	QuoteID               string   `json:"quote_id,omitempty"`
	QuoteVersion          int64    `json:"quote_version,omitempty"`
	PricingVersion        string   `json:"pricing_version"`
	State                 string   `json:"state"`
	Reason                string   `json:"reason,omitempty"`
	Cost                  *string  `json:"cost"`
	RequestCost           *string  `json:"request_cost"`
	RequestQuantity       int64    `json:"request_quantity"`
	Fee                   *string  `json:"fee"`
	ReferenceCost         *string  `json:"reference_cost"`
	Ratio                 *string  `json:"ratio"`
	ReferenceGroupModelID string   `json:"reference_group_model_id,omitempty"`
	Unit                  string   `json:"unit"`
	Quantity              int64    `json:"quantity"`
	Samples               int      `json:"samples"`
	OwnSamples            int      `json:"own_samples"`
	Source                string   `json:"source"`
	CacheReadShare        *string  `json:"cache_read_share"`
	Workload              Workload `json:"workload"`
}

// Only aggregates are public. Per-call facts, consumer/channel income and Key
// identities never appear in an estimate response.
type Workload struct {
	Requests         int64            `json:"requests"`
	InputTokens      int64            `json:"input_tokens"`
	OutputTokens     int64            `json:"output_tokens"`
	CacheWriteTokens int64            `json:"cache_write_tokens"`
	CacheReadTokens  int64            `json:"cache_read_tokens"`
	ImagesNano       int64            `json:"images_nano"`
	Tokens           map[string]int64 `json:"tokens"`
	ServiceTiers     map[string]int64 `json:"service_tiers"`
	Units            map[string]int64 `json:"units_nano"`
}
type Snapshot struct {
	USDCNYNano   int64      `json:"usd_cny_rate_nano"`
	FeeRateNano  int64      `json:"fee_rate_nano"`
	Version      string     `json:"version"`
	Config       Config     `json:"config"`
	InputVersion string     `json:"input_version"`
	UpdatedAt    time.Time  `json:"updated_at"`
	WindowStart  time.Time  `json:"window_start"`
	Watermark    time.Time  `json:"watermark"`
	ReconciledAt time.Time  `json:"reconciled_at"`
	Items        []Estimate `json:"items"`
}
type Status struct {
	Running     bool      `json:"running"`
	Pending     bool      `json:"pending"`
	LastAttempt time.Time `json:"last_attempt"`
	LastError   string    `json:"last_error"`
	Saturated   bool      `json:"saturated"`
}
