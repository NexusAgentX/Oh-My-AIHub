package snapshot

import (
	"context"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
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
