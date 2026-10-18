package typical

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync/atomic"
	"testing"
	"time"
)

type sourceFunc func(context.Context) (Input, error)

func (f sourceFunc) Current(ctx context.Context) (Input, error) { return f(ctx) }

type memoryRepo struct {
	c       Config
	cp      Checkpoint
	raw     []RawCall
	samples map[string]Sample
	snap    *Snapshot
	calls   int
	fail    error
}

func memory(c Config) *memoryRepo {
	return &memoryRepo{c: c, cp: Checkpoint{Generation: 1}, samples: map[string]Sample{}}
}
func (m *memoryRepo) Config(context.Context) (Config, error) { return m.c, m.fail }
func (m *memoryRepo) SetConfig(_ context.Context, _ string, c Config) (Config, error) {
	if c.Version != m.c.Version {
		return Config{}, ErrConflict
	}
	c.Version++
	m.c = c
	return c, nil
}
func (m *memoryRepo) Checkpoint(context.Context) (Checkpoint, error) { return m.cp, nil }
func (m *memoryRepo) Calls(_ context.Context, scan Scan, limit int) ([]RawCall, error) {
	m.calls++
	if m.fail != nil {
		return nil, m.fail
	}
	out := []RawCall{}
	for _, r := range m.raw {
		if !r.CompletedAt.Before(scan.Floor) && !r.CompletedAt.After(scan.Ceiling) && greater(Cursor{At: r.CompletedAt, ID: r.ID}, scan.After) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return greater(Cursor{At: out[j].CompletedAt, ID: out[j].ID}, Cursor{At: out[i].CompletedAt, ID: out[i].ID})
	})
	return out[:min(len(out), limit)], nil
}
func greater(a, b Cursor) bool { return a.At.After(b.At) || a.At.Equal(b.At) && a.ID > b.ID }
func (m *memoryRepo) Ingest(_ context.Context, cp Checkpoint, rows []Sample) error {
	if m.cp.Generation != cp.Generation {
		return ErrConflict
	}
	for _, r := range rows {
		m.samples[r.ID] = r
	}
	cp.Generation++
	m.cp = cp
	return nil
}
func (m *memoryRepo) Samples(_ context.Context, floor time.Time, after Cursor, limit int) ([]Sample, error) {
	out := []Sample{}
	for _, s := range m.samples {
		if !s.CompletedAt.Before(floor) && greater(Cursor{s.CompletedAt, s.ID}, after) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return greater(Cursor{out[j].CompletedAt, out[j].ID}, Cursor{out[i].CompletedAt, out[i].ID})
	})
	return out[:min(len(out), limit)], nil
}
func (m *memoryRepo) Prune(_ context.Context, floor time.Time, limit int) error {
	for id, s := range m.samples {
		if limit > 0 && s.CompletedAt.Before(floor) {
			delete(m.samples, id)
			limit--
		}
	}
	return nil
}
func (m *memoryRepo) LoadSnapshot(context.Context) (*Snapshot, error) { return m.snap, nil }
func (m *memoryRepo) SaveSnapshot(_ context.Context, s *Snapshot) error {
	if s.Config.Version != m.c.Version {
		return ErrStale
	}
	m.snap = s
	return nil
}
func rawSample(s Sample) RawCall {
	lines := []any{}
	for _, r := range s.References {
		lines = append(lines, map[string]any{"model_id": r.ModelID, "group_model_id": r.GroupModelID, "quote_id": r.QuoteID, "quote_version": r.QuoteVersion, "state": "priced", "kind": "main", "usage": s.Facts.Usage})
	}
	raw, _ := json.Marshal(map[string]any{"detail": s.Facts.Usage.Detail, "group_quote": map[string]any{"version": "group-quote.v1", "group_id": s.GroupID, "components": lines}})
	return RawCall{ID: s.ID, ModelID: s.ModelID, Format: "openai_responses", Outcome: "succeeded", UsageStatus: "reported", StartedAt: s.At, CompletedAt: s.CompletedAt, Usage: s.Facts.Usage, Snapshot: raw}
}
func TestIncrementalRepairDeduplicatesAndOldFailureCannotReplaceSnapshot(t *testing.T) {
	in, c, samples, at := fixture(t)
	repo := memory(c)
	samples[0].CompletedAt = at.Add(-time.Minute)
	samples[1].CompletedAt = at.Add(-2 * time.Minute)
	repo.raw = []RawCall{rawSample(samples[0])}
	source := sourceFunc(func(context.Context) (Input, error) { return in, nil })
	service, err := New(t.Context(), repo, source)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return at }
	if err = service.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(repo.samples) != 1 || row(t, repo.snap, "group", "a").Samples != 1 || repo.calls > ScanPages {
		t.Fatal("delta/repair duplicated samples or exceeded page budget", repo.calls)
	}
	first := repo.snap
	repo.fail = errors.New("synthetic DB unavailable")
	if err = service.Step(t.Context()); err == nil {
		t.Fatal("outage published")
	}
	current, status := service.Snapshot()
	if current != first || status.LastError != "update_failed" {
		t.Fatal("last result lost during outage", status)
	}
	repo.fail = nil
	// This transaction became visible only after the first delta waterline.
	repo.raw = append(repo.raw, rawSample(samples[1]))
	service.now = func() time.Time { return at.Add(24 * time.Hour) }
	before := repo.calls
	if err = service.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(repo.samples) != 2 || row(t, repo.snap, "model", "m").Samples != 2 || repo.calls-before > ScanPages {
		t.Fatal("late settlement lost or duplicate counted", repo.calls-before)
	}
	if err = service.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(repo.samples) != 2 {
		t.Fatal("retry inflated samples")
	}
}
func TestInputChangeBeforePublishPreservesPreviousAndConfigIsOptimistic(t *testing.T) {
	in, c, samples, at := fixture(t)
	repo := memory(c)
	repo.raw = []RawCall{rawSample(samples[0])}
	calls := 0
	change := false
	source := sourceFunc(func(context.Context) (Input, error) {
		calls++
		out := in
		if change && calls%2 == 0 {
			out.USDCNYNano++
		}
		return out, nil
	})
	service, err := New(t.Context(), repo, source)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return at }
	if err = service.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	previous := repo.snap
	calls = 0
	change = true
	if err = service.Step(t.Context()); !errors.Is(err, ErrStale) || repo.snap != previous {
		t.Fatal("stale FX job replaced snapshot", err)
	}
	c.TypicalTokens = 100
	updated, err := service.Update(t.Context(), "admin", c)
	if err != nil || updated.Version != 2 {
		t.Fatal(err, updated)
	}
	if _, err = service.Update(t.Context(), "admin", c); !errors.Is(err, ErrConflict) {
		t.Fatal("stale settings overwrote new version", err)
	}
}

