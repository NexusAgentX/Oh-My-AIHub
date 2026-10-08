// Package keypg is the PostgreSQL implementation of apikey.Store and
// routing.Store: platform API keys and per-model routing preferences.
package keypg

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: New(pool)} }

func decodeAliases(raw []byte) map[string]string {
	aliases := map[string]string{}
	_ = json.Unmarshal(raw, &aliases)
	return aliases
}

func toKey(row ApiKey) apikey.Key {
	return apikey.Key{
		ID: row.ID, OwnerID: row.OwnerID, Name: row.Name, Prefix: row.Prefix, Status: apikey.Status(row.Status),
		ExpiresAt: row.ExpiresAt, AllowedModels: nonNil(row.AllowedModels), BudgetDaily: row.BudgetDailyNano,
		BudgetMonthly: row.BudgetMonthlyNano, BudgetTotal: row.BudgetTotalNano, ModelAliases: decodeAliases(row.ModelAliases),
		RoutedModels: []string{}, LastUsedAt: row.LastUsedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// hydrate fills spend, routed models and the default flag (the oldest key of
// the owner is its default key).
func hydrate(ctx context.Context, q *Queries, keys []apikey.Key, oldestID string) ([]apikey.Key, error) {
	if len(keys) == 0 {
		return keys, nil
	}
	ids := make([]string, len(keys))
	for index, key := range keys {
		ids[index] = key.ID
	}
	now := time.Now()
	spend, err := q.KeySpend(ctx, KeySpendParams{DayStart: localtime.DayStart(now), MonthStart: localtime.MonthStart(now), KeyIds: ids})
	if err != nil {
		return nil, err
	}
	spendByKey := map[string]apikey.Spend{}
	for _, row := range spend {
		if row.ApiKeyID != nil {
			spendByKey[*row.ApiKeyID] = apikey.Spend{Today: money.FromNano(row.TodayNano), Month: money.FromNano(row.MonthNano), Total: money.FromNano(row.TotalNano)}
		}
	}
	routed, err := q.KeyRoutedModels(ctx, ids)
	if err != nil {
		return nil, err
	}
	routedByKey := map[string][]string{}
	for _, row := range routed {
		if row.ApiKeyID != nil {
			routedByKey[*row.ApiKeyID] = append(routedByKey[*row.ApiKeyID], row.ModelID)
		}
	}
	for index := range keys {
		keys[index].Spend = spendByKey[keys[index].ID]
		if models, ok := routedByKey[keys[index].ID]; ok {
			keys[index].RoutedModels = models
		}
		keys[index].IsDefault = keys[index].ID == oldestID
	}
	return keys, nil
}

func insert(ctx context.Context, q *Queries, sealed apikey.Sealed) error {
	aliases, err := json.Marshal(nonNilMap(sealed.ModelAliases))
	if err != nil {
		return err
	}
	return q.InsertKey(ctx, InsertKeyParams{
		ID: sealed.ID, OwnerID: sealed.OwnerID, Name: sealed.Name, Prefix: sealed.Prefix, KeyHash: sealed.Hash,
		KeyCiphertext: sealed.Credential.Ciphertext, KeyNonce: sealed.Credential.Nonce, KeyKeyID: sealed.Credential.KeyID,
		Status: string(sealed.Status), ExpiresAt: sealed.ExpiresAt, AllowedModels: nonNil(sealed.AllowedModels),
		BudgetDailyNano: sealed.BudgetDaily, BudgetMonthlyNano: sealed.BudgetMonthly, BudgetTotalNano: sealed.BudgetTotal,
		ModelAliases: aliases,
	})
}

func nonNilMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

func (s *Store) one(ctx context.Context, q *Queries, ownerID, id string) (apikey.Key, error) {
	row, err := q.GetOwnerKey(ctx, GetOwnerKeyParams{OwnerID: ownerID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return apikey.Key{}, apikey.ErrNotFound
	}
	if err != nil {
		return apikey.Key{}, err
	}
	all, err := q.ListOwnerKeys(ctx, ownerID)
	if err != nil {
		return apikey.Key{}, err
	}
	hydrated, err := hydrate(ctx, q, []apikey.Key{toKey(row)}, all[0].ID)
	if err != nil {
		return apikey.Key{}, err
	}
	return hydrated[0], nil
}

func (s *Store) Create(ctx context.Context, sealed apikey.Sealed) (apikey.Key, error) {
	var created apikey.Key
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		if err := q.LockAccount(ctx, sealed.OwnerID); err != nil {
			return err
		}
		count, err := q.CountOwnerKeys(ctx, sealed.OwnerID)
		if err != nil {
			return err
		}
		if count >= apikey.MaxPerOwner {
			return apikey.ErrLimitReached
		}
		if err := insert(ctx, q, sealed); err != nil {
			return err
		}
		created, err = s.one(ctx, q, sealed.OwnerID, sealed.ID)
		return err
	})
	return created, err
}

func (s *Store) EnsureDefault(ctx context.Context, sealed apikey.Sealed) (bool, error) {
	created := false
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		if _, err := q.MarkDefaultKeyCreated(ctx, sealed.OwnerID); errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		created = true
		return insert(ctx, q, sealed)
	})
	return created, err
}

