package snapshot

import (
	"context"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
)

// The stores below sit between a domain service and its database store. Each
// write that changes what the snapshots hold calls Invalidate once the store
// has returned, which is after the transaction committed; reads pass through
// untouched. A write that fails does not invalidate: its transaction rolled
// back, so nothing changed (routing.Store.Set is the one exception, see
// reads.go).
//
// Every wrapped interface has two lists, <name>Writes (the methods that
// invalidate, with what they invalidate) and <name>Others (the methods that
// change nothing the snapshots hold). TestEveryStoreMethodIsClassified fails
// when a method of the interface is in neither, so a new write method cannot
// slip past the snapshots: add it to a list and, if it writes, hook it below.
// Anything that still slips through (a statement outside these stores) is
// picked up by the periodic rebuild.

var keyStoreWrites = map[string]Domain{
	"Create":        DomainKeys,
	"EnsureDefault": DomainKeys,
	"Update":        DomainKeys,
	// Deleting a key deletes its routing preferences with it.
	"Delete": DomainKeys | DomainRoutes,
}

// RecordReveal writes only an audit row.
var keyStoreOthers = []string{"List", "Get", "Credential", "RecordReveal"}

// Keys wraps the store behind the API key service: creating (the default key
// the first time its owner asks for it included), changing, disabling and
// deleting a key take effect for the gateway before the call returns.
func (c *Cache) Keys(raw apikey.Store) apikey.Store { return keyStore{Store: raw, c: c} }

type keyStore struct {
	apikey.Store
	c *Cache
}

func (s keyStore) Create(ctx context.Context, key apikey.Sealed) (apikey.Key, error) {
	created, err := s.Store.Create(ctx, key)
	if err == nil {
		s.c.Invalidate(ctx, keyStoreWrites["Create"])
	}
	return created, err
}

func (s keyStore) EnsureDefault(ctx context.Context, key apikey.Sealed) (bool, error) {
	created, err := s.Store.EnsureDefault(ctx, key)
	if err == nil && created {
		s.c.Invalidate(ctx, keyStoreWrites["EnsureDefault"])
	}
	return created, err
}

func (s keyStore) Update(ctx context.Context, ownerID, id string, update apikey.Update) (apikey.Key, error) {
	updated, err := s.Store.Update(ctx, ownerID, id, update)
	if err == nil {
		s.c.Invalidate(ctx, keyStoreWrites["Update"])
	}
	return updated, err
}

func (s keyStore) Delete(ctx context.Context, ownerID, id string) error {
	err := s.Store.Delete(ctx, ownerID, id)
	if err == nil {
		s.c.Invalidate(ctx, keyStoreWrites["Delete"])
	}
	return err
}

var channelStoreWrites = map[string]Domain{
	"Create":     DomainFleet,
	"Update":     DomainFleet, // status, address, credential, advanced settings and models
	"SoftDelete": DomainFleet,
	"SaveTests":  DomainFleet, // may rewrite the formats of the models
	"Suspend":    DomainFleet, // an administrator takes a channel down
	"Unsuspend":  DomainFleet,
}

// RecordEvent writes the channel's event log, which no snapshot holds.
var channelStoreOthers = []string{"Get", "ListByOwner", "ListAll", "Credential", "Events", "RecordEvent"}

// channelStoreEdits are the writes that change a channel that already exists.
// Once they have succeeded the hook of OnChannelEdited runs, so the gateway
// forgets the 4xx answers the channel gave under its old configuration (Epic
// #360, G4b). SaveTests counts only when it applies the probe results, which
// rewrites the formats of the models; a probe that only records them changes
// nothing. A new channel has no history, so Create is the one write that is
// not an edit. Every method of channelStoreWrites is in exactly one of the two
// lists, which TestEveryChannelWriteIsDecidedOnForTheAnomalyStats checks.
var (
	channelStoreEdits    = []string{"Update", "SoftDelete", "SaveTests", "Suspend", "Unsuspend"}
	channelStoreNotEdits = []string{"Create"}
)

// OnChannelEdited registers fn, which is called with the channel's ID after a
// successful edit (channelStoreEdits) has invalidated the snapshots, before
// the write returns. The server wires the engine's ChannelEdited to it. There
// is at most one; registering again replaces it.
func (c *Cache) OnChannelEdited(fn func(channelID string)) { c.edited.Store(&fn) }

func (c *Cache) channelEdited(id string) {
	if fn := c.edited.Load(); fn != nil {
		(*fn)(id)
	}
}

// Channels wraps the store behind the channel service: listing, unlisting,
// suspending, deleting and every change to a channel or its models take effect
// for the gateway before the call returns.
func (c *Cache) Channels(raw channel.Store) channel.Store { return channelStore{Store: raw, c: c} }

type channelStore struct {
	channel.Store
	c *Cache
}

func (s channelStore) Create(ctx context.Context, in channel.NewChannel) (channel.Channel, error) {
	created, err := s.Store.Create(ctx, in)
	if err == nil {
		s.c.Invalidate(ctx, channelStoreWrites["Create"])
	}
	return created, err
}

