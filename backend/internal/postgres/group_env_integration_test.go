package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/quotepricing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/snapshot"
)

// newGroupEnv runs the same database/auth/settlement infrastructure as the old
// integration fixture, using approved group quotes and the real group-channel
// handlers. It never seeds models or channel_models. The explicit synthetic
// stream and capacity facts below exercise forwarding mechanics, not provider
// detection: no report/evidence rows are published or accepted as real proof.
func newGroupEnv(t *testing.T) *env { return newGroupEnvWith(t, nil) }

func newGroupEnvWith(t *testing.T, configure func(*gateway.Dependencies)) *env {
	t.Helper()
	pool, store := isolatedDatabase(t)
	return newGroupEnvOn(t, pool, store, configure)
}

type groupIntegrationEvidence struct {
	snapshot.Loader
	Prices *groupIntegrationPriceSource
	Proofs *groupFixtureProofs
}

func (l groupIntegrationEvidence) LoadCandidates(ctx context.Context) ([]gateway.CandidateRecord, error) {
	rows, err := l.Loader.LoadCandidates(ctx)
	rows = append([]gateway.CandidateRecord(nil), rows...)
	comparisons := &groupComparisonSnapshot{values: map[groupComparisonKey]money.Amount{}}
	for i := range rows {
		c := &rows[i].Candidate
		c.Evidence = map[string]bool{}
		l.Proofs.copyInto(c.ChannelID, c.Evidence)
		c.VerifiedCapacity = map[string]int64{}
		for _, protocol := range []string{"openai-chat-completions", "openai-responses", "native-messages", "native-generate-content"} {
			c.Evidence["upstream_protocol/"+protocol+".stream/text_lifecycle"] = true
			c.VerifiedCapacity[protocol+".length_search@upstream_protocol/max_output"] = 8192
		}
		// An explicit 1000-input/500-output synthetic workload is evaluated
		// off the request path against this immutable CNY quote generation.
		if c.Quotes != nil {
			if offer, ok := c.Quotes.Offers[rows[i].ModelID]; ok && offer.Quote.Currency == ledger.CurrencyCNY {
				statement, calcErr := quotepricing.Calculate(c.Quotes, rows[i].ModelID, quotepricing.Facts{MainReliable: true, Usage: ledger.Usage{InputTokens: 1000, OutputTokens: 500}}, time.Now(), c.MultiplierNano, ledger.UnitRate, 0)
				if calcErr == nil {
					comparisons.values[groupComparisonKey{c.ChannelID, rows[i].ModelID, c.GroupID, c.PricingVersion, c.MultiplierNano}] = statement.Cost
				}
			}
		}
	}
	if err == nil {
		l.Prices.current.Store(comparisons)
	}
	return rows, err
}

func (e *env) publishGroupFixture(changes market.Changes) market.Directory {
	e.t.Helper()
	marketApply(e.t, e.store, e.admin.id, changes)
	d := marketSnapshot(e.t, e.store)
	if err := e.groups.Install(context.Background(), d); err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *env) seedGroupModelIDs() {
	e.t.Helper()
	s := market.Supplier{ID: marketID(e.t), Name: "Synthetic ID directory", Kind: "relay", Enabled: true, WebsiteURL: "https://synthetic.example"}
	g := market.Group{ID: marketID(e.t), SupplierID: s.ID, Name: "Approved IDs without channels", Enabled: true}
	c := market.Changes{Suppliers: []market.Change[market.Supplier]{{Value: s}}, Groups: []market.Change[market.Group]{{Value: g}}}
	for _, model := range []string{"gpt-test", "claude-test", "gemini-test"} {
		c.Models = append(c.Models, market.Change[market.GroupModel]{Value: market.GroupModel{ID: marketID(e.t), GroupID: g.ID, ModelID: model, ModelType: "text", Enabled: true}})
	}
	e.publishGroupFixture(c)
}