func (s *Store) List(ctx context.Context, ownerID string) ([]apikey.Key, error) {
	rows, err := s.q.ListOwnerKeys(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	keys := make([]apikey.Key, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, toKey(row))
	}
	oldest := ""
	if len(rows) > 0 {
		oldest = rows[0].ID
	}
	return hydrate(ctx, s.q, keys, oldest)
}

func (s *Store) Get(ctx context.Context, ownerID, id string) (apikey.Key, error) {
	return s.one(ctx, s.q, ownerID, id)
}

func (s *Store) Update(ctx context.Context, ownerID, id string, update apikey.Update) (apikey.Key, error) {
	var updated apikey.Key
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		row, err := q.GetOwnerKeyForUpdate(ctx, GetOwnerKeyForUpdateParams{OwnerID: ownerID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return apikey.ErrNotFound
		}
		if err != nil {
			return err
		}
		key := toKey(row)
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
			key.AllowedModels = *update.AllowedModels
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
			key.ModelAliases = *update.ModelAliases
		}
		aliases, err := json.Marshal(nonNilMap(key.ModelAliases))
		if err != nil {
			return err
		}
		if err := q.UpdateKey(ctx, UpdateKeyParams{
			OwnerID: ownerID, ID: id, Name: key.Name, Status: string(key.Status), ExpiresAt: key.ExpiresAt,
			AllowedModels: nonNil(key.AllowedModels), BudgetDailyNano: key.BudgetDaily, BudgetMonthlyNano: key.BudgetMonthly,
			BudgetTotalNano: key.BudgetTotal, ModelAliases: aliases,
		}); err != nil {
			return err
		}
		updated, err = s.one(ctx, q, ownerID, id)
		return err
	})
	return updated, err
}

func (s *Store) Delete(ctx context.Context, ownerID, id string) error {
	return pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		affected, err := q.SoftDeleteKey(ctx, SoftDeleteKeyParams{OwnerID: ownerID, ID: id})
		if err != nil {
			return err
		}
		if affected == 0 {
			return apikey.ErrNotFound
		}
		return q.DeleteKeyRoutes(ctx, &id)
	})
}

func (s *Store) Credential(ctx context.Context, ownerID, id string) (channel.EncryptedCredential, error) {
	row, err := s.q.GetKeyCredential(ctx, GetKeyCredentialParams{OwnerID: ownerID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return channel.EncryptedCredential{}, apikey.ErrNotFound
	}
	if err != nil {
		return channel.EncryptedCredential{}, err
	}
	return channel.EncryptedCredential{Version: 1, KeyID: row.KeyKeyID, Nonce: row.KeyNonce, Ciphertext: row.KeyCiphertext}, nil
}

func (s *Store) RecordReveal(ctx context.Context, ownerID, id string) error {
	return auditpg.Record(ctx, s.pool, auditpg.Event{ActorID: ownerID, Action: audit.ActionAPIKeyReveal, TargetType: "api_key", TargetID: id})
}

// ---- routing.Store ----

// Routes implements routing.Store.
type Routes struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewRoutes(pool *pgxpool.Pool) *Routes { return &Routes{pool: pool, q: New(pool)} }

func scopeKey(keyID string) *string {
	if keyID == "" {
		return nil
	}
	return &keyID
}

func foreignKeyViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23503"
}