func (s channelStore) Update(ctx context.Context, id string, update channel.Update) (channel.Channel, error) {
	updated, err := s.Store.Update(ctx, id, update)
	if err == nil {
		s.c.Invalidate(ctx, channelStoreWrites["Update"])
		s.c.channelEdited(id)
	}
	return updated, err
}

func (s channelStore) SoftDelete(ctx context.Context, id string) error {
	err := s.Store.SoftDelete(ctx, id)
	if err == nil {
		s.c.Invalidate(ctx, channelStoreWrites["SoftDelete"])
		s.c.channelEdited(id)
	}
	return err
}

func (s channelStore) SaveTests(ctx context.Context, id string, results []channel.TestOutcome, testedAt time.Time, apply bool) (channel.Channel, error) {
	saved, err := s.Store.SaveTests(ctx, id, results, testedAt, apply)
	if err == nil {
		s.c.Invalidate(ctx, channelStoreWrites["SaveTests"])
		if apply {
			s.c.channelEdited(id)
		}
	}
	return saved, err
}

func (s channelStore) Suspend(ctx context.Context, actorID, id, reason string) (channel.Channel, error) {
	suspended, err := s.Store.Suspend(ctx, actorID, id, reason)
	if err == nil {
		s.c.Invalidate(ctx, channelStoreWrites["Suspend"])
		s.c.channelEdited(id)
	}
	return suspended, err
}

func (s channelStore) Unsuspend(ctx context.Context, actorID, id, reason string) (channel.Channel, error) {
	restored, err := s.Store.Unsuspend(ctx, actorID, id, reason)
	if err == nil {
		s.c.Invalidate(ctx, channelStoreWrites["Unsuspend"])
		s.c.channelEdited(id)
	}
	return restored, err
}

var catalogStoreWrites = map[string]Domain{
	// A new model takes over the custom models of the same name: they become
	// official, disabled channel models waiting for a multiplier.
	"CreateModel": DomainModels | DomainFleet,
	// A change of currency resets the channels' exchange-rate discount.
	"UpdateModel": DomainModels | DomainFleet,
	// Deleting a model deletes its routing preferences and the channel models
	// of deleted channels.
	"DeleteModel": DomainModels | DomainFleet | DomainRoutes,
}

var catalogStoreOthers = []string{"ListModels", "GetModel"}

// Catalog wraps the store behind the catalog service: creating, changing
// (prices, tiers, currency, enabling) and deleting a model take effect for the
// gateway before the call returns. Its reads stay on the database; the engine
// reads the catalog through Models.
func (c *Cache) Catalog(raw catalog.Store) catalog.Store { return catalogStore{Store: raw, c: c} }

type catalogStore struct {
	catalog.Store
	c *Cache
}

func (s catalogStore) CreateModel(ctx context.Context, actorID string, model catalog.Model) (catalog.Model, error) {
	created, err := s.Store.CreateModel(ctx, actorID, model)
	if err == nil {
		s.c.Invalidate(ctx, catalogStoreWrites["CreateModel"])
	}
	return created, err
}

func (s catalogStore) UpdateModel(ctx context.Context, actorID, id string, mutate func(catalog.Model) (catalog.Model, error)) (catalog.Model, error) {
	updated, err := s.Store.UpdateModel(ctx, actorID, id, mutate)
	if err == nil {
		s.c.Invalidate(ctx, catalogStoreWrites["UpdateModel"])
	}
	return updated, err
}

func (s catalogStore) DeleteModel(ctx context.Context, actorID, id string) error {
	err := s.Store.DeleteModel(ctx, actorID, id)
	if err == nil {
		s.c.Invalidate(ctx, catalogStoreWrites["DeleteModel"])
	}
	return err
}

var identityStoreWrites = map[string]Domain{
	// Disabling an account stops its keys and takes its channels out of the
	// candidates; enabling it again brings them back.
	"UpdateAccount": DomainKeys | DomainFleet,
}

// The remaining methods change sessions, passwords and new accounts, which
// no snapshot holds: a new account has no keys, no channels and no cached
// balance yet, and the first key it asks for goes through the key store.
var identityStoreOthers = []string{
	"FindAccountByUsername", "FindAccountByID", "FindAccountBySession", "CreateSession", "DeleteSession",
	"ReplacePasswordAndSessions", "CreateAccount", "CreateBootstrapAdmin", "HasAdministrator", "ListAccounts",
	"GetAccount", "ResetPassword",
}

// Identity wraps the store behind the identity service: disabling or
// re-enabling an account, and a new credit limit, take effect for the gateway
// before the call returns.
func (c *Cache) Identity(raw identity.Store) identity.Store { return identityStore{Store: raw, c: c} }

type identityStore struct {
	identity.Store
	c *Cache
}

func (s identityStore) UpdateAccount(ctx context.Context, actorID, accountID string, update identity.AccountUpdate) (identity.AdminAccount, error) {
	updated, err := s.Store.UpdateAccount(ctx, actorID, accountID, update)
	if err == nil {
		s.c.Invalidate(ctx, identityStoreWrites["UpdateAccount"])
		// The credit limit is part of the cached balance entry.
		s.c.InvalidateBalances(accountID)
	}
	return updated, err
}
