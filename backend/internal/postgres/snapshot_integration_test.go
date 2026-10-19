package postgres_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/snapshot"
)

// The gateway reads keys, the catalog, channels and routing preferences from
// process memory (ADR-0040). These tests run the whole stack on the cached
// stores, as the server does, with no background rebuild: whatever the next
// gateway request sees after an administrative call returned can only come
// from the invalidation that call made.

func chatRequest(e *env, secret string) *httptest.ResponseRecorder {
	return e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
}

// probe sends a chat request and reports what happened: the status, and
// whether it reached an upstream.
type probe struct {
	e       *env
	secret  string
	upstrms []*upstream
}

func (p probe) hits() int {
	total := 0
	for _, upstream := range p.upstrms {
		total += len(upstream.inference())
	}
	return total
}

// expect sends one request and fails unless it ended with the status and did
// (or did not) reach an upstream.
func (p probe) expect(t *testing.T, step string, status int, reached bool) *httptest.ResponseRecorder {
	t.Helper()
	before := p.hits()
	recorder := chatRequest(p.e, p.secret)
	if recorder.Code != status || (p.hits() > before) != reached {
		t.Fatalf("%s: status %d, upstream reached %v; want %d and %v (%s)", step, recorder.Code, p.hits() > before, status, reached, recorder.Body.String())
	}
	return recorder
}

func TestSecurityChangesTakeEffectBeforeTheAdminCallReturns(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	channelID := e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	keyID, secret := e.defaultKey(consumer)
	p := probe{e: e, secret: secret, upstrms: []*upstream{relay}}
	p.expect(t, "before any change", http.StatusOK, true)

	t.Run("disabling a key", func(t *testing.T) {
		e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"status": "disabled"})
		recorder := p.expect(t, "disabled key", http.StatusUnauthorized, false)
		if e.callRow(recorder.Header().Get("X-AIHub-Request-Id")).Outcome != "rejected_key" {
			t.Fatalf("outcome of a disabled key: %s", recorder.Body.String())
		}
		e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"status": "enabled"})
		p.expect(t, "re-enabled key", http.StatusOK, true)
	})

	t.Run("deleting a key", func(t *testing.T) {
		created := e.apiJSON(consumer, http.StatusCreated, http.MethodPost, "/api/keys", map[string]any{"name": "doomed"})
		doomed := probe{e: e, secret: created["secret"].(string), upstrms: []*upstream{relay}}
		doomed.expect(t, "new key", http.StatusOK, true)
		if recorder := e.api(consumer, http.MethodDelete, "/api/keys/"+created["key"].(map[string]any)["id"].(string), nil); recorder.Code != http.StatusNoContent {
			t.Fatalf("delete key = %d %s", recorder.Code, recorder.Body.String())
		}
		doomed.expect(t, "deleted key", http.StatusUnauthorized, false)
		p.expect(t, "the other key", http.StatusOK, true)
	})

	t.Run("disabling the account of the key", func(t *testing.T) {
		e.apiJSON(e.admin, http.StatusOK, http.MethodPatch, "/api/admin/accounts/"+consumer.id, map[string]any{"status": "disabled"})
		p.expect(t, "disabled account", http.StatusForbidden, false)
		e.apiJSON(e.admin, http.StatusOK, http.MethodPatch, "/api/admin/accounts/"+consumer.id, map[string]any{"status": "active"})
		p.expect(t, "re-enabled account", http.StatusOK, true)
	})

	t.Run("disabling the account that owns the only channel", func(t *testing.T) {
		e.apiJSON(e.admin, http.StatusOK, http.MethodPatch, "/api/admin/accounts/"+sharer.id, map[string]any{"status": "disabled"})
		p.expect(t, "channel of a disabled account", http.StatusServiceUnavailable, false)
		e.apiJSON(e.admin, http.StatusOK, http.MethodPatch, "/api/admin/accounts/"+sharer.id, map[string]any{"status": "active"})
		p.expect(t, "channel of a re-enabled account", http.StatusOK, true)
		// Disabling an account ends its sessions.
		sharer = e.login(sharer.username, "New-password-2026-"+sharer.username, sharer.id, false)
	})

	t.Run("the owner unlists the channel", func(t *testing.T) {
		e.patchGroupedChannel(sharer, channelID, map[string]any{"status": "unlisted"})
		p.expect(t, "unlisted channel", http.StatusServiceUnavailable, false)
		e.patchGroupedChannel(sharer, channelID, map[string]any{"status": "listed"})
		p.expect(t, "listed again", http.StatusOK, true)
	})

	t.Run("an administrator takes the channel down", func(t *testing.T) {
		if reply := e.api(e.admin, http.MethodPost, "/api/admin/group-channels/"+channelID+"/moderate", map[string]any{"reason": "abuse report"}); reply.Code != http.StatusNoContent {
			t.Fatal(reply.Code, reply.Body.String())
		}
		p.expect(t, "suspended channel", http.StatusServiceUnavailable, false)
		if reply := e.api(e.admin, http.MethodPost, "/api/admin/group-channels/"+channelID+"/moderate", map[string]any{"reason": ""}); reply.Code != http.StatusNoContent {
			t.Fatal(reply.Code, reply.Body.String())
		}
		p.expect(t, "restored channel", http.StatusOK, true)
	})

	t.Run("the owner deletes the channel", func(t *testing.T) {
		if recorder := e.deleteGroupedChannel(sharer, channelID); recorder.Code != http.StatusNoContent {
			t.Fatalf("delete channel = %d %s", recorder.Code, recorder.Body.String())
		}
		p.expect(t, "deleted channel", http.StatusServiceUnavailable, false)
	})
	e.assertZeroSum()
}

