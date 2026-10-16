package postgres_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/snapshot"
)

// The gateway reads keys, the catalog, channels and routing preferences from
// process memory (ADR-0040). These tests run the whole stack on the cached
// stores, as the server does, with no background rebuild: whatever the next
// gateway request sees after an administrative call returned can only come
// from the invalidation that call made.

func chatRequest(e *env, secret string) *httptest.ResponseRecorder {
	return e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
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
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	channelID := e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	keyID, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
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
		e.apiJSON(sharer, http.StatusOK, http.MethodPatch, "/api/channels/"+channelID, map[string]any{"status": "unlisted"})
		p.expect(t, "unlisted channel", http.StatusServiceUnavailable, false)
		e.apiJSON(sharer, http.StatusOK, http.MethodPatch, "/api/channels/"+channelID, map[string]any{"status": "listed"})
		p.expect(t, "listed again", http.StatusOK, true)
	})

	t.Run("an administrator takes the channel down", func(t *testing.T) {
		e.apiJSON(e.admin, http.StatusOK, http.MethodPost, "/api/admin/channels/"+channelID+"/suspend", map[string]any{"reason": "abuse report"})
		p.expect(t, "suspended channel", http.StatusServiceUnavailable, false)
		e.apiJSON(e.admin, http.StatusOK, http.MethodPost, "/api/admin/channels/"+channelID+"/unsuspend", map[string]any{"reason": "cleared"})
		p.expect(t, "restored channel", http.StatusOK, true)
	})

	t.Run("the owner deletes the channel", func(t *testing.T) {
		if recorder := e.api(sharer, http.MethodDelete, "/api/channels/"+channelID, nil); recorder.Code != http.StatusNoContent {
			t.Fatalf("delete channel = %d %s", recorder.Code, recorder.Body.String())
		}
		p.expect(t, "deleted channel", http.StatusServiceUnavailable, false)
	})
	e.assertZeroSum()
}

func TestNewKeysWorkAtOnce(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	e.selectFixtureChannels(consumer)
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
		e.selectFixtureChannels(member)
		p.expect(t, fmt.Sprintf("default key of member %d right after it was created", index), http.StatusOK, true)
	}
}