func (e *env) createGroupedChannel(owner person, spec channelSpec) string {
	e.t.Helper()
	if spec.model == "" {
		spec.model = "gpt-test"
	}
	if spec.multiplier == "" {
		spec.multiplier = "2"
	}
	if spec.key == "" {
		spec.key = "upstream-key-" + spec.name
	}
	if spec.formats == nil {
		spec.formats = []string{"openai_chat", "openai_responses", "anthropic", "gemini"}
	}
	base := spec.baseURL
	if base == "" {
		base = spec.upstream.server.URL
	}
	u, err := url.Parse(base)
	if err != nil {
		e.t.Fatal(err)
	}
	// The market uses the real hostname normalizer. httptest's certificate
	// includes example.com; only this fixture's transport resolves that host
	// and its exact owned port to its loopback TLS server.
	if u.Hostname() == "127.0.0.1" {
		port := u.Port()
		u.Host = net.JoinHostPort("example.com", port)
		base = u.String()
		e.installGroupLoopback(port)
	}
	d := marketSnapshot(e.t, e.store)
	var address market.Address
	for _, a := range d.Addresses {
		if a.BaseURL == base {
			address = a
		}
	}
	c := market.Changes{}
	if address.ID == "" {
		supplierID := spec.supplierID
		if supplierID == "" {
			supplier := market.Supplier{ID: marketID(e.t), Name: spec.name + " supplier", Kind: "relay", Enabled: true, WebsiteURL: "https://synthetic.example"}
			supplierID = supplier.ID
			c.Suppliers = append(c.Suppliers, market.Change[market.Supplier]{Value: supplier})
		}
		address = market.Address{ID: marketID(e.t), SupplierID: supplierID, BaseURL: base}
		c.Addresses = append(c.Addresses, market.Change[market.Address]{Value: address})
	}
	g := market.Group{ID: marketID(e.t), SupplierID: address.SupplierID, Name: spec.name + " group", Enabled: true}
	m := market.GroupModel{ID: marketID(e.t), GroupID: g.ID, ModelID: spec.model, UpstreamModel: spec.upstreamAs, ModelType: "text", Enabled: true}
	link := market.GroupAddress{ID: marketID(e.t), GroupID: g.ID, AddressID: address.ID, SupplierID: address.SupplierID}
	c.Groups = []market.Change[market.Group]{{Value: g}}
	c.Models = []market.Change[market.GroupModel]{{Value: m}}
	c.GroupAddresses = []market.GroupAddressSet{{GroupID: g.ID, Links: []market.GroupAddress{link}}}
	c.Quotes = []market.Quote{{ID: marketID(e.t), GroupModelID: m.ID, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: mustAmount(e.t, "10"), Output: mustAmount(e.t, "20"), CacheWrite: mustAmount(e.t, "12.5"), CacheRead: mustAmount(e.t, "1")}}}
	d = e.publishGroupFixture(c)
	var overrides market.Defaults
	if spec.advanced != nil {
		raw, _ := json.Marshal(spec.advanced)
		if err := json.Unmarshal(raw, &overrides); err != nil {
			e.t.Fatal(err)
		}
	}
	body := map[string]any{"group_id": g.ID, "name": spec.name, "api_key": spec.key, "overrides": overrides, "models": []any{map[string]any{"group_model_id": m.ID, "discount": spec.multiplier}}}
	created := e.apiJSON(owner, http.StatusCreated, http.MethodPost, "/api/group-channels", body)
	id := created["id"].(string)
	r, err := e.groupStore.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, v := range d.Groups {
		if v.ID == g.ID {
			g = v
		}
	}
	for _, v := range d.Models {
		if v.ID == m.ID {
			m = v
		}
	}
	tests := map[channel.Format]channel.FormatTest{}
	for _, format := range spec.formats {
		tests[channel.Format(format)] = channel.FormatTest{OK: true, TestedAt: time.Now()}
	}
	ok, err := e.groupStore.SaveProbe(context.Background(), r, g, m, groupchannel.Probe{ChannelID: id, GroupModelID: m.ID, Binding: groupchannel.ProbeBinding(r, g, m), Tests: tests, TestedAt: time.Now()})
	if err != nil || !ok {
		e.t.Fatal("synthetic callability fixture rejected", err)
	}
	if err := e.groups.Refresh(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	if spec.status != "" && spec.status != "listed" {
		e.apiJSON(owner, http.StatusOK, http.MethodPatch, "/api/group-channels/"+id, map[string]any{"revision": created["revision"], "status": spec.status})
	}
	return id
}

type groupComparisonKey struct {
	Channel, Model, Group, Version string
	Discount                       int64
}
type groupComparisonSnapshot struct {
	values map[groupComparisonKey]money.Amount
}

func (s *groupComparisonSnapshot) LookupComparison(channel, model, group, version string, discount, _ int64) (money.Amount, string, int64, bool) {
	if s == nil {
		return 0, "", 0, false
	}
	price, ok := s.values[groupComparisonKey{channel, model, group, version, discount}]
	return price, "per_request", 1, ok
}

type groupIntegrationPriceSource struct {
	current atomic.Pointer[groupComparisonSnapshot]
}

