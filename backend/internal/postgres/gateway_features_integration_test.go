package postgres_test

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

func TestGatewayAppliesChannelHeaderRulesAndUserAgent(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, advanced: map[string]any{
		"user_agent":   "relay-agent/1.0",
		"header_rules": map[string]any{"set": []any{map[string]any{"name": "X-Relay-Tenant", "value": "acme"}, map[string]any{"name": "X-Client", "value": "overwritten"}}, "remove": []any{"X-Strip"}},
	}})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), map[string]string{
		"User-Agent": "client-agent/9", "X-Client": "original", "X-Strip": "gone", "X-Forwarded-For": "203.0.113.9", "X-Keep": "yes",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("call = %d %s", recorder.Code, recorder.Body.String())
	}
	seen := relay.inference()[0].Header
	if seen.Get("User-Agent") != "relay-agent/1.0" || seen.Get("X-Relay-Tenant") != "acme" || seen.Get("X-Client") != "overwritten" ||
		seen.Get("X-Strip") != "" || seen.Get("X-Forwarded-For") != "" || seen.Get("X-Keep") != "yes" {
		t.Fatalf("upstream headers = %v", seen)
	}
	row := e.pool.QueryRow(context.Background(), `SELECT client_user_agent FROM calls WHERE id = $1`, recorder.Header().Get("X-AIHub-Request-Id"))
	var ua string
	if err := row.Scan(&ua); err != nil || ua != "client-agent/9" {
		t.Fatalf("recorded client user agent = %q, %v", ua, err)
	}

	// Rules may not touch credentials or framing.
	bad := e.api(sharer, http.MethodPost, "/api/channels", map[string]any{
		"name": "bad", "base_url": relay.server.URL, "api_key": "k", "models": []any{map[string]any{"model_id": "gpt-test"}},
		"advanced": map[string]any{"header_rules": map[string]any{"set": []any{map[string]any{"name": "Authorization", "value": "x"}}, "remove": []any{}}},
	})
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("authorization rule = %d %s", bad.Code, bad.Body.String())
	}
}

func TestGatewayKeepsResponsesConversationsOnTheSameChannel(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	pricey := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		_, _ = io.WriteString(w, `{"id":"resp_pricey","object":"response","usage":{"input_tokens":10,"output_tokens":5}}`)
	})
	cheap := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		_, _ = io.WriteString(w, `{"id":"resp_cheap","object":"response","usage":{"input_tokens":10,"output_tokens":5}}`)
	})
	priceyID := e.createChannel(sharer, channelSpec{name: "pricey", upstream: pricey, multiplier: "5"})
	cheapID := e.createChannel(sharer, channelSpec{name: "cheap", upstream: cheap, multiplier: "1"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	// Route the first turn to the pricey channel by ticking only it.
	e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "manual", "order": []string{priceyID}})
	first := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","input":"a"}`), nil)
	if first.Code != http.StatusOK || len(pricey.inference()) != 1 {
		t.Fatalf("first turn = %d, pricey hits %d", first.Code, len(pricey.inference()))
	}
	// Back to cheapest-first: a follow-up with previous_response_id still returns to the first channel.
	e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "manual", "order": []string{cheapID, priceyID}})
	second := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","input":"b","previous_response_id":"resp_pricey"}`), nil)
	if second.Code != http.StatusOK || len(pricey.inference()) != 2 || len(cheap.inference()) != 0 {
		t.Fatalf("follow-up = %d, pricey %d cheap %d", second.Code, len(pricey.inference()), len(cheap.inference()))
	}
	unrelated := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","input":"c"}`), nil)
	if unrelated.Code != http.StatusOK || len(cheap.inference()) != 1 {
		t.Fatalf("unrelated request should take the cheapest channel: cheap %d", len(cheap.inference()))
	}
}

