package channel

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var (
	ErrNotFound = errors.New("channel not found")
	ErrConflict = errors.New("channel state conflict")
	// ErrSuspended rejects owner changes of a channel an administrator took down.
	ErrSuspended = errors.New("channel is suspended")
)

type Status string

const (
	StatusListed    Status = "listed"
	StatusUnlisted  Status = "unlisted"
	StatusSuspended Status = "suspended"
)

// Formats lists the four native API formats in display order.
var Formats = []Format{FormatOpenAIChat, FormatOpenAIResponses, FormatAnthropic, FormatGemini}

func (f Format) Valid() bool { return slices.Contains(Formats, f) }

// MaxModels bounds the models one channel can carry.
const MaxModels = 200

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
	DailyRevenueCap  *money.Amount
	TTFTTimeoutMS    *int32
	TotalTimeoutMS   *int32
	CooldownFailures *int32
	CooldownSeconds  *int32
}

type FormatTest struct {
	OK         bool      `json:"ok"`
	StatusCode *int      `json:"status_code"`
	Error      *string   `json:"error"`
	DurationMS *int      `json:"duration_ms"`
	TestedAt   time.Time `json:"tested_at"`
}

// Model is one catalog model offered by a channel: the upstream name it maps
// to, the price multiplier and the formats the upstream actually serves.
type Model struct {
	ModelID        string
	UpstreamModel  string
	MultiplierNano int64
	Formats        []Format
	FormatTests    map[Format]FormatTest
	Enabled        bool
}

type Owner struct {
	ID          string
	Username    string
	DisplayName string
}

// Today is the channel's revenue and success statistics since local midnight
// (Asia/Shanghai).
type Today struct {
	Revenue     money.Amount
	Calls       int64
	SuccessRate *float64
}

type Channel struct {
	ID              string
	Owner           Owner
	Name            string
	BaseURL         string
	Status          Status
	SuspendedReason *string
	Advanced        Advanced
	Models          []Model
	Today           Today
	CreatedAt       time.Time
	UpdatedAt       time.Time
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
)

// NewChannel is a validated creation request.
type NewChannel struct {
	ID         string
	OwnerID    string
	Name       string
	BaseURL    string
	Credential EncryptedCredential
	Status     Status
	Advanced   Advanced
	Models     []Model
}

// Update changes the given fields; nil fields stay unchanged. Advanced and
// Models replace the whole block.
type Update struct {
	Name       *string
	BaseURL    *string
	Credential *EncryptedCredential
	Status     *Status
	Advanced   *Advanced
	Models     *[]Model
}

type AdminFilter struct {
	Query    string
	Status   Status
	OwnerID  string
	AfterKey string // created_at/id cursor of the previous page
	Limit    int
}

// TestOutcome is the result of one model × format probe.
type TestOutcome struct {
	ModelID    string
	Format     Format
	OK         bool
	StatusCode *int
	Error      *string
	DurationMS int
}

type Store interface {
	Create(ctx context.Context, channel NewChannel) (Channel, error)
	Get(ctx context.Context, id string) (Channel, error)
	ListByOwner(ctx context.Context, ownerID string) ([]Channel, error)
	ListAll(ctx context.Context, filter AdminFilter) ([]Channel, error)
	Update(ctx context.Context, id string, update Update) (Channel, error)
	SoftDelete(ctx context.Context, id string) error
	Credential(ctx context.Context, id string) (EncryptedCredential, error)
	Events(ctx context.Context, id string, limit int) ([]Event, error)
	RecordEvent(ctx context.Context, id, kind, reason string) error
	// SaveTests stores the probe results in format_tests and, when apply is
	// set, rewrites the tested formats of each model to the passing ones.
	SaveTests(ctx context.Context, id string, results []TestOutcome, testedAt time.Time, apply bool) (Channel, error)
	Suspend(ctx context.Context, actorID, id, reason string) (Channel, error)
	Unsuspend(ctx context.Context, actorID, id, reason string) (Channel, error)
}

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
		!inRange(advanced.TTFTTimeoutMS, 1000, 600000) || !inRange(advanced.TotalTimeoutMS, 1000, 3600000) ||
		!inRange(advanced.CooldownFailures, 1, 100) || !inRange(advanced.CooldownSeconds, 10, 86400) {
		return ErrInvalidInput
	}
	if advanced.DailyRevenueCap != nil && *advanced.DailyRevenueCap <= 0 {
		return ErrInvalidInput
	}
	if advanced.TTFTTimeoutMS != nil && advanced.TotalTimeoutMS != nil && *advanced.TotalTimeoutMS < *advanced.TTFTTimeoutMS {
		return ErrInvalidInput
	}
	return nil
}

// MaxMultiplierNano is the 1000x ceiling of the database constraint.
const MaxMultiplierNano int64 = 1000 * money.Scale

// NormalizeModels trims and validates the offered models. known reports
// whether a model ID exists in the catalog.
func NormalizeModels(models []Model, known func(string) bool) ([]Model, error) {
	if len(models) == 0 || len(models) > MaxModels {
		return nil, ErrInvalidInput
	}
	seen := map[string]bool{}
	result := make([]Model, 0, len(models))
	for _, model := range models {
		model.ModelID = strings.TrimSpace(model.ModelID)
		model.UpstreamModel = strings.TrimSpace(model.UpstreamModel)
		if model.UpstreamModel == "" {
			model.UpstreamModel = model.ModelID
		}
		if model.ModelID == "" || seen[model.ModelID] || !known(model.ModelID) ||
			utf8.RuneCountInString(model.UpstreamModel) > 256 || hasControl(model.UpstreamModel) ||
			model.MultiplierNano < 0 || model.MultiplierNano > MaxMultiplierNano {
			return nil, ErrInvalidInput
		}
		seen[model.ModelID] = true
		formats := make([]Format, 0, len(model.Formats))
		for _, format := range Formats {
			if slices.Contains(model.Formats, format) {
				formats = append(formats, format)
			}
		}
		if len(formats) == 0 || len(formats) != len(uniqueFormats(model.Formats)) {
			return nil, ErrInvalidInput
		}
		model.Formats = formats
		result = append(result, model)
	}
	return result, nil
}

func uniqueFormats(formats []Format) []Format {
	var result []Format
	for _, format := range formats {
		if !slices.Contains(result, format) {
			result = append(result, format)
		}
	}
	return result
}
