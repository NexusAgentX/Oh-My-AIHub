package postgres

import (
	"context"
	"log/slog"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/snapshot"
)

// Cached is the Store with the process-memory snapshots of the gateway's
// read-mostly data in front of it (ADR-0040). Wire the services that write
// that data to the stores here, not to the plain ones: every write through
// them rebuilds the affected snapshots before it returns, and that is what
// makes a disabled key, a disabled account or an unlisted channel stop working
// at once.
//
// The stores that are not listed here (ledger, settings, forum, C2C, observe,
// audit) write nothing the snapshots hold. What they book does reach the
// gateway's balance cache, whichever way their services are built: the cache
// subscribes to the Books every posting goes through (Store.Books).
type Cached struct {
	Cache *snapshot.Cache

	// For the gateway engine.
	Gateway gateway.Store
	Models  gateway.Models
	Routing routing.Store

	// For the services that change the data behind the snapshots.
	Keys     apikey.Store
	Identity identity.Store
}

// Cached builds the snapshots over the stores of s. Call Cache.Load before
// serving, and run Cache.Run for the periodic rebuild. Options adjust the
// snapshot configuration (tests shorten the rebuild interval).
func (s *Store) Cached(logger *slog.Logger, options ...func(*snapshot.Config)) *Cached {
	config := snapshot.Config{Loader: snapshotLoader{s}, Logger: logger}
	for _, option := range options {
		option(&config)
	}
	cache := snapshot.New(config)
	// Every ledger posting of this store, from any service, reports to the
	// balance cache once its transaction has ended.
	s.Books.Observe(cache.Balances())
	return &Cached{
		Cache:    cache,
		Gateway:  cache.Gateway(s.Gateway),
		Models:   cache.Models(),
		Routing:  cache.Routing(s.Routes),
		Keys:     cache.Keys(s.Keys),
		Identity: cache.Identity(s.Identity),
	}
}

// snapshotLoader reads authentication, preferences and balances. Market
// models/candidates are supplied only by groupchannel.Wrap; an unwired market
// remains empty instead of reading retired global configuration.
type snapshotLoader struct{ store *Store }

func (l snapshotLoader) LoadKeys(ctx context.Context) ([]gateway.KeyRecord, error) {
	return l.store.Gateway.LoadKeys(ctx)
}

func (l snapshotLoader) LoadCandidates(ctx context.Context) ([]gateway.CandidateRecord, error) {
	return []gateway.CandidateRecord{}, nil
}

func (l snapshotLoader) LoadRoutePrefs(ctx context.Context) ([]routing.Scoped, error) {
	return l.store.Routes.LoadPrefs(ctx)
}

func (l snapshotLoader) LoadModels(ctx context.Context) ([]catalog.Model, error) {
	return []catalog.Model{}, nil
}

func (l snapshotLoader) LoadPoints(ctx context.Context, accountIDs []string) (map[string]ledger.Points, error) {
	return l.store.Ledger.ListPoints(ctx, accountIDs)
}