func TestNewKeysWorkAtOnce(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	p := probe{e: e, upstrms: []*upstream{relay}}

	// A key made through the API.
	for index := range 10 {
		created := e.apiJSON(consumer, http.StatusCreated, http.MethodPost, "/api/keys", map[string]any{"name": fmt.Sprintf("key %d", index)})
		p.secret = created["secret"].(string)
		p.expect(t, fmt.Sprintf("key %d right after it was created", index), http.StatusOK, true)
	}
	// The default key, which an account gets lazily the first time it asks.
	for index := range 5 {
		member := e.member(fmt.Sprintf("late%d", index), "100")
		_, p.secret = e.defaultKey(member)
		p.expect(t, fmt.Sprintf("default key of member %d right after it was created", index), http.StatusOK, true)
	}
}

func TestEveryKindOfChangeReachesTheNextRequest(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	relay, other := okUpstream(t), okUpstream(t)
	channelID := e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	keyID, secret := e.defaultKey(consumer)
	p := probe{e: e, secret: secret, upstrms: []*upstream{relay, other}}
	cost := func(step string, reply *httptest.ResponseRecorder) int64 {
		t.Helper()
		if reply.Code != 200 {
			t.Fatalf("%s: %d %s", step, reply.Code, reply.Body.String())
		}
		return e.callRow(reply.Header().Get("X-AIHub-Request-Id")).CostNano
	}
	currentModel := func(modelID string) market.GroupModel {
		t.Helper()
		row, err := e.groupStore.Get(context.Background(), channelID)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range marketSnapshot(t, e.store).Models {
			if m.GroupID == row.GroupID && m.ModelID == modelID {
				return m
			}
		}
		t.Fatal("missing group offering", modelID)
		return market.GroupModel{}
	}
	installProbe := func(modelID string, formats []string) {
		t.Helper()
		row, err := e.groupStore.Get(context.Background(), channelID)
		if err != nil {
			t.Fatal(err)
		}
		model := currentModel(modelID)
		var group market.Group
		for _, g := range marketSnapshot(t, e.store).Groups {
			if g.ID == row.GroupID {
				group = g
			}
		}
		tests := map[channel.Format]channel.FormatTest{}
		for _, f := range formats {
			tests[channel.Format(f)] = channel.FormatTest{OK: true, TestedAt: time.Now()}
		}
		saved, err := e.groupStore.SaveProbe(context.Background(), row, group, model, groupchannel.Probe{ChannelID: row.ID, GroupModelID: model.ID, Binding: groupchannel.ProbeBinding(row, group, model), Tests: tests, TestedAt: time.Now()})
		if err != nil || !saved {
			t.Fatal("synthetic key probe", err)
		}
		if err = e.groups.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	allFormats := []string{"openai_chat", "openai_responses", "anthropic", "gemini"}
	baselineReply := p.expect(t, "baseline", 200, true)
	base := cost("baseline", baselineReply)
	if base != 20_000_000 {
		t.Fatalf("baseline cost=%d", base)
	}
	baselineID := baselineReply.Header().Get("X-AIHub-Request-Id")
	var frozenPrice string
	if err := e.pool.QueryRow(context.Background(), `SELECT price_snapshot::text FROM calls WHERE id=$1`, baselineID).Scan(&frozenPrice); err != nil {
		t.Fatal(err)
	}
	model := currentModel("gpt-test")
	e.publishGroupFixture(market.Changes{Quotes: []market.Quote{{ID: marketID(t), GroupModelID: model.ID, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: mustAmount(t, "20"), Output: mustAmount(t, "40"), CacheWrite: mustAmount(t, "12.5"), CacheRead: mustAmount(t, "1")}}}})
	if got := cost("new quote", p.expect(t, "new quote", 200, true)); got != 2*base {
		t.Fatalf("quote cost=%d", got)
	}
	var afterPrice string
	if err := e.pool.QueryRow(context.Background(), `SELECT price_snapshot::text FROM calls WHERE id=$1`, baselineID).Scan(&afterPrice); err != nil || afterPrice != frozenPrice {
		t.Fatal("quote update rewrote history", err)
	}
	e.publishGroupFixture(market.Changes{Governance: []market.Change[market.ModelRule]{{Value: market.ModelRule{ModelID: "gpt-test", Disabled: true, Reason: "synthetic governance"}}}})
	p.expect(t, "governed model", 404, false)
	rule := marketSnapshot(t, e.store).Governance[0]
	rule.Disabled = false
	e.publishGroupFixture(market.Changes{Governance: []market.Change[market.ModelRule]{{Value: rule, BaseRevision: rule.Revision}}})
	p.expect(t, "restored model", 200, true)
	model = currentModel("gpt-test")
	lateModel := market.GroupModel{ID: marketID(t), GroupID: model.GroupID, ModelID: "late-model", ModelType: "text", Enabled: true}
	e.publishGroupFixture(market.Changes{Models: []market.Change[market.GroupModel]{{Value: lateModel}}, Quotes: []market.Quote{{ID: marketID(t), GroupModelID: lateModel.ID, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: mustAmount(t, "1"), Output: mustAmount(t, "1")}}}})
	late := func() *httptest.ResponseRecorder {
		return e.call(secret, "POST", "/v1/chat/completions", []byte(`{"model":"late-model"}`), nil)
	}
	if got := late().Code; got != 503 {
		t.Fatalf("new offering without key probe=%d", got)
	}
	installProbe("late-model", []string{"openai_chat"})
	if got := late().Code; got != 200 {
		t.Fatalf("new offering after probe=%d", got)
	}
	e.patchGroupedChannel(sharer, channelID, map[string]any{"models": []any{map[string]any{"group_model_id": lateModel.ID, "excluded": true, "discount": "1"}}})
	if got := late().Code; got != 503 {
		t.Fatalf("excluded offering=%d", got)
	}
	lateModel = currentModel("late-model")
	now := time.Now().UTC()
	lateModel.DeletedAt = &now
	e.publishGroupFixture(market.Changes{Models: []market.Change[market.GroupModel]{{Value: lateModel, BaseRevision: lateModel.Revision}}})
	if got := late().Code; got != 404 {
		t.Fatalf("deleted offering=%d", got)
	}
	model = currentModel("gpt-test")
	e.patchGroupedChannel(sharer, channelID, map[string]any{"models": []any{map[string]any{"group_model_id": model.ID, "discount": "2"}}})
	if got := cost("discount", p.expect(t, "discount", 200, true)); got != 4*base {
		t.Fatalf("discount cost=%d", got)
	}
	installProbe("gpt-test", []string{"anthropic"})
	p.expect(t, "removed chat callability", 503, false)
	installProbe("gpt-test", allFormats)
	p.expect(t, "restored chat callability", 200, true)
	model = currentModel("gpt-test")
	model.UpstreamModel = "renamed-upstream"
	e.publishGroupFixture(market.Changes{Models: []market.Change[market.GroupModel]{{Value: model, BaseRevision: model.Revision}}})
	p.expect(t, "changed engine before new key probe", 503, false)
	installProbe("gpt-test", allFormats)
	p.expect(t, "upstream rename", 200, true)
	if last := relay.inference(); !strings.Contains(string(last[len(last)-1].Body), `"renamed-upstream"`) {
		t.Fatal("upstream rename not applied")
	}
	e.replaceGroupedChannelKey(sharer, channelID, "rotated-upstream-key")
	e.patchGroupedChannel(sharer, channelID, map[string]any{"overrides": map[string]any{"user_agent": "relay-agent/2"}})
	// The controlled engine accepts this synthetic UA too; no real report is asserted.
	installProbe("gpt-test", allFormats)
	p.expect(t, "key and UA", 200, true)
	if last := relay.inference(); last[len(last)-1].Header.Get("Authorization") != "Bearer rotated-upstream-key" || last[len(last)-1].Header.Get("User-Agent") != "relay-agent/2" {
		t.Fatal("key/UA change not applied")
	}
	row, err := e.groupStore.Get(context.Background(), channelID)
	if err != nil {
		t.Fatal(err)
	}
	var address market.Address
	directory := marketSnapshot(t, e.store)
	for _, link := range directory.GroupAddresses {
		if link.GroupID == row.GroupID {
			for _, a := range directory.Addresses {
				if a.ID == link.AddressID {
					address = a
				}
			}
		}
	}
	originalURL := address.BaseURL
	otherURL, err := url.Parse(other.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	e.installGroupLoopback(otherURL.Port())
	otherURL.Host = net.JoinHostPort("example.com", otherURL.Port())
	address.BaseURL = otherURL.String()
	e.publishGroupFixture(market.Changes{Addresses: []market.Change[market.Address]{{Value: address, BaseRevision: address.Revision}}})
	beforeOtherAddress := len(other.inference())
	p.expect(t, "address change without new engine report", 200, true)
	if len(other.inference()) != beforeOtherAddress+1 {
		t.Fatal("address update did not take effect")
	}
	for _, a := range marketSnapshot(t, e.store).Addresses {
		if a.ID == address.ID {
			address = a
		}
	}
	address.BaseURL = originalURL
	e.publishGroupFixture(market.Changes{Addresses: []market.Change[market.Address]{{Value: address, BaseRevision: address.Revision}}})

	// Key: allowed models, aliases, expiry, budget.
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"allowed_models": []string{"claude-test"}})
	p.expect(t, "model outside the allowed list", http.StatusForbidden, false)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"allowed_models": []string{}, "model_aliases": map[string]string{"my-gpt": "gpt-test"}})
	if got := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"my-gpt"}`), nil).Code; got != http.StatusOK {
		t.Fatalf("alias = %d", got)
	}
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"model_aliases": map[string]string{}})
	if got := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"my-gpt"}`), nil).Code; got != http.StatusNotFound {
		t.Fatalf("removed alias = %d", got)
	}
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"expires_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)})
	p.expect(t, "expired key", http.StatusUnauthorized, false)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"expires_at": nil})
	p.expect(t, "key without expiry", http.StatusOK, true)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"budget_total": "0.001"})
	p.expect(t, "budget already used up", http.StatusPaymentRequired, false)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"budget_total": nil})
	p.expect(t, "budget removed", http.StatusOK, true)

	// Routing: an account setting, a key override, and removing each.
	second := e.createChannel(sharer, channelSpec{name: "second", upstream: other, multiplier: "3"})
	hitsOf := func(u *upstream) int { return len(u.inference()) }
	e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "manual", "order": []string{second}})
	beforeOther := hitsOf(other)
	p.expect(t, "manual routing", http.StatusOK, true)
	if hitsOf(other) != beforeOther+1 {
		t.Fatal("manual routing did not choose the second channel")
	}
	e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/keys/"+keyID+"/routing/gpt-test", map[string]any{"mode": "manual", "order": []string{channelID}})
	beforeRelay := hitsOf(relay)
	p.expect(t, "key override", http.StatusOK, true)
	if hitsOf(relay) != beforeRelay+1 {
		t.Fatal("the key override did not choose the first channel")
	}
	if recorder := e.api(consumer, http.MethodDelete, "/api/keys/"+keyID+"/routing/gpt-test", nil); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete key routing = %d", recorder.Code)
	}
	beforeOther = hitsOf(other)
	p.expect(t, "key override removed", http.StatusOK, true)
	if hitsOf(other) != beforeOther+1 {
		t.Fatal("removing the key override did not bring back the account setting")
	}

	// The model list follows the channels that serve the model.
	listed := func() []string {
		t.Helper()
		recorder := e.call(secret, http.MethodGet, "/v1/models", nil, nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("list = %d %s", recorder.Code, recorder.Body.String())
		}
		return modelIDsOf(t, recorder.Body.Bytes())
	}
	if ids := listed(); len(ids) != 1 || ids[0] != "gpt-test" {
		t.Fatalf("models = %v", ids)
	}
	for _, id := range []string{channelID, second} {
		e.patchGroupedChannel(sharer, id, map[string]any{"status": "unlisted"})
	}
	if ids := listed(); len(ids) != 0 {
		t.Fatalf("models with every channel unlisted = %v", ids)
	}
	e.patchGroupedChannel(sharer, channelID, map[string]any{"status": "listed"})
	if ids := listed(); len(ids) != 1 {
		t.Fatalf("models with a channel listed again = %v", ids)
	}
	e.assertZeroSum()
}