func TestManualRoutingExclusionAndKeyOverride(t *testing.T) {
	e := newEnv(t)
	consumer, sharer := e.member("consumer", "100"), e.member("sharer", "0")
	a, b := okUpstream(t), okUpstream(t)
	idA := e.createChannel(sharer, channelSpec{name: "a", upstream: a})
	idB := e.createChannel(sharer, channelSpec{name: "b", upstream: b})
	keyID, secret := e.defaultKey(consumer)
	empty := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","input":"hi"}`), nil)
	if empty.Code != 400 || !strings.Contains(empty.Body.String(), "routing_not_configured") || len(a.inference())+len(b.inference()) != 0 {
		t.Fatalf("default route: %d %s", empty.Code, empty.Body.String())
	}
	e.apiJSON(consumer, 200, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "manual", "order": []string{idB, idA}})
	send := func() {
		result := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","input":"hi"}`), nil)
		if result.Code != 200 {
			t.Fatal(result.Code, result.Body.String())
		}
	}
	send()
	if len(b.inference()) != 1 || len(a.inference()) != 0 {
		t.Fatal("manual order ignored")
	}
	e.apiJSON(consumer, 200, http.MethodPut, "/api/keys/"+keyID+"/routing/gpt-test", map[string]any{"mode": "manual", "order": []string{idA}})
	send()
	if len(a.inference()) != 1 {
		t.Fatal("key override ignored")
	}
	e.apiJSON(consumer, 204, http.MethodDelete, "/api/keys/"+keyID+"/routing/gpt-test", nil)
	send()
	if len(b.inference()) != 2 {
		t.Fatal("account inheritance lost")
	}
	for _, mode := range []string{"cheapest", "reliable", "fastest"} {
		e.apiJSON(consumer, 422, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": mode})
	}
	e.apiJSON(consumer, 200, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "manual", "order": []string{idB, idA}, "excluded": []string{idB}})
	send()
	if len(a.inference()) != 2 {
		t.Fatal("excluded channel used")
	}
}

// A non-streaming call's first byte is the finished answer, so its "first
// token" time is the whole generation. Only streaming calls may feed the
// first-token median that the fast routing mode ranks by; success counts keep
// every call.
func TestChannelFirstTokenMedianCountsOnlyStreamingCalls(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	const generation = 250 * time.Millisecond
	// Streams at once, but needs a while to produce a whole answer.
	slowAnswer := func() *upstream {
		return newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
			if strings.Contains(string(body), `"stream":true`) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, chatStreamBody)
				return
			}
			time.Sleep(generation)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, chatBody)
		})
	}
	mixed := e.createChannel(sharer, channelSpec{name: "mixed", upstream: slowAnswer(), model: "gpt-test"})
	plain := e.createChannel(sharer, channelSpec{name: "plain", upstream: slowAnswer(), model: "claude-test"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)

	firstAttemptTTFT := func(recorder *httptest.ResponseRecorder) int {
		t.Helper()
		if recorder.Code != http.StatusOK {
			t.Fatalf("call = %d %s", recorder.Code, recorder.Body.String())
		}
		var ttft int
		err := e.pool.QueryRow(context.Background(), `SELECT (attempts->0->>'ttft_ms')::int FROM calls WHERE id = $1`,
			recorder.Header().Get("X-AIHub-Request-Id")).Scan(&ttft)
		if err != nil {
			t.Fatal(err)
		}
		return ttft
	}
	send := func(body string) int {
		t.Helper()
		return firstAttemptTTFT(e.call(secret, http.MethodPost, "/v1/responses", []byte(body), nil))
	}
	streamTTFT := send(`{"model":"gpt-test","stream":true}`)
	for range 2 {
		if ttft := send(`{"model":"gpt-test"}`); ttft < int(generation.Milliseconds()) {
			t.Fatalf("non-streaming first byte after %d ms, want at least the %s generation", ttft, generation)
		}
	}
	send(`{"model":"claude-test"}`)

	e.engine.RefreshStats()
	stats := e.engine.Stats()
	got := stats[mixed]
	if got.Attempts != 3 || got.Successes != 3 {
		t.Fatalf("mixed channel attempts = %d, successes = %d", got.Attempts, got.Successes)
	}
	// The only streaming sample is the median; the two slow answers must not move it.
	if got.TTFTP50MS == nil {
		t.Fatalf("mixed channel has no first-token median, want the streaming call's %d ms", streamTTFT)
	}
	// The median is read off a histogram: the streaming call's time to within the 4.6% a bin can be off.
	if math.Abs(*got.TTFTP50MS-float64(streamTTFT)) > 0.046*float64(streamTTFT) {
		t.Fatalf("mixed channel first-token median = %v ms, want the streaming call's %d ms", *got.TTFTP50MS, streamTTFT)
	}
	// A channel that has only served non-streaming calls has no first-token figure at all.
	if got := stats[plain]; got.Attempts != 1 || got.Successes != 1 || got.TTFTP50MS != nil {
		t.Fatalf("plain channel stats = %+v", got)
	}
}

