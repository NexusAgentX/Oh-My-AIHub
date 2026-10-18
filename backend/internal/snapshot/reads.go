package snapshot

import (
	"context"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

// Gateway returns the store the engine reads through: key lookup, candidates,
// the online models, the Responses stickiness and the balances come from
// memory, everything else (opening and closing calls, spend, stats) goes to
// raw.
//
// A key the snapshot does not know is unknown: there is no fallback to the
// database. The snapshot holds every key, so a missing one does not exist;
// and a key that was just created is in it, because creating it rebuilt the
// snapshot before the creating request was answered.
func (c *Cache) Gateway(raw gateway.Store) gateway.Store { return gatewayStore{Store: raw, c: c} }

type gatewayStore struct {
	gateway.Store
	c *Cache
}

func (s gatewayStore) LookupKey(ctx context.Context, hash []byte) (gateway.KeyAuth, error) {
	keys, err := s.c.keys.get(ctx)
	if err != nil {
		return gateway.KeyAuth{}, err
	}
	key, found := keys[string(hash)]
	if !found {
		return gateway.KeyAuth{}, gateway.ErrKeyNotFound
	}
	return key, nil
}

// Candidates returns a copy the caller may change (the engine sets each
// candidate's rate); the values inside it are shared and must not be.
func (s gatewayStore) Candidates(ctx context.Context, modelID string, format channel.Format) ([]gateway.Candidate, error) {
	fleet, err := s.c.fleet.get(ctx)
	if err != nil {
		return nil, err
	}
	list := fleet.candidates[candidateKey{model: modelID, format: format}]
	candidates := make([]gateway.Candidate, len(list))
	copy(candidates, list)
	return candidates, nil
}

func (s gatewayStore) AuxiliaryCandidates(ctx context.Context, format channel.Format) ([]gateway.CandidateRecord, error) {
	fleet, err := s.c.fleet.get(ctx)
	if err != nil {
		return nil, err
	}
	return append([]gateway.CandidateRecord{}, fleet.auxiliary[format]...), nil
}

// Points is the balance and credit limit for the check before a request is let
// through. It comes from the balance cache (balances.go), which the ledger's
// bookings keep current; the engine never learns the difference.
func (s gatewayStore) Points(ctx context.Context, accountID string) (ledger.Points, error) {
	return s.c.balances.get(ctx, accountID)
}

func (s gatewayStore) OnlineModels(ctx context.Context) ([]gateway.OnlineModel, error) {
	fleet, err := s.c.fleet.get(ctx)
	if err != nil {
		return nil, err
	}
	return fleet.online, nil
}

func (s gatewayStore) CustomModelOnline(ctx context.Context, modelID string) (bool, error) {
	fleet, err := s.c.fleet.get(ctx)
	if err != nil {
		return false, err
	}
	_, online := fleet.customOnline[modelID]
	return online, nil
}

// ResponseChannel answers from the recent Responses turns this process has
// closed, and asks the database for older ones.
func (s gatewayStore) ResponseChannel(ctx context.Context, accountID, responseID string) (string, error) {
	if channelID, found := s.c.sticky.get(accountID, responseID); found {
		return channelID, nil
	}
	channelID, err := s.Store.ResponseChannel(ctx, accountID, responseID)
	if err == nil {
		s.c.sticky.put(accountID, responseID, channelID)
	}
	return channelID, err
}

// FinishCall closes the call and remembers which channel answered it, for the
// next Responses turn that continues from it.
func (s gatewayStore) FinishCall(ctx context.Context, finish gateway.CallFinish) (gateway.FinishResult, error) {
	result, err := s.Store.FinishCall(ctx, finish)
	if err == nil && result.Finished && finish.UpstreamResponseID != "" && finish.ChannelID != "" {
		s.c.sticky.put(finish.AccountID, finish.UpstreamResponseID, finish.ChannelID)
	}
	return result, err
}

// What each method of gateway.Store does here. The first list is answered from
// the snapshots and the balance cache; the others go to the database store and
// change nothing they hold (TouchKey only stamps last_used_at,
// RecordChannelEvent only appends to the event log, FinishCall's posting
// reaches the balance cache through the ledger's Books).
// TestEveryStoreMethodIsClassified keeps both lists complete.
var (
	gatewayStoreSnapshot = []string{"LookupKey", "Candidates", "OnlineModels", "CustomModelOnline", "ResponseChannel", "Points"}
	gatewayStoreOthers   = []string{
		"TouchKey", "KeySpend", "AttemptHistory", "InsertCall", "FinishCall",
		"SweepStaleCalls", "RecordChannelEvent",
	}
)

var routingStoreWrites = map[string]Domain{"Set": DomainRoutes, "Delete": DomainRoutes}

// Resolve is answered from the snapshot, the rest reads the database.
var routingStoreOthers = []string{"Get", "ListForKey", "Resolve"}

// Routing returns the routing store the engine resolves preferences with. The
// resolution reads the snapshot; every other method, the writes included, goes
// to raw, and a write rebuilds the snapshot before it returns.
func (c *Cache) Routing(raw routing.Store) routing.Store { return routingStore{Store: raw, c: c} }

type routingStore struct {
	routing.Store
	c *Cache
}

// Resolve is routing.Store.Resolve over the snapshot: the key's own
// preference, else the account's, else the default.
func (s routingStore) Resolve(ctx context.Context, accountID, keyID, modelID string) (routing.Pref, error) {
	prefs, err := s.c.routes.get(ctx)
	if err != nil {
		return routing.Pref{}, err
	}
	if keyID != "" {
		if pref, found := prefs[routeKey{account: accountID, key: keyID, model: modelID}]; found {
			return pref, nil
		}
	}
	if pref, found := prefs[routeKey{account: accountID, model: modelID}]; found {
		return pref, nil
	}
	return routing.Default(modelID), nil
}

// Set rebuilds even when it fails: the preference is written and committed
// before the stored row is read back, and that read can fail afterwards.
func (s routingStore) Set(ctx context.Context, accountID, keyID string, pref routing.Pref) (routing.Pref, error) {
	saved, err := s.Store.Set(ctx, accountID, keyID, pref)
	s.c.Invalidate(ctx, routingStoreWrites["Set"])
	return saved, err
}

func (s routingStore) Delete(ctx context.Context, accountID, keyID, modelID string) error {
	err := s.Store.Delete(ctx, accountID, keyID, modelID)
	if err == nil {
		s.c.Invalidate(ctx, routingStoreWrites["Delete"])
	}
	return err
}

// Models returns the catalog the engine reads: names, prices and tiers come
// from the snapshot. Writes go through Catalog.
func (c *Cache) Models() gateway.Models { return modelReader{c: c} }

type modelReader struct{ c *Cache }

func (r modelReader) Get(ctx context.Context, id string) (catalog.Model, error) {
	models, err := r.c.models.get(ctx)
	if err != nil {
		return catalog.Model{}, err
	}
	model, found := models.byID[strings.TrimSpace(id)]
	if !found {
		return catalog.Model{}, catalog.ErrNotFound
	}
	return model, nil
}

func (r modelReader) List(ctx context.Context, includeDisabled bool) ([]catalog.Model, error) {
	models, err := r.c.models.get(ctx)
	if err != nil {
		return nil, err
	}
	if includeDisabled {
		return models.all, nil
	}
	return models.enabled, nil
}
