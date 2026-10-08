package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalogsync"
)

func TestProviderWhitelistSelectionCleanupAndPreservation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	svc := catalog.NewService(e.store.Catalog)
	feed := map[string]string{"a/shared": "first", "b/shared": "second", "a/version:0@date": "first", "a/extra": "other"}
	entries := func(rate string) []catalogsync.Entry {
		out := []catalogsync.Entry{}
		for key, provider := range feed {
			raw := json.RawMessage(fmt.Sprintf(`{"mode":"chat","provider":%q,"input_cost_per_token":0.000001,"output_cost_per_token":0}`, provider))
			out = append(out, catalogsync.Parse(key, raw, rate))
		}
		return out
	}
	run := func() {
		t.Helper()
		if err := e.store.Catalog.RunSync(ctx, func(rate string) ([]catalogsync.Entry, error) { return entries(rate), nil }); err != nil {
			t.Fatal(err)
		}
	}
	run()
	if _, err := svc.Get(ctx, "shared"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("unconfigured imports")
	}
	status, _ := e.store.Catalog.SyncStatus(ctx)
	if len(status.AvailableProviders) != 3 || status.ProvidersConfigured {
		t.Fatal(status)
	}
	configure := func(providers ...string) {
		t.Helper()
		if err := e.store.Catalog.SetSyncConfig(ctx, e.admin.id, "1", providers); err != nil {
			t.Fatal(err)
		}
	}
	configure("second", "first")
	run()
	m, _ := svc.Get(ctx, "shared")
	if m.Source.Key != "b/shared" || m.DisplayName != "b/shared" {
		t.Fatal(m)
	}
	if _, err := svc.Get(ctx, "extra"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("excluded provider imported")
	}
	if _, err := svc.Get(ctx, "version:0@date"); err != nil {
		t.Fatal(err)
	}
	configure("first", "second")
	run()
	m, _ = svc.Get(ctx, "shared")
	if m.Source.Key != "a/shared" || m.DisplayName != "a/shared" {
		t.Fatal("priority did not switch", m)
	}
	// An opt-out model remains unchanged even if its provider is excluded.
	no := false
	if _, err := svc.Update(ctx, e.admin.id, "shared", catalog.ModelPatch{SyncEnabled: &no}); err != nil {
		t.Fatal(err)
	}
	configure()
	run()
	if _, err := svc.Get(ctx, "version:0@date"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("unused model not removed")
	}
	m, err := svc.Get(ctx, "shared")
	if err != nil || m.Source.SyncEnabled {
		t.Fatal("optout lost")
	}
	if _, err := svc.Get(ctx, "gpt-test"); err != nil {
		t.Fatal("manual model removed")
	}
	// Ordinary exclusion is not a tombstone. Re-selecting restores the source.
	configure("first")
	run()
	if _, err := svc.Get(ctx, "version:0@date"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, e.admin.id, "version:0@date"); err != nil {
		t.Fatal(err)
	}
	feed["different/version:0@date"] = "second"
	configure("second", "first")
	run()
	if _, err := svc.Get(ctx, "version:0@date"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("deleted model reappeared via other provider")
	}
	// Explicit API Key references and historical calls retain their exact old IDs.
	for _, id := range []string{"by-key", "by-alias", "by-call", "by-route", "by-channel", "unused"} {
		feed["first/"+id] = "first"
	}
	configure("first")
	run()
	member := e.member("refs", "0")
	keyID, _ := e.defaultKey(member)
	allowed := []string{"by-key"}
	aliases := map[string]string{"nice": "by-alias"}
	if _, err := e.store.Keys.Update(ctx, member.id, keyID, apikey.Update{AllowedModels: &allowed, ModelAliases: &aliases}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO calls(account_id,api_key_id,model_id,requested_model,format,outcome) VALUES($1,$2,'by-call','by-call','openai_chat','rejected_no_channel')`, member.id, keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO route_prefs(account_id,model_id,mode) VALUES($1,'by-route','cheapest')`, member.id); err != nil {
		t.Fatal(err)
	}
	e.createChannel(member, channelSpec{name: "reference", model: "by-channel", upstream: okUpstream(t), multiplier: "1"})
	configure()
	run()
	for _, id := range []string{"by-key", "by-alias", "by-call", "by-route", "by-channel"} {
		m, err := svc.Get(ctx, id)
		if err != nil || m.Source.Status != "retained" || m.Source.RetainedReason == "" {
			t.Fatalf("not retained %s %+v %v", id, m, err)
		}
	}
	if _, err := svc.Get(ctx, "unused"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("unused retained")
	}
	// Store validation prevents a stale service-level known-model check from writing dangling refs.
	stale := []string{"unused"}
	if _, err := e.store.Keys.Update(ctx, member.id, keyID, apikey.Update{AllowedModels: &stale}); !errors.Is(err, apikey.ErrInvalidInput) {
		t.Fatalf("stale reference accepted %v", err)
	}
}

