package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// fakeStore is an in-memory implementation of every Feature A store, so
// handler tests run the real services and response builders end to end.
type fakeStore struct {
	mu       sync.Mutex
	clock    time.Time
	nextID   int
	accounts map[string]*fakeAccount
	sessions map[string]identity.Session
	balances map[string]money.Amount // keyed by account ID or system code
	entries  []ledger.EntryView
	txByKey  map[string]ledger.Posted
	models   map[string]catalog.Model
	settings settings.Settings
	audit    []audit.Entry
}

type fakeAccount struct {
	identity.Account
	passwordHash string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		clock:    time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC),
		accounts: map[string]*fakeAccount{},
		sessions: map[string]identity.Session{},
		balances: map[string]money.Amount{},
		txByKey:  map[string]ledger.Posted{},
		models:   map[string]catalog.Model{},
		settings: settings.Settings{
			FeeRateNano: 1_000_000, C2CPaymentTimeoutMinutes: 30, DefaultMaxAttempts: 3, DefaultTTFTTimeoutMS: 30000,
			DefaultTotalTimeoutMS: 600000, DefaultCooldownFailures: 3, DefaultCooldownSeconds: 300, ExtraBlockedHosts: []string{},
		},
	}
}

func (s *fakeStore) id() string {
	s.nextID++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", s.nextID)
}

func (s *fakeStore) record(actorID, action, targetType, targetID, reason string) {
	entry := audit.Entry{ID: int64(len(s.audit) + 1), Action: action, TargetType: targetType, TargetID: targetID, Reason: reason, Detail: []byte(`{"k":1}`), CreatedAt: s.clock}
	if actor, ok := s.accounts[actorID]; ok {
		entry.Actor = &audit.Actor{ID: actor.ID, Username: actor.Username, DisplayName: actor.DisplayName}
	}
	s.audit = append(s.audit, entry)
}

func (s *fakeStore) admin(id string) identity.AdminAccount {
	account := s.accounts[id]
	return identity.AdminAccount{Account: account.Account, Balance: s.balances[id]}
}

// --- identity.Store ---

func (s *fakeStore) FindAccountByUsername(_ context.Context, username string) (identity.AccountWithPassword, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, account := range s.accounts {
		if account.Username == username {
			return identity.AccountWithPassword{Account: account.Account, PasswordHash: account.passwordHash}, nil
		}
	}
	return identity.AccountWithPassword{}, identity.ErrNotFound
}

func (s *fakeStore) FindAccountByID(_ context.Context, id string) (identity.AccountWithPassword, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[id]
	if !ok {
		return identity.AccountWithPassword{}, identity.ErrNotFound
	}
	return identity.AccountWithPassword{Account: account.Account, PasswordHash: account.passwordHash}, nil
}

func (s *fakeStore) FindAccountBySession(_ context.Context, hash []byte, now time.Time) (identity.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[string(hash)]
	if !ok || !session.ExpiresAt.After(now) {
		return identity.Account{}, identity.ErrNotFound
	}
	account := s.accounts[session.AccountID]
	if account.Status != identity.StatusActive || account.PasswordVersion != session.PasswordVersion {
		return identity.Account{}, identity.ErrNotFound
	}
	return account.Account, nil
}

func (s *fakeStore) CreateSession(_ context.Context, session identity.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[string(session.TokenHash)] = session
	return nil
}

func (s *fakeStore) DeleteSession(_ context.Context, hash []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, string(hash))
	return nil
}

func (s *fakeStore) deleteSessions(accountID string) {
	for key, session := range s.sessions {
		if session.AccountID == accountID {
			delete(s.sessions, key)
		}
	}
}

func (s *fakeStore) ReplacePasswordAndSessions(_ context.Context, accountID string, expected int64, hash string, session identity.Session, changedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	account := s.accounts[accountID]
	if account.PasswordVersion != expected {
		return identity.ErrConflict
	}
	account.passwordHash, account.PasswordVersion, account.MustChangePassword, account.PasswordChangedAt = hash, expected+1, false, &changedAt
	s.deleteSessions(accountID)
	s.sessions[string(session.TokenHash)] = session
	return nil
}

func (s *fakeStore) insert(account identity.NewAccount) *fakeAccount {
	creditLimit := s.settings.DefaultCreditLimit
	if account.CreditLimit != nil {
		creditLimit = *account.CreditLimit
	}
	created := &fakeAccount{Account: identity.Account{
		ID: s.id(), Username: account.Username, DisplayName: account.DisplayName, IsAdmin: account.IsAdmin,
		Status: identity.StatusActive, MustChangePassword: account.MustChangePassword, PasswordVersion: 1,
		CreditLimit: creditLimit, CreatedAt: s.clock, UpdatedAt: s.clock,
	}, passwordHash: account.PasswordHash}
	s.accounts[created.ID] = created
	s.balances[created.ID] = 0
	s.record(account.ActorID, audit.ActionAccountCreated, "account", created.ID, "")
	return created
}

