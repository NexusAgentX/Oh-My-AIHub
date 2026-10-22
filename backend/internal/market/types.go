// Package market owns contributed suppliers, groups and versioned quotes.
// Model IDs are strings aggregated from group offerings, never a global model
// listing prerequisite. Publication/approval is owned by the contribution layer.
package market

import (
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var (
	ErrInvalid   = errors.New("invalid market data")
	ErrNotFound  = errors.New("market object not found")
	ErrConflict  = errors.New("market revision or ownership conflict")
	ErrHasGroups = errors.New("supplier still has groups")
)

type Supplier struct {
	ID            string     `json:"id"`
	ContributorID string     `json:"contributor_id"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	WebsiteURL    string     `json:"website_url"`
	IconData      string     `json:"icon_data"`
	IconSource    string     `json:"icon_source"`
	Summary       string     `json:"summary"`
	Description   string     `json:"description"`
	DocsURL       string     `json:"docs_url"`
	Enabled       bool       `json:"enabled"`
	Revision      int64      `json:"revision"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// Defaults deliberately uses pointer blocks. nil HeaderRules inherits the
// whole block, while &HeaderRules{} explicitly clears inherited edits. A pointer
// to zero is an override and must not be coalesced with nil.
type HeaderSet struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type HeaderRules struct {
	Set    []HeaderSet `json:"set,omitempty"`
	Remove []string    `json:"remove,omitempty"`
}
type Defaults struct {
	UserAgent        *string      `json:"user_agent"`
	HeaderRules      *HeaderRules `json:"header_rules"`
	ConcurrencyLimit *int32       `json:"concurrency_limit"`
	RPMLimit         *int32       `json:"rpm_limit"`
	TTFTTimeoutMS    *int32       `json:"ttft_timeout_ms"`
	RequestTimeoutMS *int32       `json:"request_timeout_ms"`
	IdleTimeoutMS    *int32       `json:"idle_timeout_ms"`
	CooldownFailures *int32       `json:"cooldown_failures"`
	CooldownSeconds  *int32       `json:"cooldown_seconds"`
}

type Group struct {
	ID                string     `json:"id"`
	SupplierID        string     `json:"supplier_id"`
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	StatusURL         string     `json:"status_url"`
	Defaults          Defaults   `json:"defaults"`
	Enabled           bool       `json:"enabled"`
	Revision          int64      `json:"revision"`
	ConnectionVersion int64      `json:"connection_version"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type Address struct {
	ID         string     `json:"id"`
	SupplierID string     `json:"supplier_id"`
	BaseURL    string     `json:"base_url"`
	Label      string     `json:"label"`
	Revision   int64      `json:"revision"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
}

type GroupAddress struct {
	ID         string     `json:"id"`
	GroupID    string     `json:"group_id"`
	AddressID  string     `json:"address_id"`
	SupplierID string     `json:"supplier_id"`
	Position   int32      `json:"position"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
}

type GroupModel struct {
	ID               string     `json:"id"`
	GroupID          string     `json:"group_id"`
	ModelID          string     `json:"model_id"`
	DisplayName      string     `json:"display_name"`
	UpstreamModel    string     `json:"upstream_model"`
	ModelType        string     `json:"model_type"`
	ContextWindow    *int64     `json:"context_window"`
	MaxOutputTokens  *int64     `json:"max_output_tokens"`
	Notes            string     `json:"notes"`
	Enabled          bool       `json:"enabled"`
	IsReference      bool       `json:"is_reference"`
	QuoteID          *string    `json:"quote_id"`
	Revision         int64      `json:"revision"`
	CapabilityDigest string     `json:"capability_digest"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type TokenPrices struct {
	Input      money.Amount            `json:"input"`
	Output     money.Amount            `json:"output"`
	CacheWrite money.Amount            `json:"cache_write"`
	CacheRead  money.Amount            `json:"cache_read"`
	Details    map[string]money.Amount `json:"details"`
}

func (p TokenPrices) Ledger() ledger.Prices {
	return ledger.Prices{InputPerMillion: p.Input, OutputPerMillion: p.Output, CacheWritePerMillion: p.CacheWrite, CacheReadPerMillion: p.CacheRead, TokenPrices: p.Details}
}

// Conditions are evaluated against reliable response facts and call start.
type Conditions struct {
	ServiceTier     string `json:"service_tier"`
	MinPromptTokens *int64 `json:"min_prompt_tokens"`
	MaxPromptTokens *int64 `json:"max_prompt_tokens"`
	Timezone        string `json:"timezone"`
	Weekdays        []int  `json:"weekdays"`
	StartMinute     *int16 `json:"start_minute"`
	EndMinute       *int16 `json:"end_minute"`
}
type Tier struct {
	Name       string      `json:"name"`
	Conditions Conditions  `json:"conditions"`
	Prices     TokenPrices `json:"prices"`
}

func (t Tier) Ledger() ledger.PriceTier {
	c := t.Conditions
	return ledger.PriceTier{Name: t.Name, ServiceTier: c.ServiceTier, MinPromptTokens: c.MinPromptTokens, MaxPromptTokens: c.MaxPromptTokens, Timezone: c.Timezone, Weekdays: c.Weekdays, StartMinute: c.StartMinute, EndMinute: c.EndMinute, InputPrice: t.Prices.Input, OutputPrice: t.Prices.Output, CacheWritePrice: t.Prices.CacheWrite, CacheReadPrice: t.Prices.CacheRead, TokenPrices: t.Prices.Details}
}

// UnitPrice also covers reliable in-call tool units. Tool is empty for the
// independent image/request unit. No storage/file/container resource tariff.
type UnitPrice struct {
	Unit            string       `json:"unit"`
	Tool            string       `json:"tool"`
	ImageSize       string       `json:"image_size"`
	ImageQuality    string       `json:"image_quality"`
	ImageBackground string       `json:"image_background"`
	ServiceTier     string       `json:"service_tier"`
	Price           money.Amount `json:"price"`
}
type Quote struct {
	ID           string      `json:"id"`
	GroupModelID string      `json:"group_model_id"`
	Version      int64       `json:"version"`
	Currency     string      `json:"currency"`
	BillingUnit  string      `json:"billing_unit"`
	Prices       TokenPrices `json:"prices"`
	Tiers        []Tier      `json:"tiers"`
	UnitPrices   []UnitPrice `json:"unit_prices"`
	CreatedAt    time.Time   `json:"created_at"`
}

type EvidenceBinding struct {
	TargetKind       string `json:"target_kind"`
	TargetID         string `json:"target_id"`
	TargetRevision   int64  `json:"target_revision"`
	CapabilityDigest string `json:"capability_digest"`
	ManifestVersion  string `json:"manifest_version"`
	CodeVersion      string `json:"code_version"`
	CredentialScope  string `json:"-"`
	ParameterVersion string `json:"parameter_version"`
}
type Capability struct {
	ID       string `json:"id"`
	Layer    string `json:"layer"`
	State    string `json:"state"`
	Reason   string `json:"reason"`
	ReportID string `json:"report_id"`
}

// ModelRule governs an exact ID; it does not create a model catalog entry.
type ModelRule struct {
	ModelID   string    `json:"model_id"`
	Disabled  bool      `json:"disabled"`
	Reason    string    `json:"reason"`
	Revision  int64     `json:"revision"`
	ActorID   string    `json:"actor_id"`
	UpdatedAt time.Time `json:"updated_at"`
}
