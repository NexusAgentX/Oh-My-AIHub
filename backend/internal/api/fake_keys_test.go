package api

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

var fakeUUIDCounter atomic.Int64

// fakeUUID returns a fresh lower-case version-4 UUID for records the fakes create.
func fakeUUID() string {
	return fmt.Sprintf("20000000-0000-4000-8000-%012d", fakeUUIDCounter.Add(1))
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return slices.Clone(values)
}

// fakeKeyStore is an in-memory apikey.Store. Like the PostgreSQL store it never
// returns nil slices or maps for the list-valued key fields.
type fakeKeyStore struct {
	mu          sync.Mutex
	clock       time.Time
	keys        []*fakeStoredKey
	hasDefault  map[string]bool
	revealCount int
}

type fakeStoredKey struct {
	apikey.Key
	credential channel.EncryptedCredential
}

func newFakeKeyStore() *fakeKeyStore {
	return &fakeKeyStore{clock: time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC), hasDefault: map[string]bool{}}
}

func (s *fakeKeyStore) insert(sealed apikey.Sealed, isDefault bool) apikey.Key {
	aliases := map[string]string{}
	for alias, target := range sealed.ModelAliases {
		aliases[alias] = target
	}
	key := apikey.Key{
		ID: sealed.ID, OwnerID: sealed.OwnerID, Name: sealed.Name, Prefix: sealed.Prefix, Status: sealed.Status, IsDefault: isDefault,
		ExpiresAt: sealed.ExpiresAt, AllowedModels: nonNilStrings(sealed.AllowedModels), BudgetDaily: sealed.BudgetDaily,
		BudgetMonthly: sealed.BudgetMonthly, BudgetTotal: sealed.BudgetTotal, ModelAliases: aliases, RoutedModels: []string{},
		CreatedAt: s.clock, UpdatedAt: s.clock,
	}
	s.keys = append(s.keys, &fakeStoredKey{Key: key, credential: sealed.Credential})
	return key
}

func (s *fakeKeyStore) count(ownerID string) int {
	total := 0
	for _, key := range s.keys {
		if key.OwnerID == ownerID {
			total++
		}
	}
	return total
}

func (s *fakeKeyStore) Create(_ context.Context, sealed apikey.Sealed) (apikey.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count(sealed.OwnerID) >= apikey.MaxPerOwner {
		return apikey.Key{}, apikey.ErrLimitReached
	}
	return s.insert(sealed, false), nil
}

func (s *fakeKeyStore) EnsureDefault(_ context.Context, sealed apikey.Sealed) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasDefault[sealed.OwnerID] {
		return false, nil
	}
	s.hasDefault[sealed.OwnerID] = true
	s.insert(sealed, true)
	return true, nil
}

func (s *fakeKeyStore) find(ownerID, id string) (*fakeStoredKey, error) {
	for _, key := range s.keys {
		if key.ID == id && key.OwnerID == ownerID {
			return key, nil
		}
	}
	return nil, apikey.ErrNotFound
}

func (s *fakeKeyStore) List(_ context.Context, ownerID string) ([]apikey.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := []apikey.Key{}
	for _, key := range s.keys {
		if key.OwnerID == ownerID {
			keys = append(keys, key.Key)
		}
	}
	return keys, nil
}

func (s *fakeKeyStore) Get(_ context.Context, ownerID, id string) (apikey.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, err := s.find(ownerID, id)
	if err != nil {
		return apikey.Key{}, err
	}
	return key.Key, nil
}

func (s *fakeKeyStore) Update(_ context.Context, ownerID, id string, update apikey.Update) (apikey.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, err := s.find(ownerID, id)
	if err != nil {
		return apikey.Key{}, err
	}
	if update.Name != nil {
		key.Name = *update.Name
	}
	if update.Status != nil {
		key.Status = *update.Status
	}
	if update.ExpiresAt != nil {
		key.ExpiresAt = *update.ExpiresAt
	}
	if update.AllowedModels != nil {
		key.AllowedModels = nonNilStrings(*update.AllowedModels)
	}
	if update.BudgetDaily != nil {
		key.BudgetDaily = *update.BudgetDaily
	}
	if update.BudgetMonthly != nil {
		key.BudgetMonthly = *update.BudgetMonthly
	}
	if update.BudgetTotal != nil {
		key.BudgetTotal = *update.BudgetTotal
	}
	if update.ModelAliases != nil {
		key.ModelAliases = map[string]string{}
		for alias, target := range *update.ModelAliases {
			key.ModelAliases[alias] = target
		}
	}
	key.UpdatedAt = s.clock.Add(time.Minute)
	return key.Key, nil
}

func (s *fakeKeyStore) Delete(_ context.Context, ownerID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, err := s.find(ownerID, id)
	if err != nil {
		return err
	}
	s.keys = slices.DeleteFunc(s.keys, func(candidate *fakeStoredKey) bool { return candidate == key })
	return nil
}

func (s *fakeKeyStore) Credential(_ context.Context, ownerID, id string) (channel.EncryptedCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, err := s.find(ownerID, id)
	if err != nil {
		return channel.EncryptedCredential{}, err
	}
	return key.credential, nil
}

func (s *fakeKeyStore) RecordReveal(_ context.Context, ownerID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.find(ownerID, id); err != nil {
		return err
	}
	s.revealCount++
	return nil
}

// fakeRoutingStore is an in-memory routing.Store: key-level preferences
// override the account-level one, which overrides the default.
type fakeRoutingStore struct {
	mu    sync.Mutex
	clock time.Time
	prefs map[string]routing.Pref
}

func newFakeRoutingStore() *fakeRoutingStore {
	return &fakeRoutingStore{clock: time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC), prefs: map[string]routing.Pref{}}
}

func routingKey(accountID, keyID, modelID string) string {
	return accountID + "|" + keyID + "|" + modelID
}

func (s *fakeRoutingStore) Set(_ context.Context, accountID, keyID string, pref routing.Pref) (routing.Pref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pref.Source = routing.SourceAccount
	if keyID != "" {
		pref.Source = routing.SourceKey
	}
	updated := s.clock
	pref.UpdatedAt = &updated
	s.prefs[routingKey(accountID, keyID, pref.ModelID)] = pref
	return pref, nil
}

func (s *fakeRoutingStore) Delete(_ context.Context, accountID, keyID, modelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := routingKey(accountID, keyID, modelID)
	if _, ok := s.prefs[key]; !ok {
		return routing.ErrNotFound
	}
	delete(s.prefs, key)
	return nil
}

func (s *fakeRoutingStore) Get(_ context.Context, accountID, keyID, modelID string) (routing.Pref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pref, ok := s.prefs[routingKey(accountID, keyID, modelID)]
	if !ok {
		return routing.Pref{}, routing.ErrNotFound
	}
	return pref, nil
}

func (s *fakeRoutingStore) ListForKey(_ context.Context, accountID, keyID string) ([]routing.Pref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefs := []routing.Pref{}
	for key, pref := range s.prefs {
		if strings.HasPrefix(key, accountID+"|"+keyID+"|") {
			prefs = append(prefs, pref)
		}
	}
	sort.Slice(prefs, func(i, j int) bool { return prefs[i].ModelID < prefs[j].ModelID })
	return prefs, nil
}

func (s *fakeRoutingStore) Resolve(ctx context.Context, accountID, keyID, modelID string) (routing.Pref, error) {
	for _, scope := range []string{keyID, ""} {
		if pref, err := s.Get(ctx, accountID, scope, modelID); err == nil {
			return pref, nil
		}
	}
	return routing.Default(modelID), nil
}
