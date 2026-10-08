// Package catalog is the model catalog: model names, four base prices
// (points per million tokens) and conditional price tiers (ADR-0012).
package catalog

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

const (
	// MaxPriceNanoPerMillion is the catalog ceiling of 100000 points per
	// million tokens. Combined with the channel multiplier ceiling of 1000x,
	// the effective per-million price stays representable by money.Amount.
	MaxPriceNanoPerMillion money.Amount = 100_000 * money.Amount(money.Scale)

	// MaxPriceTiers bounds the conditional tiers of one model; the schema
	// enforces the same bound on tier sequence numbers.
	MaxPriceTiers = 16
)

var (
	ErrNotFound     = errors.New("model not found")
	ErrInUse        = errors.New("model is used by channels")
	ErrConflict     = errors.New("model already exists")
	ErrInvalidInput = errors.New("invalid model")
)

// Model IDs are the names clients send. They cannot contain "/" or ":" so
// they fit one path segment, including Gemini's "{model}:generateContent".
var modelIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Model struct {
	ID                       string
	DisplayName              string
	InputPrice               money.Amount
	OutputPrice              money.Amount
	CacheWritePrice          money.Amount
	CacheReadPrice           money.Amount
	PriceTiers               []ledger.PriceTier
	Enabled                  bool
	SortOrder                int32
	Provider                 string
	ContextWindow            *int64
	InputModalities          []string
	OutputModalities         []string
	SupportsTools            bool
	SupportsStructuredOutput bool
	SupportsVision           bool
	ParameterInfo            string
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// BasePrices projects the four base prices into the pricing formula input.
func (m Model) BasePrices() ledger.Prices {
	return ledger.Prices{
		InputPerMillion: m.InputPrice, OutputPerMillion: m.OutputPrice,
		CacheWritePerMillion: m.CacheWritePrice, CacheReadPerMillion: m.CacheReadPrice,
	}
}

// ModelPatch changes the given fields; PriceTiers non-nil replaces the whole tier list.
type ModelPatch struct {
	DisplayName              *string
	InputPrice               *money.Amount
	OutputPrice              *money.Amount
	CacheWritePrice          *money.Amount
	CacheReadPrice           *money.Amount
	PriceTiers               *[]ledger.PriceTier
	Enabled                  *bool
	SortOrder                *int32
	Provider                 *string
	ContextWindow            **int64
	InputModalities          *[]string
	OutputModalities         *[]string
	SupportsTools            *bool
	SupportsStructuredOutput *bool
	SupportsVision           *bool
	ParameterInfo            *string
}

func (p ModelPatch) empty() bool {
	return p == ModelPatch{}
}

// Apply returns the model with the patch applied.
func (p ModelPatch) Apply(model Model) Model {
	set := func(target *string, value *string) {
		if value != nil {
			*target = *value
		}
	}
	setAmount := func(target *money.Amount, value *money.Amount) {
		if value != nil {
			*target = *value
		}
	}
	setBool := func(target *bool, value *bool) {
		if value != nil {
			*target = *value
		}
	}
	set(&model.DisplayName, p.DisplayName)
	setAmount(&model.InputPrice, p.InputPrice)
	setAmount(&model.OutputPrice, p.OutputPrice)
	setAmount(&model.CacheWritePrice, p.CacheWritePrice)
	setAmount(&model.CacheReadPrice, p.CacheReadPrice)
	if p.PriceTiers != nil {
		model.PriceTiers = *p.PriceTiers
	}
	setBool(&model.Enabled, p.Enabled)
	if p.SortOrder != nil {
		model.SortOrder = *p.SortOrder
	}
	set(&model.Provider, p.Provider)
	if p.ContextWindow != nil {
		model.ContextWindow = *p.ContextWindow
	}
	if p.InputModalities != nil {
		model.InputModalities = *p.InputModalities
	}
	if p.OutputModalities != nil {
		model.OutputModalities = *p.OutputModalities
	}
	setBool(&model.SupportsTools, p.SupportsTools)
	setBool(&model.SupportsStructuredOutput, p.SupportsStructuredOutput)
	setBool(&model.SupportsVision, p.SupportsVision)
	set(&model.ParameterInfo, p.ParameterInfo)
	return model
}

type Store interface {
	DeleteModel(ctx context.Context, actorID, id string) error
	ListModels(ctx context.Context, includeDisabled bool) ([]Model, error)
	GetModel(ctx context.Context, id string) (Model, error)
	// CreateModel inserts the model and its tiers and records an audit row.
	CreateModel(ctx context.Context, actorID string, model Model) (Model, error)
	// UpdateModel locks the model, passes the current value to mutate and
	// stores the result (replacing tiers) with an audit row, in one transaction.
	UpdateModel(ctx context.Context, actorID, id string, mutate func(Model) (Model, error)) (Model, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) List(ctx context.Context, includeDisabled bool) ([]Model, error) {
	return s.store.ListModels(ctx, includeDisabled)
}

func (s *Service) Get(ctx context.Context, id string) (Model, error) {
	return s.store.GetModel(ctx, strings.TrimSpace(id))
}

func (s *Service) Create(ctx context.Context, actorID string, model Model) (Model, error) {
	model = Normalize(model)
	if err := Validate(model); err != nil {
		return Model{}, err
	}
	return s.store.CreateModel(ctx, actorID, model)
}

func (s *Service) Update(ctx context.Context, actorID, id string, patch ModelPatch) (Model, error) {
	if patch.empty() {
		return Model{}, ErrInvalidInput
	}
	return s.store.UpdateModel(ctx, actorID, id, func(current Model) (Model, error) {
		updated := Normalize(patch.Apply(current))
		if err := Validate(updated); err != nil {
			return Model{}, err
		}
		return updated, nil
	})
}

func (s *Service) Delete(ctx context.Context, actorID, id string) error {
	return s.store.DeleteModel(ctx, actorID, id)
}

// Normalize trims text, deduplicates modalities and tier weekdays and fills
// the default tier timezone.
func Normalize(model Model) Model {
	model.ID = strings.TrimSpace(model.ID)
	model.DisplayName = strings.TrimSpace(model.DisplayName)
	model.Provider = strings.TrimSpace(model.Provider)
	model.ParameterInfo = strings.TrimSpace(model.ParameterInfo)
	model.InputModalities = normalizeModalities(model.InputModalities)
	model.OutputModalities = normalizeModalities(model.OutputModalities)
	model.PriceTiers = normalizePriceTiers(model.PriceTiers)
	return model
}

func normalizePriceTiers(tiers []ledger.PriceTier) []ledger.PriceTier {
	if len(tiers) == 0 {
		return nil
	}
	normalized := make([]ledger.PriceTier, 0, len(tiers))
	for _, tier := range tiers {
		tier.Name = strings.TrimSpace(tier.Name)
		if tier.Timezone = strings.TrimSpace(tier.Timezone); tier.Timezone == "" {
			tier.Timezone = "UTC"
		}
		if len(tier.Weekdays) > 0 {
			weekdays := make([]int, 0, len(tier.Weekdays))
			seen := make(map[int]struct{}, len(tier.Weekdays))
			for _, weekday := range tier.Weekdays {
				if _, ok := seen[weekday]; ok {
					continue
				}
				seen[weekday] = struct{}{}
				weekdays = append(weekdays, weekday)
			}
			sort.Ints(weekdays)
			tier.Weekdays = weekdays
		} else {
			tier.Weekdays = nil
		}
		normalized = append(normalized, tier)
	}
	return normalized
}

func normalizeModalities(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return []string{"text"}
	}
	return result
}

func validPrice(price money.Amount) bool {
	return price >= 0 && price <= MaxPriceNanoPerMillion
}

// Validate checks a normalized model against the catalog rules.
func Validate(model Model) error {
	if !modelIDPattern.MatchString(model.ID) || model.DisplayName == "" || utf8.RuneCountInString(model.DisplayName) > 128 {
		return ErrInvalidInput
	}
	if utf8.RuneCountInString(model.Provider) > 64 || utf8.RuneCountInString(model.ParameterInfo) > 500 {
		return ErrInvalidInput
	}
	if model.ContextWindow != nil && *model.ContextWindow <= 0 {
		return ErrInvalidInput
	}
	if !validPrice(model.InputPrice) || !validPrice(model.OutputPrice) || !validPrice(model.CacheWritePrice) || !validPrice(model.CacheReadPrice) {
		return ErrInvalidInput
	}
	return validatePriceTiers(model.PriceTiers)
}

func validatePriceTiers(tiers []ledger.PriceTier) error {
	if len(tiers) > MaxPriceTiers {
		return ErrInvalidInput
	}
	for _, tier := range tiers {
		if !tier.HasPredicate() || utf8.RuneCountInString(tier.Name) > 64 {
			return ErrInvalidInput
		}
		if (tier.MinPromptTokens != nil && *tier.MinPromptTokens < 0) || (tier.MaxPromptTokens != nil && *tier.MaxPromptTokens < 0) {
			return ErrInvalidInput
		}
		if tier.MinPromptTokens != nil && tier.MaxPromptTokens != nil && *tier.MinPromptTokens >= *tier.MaxPromptTokens {
			return ErrInvalidInput
		}
		if (tier.StartMinute == nil) != (tier.EndMinute == nil) {
			return ErrInvalidInput
		}
		if tier.StartMinute != nil && (*tier.StartMinute < 0 || *tier.StartMinute > 1439 || *tier.EndMinute < 1 || *tier.EndMinute > 1440 || *tier.StartMinute == *tier.EndMinute) {
			return ErrInvalidInput
		}
		for _, weekday := range tier.Weekdays {
			if weekday < 1 || weekday > 7 {
				return ErrInvalidInput
			}
		}
		if _, err := time.LoadLocation(tier.Timezone); err != nil {
			return ErrInvalidInput
		}
		if !validPrice(tier.InputPrice) || !validPrice(tier.OutputPrice) || !validPrice(tier.CacheWritePrice) || !validPrice(tier.CacheReadPrice) {
			return ErrInvalidInput
		}
	}
	return nil
}
