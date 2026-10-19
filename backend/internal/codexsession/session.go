// Package codexsession binds explicitly opted-in Codex sessions before their
// first upstream exchange. It stores routing metadata only, never contents.
package codexsession

import (
	"context"
	"errors"
	"sync"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

type Identity struct {
	AccountID, KeyID, SessionID string
}

type Route struct {
	gatewaycontract.Binding
	CredentialScope string
}

func (r Route) Valid() bool {
	return r.GroupID != "" && r.ChannelID != "" && r.CredentialVersion != "" && r.CredentialScope != ""
}

type Repository interface {
	LookupSession(context.Context, Identity) (Route, error)
	// ClaimSession atomically returns the immutable winner, including when
	// another first request concurrently proposed a different route.
	ClaimSession(context.Context, Identity, Route) (Route, error)
}

// Service caches only positive immutable bindings. FIFO eviction forgets the
// cache entry, never its durable binding. SQL uses the isolated metadata pool.
type Service struct {
	repo    Repository
	ioSlots chan struct{}
	mu      sync.Mutex
	cache   map[Identity]Route
	order   []Identity
	next    int
}

const cacheCapacity = 4096
const repositoryConcurrency = 32

var ErrBusy = errors.New("codex session metadata busy")

func New(repo Repository) *Service {
	return &Service{repo: repo, cache: make(map[Identity]Route), ioSlots: make(chan struct{}, repositoryConcurrency)}
}

func valid(id Identity) bool {
	return id.AccountID != "" && id.KeyID != "" && len(id.SessionID) == 36
}

func (s *Service) remember(id Identity, route Route) error {
	if !route.Valid() {
		return gatewaycontract.ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.cache[id]; ok {
		if prior != route {
			return gatewaycontract.ErrConflict
		}
		return nil
	}
	if len(s.order) < cacheCapacity {
		s.order = append(s.order, id)
	} else {
		delete(s.cache, s.order[s.next])
		s.order[s.next] = id
		s.next = (s.next + 1) % cacheCapacity
	}
	s.cache[id] = route
	return nil
}

func (s *Service) Lookup(ctx context.Context, id Identity) (Route, error) {
	if !valid(id) || s == nil || s.repo == nil {
		return Route{}, gatewaycontract.ErrConflict
	}
	s.mu.Lock()
	route, ok := s.cache[id]
	s.mu.Unlock()
	if ok {
		return route, nil
	}
	select {
	case s.ioSlots <- struct{}{}:
		defer func() { <-s.ioSlots }()
	default:
		return Route{}, ErrBusy
	}
	route, err := s.repo.LookupSession(ctx, id)
	if err != nil {
		return Route{}, err
	}
	return route, s.remember(id, route)
}

func (s *Service) Claim(ctx context.Context, id Identity, proposed Route) (Route, error) {
	if !valid(id) || !proposed.Valid() || s == nil || s.repo == nil {
		return Route{}, gatewaycontract.ErrConflict
	}
	select {
	case s.ioSlots <- struct{}{}:
		defer func() { <-s.ioSlots }()
	default:
		return Route{}, ErrBusy
	}
	route, err := s.repo.ClaimSession(ctx, id, proposed)
	if err != nil {
		return Route{}, err
	}
	return route, s.remember(id, route)
}