// One administrator flips a key on and off while traffic runs on other keys:
// after every call that returned, the next request of that key sees the new
// state, and the traffic of the others is never disturbed.
func TestKeyStateChangesAreSeenAtOnceUnderLoad(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	keyID, secret := e.defaultKey(consumer)
	bystander := e.member("bystander", "1000")
	_, bystanderSecret := e.defaultKey(bystander)

	stop := make(chan struct{})
	var traffic sync.WaitGroup
	for range 4 {
		traffic.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if code := chatRequest(e, bystanderSecret).Code; code != http.StatusOK {
					t.Errorf("a request of an untouched key = %d", code)
					return
				}
			}
		})
	}
	for round := range 25 {
		e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"status": "disabled"})
		if code := chatRequest(e, secret).Code; code != http.StatusUnauthorized {
			t.Errorf("round %d: disabled key = %d", round, code)
			break
		}
		e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"status": "enabled"})
		if code := chatRequest(e, secret).Code; code != http.StatusOK {
			t.Errorf("round %d: enabled key = %d", round, code)
			break
		}
	}
	close(stop)
	traffic.Wait()
}

// A change made behind the server's back (manual SQL, a migration) is picked
// up by the periodic rebuild, and until then it is not seen: the snapshots are
// really what the requests read.
func TestPeriodicRebuildPicksUpChangesMadeBehindTheServer(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "100")
	keyID, secret := e.defaultKey(consumer)
	fast := e.store.Cached(slog.New(slog.NewTextHandler(io.Discard, nil)), func(c *snapshot.Config) { c.Interval = 50 * time.Millisecond; c.Loader = e.groups.Wrap(c.Loader) })
	ctx := context.Background()
	if err := fast.Cache.Load(ctx); err != nil {
		t.Fatal(err)
	}
	hash := apikey.Hash(secret)
	enabled := func() bool {
		key, err := fast.Gateway.LookupKey(ctx, hash)
		if err != nil {
			t.Fatal(err)
		}
		return key.Enabled
	}
	if !enabled() {
		t.Fatal("setup: key not enabled")
	}
	if _, err := e.pool.Exec(ctx, `UPDATE api_keys SET status = 'disabled' WHERE id = $1`, keyID); err != nil {
		t.Fatal(err)
	}
	if !enabled() {
		t.Fatal("the snapshot followed a change nobody announced before any rebuild: it is not what the requests read")
	}
	runContext, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { fast.Cache.Run(runContext); close(done) }()
	defer func() { stop(); <-done }()
	deadline := time.Now().Add(10 * time.Second)
	for enabled() {
		if time.Now().After(deadline) {
			t.Fatal("the periodic rebuild never picked up the change")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