// Every other kind of change an administrator, a channel owner or a user can
// make is seen by the very next request too.
func TestEveryKindOfChangeReachesTheNextRequest(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	other := okUpstream(t)
	channelID := e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	keyID, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	p := probe{e: e, secret: secret, upstrms: []*upstream{relay, other}}
	cost := func(step string, recorder *httptest.ResponseRecorder) int64 {
		t.Helper()
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", step, recorder.Code, recorder.Body.String())
		}
		return e.callRow(recorder.Header().Get("X-AIHub-Request-Id")).CostNano
	}
	patchChannel := func(body map[string]any) {
		t.Helper()
		e.apiJSON(sharer, http.StatusOK, http.MethodPatch, "/api/channels/"+channelID, body)
	}
	models := func(multiplier string, formats []string, extra map[string]any) []any {
		model := map[string]any{"model_id": "gpt-test", "multiplier": multiplier}
		for name, value := range extra {
			model[name] = value
		}
		return []any{model}
	}
	allFormats := []string{"openai_chat", "openai_responses", "anthropic", "gemini"}

	base := cost("baseline", p.expect(t, "baseline", http.StatusOK, true)) // (1000 x 10 + 500 x 20) / 1e6 points
	if base != 20_000_000 {
		t.Fatalf("baseline cost = %d", base)
	}

	// Model: price, enabled, deleted.
	e.apiJSON(e.admin, http.StatusOK, http.MethodPatch, "/api/admin/models/gpt-test", map[string]any{
		"base_prices": map[string]string{"input": "20", "output": "40", "cache_write": "12.5", "cache_read": "1"},
	})
	if got := cost("after a price change", p.expect(t, "after a price change", http.StatusOK, true)); got != 2*base {
		t.Fatalf("cost after doubling the price = %d, want %d", got, 2*base)
	}
	e.apiJSON(e.admin, http.StatusOK, http.MethodPatch, "/api/admin/models/gpt-test", map[string]any{"enabled": false})
	p.expect(t, "disabled model", http.StatusNotFound, false)
	e.apiJSON(e.admin, http.StatusOK, http.MethodPatch, "/api/admin/models/gpt-test", map[string]any{"enabled": true})
	p.expect(t, "enabled model", http.StatusOK, true)
	e.apiJSON(e.admin, http.StatusCreated, http.MethodPost, "/api/admin/models", map[string]any{
		"id": "late-model", "display_name": "Late", "base_prices": map[string]string{"input": "1", "output": "1", "cache_write": "0", "cache_read": "0"},
	})
	late := func() *httptest.ResponseRecorder {
		return e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"late-model"}`), nil)
	}
	if got := late().Code; got != http.StatusBadRequest {
		t.Fatalf("new model without a channel = %d", got)
	}
	patchChannel(map[string]any{"models": append(models("1", allFormats, nil), map[string]any{"model_id": "late-model", "multiplier": "1"})})
	e.selectFixtureChannels(consumer)
	if got := late().Code; got != http.StatusOK {
		t.Fatalf("new model once a channel offers it = %d", got)
	}
	patchChannel(map[string]any{"models": models("1", allFormats, nil)})
	if got := late().Code; got != http.StatusServiceUnavailable {
		t.Fatalf("model the channel stopped offering = %d", got)
	}
	e.apiJSON(e.admin, http.StatusNoContent, http.MethodDelete, "/api/admin/models/late-model", nil)
	if got := late().Code; got != http.StatusNotFound {
		t.Fatalf("deleted model = %d", got)
	}

	// Channel: multiplier, formats, upstream name, address and credential, advanced settings.
	patchChannel(map[string]any{"models": models("2", allFormats, nil)})
	if got := cost("after a multiplier change", p.expect(t, "after a multiplier change", http.StatusOK, true)); got != 4*base {
		t.Fatalf("cost with multiplier 2 and doubled prices = %d, want %d", got, 4*base)
	}
	patchChannel(map[string]any{"advanced": map[string]any{"compatibility": map[string]any{"codex": []string{"inference", "nonstream"}}}})
	p.expect(t, "a format the channel no longer serves", http.StatusServiceUnavailable, false)
	patchChannel(map[string]any{"advanced": map[string]any{"compatibility": map[string]any{"responses": []string{"inference", "nonstream", "stream"}}}})
	p.expect(t, "the format is served again", http.StatusOK, true)

	patchChannel(map[string]any{"models": models("1", allFormats, map[string]any{"upstream_model": "renamed-upstream"})})
	p.expect(t, "upstream rename", http.StatusOK, true)
	if last := relay.inference(); !strings.Contains(string(last[len(last)-1].Body), `"renamed-upstream"`) {
		t.Fatalf("the upstream received %s, want the renamed model", last[len(last)-1].Body)
	}
	patchChannel(map[string]any{"api_key": "rotated-upstream-key", "advanced": map[string]any{"user_agent": "relay-agent/2", "compatibility": map[string]any{"responses": []string{"inference", "nonstream", "stream"}}}})
	p.expect(t, "credential and user agent change", http.StatusOK, true)
	last := relay.inference()
	if seen := last[len(last)-1].Header; seen.Get("Authorization") != "Bearer rotated-upstream-key" || seen.Get("User-Agent") != "relay-agent/2" {
		t.Fatalf("upstream saw Authorization %q and User-Agent %q", seen.Get("Authorization"), seen.Get("User-Agent"))
	}
	patchChannel(map[string]any{"base_url": other.server.URL})
	before := len(other.inference())
	p.expect(t, "address change", http.StatusOK, true)
	if len(other.inference()) != before+1 {
		t.Fatal("the request did not go to the new address")
	}
	patchChannel(map[string]any{"base_url": relay.server.URL})

	// Key: allowed models, aliases, expiry, budget.
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"allowed_models": []string{"claude-test"}})
	p.expect(t, "model outside the allowed list", http.StatusForbidden, false)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"allowed_models": []string{}, "model_aliases": map[string]string{"my-gpt": "gpt-test"}})
	if got := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"my-gpt"}`), nil).Code; got != http.StatusOK {
		t.Fatalf("alias = %d", got)
	}
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"model_aliases": map[string]string{}})
	if got := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"my-gpt"}`), nil).Code; got != http.StatusNotFound {
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
		e.apiJSON(sharer, http.StatusOK, http.MethodPatch, "/api/channels/"+id, map[string]any{"status": "unlisted"})
	}
	if ids := listed(); len(ids) != 0 {
		t.Fatalf("models with every channel unlisted = %v", ids)
	}
	e.apiJSON(sharer, http.StatusOK, http.MethodPatch, "/api/channels/"+channelID, map[string]any{"status": "listed"})
	if ids := listed(); len(ids) != 1 {
		t.Fatalf("models with a channel listed again = %v", ids)
	}
	e.assertZeroSum()
}

// One administrator flips a key on and off while traffic runs on other keys:
// after every call that returned, the next request of that key sees the new
// state, and the traffic of the others is never disturbed.
func TestKeyStateChangesAreSeenAtOnceUnderLoad(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	keyID, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	bystander := e.member("bystander", "1000")
	_, bystanderSecret := e.defaultKey(bystander)
	e.selectFixtureChannels(bystander)

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
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	keyID, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	fast := e.store.Cached(slog.New(slog.NewTextHandler(io.Discard, nil)), func(c *snapshot.Config) { c.Interval = 50 * time.Millisecond })
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