func TestGatewayEnforcesRPMDailyRevenueAndConcurrencyLimits(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	call := func() *httptest.ResponseRecorder {
		return e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	}

	rpm := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "rpm", upstream: rpm, advanced: map[string]any{"rpm_limit": 1}})
	e.selectFixtureChannels(consumer)
	if call().Code != http.StatusOK {
		t.Fatal("first request refused")
	}
	if refused := call(); refused.Code != http.StatusServiceUnavailable || !strings.Contains(refused.Body.String(), `"no_channel_available"`) {
		t.Fatalf("second request within the minute = %d %s", refused.Code, refused.Body.String())
	}

	capped := e.member("capped", "0")
	capUpstream := okUpstream(t)
	capChannel := e.createChannel(capped, channelSpec{name: "capped", upstream: capUpstream, model: "claude-test", advanced: map[string]any{"daily_revenue_cap": "0.05"}})
	e.selectFixtureChannels(consumer)
	callClaude := func() *httptest.ResponseRecorder {
		return e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"claude-test"}`), nil)
	}
	for index := range 2 { // 0.04 then 0.08: the cap of 0.05 is only reached after the second call
		if code := callClaude().Code; code != http.StatusOK {
			t.Fatalf("call %d under the cap = %d", index, code)
		}
	}
	if refused := callClaude(); refused.Code != http.StatusServiceUnavailable {
		t.Fatalf("call over the cap = %d %s", refused.Code, refused.Body.String())
	}
	_ = capChannel

	// Concurrency: the second request arrives while the first is still streaming.
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	slow := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		once.Do(func() { close(started) })
		<-release
		_, _ = io.WriteString(w, chatBody)
	})
	slowOwner := e.member("slowowner", "0")
	e.createChannel(slowOwner, channelSpec{name: "slow", upstream: slow, model: "gemini-test", advanced: map[string]any{"concurrency_limit": 1}})
	e.selectFixtureChannels(consumer)
	done := make(chan int, 1)
	go func() {
		done <- e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gemini-test"}`), nil).Code
	}()
	<-started
	if refused := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gemini-test"}`), nil); refused.Code != http.StatusServiceUnavailable {
		t.Fatalf("concurrent request = %d %s", refused.Code, refused.Body.String())
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("held request = %d", code)
	}
}

