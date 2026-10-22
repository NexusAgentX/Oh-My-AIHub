// Package snapshot keeps the data the gateway reads on every request in
// process memory (ADR-0040): the platform keys, the model catalog, the
// candidate channels of each model and format, the routing preferences, and
// the balance and credit limit of the accounts that call it.
//
// The backend is the only process that writes this data (ADR-0039), so it
// knows when it changes. Each write path calls Invalidate after its
// transaction has committed; the affected snapshots are rebuilt before the
// write returns, so the next request already sees the change. That holds for
// every change, and it is what the security-relevant ones need: a key that
// was disabled or deleted, an account that was disabled, a channel that was
// unlisted, suspended or deleted stops being accepted or chosen the moment
// the administrator's or owner's request is answered. Everything is also
// rebuilt once a minute, which bounds how long a change made behind the
// process's back (manual SQL, a migration) stays unseen.
//
// The balances are not a snapshot but a per-account cache with the same
// discipline (balances.go): a gateway settlement is added to the cached
// balance when its transaction commits, any other posting or a change of a
// credit limit drops the account's entry after the commit, and the entries
// are reloaded every minute. They follow the ledger through the Books of the
// persistence layer, so every posting is seen, not only the hooked ones.
//
// The Cache is a layer behind the interfaces the engine already uses
// (gateway.Store, routing.Store, gateway.Models); the wrappers for the stores
// that write the data are in wrap.go.
package snapshot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

// RefreshInterval is how often everything is rebuilt as a safety net.
const RefreshInterval = 60 * time.Second

// Loader reads the full contents of the snapshots from the database. Each
// method is one consistent read.
type Loader interface {
	// LoadKeys returns every undeleted platform key.
	LoadKeys(ctx context.Context) ([]gateway.KeyRecord, error)
	// LoadCandidates returns every channel model gateway.Store.Candidates
	// can offer, in model and channel order.
	LoadCandidates(ctx context.Context) ([]gateway.CandidateRecord, error)
	// LoadRoutePrefs returns every stored routing preference.
	LoadRoutePrefs(ctx context.Context) ([]routing.Scoped, error)
	// LoadModels returns the whole catalog, disabled models included, in
	// display order.
	LoadModels(ctx context.Context) ([]catalog.Model, error)
	// LoadPoints returns the balance and credit limit of each of the given
	// accounts that exists.
	LoadPoints(ctx context.Context, accountIDs []string) (map[string]ledger.Points, error)
}

// Config wires a Cache.
type Config struct {
	Loader Loader
	Logger *slog.Logger
	// Interval is the period of the full rebuild; zero means RefreshInterval.
	Interval time.Duration
}

// Domain names the snapshots a change touches; combine with |.
type Domain uint8

const (
	// DomainKeys is the platform keys with the state of their owners.
	DomainKeys Domain = 1 << iota
	// DomainModels is the model catalog.
	DomainModels
	// DomainFleet is the channels and their models: the candidates, the
	// models online and the custom ones among them.
	DomainFleet
	// DomainRoutes is the routing preferences.
	DomainRoutes

	domainAll = DomainKeys | DomainModels | DomainFleet | DomainRoutes
)

// Cache is the set of snapshots. It is safe for concurrent use.
type Cache struct {
	logger   *slog.Logger
	interval time.Duration

	keys   *cell[keySet]
	models *cell[modelSet]
	fleet  *cell[fleetSet]
	routes *cell[routeSet]

	balances *balanceBook
}

// New builds a Cache. Nothing is loaded yet: the first read of a snapshot
// loads it, Load loads all of them up front.
func New(config Config) *Cache {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Interval <= 0 {
		config.Interval = RefreshInterval
	}
	loader := config.Loader
	return &Cache{
		logger:   config.Logger,
		interval: config.Interval,
		keys: newCell("keys", func(ctx context.Context) (keySet, error) {
			records, err := loader.LoadKeys(ctx)
			return buildKeys(records), err
		}),
		models: newCell("models", func(ctx context.Context) (modelSet, error) {
			models, err := loader.LoadModels(ctx)
			return buildModels(models), err
		}),
		fleet: newCell("fleet", func(ctx context.Context) (fleetSet, error) {
			records, err := loader.LoadCandidates(ctx)
			return buildFleet(records), err
		}),
		routes: newCell("routes", func(ctx context.Context) (routeSet, error) {
			prefs, err := loader.LoadRoutePrefs(ctx)
			return buildRoutes(prefs), err
		}),
		balances: newBalanceBook(loader.LoadPoints),
	}
}