func TestCostLookupRequiresCurrentQuoteDiscountAndFXWithoutWork(t *testing.T) {
	in, c, samples, at := fixture(t)
	snap, err := Calculate(t.Context(), in, c, samples, at)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{}
	service.install(snap, in)
	offers, _, err := offerings(in)
	if err != nil {
		t.Fatal(err)
	}
	version := offers["mb"].digest
	if got, ok := service.Lookup("c1", "m", "b", version, 400_000_000, in.USDCNYNano); !ok || got.String() != "0.152" {
		t.Fatal(got, ok)
	}
	for _, bad := range []struct {
		version      string
		discount, fx int64
	}{{"new-quote", 400_000_000, in.USDCNYNano}, {version, 500_000_000, in.USDCNYNano}, {version, 400_000_000, in.USDCNYNano + 1}} {
		if _, ok := service.Lookup("c1", "m", "b", bad.version, bad.discount, bad.fx); ok {
			t.Fatal("stale cost used", bad)
		}
	}
	if allocs := testing.AllocsPerRun(100, func() { _, _ = service.Lookup("c1", "m", "b", version, 400_000_000, in.USDCNYNano) }); allocs != 0 {
		t.Fatal("hot cost lookup allocated", allocs)
	}
}
func BenchmarkTypicalCostLookup(b *testing.B) {
	service := &Service{}
	service.prices.Store(&costIndex{fx: 7_000_000_000, values: map[costKey]costValue{{"channel", "model"}: {group: "group", version: "version", discount: 800_000_000, cost: 1}}})
	b.ReportAllocs()
	for b.Loop() {
		_, _ = service.Lookup("channel", "model", "group", "version", 800_000_000, 7_000_000_000)
	}
}

func TestUnavailableWarmupStartsUnknownAndRecoversWithoutAffectingSettlement(t *testing.T) {
	in, c, samples, at := fixture(t)
	repo := memory(c)
	repo.fail = errors.New("temporary statistics outage")
	service, err := New(t.Context(), repo, sourceFunc(func(context.Context) (Input, error) { return in, nil }))
	if err != nil {
		t.Fatal("optional estimates blocked startup", err)
	}
	snap, status := service.Snapshot()
	if snap != nil || status.LastError != "update_failed" {
		t.Fatal("outage became fabricated estimate", snap, status)
	}
	repo.fail = nil
	repo.raw = []RawCall{rawSample(samples[0])}
	service.now = func() time.Time { return at }
	if err = service.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if snap, _ = service.Snapshot(); snap == nil {
		t.Fatal("statistics failed to recover")
	}
}

type countedRepo struct {
	*memoryRepo
	scans atomic.Int64
}

func (r *countedRepo) Calls(ctx context.Context, scan Scan, limit int) ([]RawCall, error) {
	r.scans.Add(1)
	return r.memoryRepo.Calls(ctx, scan, limit)
}
func TestAutomaticNotificationsCoalesceWithoutRepeatedScansAndManualStillRefreshes(t *testing.T) {
	in, c, samples, at := fixture(t)
	repo := &countedRepo{memoryRepo: memory(c)}
	repo.raw = []RawCall{rawSample(samples[0])}
	service, err := New(t.Context(), repo, sourceFunc(func(context.Context) (Input, error) { return in, nil }))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return at }
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); service.Run(ctx) }()
	defer func() { cancel(); <-done }()
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for !ready() {
			select {
			case <-tick.C:
			case <-deadline.C:
				t.Fatal("worker did not reach expected boundary")
			}
		}
	}
	wait(func() bool {
		snap, status := service.Snapshot()
		return snap != nil && !status.Running && !status.Pending
	})
	before := repo.scans.Load()
	previous, _ := service.Snapshot()
	for range 10 {
		service.Notify()
	}
	wait(func() bool { _, status := service.Snapshot(); return !status.Running && !status.Pending })
	if repo.scans.Load() != before {
		t.Fatal("unchanged probe notifications repeatedly scanned calls")
	}
	if err = service.Trigger(); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		snap, status := service.Snapshot()
		return snap != previous && !status.Running && !status.Pending
	})
	if repo.scans.Load() <= before {
		t.Fatal("explicit refresh did not ingest new calls")
	}
}