func (s *groupIntegrationPriceSource) View() gateway.TypicalPriceLookup { return s.current.Load() }

type groupFixtureProofs struct {
	mu     sync.RWMutex
	values map[string]map[string]bool
}

func (p *groupFixtureProofs) copyInto(channel string, out map[string]bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for name, value := range p.values[channel] {
		out[name] = value
	}
}

// Extra capability facts belong only to a named controlled engine in a
// mechanism test. Validate their pinned manifest names, and never persist them
// as a detection report or permit them outside this _test.go fixture.
func (e *env) declareGroupedCapabilities(channel string, names ...string) {
	e.t.Helper()
	m, err := gatewaycontract.Load()
	if err != nil {
		e.t.Fatal(err)
	}
	known := map[string]bool{}
	for _, suite := range m.Suites {
		for _, requirement := range suite.Requirements {
			for _, assertion := range requirement.Assertions {
				known["upstream_protocol/"+requirement.ID+"/"+assertion] = true
			}
			// Setting evidence projects finite native tier subcapabilities in
			// addition to the requirement's static assertions. These are the
			// pinned tiers explicitly exercised by the pricing/scan fixtures.
			for _, tier := range map[string][]string{
				"openai-chat-completions.cache_tier": {"openai:fast", "openai:flex", "openai:scale"},
				"openai-responses.cache_tier":        {"openai:fast"},
				"native-generate-content.cache_tier": {"gemini:priority"},
			}[requirement.ID] {
				known["upstream_protocol/"+requirement.ID+"/service_tier/"+tier] = true
			}
		}
	}
	for _, name := range names {
		if !known[name] {
			e.t.Fatal("fixture capability has no retained assertion", name)
		}
	}
	e.groupProofs.mu.Lock()
	if e.groupProofs.values[channel] == nil {
		e.groupProofs.values[channel] = map[string]bool{}
	}
	for _, name := range names {
		e.groupProofs.values[channel][name] = true
	}
	e.groupProofs.mu.Unlock()
	if err := e.groups.Refresh(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

// Revise only this channel's approved group-model quote. It exercises the
// append-only publication and refresh path, preserving every prior quote.
func (e *env) reviseGroupedQuote(channel, model string, edit func(*market.Quote)) market.Quote {
	e.t.Helper()
	r, err := e.groupStore.Get(context.Background(), channel)
	if err != nil {
		e.t.Fatal(err)
	}
	d := marketSnapshot(e.t, e.store)
	var quoteID string
	for _, m := range d.Models {
		if m.GroupID == r.GroupID && m.ModelID == model && m.QuoteID != nil {
			quoteID = *m.QuoteID
		}
	}
	for _, q := range d.Quotes {
		if q.ID != quoteID {
			continue
		}
		q.ID, q.Version = marketID(e.t), 0
		edit(&q)
		after := e.publishGroupFixture(market.Changes{Quotes: []market.Quote{q}})
		for _, current := range after.Quotes {
			if current.ID == q.ID {
				return current
			}
		}
	}
	e.t.Fatal("missing approved quote", model)
	return market.Quote{}
}

type groupLoopbackPorts struct {
	mu     sync.RWMutex
	values map[string]bool
}

func groupFixtureOutbound(outbound channel.Outbound) (channel.Outbound, *groupLoopbackPorts) {
	current, ok := outbound.(testOutbound)
	if !ok {
		return outbound, nil
	}
	transport, ok := current.client.Transport.(*http.Transport)
	if !ok {
		return outbound, nil
	}
	registry := &groupLoopbackPorts{values: map[string]bool{}}
	copied := transport.Clone()
	previous := copied.DialContext
	copied.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		registry.mu.RLock()
		owned := registry.values[port]
		registry.mu.RUnlock()
		if host != "example.com" || !owned {
			return nil, fmt.Errorf("unregistered group fixture upstream")
		}
		address = net.JoinHostPort("127.0.0.1", port)
		if previous != nil {
			return previous(ctx, network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	client := *current.client
	client.Transport = copied
	return testOutbound{client: &client}, registry
}
func (e *env) installGroupLoopback(port string) {
	e.t.Helper()
	if e.groupPorts == nil {
		e.t.Fatal("group fixture requires its controlled stable transport")
	}
	e.groupPorts.mu.Lock()
	e.groupPorts.values[port] = true
	e.groupPorts.mu.Unlock()
}

// The patch contains the actual group API fields. The helper supplies its
// optimistic revision by reading through the same owner-scoped handler.
func (e *env) patchGroupedChannel(owner person, id string, fields map[string]any) map[string]any {
	e.t.Helper()
	current := e.apiJSON(owner, 200, "GET", "/api/group-channels/"+id, nil)
	body := map[string]any{"revision": current["revision"]}
	for k, v := range fields {
		body[k] = v
	}
	return e.apiJSON(owner, 200, "PATCH", "/api/group-channels/"+id, body)
}

func (e *env) deleteGroupedChannel(owner person, id string) *httptest.ResponseRecorder {
	e.t.Helper()
	current := e.apiJSON(owner, 200, "GET", "/api/group-channels/"+id, nil)
	return e.api(owner, "DELETE", "/api/group-channels/"+id, map[string]any{"revision": current["revision"]})
}

// These credential-cache tests declare that their controlled engine continues
// to accept the same formats after rotating its synthetic key. Rebind only the
// fixture's prior minimal callability facts; never create a capability report.
func (e *env) replaceGroupedChannelKey(owner person, id, key string) {
	e.t.Helper()
	ctx := context.Background()
	before, err := e.groupStore.Snapshot(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	e.patchGroupedChannel(owner, id, map[string]any{"api_key": key})
	e.rebindGroupedCallability(id, before)
}

func (e *env) rebindGroupedCallability(id string, before groupchannel.Data) {
	e.t.Helper()
	ctx := context.Background()
	r, err := e.groupStore.Get(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	d := marketSnapshot(e.t, e.store)
	var group market.Group
	models := map[string]market.GroupModel{}
	for _, g := range d.Groups {
		if g.ID == r.GroupID {
			group = g
		}
	}
	for _, m := range d.Models {
		models[m.ID] = m
	}
	for _, p := range before.Probes {
		if p.ChannelID != id {
			continue
		}
		p.Binding = groupchannel.ProbeBinding(r, group, models[p.GroupModelID])
		p.TestedAt = time.Now()
		if saved, err := e.groupStore.SaveProbe(ctx, r, group, models[p.GroupModelID], p); err != nil || !saved {
			e.t.Fatal("rotated callability fixture rejected", err)
		}
	}
	if err := e.groups.Refresh(ctx); err != nil {
		e.t.Fatal(err)
	}
}

// Resetting a Key probe is asynchronous in the product. Mechanism fixtures
// explicitly install their declared callability again after verifying pending.
func (e *env) retestGroupedFixture(owner person, id string) {
	e.t.Helper()
	before, err := e.groupStore.Snapshot(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	current := e.apiJSON(owner, 200, "GET", "/api/group-channels/"+id, nil)
	reply := e.apiJSON(owner, 202, "POST", "/api/group-channels/"+id+"/test", map[string]any{"revision": current["revision"]})
	for _, raw := range reply["models"].([]any) {
		if raw.(map[string]any)["probe_state"] != "pending" {
			e.t.Fatal("retest reused prior key proof", reply)
		}
	}
	e.rebindGroupedCallability(id, before)
}

func TestGroupedFixtureStartsEmptyAndUsesApprovedQuotes(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("group-consumer", "100")
	provider := e.member("group-provider", "0")
	var models, legacyChoices int
	if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM models),(SELECT count(*) FROM channel_models)`).Scan(&models, &legacyChoices); err != nil || models != 0 || legacyChoices != 0 {
		t.Fatal("group fixture created global configuration", models, legacyChoices, err)
	}
	_, key := e.defaultKey(consumer)
	empty := e.apiJSON(consumer, 200, "GET", "/api/market/channels", nil)
	if empty["total"] != float64(0) {
		t.Fatal(empty)
	}
	id := e.createChannel(provider, channelSpec{name: "group-provider", upstream: okUpstream(t)})
	reply := e.call(key, "POST", "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if reply.Code != 200 {
		t.Fatalf("group call failed: %d %s", reply.Code, reply.Body.String())
	}
	row := e.callRow(reply.Header().Get("X-AIHub-Request-Id"))
	if row.FinalChannel != id || !row.HasLedgerTx || row.CostNano != 40_000_000 {
		t.Fatalf("group settlement: %+v", row)
	}
	var price string
	if err := e.pool.QueryRow(context.Background(), `SELECT price_snapshot::text FROM calls WHERE id=$1`, reply.Header().Get("X-AIHub-Request-Id")).Scan(&price); err != nil || !strings.Contains(price, "quote_id") {
		t.Fatal("missing independent quote", price, err)
	}
	e.assertZeroSum()
}
