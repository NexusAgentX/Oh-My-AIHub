package catalog

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"

	// MaxPriceNanoPerMillion is the public catalog ceiling. Combined with the
	// channel multiplier ceiling of 1000x, the displayed per-million price is
	// always representable by money.Amount.
	MaxPriceNanoPerMillion money.Amount = 100_000 * money.Amount(money.Scale)

	// MaxPriceTiers bounds the conditional tiers of one model. The storage
	// schema enforces the same bound on tier sequence numbers.
	MaxPriceTiers = 16
)

var (
	ErrNotFound     = errors.New("model not found")
	ErrConflict     = errors.New("model already exists")
	ErrInvalidInput = errors.New("invalid model")
)

type Model struct {
	ID                       string
	Name                     string
	Provider                 string
	ContextWindow            int64
	ParameterInfo            string
	InputModalities          []string
	OutputModalities         []string
	SupportsTools            bool
	SupportsStructuredOutput bool
	SupportsVision           bool
	InputPrice               money.Amount
	OutputPrice              money.Amount
	CacheWritePrice          money.Amount
	CacheReadPrice           money.Amount
	PriceTiers               []ledger.PriceTier
	Status                   Status
	Version                  int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
	PriceUpdatedAt           time.Time
}

type Store interface {
	ListModels(context.Context, bool, string) ([]Model, error)
	GetModel(context.Context, string, bool) (Model, error)
	CreateModel(context.Context, string, Model) (Model, error)
	UpdateModel(context.Context, string, string, int64, Model) (Model, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) ListPublic(ctx context.Context, query string) ([]Model, error) {
	return s.store.ListModels(ctx, false, strings.TrimSpace(query))
}

func (s *Service) GetPublic(ctx context.Context, id string) (Model, error) {
	return s.store.GetModel(ctx, strings.TrimSpace(id), false)
}

func (s *Service) ListAdmin(ctx context.Context, actor identity.Account, query string) ([]Model, error) {
	if !actor.IsAdmin {
		return nil, identity.ErrForbidden
	}
	return s.store.ListModels(ctx, true, strings.TrimSpace(query))
}

func (s *Service) GetAdmin(ctx context.Context, actor identity.Account, id string) (Model, error) {
	if !actor.IsAdmin {
		return Model{}, identity.ErrForbidden
	}
	return s.store.GetModel(ctx, strings.TrimSpace(id), true)
}

func (s *Service) Create(ctx context.Context, actor identity.Account, model Model) (Model, error) {
	if !actor.IsAdmin {
		return Model{}, identity.ErrForbidden
	}
	model = normalize(model)
	if err := validate(model); err != nil {
		return Model{}, err
	}
	return s.store.CreateModel(ctx, actor.ID, model)
}

func (s *Service) Update(ctx context.Context, actor identity.Account, id string, expectedVersion int64, model Model) (Model, error) {
	if !actor.IsAdmin {
		return Model{}, identity.ErrForbidden
	}
	if expectedVersion <= 0 {
		return Model{}, ErrInvalidInput
	}
	model = normalize(model)
	model.ID = strings.TrimSpace(id)
	if err := validate(model); err != nil {
		return Model{}, err
	}
	return s.store.UpdateModel(ctx, actor.ID, id, expectedVersion, model)
}

var modelIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*(?:/[A-Za-z0-9][A-Za-z0-9._:-]*)*$`)

func normalize(model Model) Model {
	model.ID = strings.TrimSpace(model.ID)
	model.Name = strings.TrimSpace(model.Name)
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
				if weekday < 1 || weekday > 7 {
					continue
				}
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
	return result
}

func validate(model Model) error {
	if len(model.ID) > 128 || !modelIDPattern.MatchString(model.ID) || model.Name == "" || model.Provider == "" || model.ContextWindow <= 0 {
		return ErrInvalidInput
	}
	if len(model.InputModalities) == 0 || len(model.OutputModalities) == 0 {
		return ErrInvalidInput
	}
	if model.Status != StatusActive && model.Status != StatusDisabled {
		return ErrInvalidInput
	}
	if model.InputPrice < 0 || model.InputPrice > MaxPriceNanoPerMillion ||
		model.OutputPrice < 0 || model.OutputPrice > MaxPriceNanoPerMillion ||
		model.CacheWritePrice < 0 || model.CacheWritePrice > MaxPriceNanoPerMillion ||
		model.CacheReadPrice < 0 || model.CacheReadPrice > MaxPriceNanoPerMillion {
		return ErrInvalidInput
	}
	return validatePriceTiers(model.PriceTiers)
}

func validatePriceTiers(tiers []ledger.PriceTier) error {
	if len(tiers) > MaxPriceTiers {
		return ErrInvalidInput
	}
	for _, tier := range tiers {
		if !tier.HasPredicate() {
			return ErrInvalidInput
		}
		if utf8.RuneCountInString(tier.Name) > 64 {
			return ErrInvalidInput
		}
		if tier.MinPromptTokens != nil && *tier.MinPromptTokens < 0 {
			return ErrInvalidInput
		}
		if tier.MaxPromptTokens != nil && *tier.MaxPromptTokens < 0 {
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
		if tier.InputPrice < 0 || tier.InputPrice > MaxPriceNanoPerMillion ||
			tier.OutputPrice < 0 || tier.OutputPrice > MaxPriceNanoPerMillion ||
			tier.CacheWritePrice < 0 || tier.CacheWritePrice > MaxPriceNanoPerMillion ||
			tier.CacheReadPrice < 0 || tier.CacheReadPrice > MaxPriceNanoPerMillion {
			return ErrInvalidInput
		}
	}
	return nil
}