func (s *fakeStore) CreateAccount(_ context.Context, account identity.NewAccount) (identity.AdminAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.accounts {
		if existing.Username == account.Username {
			return identity.AdminAccount{}, identity.ErrConflict
		}
	}
	return s.admin(s.insert(account).ID), nil
}

func (s *fakeStore) CreateBootstrapAdmin(_ context.Context, account identity.NewAccount) (identity.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.accounts {
		if existing.IsAdmin {
			return identity.Account{}, identity.ErrConflict
		}
	}
	return s.insert(account).Account, nil
}

func (s *fakeStore) HasAdministrator(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, account := range s.accounts {
		if account.IsAdmin {
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeStore) ListAccounts(_ context.Context, filter identity.AccountFilter) ([]identity.AdminAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []identity.AdminAccount
	for id, account := range s.accounts {
		if account.Username <= filter.AfterCursor || (filter.Status != "" && account.Status != filter.Status) ||
			(filter.Query != "" && !strings.Contains(account.Username, filter.Query)) {
			continue
		}
		result = append(result, s.admin(id))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Username < result[j].Username })
	if len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func (s *fakeStore) GetAccount(_ context.Context, id string) (identity.AdminAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[id]; !ok {
		return identity.AdminAccount{}, identity.ErrNotFound
	}
	return s.admin(id), nil
}

func (s *fakeStore) UpdateAccount(_ context.Context, actorID, id string, update identity.AccountUpdate) (identity.AdminAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[id]
	if !ok {
		return identity.AdminAccount{}, identity.ErrNotFound
	}
	next := account.Account
	if update.DisplayName != nil {
		next.DisplayName = *update.DisplayName
	}
	if update.Status != nil {
		next.Status = *update.Status
	}
	if update.CreditLimit != nil {
		next.CreditLimit = *update.CreditLimit
	}
	if update.IsAdmin != nil {
		next.IsAdmin = *update.IsAdmin
	}
	if account.IsAdmin && account.Status == identity.StatusActive && !(next.IsAdmin && next.Status == identity.StatusActive) {
		active := 0
		for _, other := range s.accounts {
			if other.IsAdmin && other.Status == identity.StatusActive {
				active++
			}
		}
		if active <= 1 {
			return identity.AdminAccount{}, identity.ErrLastAdministrator
		}
	}
	account.Account = next
	if next.Status == identity.StatusDisabled {
		s.deleteSessions(id)
	}
	s.record(actorID, audit.ActionAccountUpdated, "account", id, "")
	return s.admin(id), nil
}

func (s *fakeStore) ResetPassword(_ context.Context, actorID, id, hash string, changedAt time.Time) (identity.AdminAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[id]
	if !ok {
		return identity.AdminAccount{}, identity.ErrNotFound
	}
	account.passwordHash, account.PasswordVersion, account.MustChangePassword, account.PasswordChangedAt = hash, account.PasswordVersion+1, true, &changedAt
	s.deleteSessions(id)
	s.record(actorID, audit.ActionAccountPasswordReset, "account", id, "")
	return s.admin(id), nil
}

// --- ledger.Store ---

func (s *fakeStore) Points(_ context.Context, accountID string) (ledger.Points, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[accountID]
	if !ok {
		return ledger.Points{}, ledger.ErrNotFound
	}
	return ledger.Points{Balance: s.balances[accountID], CreditLimit: account.CreditLimit, UpdatedAt: s.clock}, nil
}

func (s *fakeStore) ListEntries(_ context.Context, filter ledger.EntryFilter) ([]ledger.EntryView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []ledger.EntryView
	for index := len(s.entries) - 1; index >= 0; index-- {
		entry := s.entries[index]
		if entry.RelatedID != filter.AccountID || (filter.BeforeID > 0 && entry.ID >= filter.BeforeID) || (filter.Type != "" && entry.Type != filter.Type) {
			continue
		}
		result = append(result, entry)
		if len(result) == filter.Limit {
			break
		}
	}
	return result, nil
}

func (s *fakeStore) post(key string, kind ledger.TransactionType, accountID, reason string, amount money.Amount, counterparty ledger.SystemCode) ledger.Posted {
	if posted, ok := s.txByKey[key]; ok {
		posted.Replayed = true
		return posted
	}
	s.balances[accountID] += amount
	s.balances[string(counterparty)] -= amount
	posted := ledger.Posted{ID: s.id(), Type: kind, CreatedAt: s.clock, Entries: []ledger.PostedEntry{
		{Account: ledger.User(accountID), Amount: amount, BalanceAfter: s.balances[accountID]},
		{Account: ledger.System(counterparty), Amount: -amount, BalanceAfter: s.balances[string(counterparty)]},
	}}
	s.txByKey[key] = posted
	s.entries = append(s.entries, ledger.EntryView{
		ID: int64(len(s.entries) + 1), TransactionID: posted.ID, Type: kind, Reason: reason,
		RelatedType: "account", RelatedID: accountID, Amount: amount, BalanceAfter: s.balances[accountID], CreatedAt: s.clock,
	})
	return posted
}

func (s *fakeStore) Adjust(_ context.Context, adjustment ledger.Adjustment) (ledger.Posted, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[adjustment.AccountID]; !ok {
		return ledger.Posted{}, ledger.ErrNotFound
	}
	posted := s.post(adjustment.IdempotencyKey, ledger.TypeAdminAdjust, adjustment.AccountID, adjustment.Reason, adjustment.Amount, ledger.SystemPlatformRevenue)
	s.record(adjustment.ActorID, audit.ActionLedgerAdjust, "account", adjustment.AccountID, adjustment.Reason)
	return posted, nil
}

func (s *fakeStore) WriteOff(_ context.Context, writeOff ledger.WriteOff) (ledger.Posted, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if posted, ok := s.txByKey[writeOff.IdempotencyKey]; ok {
		posted.Replayed = true
		return posted, nil
	}
	if s.balances[writeOff.AccountID] >= 0 {
		return ledger.Posted{}, ledger.ErrNothingToWriteOff
	}
	posted := s.post(writeOff.IdempotencyKey, ledger.TypeBadDebtWriteOff, writeOff.AccountID, writeOff.Reason, -s.balances[writeOff.AccountID], ledger.SystemBadDebt)
	s.record(writeOff.ActorID, audit.ActionLedgerWriteOff, "account", writeOff.AccountID, writeOff.Reason)
	return posted, nil
}

// --- catalog.Store ---

func (s *fakeStore) ListModels(_ context.Context, includeDisabled bool) ([]catalog.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []catalog.Model
	for _, model := range s.models {
		if model.Enabled || includeDisabled {
			result = append(result, model)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (s *fakeStore) GetModel(_ context.Context, id string) (catalog.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	model, ok := s.models[id]
	if !ok {
		return catalog.Model{}, catalog.ErrNotFound
	}
	return model, nil
}

func (s *fakeStore) CreateModel(_ context.Context, actorID string, model catalog.Model) (catalog.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.models[model.ID]; exists {
		return catalog.Model{}, catalog.ErrConflict
	}
	model.CreatedAt, model.UpdatedAt = s.clock, s.clock
	s.models[model.ID] = model
	s.record(actorID, audit.ActionModelCreated, "model", model.ID, "")
	return model, nil
}

func (s *fakeStore) UpdateModel(_ context.Context, actorID, id string, mutate func(catalog.Model) (catalog.Model, error)) (catalog.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.models[id]
	if !ok {
		return catalog.Model{}, catalog.ErrNotFound
	}
	next, err := mutate(current)
	if err != nil {
		return catalog.Model{}, err
	}
	s.models[id] = next
	s.record(actorID, audit.ActionModelUpdated, "model", id, "")
	return next, nil
}

// --- settings.Store and audit.Store ---

func (s *fakeStore) GetSettings(context.Context) (settings.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.settings
	value.UpdatedAt = s.clock
	return value, nil
}

func (s *fakeStore) UpdateSettings(_ context.Context, actorID string, value settings.Settings) (settings.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value.UpdatedAt = s.clock
	s.settings = value
	s.record(actorID, audit.ActionSettingsUpdated, "settings", "platform", "")
	return value, nil
}

func (s *fakeStore) ListAudit(_ context.Context, filter audit.Filter) ([]audit.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []audit.Entry
	for index := len(s.audit) - 1; index >= 0 && len(result) < filter.Limit; index-- {
		entry := s.audit[index]
		if (filter.BeforeID > 0 && entry.ID >= filter.BeforeID) || (filter.Action != "" && entry.Action != filter.Action) {
			continue
		}
		result = append(result, entry)
	}
	return result, nil
}

func newFakeHandler(store *fakeStore) http.Handler {
	identityService, err := identity.NewService(store, time.Hour)
	if err != nil {
		panic(err)
	}
	return NewHandler(Dependencies{
		Identity: identityService, Catalog: catalog.NewService(store), Ledger: ledger.NewService(store),
		Settings: settings.NewService(store), Audit: audit.NewService(store), CookieSecure: true,
	})
}

func jsonBody(value any) *bytes.Reader {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return bytes.NewReader(encoded)
}
