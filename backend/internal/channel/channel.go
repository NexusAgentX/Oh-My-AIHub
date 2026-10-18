package channel

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var ErrNotFound = errors.New("channel not found")

type Status string

const (
	StatusListed    Status = "listed"
	StatusUnlisted  Status = "unlisted"
	StatusSuspended Status = "suspended"
)

// Formats lists the four native API formats in display order.
var Formats = []Format{FormatOpenAIChat, FormatOpenAIResponses, FormatAnthropic, FormatGemini}

func (f Format) Valid() bool { return slices.Contains(Formats, f) }

type HeaderSet struct {
	Name  string
	Value string
}

// HeaderRules are the optional per-channel request header edits (Epic #170):
// Set adds or overwrites, Remove deletes.
type HeaderRules struct {
	Set    []HeaderSet
	Remove []string
}

// Advanced holds the optional per-channel overrides; nil means the platform
// default.
type Advanced struct {
	UserAgent        *string
	HeaderRules      HeaderRules
	ConcurrencyLimit *int32
	RPMLimit         *int32
	TTFTTimeoutMS    *int32
	RequestTimeoutMS *int32
	IdleTimeoutMS    *int32
	CooldownFailures *int32
	CooldownSeconds  *int32
}

type FormatTest struct {
	EndpointID string    `json:"endpoint_id,omitempty"`
	OK         bool      `json:"ok"`
	StatusCode *int      `json:"status_code"`
	Error      *string   `json:"error"`
	DurationMS *int      `json:"duration_ms"`
	TestedAt   time.Time `json:"tested_at"`
}

// CustomPrices are the four per-million-token point prices of a custom model
// (ADR-0034); they are charged as is, without multiplier, exchange rate or
// tiers.
type CustomPrices struct {
	Input      money.Amount
	Output     money.Amount
	CacheWrite money.Amount
	CacheRead  money.Amount
}

// Blended is the 3:1 input/output blend that orders channels of a custom
// model by price (cheapest first and the lowest reference price, ADR-0034);
// cache prices do not count.
func (p CustomPrices) Blended() float64 {
	return (3*float64(p.Input.Nano()) + float64(p.Output.Nano())) / 4
}

// Model is the catalog-shaped model a channel bills for a custom model: the
// four prices in points without tiers or fine-grained prices.
func (p CustomPrices) Model(id string) catalog.Model {
	return catalog.Model{
		ID: id, DisplayName: id, PriceCurrency: "CNY", Enabled: true,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		InputPrice: p.Input, OutputPrice: p.Output, CacheWritePrice: p.CacheWrite, CacheReadPrice: p.CacheRead,
	}
}

type Owner struct {
	ID          string
	Username    string
	DisplayName string
}

type Event struct {
	ID        int64
	Kind      string
	Reason    string
	CreatedAt time.Time
}

// Event kinds recorded in channel_events.
const (
	EventCooldownStarted = "cooldown_started"
	EventCooldownEnded   = "cooldown_ended"
	EventLimitReached    = "limit_reached"
	EventSuspended       = "suspended"
	EventUnsuspended     = "unsuspended"
	EventListed          = "listed"
	EventUnlisted        = "unlisted"
	EventTestFailed      = "test_failed"
	// EventAnomalousClientErrors records that the channel answers a model with
	// 4xx almost every time while the model's other channels do not, which points
	// at the channel's configuration; the reason names the model and the counts.
	EventAnomalousClientErrors = "anomalous_client_errors"
	// EventModelPendingMultiplier records that a custom model of the channel
	// became an official catalog model; the reason is the model ID.
	EventModelPendingMultiplier = "model_pending_multiplier"
)

// NewChannel is a validated creation request.
var (
	headerNamePattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,64}$")

	// forbiddenRuleHeaders can neither be set nor removed by a rule: they are
	// owned by the transport or carry the credential.
	forbiddenRuleHeaders = []string{
		"host", "content-length", "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "authorization", "x-api-key", "x-goog-api-key",
		"cookie", "accept-encoding",
	}
)

// ForbiddenRuleHeader reports whether a header rule may not touch name.
func ForbiddenRuleHeader(name string) bool {
	return slices.Contains(forbiddenRuleHeaders, strings.ToLower(name))
}

func hasControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

// ValidateAdvanced checks the overrides against the database ranges and the
// header rule restrictions.
func ValidateAdvanced(advanced Advanced) error {
	if advanced.UserAgent != nil && (*advanced.UserAgent == "" || utf8.RuneCountInString(*advanced.UserAgent) > 512 || hasControl(*advanced.UserAgent)) {
		return ErrInvalidInput
	}
	if len(advanced.HeaderRules.Set) > 32 || len(advanced.HeaderRules.Remove) > 32 {
		return ErrInvalidInput
	}
	for _, rule := range advanced.HeaderRules.Set {
		if !headerNamePattern.MatchString(rule.Name) || ForbiddenRuleHeader(rule.Name) || utf8.RuneCountInString(rule.Value) > 1024 || hasControl(rule.Value) {
			return ErrInvalidInput
		}
	}
	for _, name := range advanced.HeaderRules.Remove {
		if !headerNamePattern.MatchString(name) || ForbiddenRuleHeader(name) {
			return ErrInvalidInput
		}
	}
	inRange := func(value *int32, low, high int32) bool { return value == nil || (*value >= low && *value <= high) }
	if !inRange(advanced.ConcurrencyLimit, 1, 1<<30) || !inRange(advanced.RPMLimit, 1, 1<<30) ||
		!inRange(advanced.TTFTTimeoutMS, 1000, 600000) || !inRange(advanced.RequestTimeoutMS, 1000, 3600000) || !inRange(advanced.IdleTimeoutMS, 1000, 3600000) ||
		!inRange(advanced.CooldownFailures, 1, 100) || !inRange(advanced.CooldownSeconds, 10, 86400) {
		return ErrInvalidInput
	}
	return nil
}
