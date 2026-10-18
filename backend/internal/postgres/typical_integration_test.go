package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/typicalpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/typical"
)

func TestTypicalDurableIngestionCheckpointRestartAndHistoricalLedgerUnchanged(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	consumer, provider := e.member("typical-consumer", "100"), e.member("typical-provider", "0")
	key, _ := e.defaultKey(consumer)
	finish := receiptFinish(t, e, consumer, provider, key, "")
	finish.ModelID = "gpt-test"
	finish.Diagnostics = &gateway.Diagnostics{UsageStatus: "reported"}
	if _, err := e.store.Gateway.FinishCall(ctx, finish); err != nil {
		t.Fatal(err)
	}
	var ledgerRows int64
	if err := e.pool.QueryRow(ctx, `SELECT count(*)FROM ledger_entries`).Scan(&ledgerRows); err != nil {
		t.Fatal(err)
	}
	repo := typicalpg.NewStore(e.pool)
	cp, err := repo.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	floor := time.Now().Add(-time.Hour)
	ceiling := time.Now().Add(time.Minute)
	rows, err := repo.Calls(ctx, typical.Scan{Floor: floor, Ceiling: ceiling, After: typical.Cursor{At: floor}}, typical.PageSize)
	if err != nil || len(rows) != 1 {
		t.Fatal("settled call not scanned", len(rows), err)
	}
	sample, ok := typical.Decode(rows[0])
	if !ok {
		t.Fatal("reliable billing facts lost", rows[0])
	}
	sample.Facts.Usage.Detail = nil
	cp.Delta = typical.Cursor{At: rows[0].CompletedAt, ID: rows[0].ID}
	cp.WindowDays = 30
	if err = repo.Ingest(ctx, cp, []typical.Sample{sample}); err != nil {
		t.Fatal(err)
	}
	if err = repo.Ingest(ctx, cp, []typical.Sample{sample}); !errors.Is(err, typical.ErrConflict) {
		t.Fatal("stale worker advanced checkpoint", err)
	}
	repo = typicalpg.NewStore(e.pool)
	cp, err = repo.Checkpoint(ctx)
	if err != nil || cp.Generation != 2 || cp.Delta.ID != finish.ID {
		t.Fatal("restart lost cursor", cp, err)
	}
	if err = repo.Ingest(ctx, cp, []typical.Sample{sample}); err != nil {
		t.Fatal("repair upsert failed", err)
	}
	profiles, err := repo.Samples(ctx, floor, typical.Cursor{At: floor}, typical.PageSize)
	if err != nil || len(profiles) != 1 || profiles[0].ID != finish.ID {
		t.Fatal("repair/restart doubled samples", profiles, err)
	}
	var after int64
	if err = e.pool.QueryRow(ctx, `SELECT count(*)FROM ledger_entries`).Scan(&after); err != nil || after != ledgerRows {
		t.Fatal("derived calculation changed ledger", after, ledgerRows, err)
	}
	if err = repo.Prune(ctx, ceiling, typical.PageSize); err != nil {
		t.Fatal(err)
	}
	profiles, err = repo.Samples(ctx, floor, typical.Cursor{At: floor}, typical.PageSize)
	if err != nil || len(profiles) != 0 {
		t.Fatal("derived pruning failed", profiles, err)
	}
	var calls int
	if err = e.pool.QueryRow(ctx, `SELECT count(*)FROM calls WHERE id=$1`, finish.ID).Scan(&calls); err != nil || calls != 1 {
		t.Fatal("pruning deleted history", calls, err)
	}
}
func TestTypicalSnapshotRejectsOldConfigAndSettingsAreAudited(t *testing.T) {
	e := newEnv(t)
	actor := e.member("typical-settings", "0")
	ctx := context.Background()
	repo := typicalpg.NewStore(e.pool)
	c, err := repo.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	initial := &typical.Snapshot{Version: "typical.v1", Config: c, InputVersion: "synthetic-v1", Items: []typical.Estimate{}}
	if err = repo.SaveSnapshot(ctx, initial); err != nil {
		t.Fatal(err)
	}
	next, err := repo.SetConfig(ctx, actor.id, c)
	if err != nil || next.Version != 2 {
		t.Fatal(next, err)
	}
	if _, err = repo.SetConfig(ctx, actor.id, c); !errors.Is(err, typical.ErrConflict) {
		t.Fatal("lost update allowed", err)
	}
	if err = repo.SaveSnapshot(ctx, initial); !errors.Is(err, typical.ErrStale) {
		t.Fatal("old calculation overwrote new config", err)
	}
	initial.Config = next
	initial.InputVersion = "synthetic-v2"
	if err = repo.SaveSnapshot(ctx, initial); err != nil {
		t.Fatal(err)
	}
	loaded, err := typicalpg.NewStore(e.pool).LoadSnapshot(ctx)
	if err != nil || loaded.InputVersion != "synthetic-v2" || loaded.Config.Version != 2 {
		t.Fatal("restart lost estimate", loaded, err)
	}
	var count int
	if err = e.pool.QueryRow(ctx, `SELECT count(*)FROM audit_log WHERE target_type='typical_cost' AND actor_id=$1`, actor.id).Scan(&count); err != nil || count != 1 {
		t.Fatal("settings audit lost/duplicated", count, err)
	}
}