func TestGatewayFallsBackOnFirstByteTimeout(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	stuck := make(chan struct{})
	slow := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		select {
		case <-stuck:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(stuck) })
	fast := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "slow", upstream: slow, multiplier: "1", advanced: map[string]any{"ttft_timeout_ms": 1000}})
	e.createChannel(sharer, channelSpec{name: "fast", upstream: fast, multiplier: "2"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	started := time.Now()
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","stream":true}`), nil)
	if recorder.Code != http.StatusOK || time.Since(started) < time.Second {
		t.Fatalf("call = %d after %s", recorder.Code, time.Since(started))
	}
	var endReason string
	err := e.pool.QueryRow(context.Background(), `SELECT attempts->0->>'end_reason' FROM calls WHERE id = $1`, recorder.Header().Get("X-AIHub-Request-Id")).Scan(&endReason)
	if err != nil || endReason != "timeout_ttft" {
		t.Fatalf("first attempt end reason = %q, %v", endReason, err)
	}
}

// A non-streaming completion sends its first byte only when generation ends,
// so the first-byte timeout does not apply: only the total timeout does.
func TestGatewayFirstByteTimeoutDoesNotApplyToNonStreamingRequests(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	slow := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		select {
		case <-time.After(1500 * time.Millisecond):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, chatBody)
		case <-r.Context().Done():
		}
	})
	fast := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "slow", upstream: slow, multiplier: "1", advanced: map[string]any{"ttft_timeout_ms": 1000}})
	e.createChannel(sharer, channelSpec{name: "fast", upstream: fast, multiplier: "2"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	started := time.Now()
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	if recorder.Code != http.StatusOK || time.Since(started) < 1500*time.Millisecond {
		t.Fatalf("call = %d after %s", recorder.Code, time.Since(started))
	}
	if len(slow.inference()) != 1 || len(fast.inference()) != 0 {
		t.Fatalf("attempts: slow %d, fast %d", len(slow.inference()), len(fast.inference()))
	}
	var attempts int
	var endReason string
	err := e.pool.QueryRow(context.Background(), `SELECT jsonb_array_length(attempts), attempts->0->>'end_reason' FROM calls WHERE id = $1`,
		recorder.Header().Get("X-AIHub-Request-Id")).Scan(&attempts, &endReason)
	if err != nil || attempts != 1 || endReason != "completed" {
		t.Fatalf("attempts = %d, first end reason = %q, %v", attempts, endReason, err)
	}
	if row := e.callRow(recorder.Header().Get("X-AIHub-Request-Id")); row.Outcome != "succeeded" {
		t.Fatalf("outcome = %q", row.Outcome)
	}
}

func TestGatewayPricesWithTheTierMatchingTheRealRequest(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	minimum := int64(900)
	if _, err := e.catalog.Update(context.Background(), e.admin.id, "gpt-test", catalog.ModelPatch{PriceTiers: &[]ledger.PriceTier{
		{Name: "long", MinPromptTokens: &minimum, InputPrice: mustAmount(t, "40"), OutputPrice: mustAmount(t, "20")},
	}}); err != nil {
		t.Fatal(err)
	}
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	row := e.callRow(recorder.Header().Get("X-AIHub-Request-Id"))
	// 1000 prompt tokens select the "long" tier: (1000*40 + 500*20) / 1e6 = 0.05.
	if row.CostNano != 50_000_000 {
		t.Fatalf("cost nano = %d", row.CostNano)
	}
	var snapshot string
	if err := e.pool.QueryRow(context.Background(), `SELECT price_snapshot::text FROM calls WHERE id = $1`, recorder.Header().Get("X-AIHub-Request-Id")).Scan(&snapshot); err != nil ||
		!strings.Contains(snapshot, `"seq": 1`) || !strings.Contains(snapshot, `"fee_rate_nano": 1000000`) {
		t.Fatalf("price snapshot = %s, %v", snapshot, err)
	}
}

func TestGatewayAppliesKeyAliasesAndAllowedModels(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, upstreamAs: "real-name"})
	keyID, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{
		"model_aliases": map[string]string{"gpt-4o": "gpt-test"}, "allowed_models": []string{"gpt-test"},
	})
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-4o"}`), nil)
	if recorder.Code != http.StatusOK || string(relay.inference()[0].Body) != `{"model":"real-name"}` {
		t.Fatalf("aliased call = %d, upstream body %s", recorder.Code, relay.inference()[0].Body)
	}
	if row := e.callRow(recorder.Header().Get("X-AIHub-Request-Id")); row.ModelID != "gpt-test" {
		t.Fatalf("resolved model = %q", row.ModelID)
	}
	forbidden := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"claude-test"}`), nil)
	if forbidden.Code != http.StatusForbidden || !strings.Contains(forbidden.Body.String(), "model_not_allowed") {
		t.Fatalf("model outside the allowed list = %d %s", forbidden.Code, forbidden.Body.String())
	}
	if missing := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"nope"}`), nil); missing.Code != http.StatusForbidden {
		t.Fatalf("unknown model with an allowed list = %d", missing.Code)
	}
	if noModel := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"messages":[]}`), nil); noModel.Code != http.StatusBadRequest {
		t.Fatalf("missing model = %d", noModel.Code)
	}
	if bad := e.api(consumer, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"model_aliases": map[string]string{"x": "not-in-catalog"}}); bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("alias to unknown model = %d", bad.Code)
	}
	// The model list shows the alias and only allowed models with online channels.
	list := e.call(secret, http.MethodGet, "/v1/models", nil, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"id":"gpt-test"`) || !strings.Contains(list.Body.String(), `"id":"gpt-4o"`) || strings.Contains(list.Body.String(), "claude-test") {
		t.Fatalf("models = %d %s", list.Code, list.Body.String())
	}
	legacyHeader := e.call("", http.MethodGet, "/v1/models", nil, map[string]string{"x-api-key": secret, "anthropic-version": "2023-06-01"})
	if legacyHeader.Code != 200 || !strings.Contains(legacyHeader.Body.String(), `"object":"list"`) {
		t.Fatal(legacyHeader.Code, legacyHeader.Body.String())
	}
	if retired := e.call(secret, http.MethodGet, "/v1beta/models", nil, nil); retired.Code != 404 {
		t.Fatal(retired.Code)
	}

}