func (s *Routes) Set(ctx context.Context, accountID, keyID string, pref routing.Pref) (routing.Pref, error) {
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		saved, err := q.UpsertRoutePref(ctx, UpsertRoutePrefParams{
			AccountID: accountID, ApiKeyID: scopeKey(keyID), ModelID: pref.ModelID, Mode: string(pref.Mode),
			MaxAttempts: pref.MaxAttempts, TtftTimeoutMs: pref.TTFTTimeoutMS,
		})
		if err != nil {
			return err
		}
		if err := q.ClearRoutePrefChannels(ctx, saved.ID); err != nil {
			return err
		}
		position := int32(0)
		for _, channelID := range pref.Order {
			if err := q.InsertRoutePrefChannel(ctx, InsertRoutePrefChannelParams{
				RoutePrefID: saved.ID, ChannelID: channelID, Position: position, Excluded: slices.Contains(pref.Excluded, channelID),
			}); err != nil {
				return err
			}
			position++
		}
		for _, channelID := range pref.Excluded {
			if slices.Contains(pref.Order, channelID) {
				continue
			}
			if err := q.InsertRoutePrefChannel(ctx, InsertRoutePrefChannelParams{RoutePrefID: saved.ID, ChannelID: channelID, Position: position, Excluded: true}); err != nil {
				return err
			}
			position++
		}
		return nil
	})
	if foreignKeyViolation(err) {
		return routing.Pref{}, routing.ErrInvalidInput
	}
	if err != nil {
		return routing.Pref{}, err
	}
	return s.Get(ctx, accountID, keyID, pref.ModelID)
}

func (s *Routes) Delete(ctx context.Context, accountID, keyID, modelID string) error {
	affected, err := s.q.DeleteRoutePref(ctx, DeleteRoutePrefParams{AccountID: accountID, ApiKeyID: scopeKey(keyID), ModelID: modelID})
	if err != nil {
		return err
	}
	if affected == 0 {
		return routing.ErrNotFound
	}
	return nil
}

func (s *Routes) list(ctx context.Context, q *Queries, accountID, keyID, modelID string) ([]routing.Pref, error) {
	rows, err := q.ListRoutePrefs(ctx, ListRoutePrefsParams{AccountID: accountID, ApiKeyID: scopeKey(keyID), ModelID: modelID})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]string, len(rows))
	for index, row := range rows {
		ids[index] = row.ID
	}
	channels, err := q.ListRoutePrefChannels(ctx, ids)
	if err != nil {
		return nil, err
	}
	source := routing.SourceAccount
	if keyID != "" {
		source = routing.SourceKey
	}
	prefs := make([]routing.Pref, 0, len(rows))
	for _, row := range rows {
		updated := row.UpdatedAt
		pref := routing.Pref{
			ModelID: row.ModelID, Source: source, Mode: routing.Mode(row.Mode), Order: []string{}, Excluded: []string{},
			MaxAttempts: row.MaxAttempts, TTFTTimeoutMS: row.TtftTimeoutMs, UpdatedAt: &updated,
		}
		for _, link := range channels {
			if link.RoutePrefID != row.ID {
				continue
			}
			pref.Order = append(pref.Order, link.ChannelID)
			if link.Excluded {
				pref.Excluded = append(pref.Excluded, link.ChannelID)
			}
		}
		prefs = append(prefs, pref)
	}
	return prefs, nil
}

func (s *Routes) Get(ctx context.Context, accountID, keyID, modelID string) (routing.Pref, error) {
	prefs, err := s.list(ctx, s.q, accountID, keyID, modelID)
	if err != nil {
		return routing.Pref{}, err
	}
	if len(prefs) == 0 {
		return routing.Pref{}, routing.ErrNotFound
	}
	return prefs[0], nil
}

func (s *Routes) ListForKey(ctx context.Context, accountID, keyID string) ([]routing.Pref, error) {
	return s.list(ctx, s.q, accountID, keyID, "")
}

func (s *Routes) Resolve(ctx context.Context, accountID, keyID, modelID string) (routing.Pref, error) {
	if keyID != "" {
		pref, err := s.Get(ctx, accountID, keyID, modelID)
		if err == nil {
			return pref, nil
		}
		if !errors.Is(err, routing.ErrNotFound) {
			return routing.Pref{}, err
		}
	}
	pref, err := s.Get(ctx, accountID, "", modelID)
	if errors.Is(err, routing.ErrNotFound) {
		return routing.Default(modelID), nil
	}
	return pref, err
}
