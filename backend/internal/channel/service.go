package channel

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
)

// credentialVersion is the AEAD version of upstream keys; keys are replaced
// wholesale, never re-versioned.
const credentialVersion = 1

// Dependencies wires the channel service. KnownModels returns every catalog
// model ID; BlockedHosts the administrator-managed extra egress block list.
type Dependencies struct {
	Store        Store
	Keyring      *Keyring
	Outbound     Outbound
	KnownModels  func(context.Context) ([]string, error)
	BlockedHosts func(context.Context) ([]string, error)
}

type Service struct {
	Dependencies
}

func NewService(dependencies Dependencies) *Service { return &Service{Dependencies: dependencies} }

type CreateInput struct {
	Name     string
	BaseURL  string
	APIKey   string
	Status   Status
	Models   []Model
	Advanced Advanced
}

type UpdateInput struct {
	Name     *string
	BaseURL  *string
	APIKey   *string
	Status   *Status
	Models   *[]Model
	Advanced *Advanced
}

func (s *Service) outbound(ctx context.Context) (Outbound, error) {
	hosts, err := s.BlockedHosts(ctx)
	if err != nil {
		return nil, err
	}
	return s.Outbound.WithExtraBlockedHosts(hosts)
}

func (s *Service) knownModels(ctx context.Context) (func(string) bool, error) {
	ids, err := s.KnownModels(ctx)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return func(id string) bool { return set[id] }, nil
}

func validName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= 64 && !hasControl(name)
}

// validateBase normalizes the Base URL under the egress policy and maps every
// policy refusal to ErrInvalidInput.
func (s *Service) validateBase(ctx context.Context, raw string) (string, error) {
	policy, err := s.outbound(ctx)
	if err != nil {
		return "", err
	}
	normalized, err := policy.ValidateBaseURL(ctx, raw)
	if err != nil {
		if errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrUnsafeUpstream) {
			return "", ErrInvalidInput
		}
		return "", ErrInvalidInput
	}
	return normalized, nil
}

func (s *Service) Create(ctx context.Context, ownerID string, in CreateInput) (Channel, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Status == "" {
		in.Status = StatusListed
	}
	if !validName(in.Name) || !validCredential(strings.TrimSpace(in.APIKey)) || (in.Status != StatusListed && in.Status != StatusUnlisted) {
		return Channel{}, ErrInvalidInput
	}
	if err := ValidateAdvanced(in.Advanced); err != nil {
		return Channel{}, err
	}
	known, err := s.knownModels(ctx)
	if err != nil {
		return Channel{}, err
	}
	models, err := NormalizeModels(in.Models, known)
	if err != nil {
		return Channel{}, err
	}
	baseURL, err := s.validateBase(ctx, in.BaseURL)
	if err != nil {
		return Channel{}, err
	}
	id, err := ids.NewUUID()
	if err != nil {
		return Channel{}, err
	}
	credential, err := s.Keyring.Encrypt(id, credentialVersion, strings.TrimSpace(in.APIKey))
	if err != nil {
		return Channel{}, err
	}
	for index := range models {
		models[index].Enabled = true
	}
	return s.Store.Create(ctx, NewChannel{
		ID: id, OwnerID: ownerID, Name: in.Name, BaseURL: baseURL, Credential: credential,
		Status: in.Status, Advanced: in.Advanced, Models: models,
	})
}

// owned loads a channel and hides it from anyone but its owner.
func (s *Service) owned(ctx context.Context, ownerID, id string) (Channel, error) {
	channel, err := s.Store.Get(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	if channel.Owner.ID != ownerID {
		return Channel{}, ErrNotFound
	}
	return channel, nil
}

func (s *Service) Get(ctx context.Context, ownerID, id string) (Channel, []Event, error) {
	channel, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return Channel{}, nil, err
	}
	events, err := s.Store.Events(ctx, id, 50)
	return channel, events, err
}

func (s *Service) List(ctx context.Context, ownerID string) ([]Channel, error) {
	return s.Store.ListByOwner(ctx, ownerID)
}

func (s *Service) Update(ctx context.Context, ownerID, id string, in UpdateInput) (Channel, error) {
	current, err := s.owned(ctx, ownerID, id)
	if err != nil {
		return Channel{}, err
	}
	update := Update{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if !validName(name) {
			return Channel{}, ErrInvalidInput
		}
		update.Name = &name
	}
	if in.BaseURL != nil {
		baseURL, err := s.validateBase(ctx, *in.BaseURL)
		if err != nil {
			return Channel{}, err
		}
		update.BaseURL = &baseURL
	}
	if in.APIKey != nil {
		key := strings.TrimSpace(*in.APIKey)
		if !validCredential(key) {
			return Channel{}, ErrInvalidInput
		}
		credential, err := s.Keyring.Encrypt(id, credentialVersion, key)
		if err != nil {
			return Channel{}, err
		}
		update.Credential = &credential
	}
	if in.Status != nil {
		if *in.Status != StatusListed && *in.Status != StatusUnlisted {
			return Channel{}, ErrInvalidInput
		}
		if current.Status == StatusSuspended {
			return Channel{}, ErrSuspended
		}
		update.Status = in.Status
	}
	if in.Advanced != nil {
		if err := ValidateAdvanced(*in.Advanced); err != nil {
			return Channel{}, err
		}
		update.Advanced = in.Advanced
	}
	if in.Models != nil {
		known, err := s.knownModels(ctx)
		if err != nil {
			return Channel{}, err
		}
		models, err := NormalizeModels(*in.Models, known)
		if err != nil {
			return Channel{}, err
		}
		update.Models = &models
	}
	return s.Store.Update(ctx, id, update)
}

func (s *Service) Delete(ctx context.Context, ownerID, id string) error {
	if _, err := s.owned(ctx, ownerID, id); err != nil {
		return err
	}
	return s.Store.SoftDelete(ctx, id)
}

func (s *Service) AdminList(ctx context.Context, filter AdminFilter) ([]Channel, error) {
	return s.Store.ListAll(ctx, filter)
}

func (s *Service) AdminGet(ctx context.Context, id string) (Channel, []Event, error) {
	channel, err := s.Store.Get(ctx, id)
	if err != nil {
		return Channel{}, nil, err
	}
	events, err := s.Store.Events(ctx, id, 50)
	return channel, events, err
}

func validReason(reason string) (string, bool) {
	reason = strings.TrimSpace(reason)
	return reason, reason != "" && utf8.RuneCountInString(reason) <= 500
}

func (s *Service) Suspend(ctx context.Context, actorID, id, reason string) (Channel, error) {
	reason, ok := validReason(reason)
	if !ok {
		return Channel{}, ErrInvalidInput
	}
	return s.Store.Suspend(ctx, actorID, id, reason)
}

func (s *Service) Unsuspend(ctx context.Context, actorID, id, reason string) (Channel, error) {
	reason, ok := validReason(reason)
	if !ok {
		return Channel{}, ErrInvalidInput
	}
	return s.Store.Unsuspend(ctx, actorID, id, reason)
}