func TestGatewayRejectsBadKeysAndDisabledAccounts(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	keyID, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	for name, key := range map[string]string{"none": "", "unknown": "sk-aih-doesnotexist", "foreign": "sk-other-123"} {
		if code := e.call(key, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusUnauthorized {
			t.Fatalf("%s key = %d", name, code)
		}
	}
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"status": "disabled"})
	disabled := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	if disabled.Code != http.StatusUnauthorized || e.callRow(disabled.Header().Get("X-AIHub-Request-Id")).Outcome != "rejected_key" {
		t.Fatalf("disabled key = %d %s", disabled.Code, disabled.Body.String())
	}
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"status": "enabled", "expires_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)})
	if code := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("expired key = %d", code)
	}
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"expires_at": nil})
	e.apiJSON(e.admin, http.StatusOK, http.MethodPatch, "/api/admin/accounts/"+consumer.id, map[string]any{"status": "disabled"})
	if code := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusForbidden {
		t.Fatalf("disabled account = %d", code)
	}
	if code := e.call(secret, http.MethodGet, "/v1/embeddings", nil, nil).Code; code != http.StatusNotFound {
		t.Fatalf("unsupported endpoint = %d", code)
	}
}

// brokenWriter fails after a number of successful writes, like a client that left.
type brokenWriter struct {
	*httptest.ResponseRecorder
	okWrites int
}

func (w *brokenWriter) Write(p []byte) (int, error) {
	if w.okWrites <= 0 {
		return 0, io.ErrClosedPipe
	}
	w.okWrites--
	return w.ResponseRecorder.Write(p)
}