func TestWhitelistFetchConfigABAAndLegacyIdentity(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	svc := catalog.NewService(e.store.Catalog)
	raw := json.RawMessage(`{"mode":"chat","provider":"first","input_cost_per_token":0.000001,"output_cost_per_token":0}`)
	// Simulate a v0.9 source identity without rewriting or deleting its local data.
	legacy := catalogsync.Parse("first/short", raw, "1")
	legacy.Model.ID = "first-short-legacyhash"
	legacy.Model.DisplayName = "first/short"
	if _, err := svc.Create(ctx, e.admin.id, legacy.Model); err != nil {
		t.Fatal(err)
	}
	info, _ := json.Marshal(catalog.SourceInfo{Key: legacy.Key, SyncEnabled: true, Status: "ready", Problems: []string{}, Warnings: []string{}, Metadata: map[string]json.RawMessage{"provider": json.RawMessage(`"first"`)}, PriceReady: true})
	if _, err := e.pool.Exec(ctx, `INSERT INTO model_sources(source_key,model_id,raw_record,info) VALUES($1,$2,$3,$4)`, legacy.Key, legacy.Model.ID, raw, info); err != nil {
		t.Fatal(err)
	}
	fetch := func(rate string) ([]catalogsync.Entry, error) {
		return []catalogsync.Entry{catalogsync.Parse("first/short", raw, rate)}, nil
	}
	if err := e.store.Catalog.RunSync(ctx, fetch); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, legacy.Model.ID); err != nil {
		t.Fatal("initial config cleaned legacy")
	}
	_ = e.store.Catalog.SetSyncConfig(ctx, e.admin.id, "1", []string{"first"})
	err := e.store.Catalog.RunSync(ctx, func(rate string) ([]catalogsync.Entry, error) {
		_ = e.store.Catalog.SetSyncConfig(ctx, e.admin.id, "1", []string{"second"})
		_ = e.store.Catalog.SetSyncConfig(ctx, e.admin.id, "1", []string{"first"})
		return fetch(rate)
	})
	if err == nil {
		t.Fatal("ABA config update not detected")
	}
	if _, err := svc.Get(ctx, legacy.Model.ID); err != nil {
		t.Fatal("failed config race cleaned legacy")
	}
	if err := e.store.Catalog.RunSync(ctx, fetch); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, legacy.Model.ID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("unused legacy not cleaned")
	}
	m, err := svc.Get(ctx, "short")
	if err != nil || m.DisplayName != "first/short" {
		t.Fatal(m, err)
	}
}

func TestProviderWhitelistMigrationPreservesV09Data(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	migration, err := os.ReadFile("../database/migrations/0002_catalog_provider_whitelist.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(migration), "-- +goose Down")
	// Exercise the real migration SQL on v0.9-shaped rows, inside this isolated schema.
	if _, err = e.pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = e.pool.Exec(ctx, `UPDATE catalog_sync SET exchange_rate='7'; INSERT INTO model_sources(source_key,model_id,ignored,sync_enabled,raw_record,info) VALUES('provider/removed','legacy-deleted',true,false,'{}','{"status":"ready"}'),('provider/gpt-test','gpt-test',true,false,'{}','{"status":"conflict"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err = e.pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	status, err := e.store.Catalog.SyncStatus(ctx)
	if err != nil || status.ExchangeRate != "7" || status.ProvidersConfigured || len(status.Providers) != 0 {
		t.Fatalf("migration lost config %+v %v", status, err)
	}
	var removed, conflict bool
	if err = e.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_deleted_models WHERE model_id='removed'), EXISTS(SELECT 1 FROM catalog_deleted_models WHERE model_id='gpt-test')`).Scan(&removed, &conflict); err != nil || !removed || conflict {
		t.Fatal("tombstone distinction lost", err)
	}
	var count int
	if err = e.pool.QueryRow(ctx, `SELECT count(*) FROM models`).Scan(&count); err != nil || count != 3 {
		t.Fatal("migration destroyed existing models", count, err)
	}
}
