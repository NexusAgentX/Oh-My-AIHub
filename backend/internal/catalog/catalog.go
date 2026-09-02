package catalog

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
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
	return model
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
	return nil
}