// A client that stops accepting data does not stop the gateway reading: the
// usage frame that comes in the end is still billed (see the live-server tests
// in gateway_drain_integration_test.go for a real hang-up).
func TestGatewayBillsAnAbandonedStreamForTheUsageItStillReceives(t *testing.T) {
	e := newEnvWith(t, func(d *gateway.Dependencies) { d.DrainWindow = time.Second })
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	twoFrames := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		flusher.Flush()
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":500}}\n\ndata: [DONE]\n\n")
	})
	e.createChannel(sharer, channelSpec{name: "stream", upstream: twoFrames})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test","stream":true}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	writer := &brokenWriter{ResponseRecorder: httptest.NewRecorder(), okWrites: 1}
	e.handler.ServeHTTP(writer, request)
	row := e.callRow(writer.Header().Get("X-AIHub-Request-Id"))
	if row.Outcome != "client_disconnected" || row.Input != 1000 || row.Output != 500 || !row.HasLedgerTx || e.balance(consumer) != "-"+expectedCharge {
		t.Fatalf("abandoned stream = %+v, balance %s", row, e.balance(consumer))
	}
	if writer.Body.String() != "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n" || row.ResponseBytes != int64(writer.Body.Len()) {
		t.Fatalf("client got %q, response bytes %d", writer.Body.String(), row.ResponseBytes)
	}

	// An upstream that dies after writing some output is an interruption.
	dying := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", "100000")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		w.(http.Flusher).Flush()
	})
	sharer2 := e.member("sharer2", "0")
	e.createChannel(sharer2, channelSpec{name: "dying", upstream: dying, model: "claude-test"})
	e.selectFixtureChannels(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"claude-test","stream":true}`), nil)
	if row := e.callRow(recorder.Header().Get("X-AIHub-Request-Id")); row.Outcome != "interrupted" || row.HasLedgerTx {
		t.Fatalf("interrupted stream = %+v", row)
	}
	e.assertZeroSum()
}

func TestChannelDiscoveryAndCompatibilityProbeDoNotGuessCapabilities(t *testing.T) {
	e := newEnv(t)
	sharer := e.member("sharer", "0")
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/models":
			io.WriteString(w, `{"data":[{"id":"GPT-TEST"},{"id":"unknown-model"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			io.WriteString(w, responsesBody)
		default:
			w.WriteHeader(404)
			io.WriteString(w, "upstream-key-test")
		}
	})
	discovery := e.apiJSON(sharer, 200, http.MethodPost, "/api/channels/discover", map[string]any{"base_url": relay.server.URL, "api_key": "upstream-key-test"})
	for _, raw := range discovery["upstream_models"].([]any) {
		item := raw.(map[string]any)
		if len(item["suggested_formats"].([]any)) != 0 {
			t.Fatal("guessed compatibility")
		}
	}
	id := e.createChannel(sharer, channelSpec{name: "probe", upstream: relay, key: "upstream-key-test"})
	tested := e.apiJSON(sharer, 200, http.MethodPost, "/api/channels/"+id+"/test", map[string]any{"apply": true})
	found := false
	for _, raw := range tested["results"].([]any) {
		item := raw.(map[string]any)
		if strings.Contains(fmt.Sprint(item["error"]), "upstream-key-test") {
			t.Fatal("secret leaked")
		}
		if item["protocol"] == "responses" && item["scope"] == "channel" && item["capability"] == "inference" {
			found = item["ok"] == true
		}
		if item["capability"] == "function_tools" && item["ok"] == true {
			t.Fatal("plain 200 incorrectly verified function tools")
		}
	}
	if !found {
		t.Fatal("inference not detected")
	}
	var calls int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM calls`).Scan(&calls); err != nil || calls != 0 {
		t.Fatal(calls, err)
	}
}

func TestKeyManagementDefaultKeyLimitSecretAuditAndDeletion(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	home := e.apiJSON(consumer, http.StatusOK, http.MethodGet, "/api/home", nil)
	defaultKey := home["default_key"].(map[string]any)
	if defaultKey["name"] != "默认 Key" || !strings.HasPrefix(defaultKey["prefix"].(string), "sk-aih-") {
		t.Fatalf("default key = %v", defaultKey)
	}
	again := e.apiJSON(consumer, http.StatusOK, http.MethodGet, "/api/home", nil)["default_key"].(map[string]any)
	if again["id"] != defaultKey["id"] {
		t.Fatal("the default key must be created once")
	}
	secret := e.apiJSON(consumer, http.StatusOK, http.MethodGet, "/api/keys/"+defaultKey["id"].(string)+"/secret", nil)["secret"].(string)
	if !strings.HasPrefix(secret, "sk-aih-") || !strings.HasPrefix(secret, defaultKey["prefix"].(string)) {
		t.Fatalf("secret = %q", secret)
	}
	var reveals int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'api_key.reveal' AND target_id = $1`, defaultKey["id"]).Scan(&reveals)
	if reveals != 1 {
		t.Fatalf("reveal audit rows = %d", reveals)
	}
	listed := e.apiJSON(consumer, http.StatusOK, http.MethodGet, "/api/keys", nil)["items"].([]any)
	if strings.Contains(fmt.Sprint(listed), secret) || listed[0].(map[string]any)["is_default"] != true {
		t.Fatalf("list leaks the secret or misses the default flag: %v", listed)
	}

	var lastID string
	for index := 2; index <= 20; index++ {
		created := e.apiJSON(consumer, http.StatusCreated, http.MethodPost, "/api/keys", map[string]any{"name": fmt.Sprintf("key %d", index)})
		if !strings.HasPrefix(created["secret"].(string), "sk-aih-") {
			t.Fatalf("created = %v", created)
		}
		lastID = created["key"].(map[string]any)["id"].(string)
	}
	over := e.api(consumer, http.MethodPost, "/api/keys", map[string]any{"name": "one too many"})
	if over.Code != http.StatusConflict || !strings.Contains(over.Body.String(), "key_limit_reached") {
		t.Fatalf("21st key = %d %s", over.Code, over.Body.String())
	}
	if recorder := e.api(consumer, http.MethodDelete, "/api/keys/"+lastID, nil); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", recorder.Code)
	}
	e.apiJSON(consumer, http.StatusCreated, http.MethodPost, "/api/keys", map[string]any{"name": "after delete", "budget_total": "5"})
	if recorder := e.api(consumer, http.MethodGet, "/api/keys/"+lastID, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("deleted key = %d", recorder.Code)
	}
	other := e.member("other", "0")
	if recorder := e.api(other, http.MethodGet, "/api/keys/"+defaultKey["id"].(string), nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("someone else's key = %d", recorder.Code)
	}
	if recorder := e.api(other, http.MethodGet, "/api/keys/"+defaultKey["id"].(string)+"/secret", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("someone else's secret = %d", recorder.Code)
	}
	if recorder := e.api(consumer, http.MethodPost, "/api/keys", map[string]any{"name": ""}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty name = %d", recorder.Code)
	}
}

