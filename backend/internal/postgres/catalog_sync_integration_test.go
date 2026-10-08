package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalogsync"
)

func TestCatalogSyncLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	svc := catalog.NewService(e.store.Catalog)
	raw := `{"mode":"chat","provider":"openai","input_cost_per_token":0.000001,"output_cost_per_token":0.000002}`
	run := func(keys ...string) {
		t.Helper()
		err := e.store.Catalog.RunSync(ctx, func(rate string) ([]catalogsync.Entry, error) {
			entries := []catalogsync.Entry{}
			for _, key := range keys {
				entries = append(entries, catalogsync.Parse(key, json.RawMessage(raw), rate))
			}
			return entries, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	get := func(id string) catalog.Model {
		t.Helper()
		m, err := svc.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	run("new-model", "gpt-test")
	m := get("new-model")
	if m.Enabled || m.Source == nil || m.Source.PriceReady || m.Source.Status != "waiting_rate" {
		t.Fatal(m)
	}
	if get("gpt-test").Source != nil {
		t.Fatal("manual model adopted")
	}
	yes, no := true, false
	if _, err := svc.Update(ctx, e.admin.id, m.ID, catalog.ModelPatch{Enabled: &yes}); err == nil {
		t.Fatal("unpriced enabled")
	}
	if err := e.store.Catalog.SetSyncRate(ctx, e.admin.id, "2"); err != nil {
		t.Fatal(err)
	}
	run("new-model")
	m = get(m.ID)
	if !m.Source.PriceReady || m.InputPrice.String() != "2" {
		t.Fatal(m)
	}
	run("new-model")
	status, err := e.store.Catalog.SyncStatus(ctx)
	if err != nil || status.Result["updated"] != 0 {
		t.Fatalf("not idempotent %+v %v", status, err)
	}
	manualPrice := mustAmount(t, "7")
	if _, err := svc.Update(ctx, e.admin.id, m.ID, catalog.ModelPatch{InputPrice: &manualPrice}); err == nil {
		t.Fatal("managed price edited")
	}
	if _, err := svc.Update(ctx, e.admin.id, m.ID, catalog.ModelPatch{SyncEnabled: &no, InputPrice: &manualPrice, Enabled: &yes}); err != nil {
		t.Fatal(err)
	}
	run("new-model")
	if get(m.ID).InputPrice != manualPrice {
		t.Fatal("optout overwritten")
	}
	if _, err := svc.Update(ctx, e.admin.id, m.ID, catalog.ModelPatch{SyncEnabled: &yes}); err != nil {
		t.Fatal(err)
	}
	run("new-model")
	if get(m.ID).InputPrice.String() != "2" || !get(m.ID).Enabled {
		t.Fatal("resume or local flag lost")
	}
	// Source failure and missing row retain last effective model and price.
	if err := e.store.Catalog.RunSync(ctx, func(string) ([]catalogsync.Entry, error) { return nil, errors.New("truncated") }); err == nil {
		t.Fatal("fetch failure lost")
	}
	if get(m.ID).InputPrice.String() != "2" {
		t.Fatal("failure mutated")
	}
	run("other-model")
	if get(m.ID).Source.Status != "missing" || !get(m.ID).Enabled {
		t.Fatal("missing destroyed model")
	}
	if err := svc.Delete(ctx, e.admin.id, m.ID); err != nil {
		t.Fatal(err)
	}
	run("new-model")
	if _, err := svc.Get(ctx, m.ID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("deleted model resurrected", err)
	}
	var systemEvents int
	if err = e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='model.synced' AND actor_id IS NULL`).Scan(&systemEvents); err != nil || systemEvents == 0 {
		t.Fatal(systemEvents, err)
	}
}
func TestCatalogSyncInFlightOptOutAndRateChange(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	svc := catalog.NewService(e.store.Catalog)
	_ = e.store.Catalog.SetSyncRate(ctx, e.admin.id, "1")
	raw := json.RawMessage(`{"mode":"chat","input_cost_per_token":0.000001,"output_cost_per_token":0}`)
	if err := e.store.Catalog.RunSync(ctx, func(rate string) ([]catalogsync.Entry, error) {
		return []catalogsync.Entry{catalogsync.Parse("race-model", raw, rate)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- e.store.Catalog.RunSync(ctx, func(rate string) ([]catalogsync.Entry, error) {
			close(started)
			<-release
			return []catalogsync.Entry{catalogsync.Parse("race-model", raw, rate)}, nil
		})
	}()
	<-started
	no := false
	price := mustAmount(t, "9")
	if _, err := svc.Update(ctx, e.admin.id, "race-model", catalog.ModelPatch{SyncEnabled: &no, InputPrice: &price}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	m, _ := svc.Get(ctx, "race-model")
	if m.InputPrice != price || m.Source.SyncEnabled {
		t.Fatal("inflight optout overwritten")
	}
	err := e.store.Catalog.RunSync(ctx, func(rate string) ([]catalogsync.Entry, error) {
		if err := e.store.Catalog.SetSyncRate(ctx, e.admin.id, "3"); err != nil {
			t.Fatal(err)
		}
		return []catalogsync.Entry{catalogsync.Parse("rate-race", raw, rate)}, nil
	})
	if err == nil {
		t.Fatal("stale rate accepted")
	}
	if _, err := svc.Get(ctx, "rate-race"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("stale model created")
	}
}

func TestCatalogSyncInvalidUpdateRetainsAppliedPriceAndRate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	svc := catalog.NewService(e.store.Catalog)
	_ = e.store.Catalog.SetSyncRate(ctx, e.admin.id, "2")
	run := func(raw string) {
		t.Helper()
		if err := e.store.Catalog.RunSync(ctx, func(rate string) ([]catalogsync.Entry, error) {
			return []catalogsync.Entry{catalogsync.Parse("preserved-model", json.RawMessage(raw), rate)}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	run(`{"mode":"chat","input_cost_per_token":0.000001,"output_cost_per_token":0.000002}`)
	_ = e.store.Catalog.SetSyncRate(ctx, e.admin.id, "3")
	run(`{"mode":"chat","input_cost_per_token":0.000005,"output_cost_per_token":0.000002,"regional_processing_uplift_multiplier_us":1.1}`)
	m, err := svc.Get(ctx, "preserved-model")
	if err != nil || m.InputPrice.String() != "2" || m.Source.AppliedRate != "2" || m.Source.Status != "needs_review" || !m.Source.PriceReady {
		t.Fatalf("last effective price lost %+v %v", m, err)
	}
	run(`{"mode":"chat","input_cost_per_token":0.000005,"output_cost_per_token":0.000002}`)
	m, _ = svc.Get(ctx, m.ID)
	if m.InputPrice.String() != "15" || m.Source.AppliedRate != "3" || m.Source.Status != "ready" {
		t.Fatal(m)
	}
}