// Load rebuilds every snapshot now. The server calls it before it starts
// serving, so a database that cannot be read stops the start. ctx limits the
// load.
func (c *Cache) Load(ctx context.Context) error {
	if err := c.run(ctx, domainAll, true); err != nil {
		return err
	}
	keys, models, fleet, routes := c.keys.cur.Load(), c.models.cur.Load(), c.fleet.cur.Load(), c.routes.cur.Load()
	c.logger.Info("snapshots loaded", "keys", len(keys.value), "models", len(models.value.all),
		"candidate_lists", len(fleet.value.candidates), "online_models", len(fleet.value.online), "route_prefs", len(routes.value))
	return nil
}

// Run rebuilds everything once per interval until ctx ends: the safety net
// under the invalidation calls. A failed rebuild is logged and the previous
// snapshot stays in use; reads of a snapshot that a counted change has
// outdated do not use it and retry on their own. The balance entries are
// reloaded in the same round.
func (c *Cache) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := c.run(ctx, domainAll, true); err != nil && ctx.Err() == nil {
			c.logger.Error("snapshots: periodic rebuild failed, keeping the previous snapshots", "error", err)
		}
		if err := c.refreshBalances(ctx); err != nil && ctx.Err() == nil {
			c.logger.Error("snapshots: periodic reload of the balances failed, keeping the previous ones", "error", err)
		}
	}
}

func (c *Cache) refreshBalances(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()
	return c.balances.refresh(ctx)
}

// Balances is the observer that keeps the cached balances behind the ledger:
// subscribe it to the Books of the persistence layer, which then reports
// every posting.
func (c *Cache) Balances() ledger.Observer { return c.balances }

// InvalidateBalances drops the cached balance and credit limit of accounts. A
// write that changes a credit limit calls it after its transaction committed.
func (c *Cache) InvalidateBalances(accountIDs ...string) { c.balances.invalidate(accountIDs...) }

// Invalidate counts a committed change to the given snapshots and rebuilds
// them before it returns. It is the call every write path makes after its
// transaction has committed. The rebuild does not end when ctx does (a client
// that hangs up after the commit must not leave the snapshot behind), but
// keeps its values. A failed rebuild is logged; the snapshot then
// counts as out of date, so no request is answered from it until a rebuild
// succeeds.
func (c *Cache) Invalidate(ctx context.Context, domains Domain) {
	if err := c.run(ctx, domains, false); err != nil {
		c.logger.Error("snapshots: rebuild after a change failed, requests retry it", "error", err)
	}
}

// InvalidateChecked gives publication/configuration services an acknowledged
// barrier: a committed edit is not reported as usable before its rebuild.
func (c *Cache) InvalidateChecked(ctx context.Context, domains Domain) error {
	return c.run(ctx, domains, false)
}

// step is one snapshot as the cache drives it.
type step struct {
	name       string
	invalidate func(context.Context) error
	refresh    func(context.Context) error
}

func (c *Cache) steps(domains Domain) []step {
	var steps []step
	if domains&DomainKeys != 0 {
		steps = append(steps, step{"keys", c.keys.invalidate, c.keys.refresh})
	}
	if domains&DomainModels != 0 {
		steps = append(steps, step{"models", c.models.invalidate, c.models.refresh})
	}
	if domains&DomainFleet != 0 {
		steps = append(steps, step{"fleet", c.fleet.invalidate, c.fleet.refresh})
	}
	if domains&DomainRoutes != 0 {
		steps = append(steps, step{"routes", c.routes.invalidate, c.routes.refresh})
	}
	return steps
}

// run rebuilds the snapshots in domains side by side, as a counted change
// (refresh false) or unconditionally (refresh true), and joins the errors.
func (c *Cache) run(ctx context.Context, domains Domain, refresh bool) error {
	steps := c.steps(domains)
	errs := make([]error, len(steps))
	var wg sync.WaitGroup
	for index, step := range steps {
		wg.Go(func() {
			rebuild := step.invalidate
			if refresh {
				rebuild = step.refresh
			}
			if err := rebuild(ctx); err != nil {
				errs[index] = fmt.Errorf("%s snapshot: %w", step.name, err)
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}