func TestAdminChannelModerationAndPublicInformationStayPrivate(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	id := e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, key: "upstream-secret-key", advanced: map[string]any{
		"header_rules": map[string]any{"set": []any{map[string]any{"name": "X-Tenant", "value": "private-tenant-value"}}, "remove": []any{}},
	}})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)

	// What other users see must not reveal the Base URL, the key or header rules.
	for _, path := range []string{"/api/models", "/api/models/gpt-test", "/api/home"} {
		body := e.api(consumer, http.MethodGet, path, nil).Body.String()
		for _, forbidden := range []string{relay.server.URL, "upstream-secret-key", "private-tenant-value", "X-Tenant", "api_key_ciphertext"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s leaks %q: %s", path, forbidden, body)
			}
		}
	}
	for _, mine := range []string{e.api(sharer, http.MethodGet, "/api/channels", nil).Body.String(), e.api(sharer, http.MethodGet, "/api/channels/"+id, nil).Body.String(),
		e.api(e.admin, http.MethodGet, "/api/admin/channels", nil).Body.String(), e.api(e.admin, http.MethodGet, "/api/admin/channels/"+id, nil).Body.String()} {
		if strings.Contains(mine, "upstream-secret-key") {
			t.Fatalf("a channel read echoes the upstream key: %s", mine)
		}
	}
	if recorder := e.api(consumer, http.MethodGet, "/api/channels/"+id, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("someone else's channel = %d", recorder.Code)
	}
	if recorder := e.api(consumer, http.MethodGet, "/api/admin/channels", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("admin list as member = %d", recorder.Code)
	}

	if recorder := e.api(e.admin, http.MethodPost, "/api/admin/channels/"+id+"/suspend", map[string]any{"reason": ""}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("suspend without a reason = %d", recorder.Code)
	}
	suspended := e.apiJSON(e.admin, http.StatusOK, http.MethodPost, "/api/admin/channels/"+id+"/suspend", map[string]any{"reason": "abuse report"})
	if suspended["channel"].(map[string]any)["status"] != "suspended" || suspended["channel"].(map[string]any)["owner"].(map[string]any)["username"] != "sharer" {
		t.Fatalf("suspended = %v", suspended)
	}
	if recorder := e.api(e.admin, http.MethodPost, "/api/admin/channels/"+id+"/suspend", map[string]any{"reason": "again"}); recorder.Code != http.StatusConflict {
		t.Fatalf("second suspend = %d", recorder.Code)
	}
	if recorder := e.api(sharer, http.MethodPatch, "/api/channels/"+id, map[string]any{"status": "listed"}); recorder.Code != http.StatusConflict {
		t.Fatalf("owner relisting a suspended channel = %d %s", recorder.Code, recorder.Body.String())
	}
	if code := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusServiceUnavailable {
		t.Fatalf("call to a suspended channel = %d", code)
	}
	e.apiJSON(e.admin, http.StatusOK, http.MethodPost, "/api/admin/channels/"+id+"/unsuspend", map[string]any{"reason": "reviewed"})
	if code := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"SECRET-PROMPT-TEXT"}]}`), nil).Code; code != http.StatusOK {
		t.Fatalf("call after unsuspend = %d", code)
	}
	var audits int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action IN ('channel.suspended', 'channel.unsuspended') AND target_id = $1`, id).Scan(&audits)
	if audits != 2 {
		t.Fatalf("audit rows = %d", audits)
	}
	events := e.channelEvents(id)
	if fmt.Sprint(events) != "[listed suspended unsuspended]" {
		t.Fatalf("channel events = %v", events)
	}

	// The structured log lines carry identifiers only: no key, no body.
	logs := e.logs.String()
	for _, forbidden := range []string{secret, "upstream-secret-key", "SECRET-PROMPT-TEXT", "private-tenant-value"} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("log contains %q", forbidden)
		}
	}
	if !strings.Contains(logs, `"msg":"gateway request"`) || !strings.Contains(logs, `"key_prefix":"sk-aih-`) {
		t.Fatalf("expected gateway log lines, got %s", logs)
	}

	// Deleting hides the channel everywhere.
	if recorder := e.api(sharer, http.MethodDelete, "/api/channels/"+id, nil); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete channel = %d", recorder.Code)
	}
	if code := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusServiceUnavailable {
		t.Fatalf("call to a deleted channel = %d", code)
	}
}

