package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/typical"
)

type apiTypicalRepo struct {
	mu sync.Mutex
	typical.Repository
	config  typical.Config
	cp      typical.Checkpoint
	samples []typical.Sample
	snap    *typical.Snapshot
	fail    error
}

func (r *apiTypicalRepo) Config(context.Context) (typical.Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.config, r.fail
}
func (r *apiTypicalRepo) SetConfig(_ context.Context, _ string, c typical.Config) (typical.Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.Version != r.config.Version {
		return typical.Config{}, typical.ErrConflict
	}
	c.Version++
	r.config = c
	return c, r.fail
}
func (r *apiTypicalRepo) Checkpoint(context.Context) (typical.Checkpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cp, r.fail
}
func (r *apiTypicalRepo) Calls(context.Context, typical.Scan, int) ([]typical.RawCall, error) {
	return nil, nil
}
func (r *apiTypicalRepo) Ingest(_ context.Context, cp typical.Checkpoint, _ []typical.Sample) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp.Generation++
	r.cp = cp
	return r.fail
}
func (r *apiTypicalRepo) Samples(_ context.Context, _ time.Time, after typical.Cursor, limit int) ([]typical.Sample, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := []typical.Sample{}
	for _, s := range r.samples {
		if s.CompletedAt.After(after.At) || s.CompletedAt.Equal(after.At) && s.ID > after.ID {
			rows = append(rows, s)
		}
	}
	return reviewClone(rows[:min(limit, len(rows))]), r.fail
}
func (r *apiTypicalRepo) Prune(context.Context, time.Time, int) error { return nil }
func (r *apiTypicalRepo) LoadSnapshot(context.Context) (*typical.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap, r.fail
}
func (r *apiTypicalRepo) SaveSnapshot(_ context.Context, s *typical.Snapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.Config.Version != r.config.Version {
		return typical.ErrStale
	}
	r.snap = s
	return r.fail
}

type apiTypicalSource struct{ input typical.Input }

func (s apiTypicalSource) Current(context.Context) (typical.Input, error) { return s.input, nil }
func typicalFixture(t *testing.T) (*typical.Service, *apiTypicalRepo) {
	t.Helper()
	now := time.Now().UTC().Add(-time.Minute)
	q1, q2 := "synthetic-q1", "synthetic-q2"
	in := typical.Input{USDCNYNano: 7 * ledger.UnitRate, FeeRateNano: 1_000_000, Directory: market.Directory{Suppliers: []market.Supplier{{ID: "supplier", Name: "合成供应商", Kind: "official_api", Enabled: true}}, Groups: []market.Group{{ID: "group", SupplierID: "supplier", Name: "标准分组", Enabled: true}}, Models: []market.GroupModel{{ID: "offering", GroupID: "group", ModelID: "fixture/model:2026", ModelType: "text", QuoteID: &q1, Enabled: true, IsReference: true, Revision: 1}, {ID: "empty-offering", GroupID: "group", ModelID: "fixture/unknown", ModelType: "text", QuoteID: &q2, Enabled: true, Revision: 1}}, Quotes: []market.Quote{{ID: q1, GroupModelID: "offering", Version: 1, Currency: "USD", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: money.FromNano(2_000_000_000), Output: money.FromNano(10_000_000_000), CacheRead: money.FromNano(200_000_000)}}, {ID: q2, GroupModelID: "empty-offering", Version: 1, Currency: "CNY", BillingUnit: "per_million_tokens"}}}, Channels: []typical.Channel{{ID: "synthetic-channel", Name: "折扣渠道", GroupID: "group", ModelID: "fixture/model:2026", DiscountNano: 800_000_000}}}
	c := typical.DefaultConfig()
	c.MinSamples = 1
	sample := typical.Sample{ID: "synthetic-business-call", GroupID: "group", ModelID: "fixture/model:2026", At: now, CompletedAt: now, References: []quotepricing.Reference{{ModelID: "fixture/model:2026", GroupModelID: "offering", QuoteID: q1, QuoteVersion: 1}}, Facts: quotepricing.Facts{MainReliable: true, Usage: ledger.Usage{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 8500}, Units: []quotepricing.Unit{{Unit: "request", QuantityNano: ledger.UnitRate}}}}
	snap, err := typical.Calculate(t.Context(), in, c, []typical.Sample{sample}, now)
	if err != nil {
		t.Fatal(err)
	}
	snap.Watermark = now
	snap.ReconciledAt = now
	repo := &apiTypicalRepo{config: c, cp: typical.Checkpoint{Generation: 1}, snap: snap, samples: []typical.Sample{sample}}
	service, err := typical.New(t.Context(), repo, apiTypicalSource{in})
	if err != nil {
		t.Fatal(err)
	}
	return service, repo
}
func TestTypicalCostContractsPermissionsSettingsAndPagination(t *testing.T) {
	service, _ := typicalFixture(t)
	p := newPlatform(t, func(d *Dependencies) { d.Typical = service })
	admin := &client{spec: p.spec, handler: p.handler}
	admin.expect(t, 201, "POST", "/api/instance/initialize", map[string]string{"username": "admin", "display_name": "管理员", "password": "Admin-Password-2026"})
	member, _ := p.member(t, "typical-member")
	member.expect(t, 403, "GET", "/api/admin/typical-costs/settings", nil)
	member.expect(t, 403, "POST", "/api/admin/typical-costs/refresh", nil)
	first := member.expect(t, 200, "GET", "/api/typical-costs?level=group&limit=1", nil)
	if len(first["items"].([]any)) != 1 || first["next_cursor"] == nil {
		t.Fatal("missing bounded page", first)
	}
	token := first["next_cursor"].(string)
	second := member.expect(t, 200, "GET", "/api/typical-costs?level=group&limit=1&cursor="+token, nil)
	if len(second["items"].([]any)) != 1 {
		t.Fatal("page missing")
	}
	member.expect(t, 409, "GET", "/api/typical-costs?level=model&limit=1&cursor="+token, nil)
	member.expect(t, 400, "GET", "/api/typical-costs?limit=101", nil)
	config := admin.expect(t, 200, "GET", "/api/admin/typical-costs/settings", nil)
	admin.expect(t, 200, "PUT", "/api/admin/typical-costs/settings", config)
	admin.expect(t, 409, "PUT", "/api/admin/typical-costs/settings", config)
	config["version"] = float64(2)
	config["window_days"] = float64(0)
	admin.expect(t, 422, "PUT", "/api/admin/typical-costs/settings", config)
	// The initial request is intentionally still queued: a second request is
	// coalesced, and no unbounded queue or request goroutine is created.
	admin.expect(t, 409, "POST", "/api/admin/typical-costs/refresh", nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); service.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		_, status := service.Snapshot()
		if !status.LastAttempt.IsZero() && !status.Running && !status.Pending {
			break
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("estimate worker did not finish")
		}
	}
	admin.expect(t, 202, "POST", "/api/admin/typical-costs/refresh", nil)
}