func TestHomeAndModelListShowTodayNumbers(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), map[string]string{"X-AIHub-Tag": "nightly"})

	home := e.apiJSON(consumer, http.StatusOK, http.MethodGet, "/api/home", nil)
	today := home["today"].(map[string]any)
	if today["spend"] != expectedCharge || today["calls"] != float64(1) || today["succeeded_calls"] != float64(1) ||
		home["points"].(map[string]any)["balance"] != "-0.04004" || home["points"].(map[string]any)["available"] != "99.95996" {
		t.Fatalf("home = %v", home)
	}
	recent := home["recent_calls"].([]any)[0].(map[string]any)
	if recent["tag"] != "nightly" || recent["charged"] != expectedCharge || recent["outcome"] != "succeeded" {
		t.Fatalf("recent call = %v", recent)
	}
	sharerHome := e.apiJSON(sharer, http.StatusOK, http.MethodGet, "/api/home", nil)
	channels := sharerHome["channels"].(map[string]any)
	if channels["today_revenue"] != expectedCost || channels["online"] != float64(1) || channels["total"] != float64(1) {
		t.Fatalf("sharer home = %v", sharerHome)
	}
	models := e.apiJSON(consumer, http.StatusOK, http.MethodGet, "/api/models", nil)["items"].([]any)
	var found map[string]any
	for _, item := range models {
		if item.(map[string]any)["id"] == "gpt-test" {
			found = item.(map[string]any)
		}
	}
	if found["online_channels"] != float64(1) || fmt.Sprint(found["formats"]) != "[openai_chat openai_responses anthropic gemini]" ||
		found["lowest_prices"].(map[string]any)["input"] != "20" {
		t.Fatalf("model list entry = %v", found)
	}
}
