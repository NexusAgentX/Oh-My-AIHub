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

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func TestGatewayAppliesChannelHeaderRulesAndUserAgent(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	channelID := e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, advanced: map[string]any{
		"user_agent":   "relay-agent/1.0",
		"header_rules": map[string]any{"set": []any{map[string]any{"name": "X-Relay-Tenant", "value": "acme"}, map[string]any{"name": "X-Client", "value": "overwritten"}}, "remove": []any{"X-Strip"}},
	}})
	_, secret := e.defaultKey(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), map[string]string{
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
	current := e.apiJSON(sharer, 200, "GET", "/api/group-channels/"+channelID, nil)
	bad := e.api(sharer, "PATCH", "/api/group-channels/"+channelID, map[string]any{"revision": current["revision"], "overrides": map[string]any{"header_rules": map[string]any{"set": []any{map[string]any{"name": "Authorization", "value": "x"}}, "remove": []any{}}}})
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("authorization rule = %d %s", bad.Code, bad.Body.String())
	}
}

func TestGatewayKeepsResponsesConversationsOnTheSameChannel(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	pricey := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		_, _ = io.WriteString(w, `{"id":"resp_pricey","object":"response","usage":{"input_tokens":10,"output_tokens":5}}`)
	})
	cheap := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		_, _ = io.WriteString(w, `{"id":"resp_cheap","object":"response","usage":{"input_tokens":10,"output_tokens":5}}`)
	})
	priceyID := e.createChannel(sharer, channelSpec{name: "pricey", upstream: pricey, multiplier: "5"})
	e.declareGroupedCapabilities(priceyID, "upstream_protocol/openai-responses.state/previous_response_id_three_turns")
	e.createChannel(sharer, channelSpec{name: "cheap", upstream: cheap, multiplier: "1"})
	_, secret := e.defaultKey(consumer)
	// Route the first turn to the pricey channel by ticking only it.
	e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "manual", "order": []string{priceyID}})
	first := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","input":"a"}`), nil)
	if first.Code != http.StatusOK || len(pricey.inference()) != 1 {
		t.Fatalf("first turn = %d, pricey hits %d", first.Code, len(pricey.inference()))
	}
	// Back to cheapest-first: a follow-up with previous_response_id still returns to the first channel.
	e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "cheapest"})
	second := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","input":"b","previous_response_id":"resp_pricey"}`), nil)
	if second.Code != http.StatusOK || len(pricey.inference()) != 2 || len(cheap.inference()) != 0 {
		t.Fatalf("follow-up = %d, pricey %d cheap %d", second.Code, len(pricey.inference()), len(cheap.inference()))
	}
	unrelated := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","input":"c"}`), nil)
	if unrelated.Code != http.StatusOK || len(cheap.inference()) != 1 {
		t.Fatalf("unrelated request should take the cheapest channel: cheap %d", len(cheap.inference()))
	}
}

func TestRoutingModesExclusionAndKeyOverride(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	a, b, c := okUpstream(t), okUpstream(t), okUpstream(t)
	idA := e.createChannel(sharer, channelSpec{name: "a", upstream: a, multiplier: "1"})
	idB := e.createChannel(sharer, channelSpec{name: "b", upstream: b, multiplier: "2"})
	idC := e.createChannel(sharer, channelSpec{name: "c", upstream: c, multiplier: "3"})
	keyID, secret := e.defaultKey(consumer)
	send := func() { e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil) }
	hits := func() [3]int { return [3]int{len(a.inference()), len(b.inference()), len(c.inference())} }

	send() // default: cheapest first
	if hits() != [3]int{1, 0, 0} {
		t.Fatalf("default routing hits = %v", hits())
	}
	// Unticking the cheapest channel moves traffic to the next one.
	e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "cheapest", "excluded": []string{idA}})
	send()
	if hits() != [3]int{1, 1, 0} {
		t.Fatalf("excluded routing hits = %v", hits())
	}
	// Manual order puts the most expensive first; a channel missing from the list counts as unticked.
	saved := e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "manual", "order": []string{idC, idA}, "max_attempts": 2})
	if saved["source"] != "account" || saved["mode"] != "manual" || saved["max_attempts"] != float64(2) {
		t.Fatalf("saved routing = %v", saved)
	}
	send()
	if hits() != [3]int{1, 1, 1} {
		t.Fatalf("manual routing hits = %v", hits())
	}
	// A key-level setting overrides the account for that key only, and deleting it restores the account setting.
	e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/keys/"+keyID+"/routing/gpt-test", map[string]any{"mode": "manual", "order": []string{idB}})
	send()
	if hits() != [3]int{1, 2, 1} {
		t.Fatalf("key override hits = %v", hits())
	}
	detail := e.apiJSON(consumer, http.StatusOK, http.MethodGet, "/api/keys/"+keyID, nil)
	if len(detail["routing"].([]any)) != 1 || detail["key"].(map[string]any)["routed_models"].([]any)[0] != "gpt-test" {
		t.Fatalf("key detail = %v", detail)
	}
	if recorder := e.api(consumer, http.MethodDelete, "/api/keys/"+keyID+"/routing/gpt-test", nil); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete key routing = %d", recorder.Code)
	}
	send()
	if hits() != [3]int{1, 2, 2} {
		t.Fatalf("restored routing hits = %v", hits())
	}
	model := e.apiJSON(consumer, http.StatusOK, http.MethodGet, "/api/market/models/gpt-test", nil)
	if model["routing"].(map[string]any)["mode"] != "manual" || len(model["routing_channels"].([]any)) != 3 {
		t.Fatalf("model detail = %v", model)
	}
	if e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "fastest"})["mode"] != "fastest" {
		t.Fatal("fastest mode not stored")
	}
	if recorder := e.api(consumer, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "manual", "max_attempts": 11}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("max_attempts 11 = %d", recorder.Code)
	}
}

// A non-streaming call's first byte is the finished answer, so its "first
// token" time is the whole generation. Only streaming calls may feed the
// first-token median that the fast routing mode ranks by; success counts keep
// every call.
func TestChannelFirstTokenMedianCountsOnlyStreamingCalls(t *testing.T) {
	e := newGroupEnv(t)
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
		return firstAttemptTTFT(e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(body), nil))
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

func TestGatewayEnforcesRPMAndConcurrencyLimits(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "1000")
	sharer := e.member("sharer", "0")
	_, secret := e.defaultKey(consumer)
	call := func() *httptest.ResponseRecorder {
		return e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	}

	rpm := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "rpm", upstream: rpm, advanced: map[string]any{"rpm_limit": 1}})
	if call().Code != http.StatusOK {
		t.Fatal("first request refused")
	}
	if refused := call(); refused.Code != http.StatusServiceUnavailable || !strings.Contains(refused.Body.String(), `"no_channel_available"`) {
		t.Fatalf("second request within the minute = %d %s", refused.Code, refused.Body.String())
	}

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
	done := make(chan int, 1)
	go func() {
		done <- e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gemini-test"}`), nil).Code
	}()
	<-started
	if refused := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gemini-test"}`), nil); refused.Code != http.StatusServiceUnavailable {
		t.Fatalf("concurrent request = %d %s", refused.Code, refused.Body.String())
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("held request = %d", code)
	}
}

func TestGatewayFallsBackOnFirstByteTimeout(t *testing.T) {
	e := newGroupEnv(t)
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
	started := time.Now()
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test","stream":true}`), nil)
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
	e := newGroupEnv(t)
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
	started := time.Now()
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
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
	e := newGroupEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	minimum := int64(900)
	relay := okUpstream(t)
	channelID := e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	configuration, err := e.groupStore.Get(context.Background(), channelID)
	if err != nil {
		t.Fatal(err)
	}
	var offering market.GroupModel
	for _, m := range marketSnapshot(t, e.store).Models {
		if m.GroupID == configuration.GroupID && m.ModelID == "gpt-test" {
			offering = m
		}
	}
	e.publishGroupFixture(market.Changes{Quotes: []market.Quote{{ID: marketID(t), GroupModelID: offering.ID, Currency: "CNY", BillingUnit: "per_million_tokens", Prices: market.TokenPrices{Input: mustAmount(t, "10"), Output: mustAmount(t, "20")}, Tiers: []market.Tier{{Name: "long", Conditions: market.Conditions{MinPromptTokens: &minimum}, Prices: market.TokenPrices{Input: mustAmount(t, "40"), Output: mustAmount(t, "20")}}}}}})
	_, secret := e.defaultKey(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
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
	e := newGroupEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, upstreamAs: "real-name"})
	keyID, secret := e.defaultKey(consumer)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{
		"model_aliases": map[string]string{"gpt-4o": "gpt-test"}, "allowed_models": []string{"gpt-test"},
	})
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-4o"}`), nil)
	if recorder.Code != http.StatusOK || string(relay.inference()[0].Body) != `{"model":"real-name"}` {
		t.Fatalf("aliased call = %d, upstream body %s", recorder.Code, relay.inference()[0].Body)
	}
	if row := e.callRow(recorder.Header().Get("X-AIHub-Request-Id")); row.ModelID != "gpt-test" {
		t.Fatalf("resolved model = %q", row.ModelID)
	}
	forbidden := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"claude-test"}`), nil)
	if forbidden.Code != http.StatusForbidden || !strings.Contains(forbidden.Body.String(), "model_not_allowed") {
		t.Fatalf("model outside the allowed list = %d %s", forbidden.Code, forbidden.Body.String())
	}
	if missing := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"nope"}`), nil); missing.Code != http.StatusForbidden {
		t.Fatalf("unknown model with an allowed list = %d", missing.Code)
	}
	if noModel := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"messages":[]}`), nil); noModel.Code != http.StatusBadRequest {
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
	anthropic := e.call("", http.MethodGet, "/v1/models?limit=1", nil, map[string]string{"x-api-key": secret, "anthropic-version": "2023-06-01"})
	if anthropic.Code != http.StatusOK || !strings.Contains(anthropic.Body.String(), `"type":"model"`) || !strings.Contains(anthropic.Body.String(), `"has_more":true`) || !strings.Contains(anthropic.Body.String(), `"lifecycle":"active"`) {
		t.Fatalf("anthropic models = %d %s", anthropic.Code, anthropic.Body.String())
	}
	if first := e.call("", http.MethodGet, "/v1beta/models?pageSize=1&key="+secret, nil, nil); first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"nextPageToken"`) || !strings.Contains(first.Body.String(), `"baseModelId"`) {
		t.Fatalf("gemini first page = %d %s", first.Code, first.Body.String())
	}
	if bad := e.call("", http.MethodGet, "/v1beta/models?pageToken=bm9wZQ&key="+secret, nil, nil); bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), `"status":"INVALID_ARGUMENT"`) {
		t.Fatalf("gemini bad token = %d %s", bad.Code, bad.Body.String())
	}
	gemini := e.call("", http.MethodGet, "/v1beta/models?key="+secret, nil, nil)
	if gemini.Code != http.StatusOK || !strings.Contains(gemini.Body.String(), `"name":"models/gpt-test"`) {
		t.Fatalf("gemini models = %d %s", gemini.Code, gemini.Body.String())
	}
}

func TestGatewayRejectsBadKeysAndDisabledAccounts(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "100")
	keyID, secret := e.defaultKey(consumer)
	for name, key := range map[string]string{"none": "", "unknown": "sk-aih-doesnotexist", "foreign": "sk-other-123"} {
		if code := e.call(key, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusUnauthorized {
			t.Fatalf("%s key = %d", name, code)
		}
	}
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"status": "disabled"})
	disabled := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if disabled.Code != http.StatusUnauthorized || e.callRow(disabled.Header().Get("X-AIHub-Request-Id")).Outcome != "rejected_key" {
		t.Fatalf("disabled key = %d %s", disabled.Code, disabled.Body.String())
	}
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"status": "enabled", "expires_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)})
	if code := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("expired key = %d", code)
	}
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"expires_at": nil})
	e.apiJSON(e.admin, http.StatusOK, http.MethodPatch, "/api/admin/accounts/"+consumer.id, map[string]any{"status": "disabled"})
	if code := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusForbidden {
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
	e := newGroupEnvWith(t, func(d *gateway.Dependencies) { d.DrainWindow = time.Second })
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	twoFrames := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		flusher.Flush()
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":500}}\n\ndata: [DONE]\n\n")
	})
	e.createChannel(sharer, channelSpec{name: "stream", upstream: twoFrames})
	_, secret := e.defaultKey(consumer)

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","stream":true}`))
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
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"claude-test","stream":true}`), nil)
	if row := e.callRow(recorder.Header().Get("X-AIHub-Request-Id")); row.Outcome != "interrupted" || row.HasLedgerTx {
		t.Fatalf("interrupted stream = %+v", row)
	}
	e.assertZeroSum()
}

func TestGroupKeyProbesPersistNativeFormatsWithoutBilling(t *testing.T) {
	e := newGroupEnv(t)
	sharer := e.member("sharer", "0")
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			io.WriteString(w, `{"id":"fixture","object":"chat.completion","created":1,"model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		case strings.HasSuffix(r.URL.Path, "/messages"):
			io.WriteString(w, `{"id":"fixture","type":"message","role":"assistant","model":"gpt-test","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`)
		default:
			w.WriteHeader(404)
			io.WriteString(w, "no such route key=upstream-key-test")
		}
	})
	id := e.createChannel(sharer, channelSpec{name: "mixed", upstream: relay, key: "upstream-key-test"})
	prober := groupchannel.NewProber(context.Background(), groupchannel.ProbeConfig{Runtime: e.groups, Repository: e.groupStore, Keyring: e.keyring, Edited: e.engine.ChannelEdited, Outbound: func(context.Context) (channel.Outbound, error) { return e.engine.Outbound, nil }})
	defer prober.Close()
	before := e.apiJSON(sharer, 200, "GET", "/api/group-channels/"+id, nil)
	reply := e.api(sharer, "POST", "/api/group-channels/"+id+"/test", map[string]any{"revision": before["revision"]})
	if reply.Code != 202 {
		t.Fatal(reply.Code, reply.Body.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	var view map[string]any
	for {
		view = e.apiJSON(sharer, 200, "GET", "/api/group-channels/"+id, nil)
		model := view["models"].([]any)[0].(map[string]any)
		if model["probe_state"] != "pending" && len(model["format_tests"].(map[string]any)) == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("key probe did not persist", view)
		}
		time.Sleep(10 * time.Millisecond)
	}
	model := view["models"].([]any)[0].(map[string]any)
	tests := model["format_tests"].(map[string]any)
	for _, format := range []string{"openai_chat", "anthropic", "openai_responses", "gemini"} {
		result := tests[format].(map[string]any)
		want := format == "openai_chat" || format == "anthropic"
		if result["ok"] != want || strings.Contains(fmt.Sprint(result), "upstream-key-test") {
			t.Fatal("native probe result or redaction", format, result)
		}
	}
	if len(model["formats"].([]any)) != 2 {
		t.Fatal("unsupported formats remained routable", model)
	}
	var calls, reports int
	if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM calls),(SELECT count(*) FROM detection_reports)`).Scan(&calls, &reports); err != nil || calls != 0 || reports != 0 {
		t.Fatal("Key probes must not create billed calls or capability reports", calls, reports, err)
	}
}

func TestKeyManagementDefaultKeyLimitSecretAuditAndDeletion(t *testing.T) {
	e := newGroupEnv(t)
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
	e := newGroupEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	id := e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, key: "upstream-secret-key", advanced: map[string]any{
		"header_rules": map[string]any{"set": []any{map[string]any{"name": "X-Tenant", "value": "private-tenant-value"}}, "remove": []any{}},
	}})
	_, secret := e.defaultKey(consumer)
	for _, path := range []string{"/api/market/models", "/api/market/models/gpt-test", "/api/market/channels/" + id, "/api/home"} {
		reply := e.api(consumer, http.MethodGet, path, nil)
		if reply.Code != http.StatusOK {
			t.Fatalf("public %s: %d %s", path, reply.Code, reply.Body.String())
		}
		for _, forbidden := range []string{relay.server.URL, strings.Replace(relay.server.URL, "127.0.0.1", "example.com", 1), "upstream-secret-key", "private-tenant-value", "X-Tenant", "api_key_ciphertext"} {
			if strings.Contains(reply.Body.String(), forbidden) {
				t.Fatalf("%s leaks %q", path, forbidden)
			}
		}
	}
	for _, view := range []struct {
		actor person
		path  string
	}{
		{sharer, "/api/group-channels"}, {sharer, "/api/group-channels/" + id}, {e.admin, "/api/admin/group-channels"}, {e.admin, "/api/admin/group-channels/" + id},
	} {
		reply := e.api(view.actor, http.MethodGet, view.path, nil)
		if reply.Code != http.StatusOK || strings.Contains(reply.Body.String(), "upstream-secret-key") || strings.Contains(reply.Body.String(), "api_key_ciphertext") {
			t.Fatalf("private metadata read: %d %s", reply.Code, reply.Body.String())
		}
	}
	if got := e.api(consumer, http.MethodGet, "/api/group-channels/"+id, nil).Code; got != http.StatusNotFound {
		t.Fatalf("another owner's channel: %d", got)
	}
	if got := e.api(consumer, http.MethodGet, "/api/admin/group-channels", nil).Code; got != http.StatusForbidden {
		t.Fatalf("admin list as member: %d", got)
	}
	moderate := "/api/admin/group-channels/" + id + "/moderate"
	if got := e.api(e.admin, http.MethodPost, moderate, map[string]any{"reason": strings.Repeat("x", 1001)}).Code; got != http.StatusUnprocessableEntity {
		t.Fatalf("overlong moderation reason: %d", got)
	}
	if got := e.api(e.admin, http.MethodPost, moderate, map[string]any{"reason": "abuse report"}).Code; got != http.StatusNoContent {
		t.Fatalf("moderation: %d", got)
	}
	suspended := e.apiJSON(e.admin, 200, "GET", "/api/admin/group-channels/"+id, nil)
	if suspended["status"] != "suspended" || suspended["owner"].(map[string]any)["username"] != "sharer" {
		t.Fatal(suspended)
	}
	if got := e.api(sharer, http.MethodPatch, "/api/group-channels/"+id, map[string]any{"revision": suspended["revision"], "status": "listed"}).Code; got != http.StatusConflict {
		t.Fatalf("owner relisted suspended channel: %d", got)
	}
	if got := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("suspended channel routed: %d", got)
	}
	// The fixed group API uses an explicit empty reason to restore a channel.
	if got := e.api(e.admin, http.MethodPost, moderate, map[string]any{"reason": ""}).Code; got != http.StatusNoContent {
		t.Fatalf("restore: %d", got)
	}
	reply := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"SECRET-PROMPT-TEXT"}]}`), nil)
	if reply.Code != http.StatusOK {
		t.Fatalf("restored call: %d %s", reply.Code, reply.Body.String())
	}
	callID := reply.Header().Get("X-AIHub-Request-Id")
	var audits int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action='group_channel.moderated' AND target_id=$1`, id).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("moderation audit", audits, err)
	}
	if events := e.channelEvents(id); fmt.Sprint(events) != "[listed suspended unsuspended]" {
		t.Fatalf("events: %v", events)
	}
	logs := e.logs.String()
	for _, forbidden := range []string{secret, "upstream-secret-key", "SECRET-PROMPT-TEXT", "private-tenant-value"} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("log contains %q", forbidden)
		}
	}
	if !strings.Contains(logs, `"msg":"gateway request"`) || !strings.Contains(logs, `"key_prefix":"sk-aih-`) {
		t.Fatal("missing gateway log identifiers")
	}
	current := e.apiJSON(sharer, 200, "GET", "/api/group-channels/"+id, nil)
	if got := e.api(sharer, http.MethodDelete, "/api/group-channels/"+id, map[string]any{"revision": current["revision"]}).Code; got != http.StatusNoContent {
		t.Fatalf("delete: %d", got)
	}
	if got := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("deleted channel routed: %d", got)
	}
	historical := e.callRow(callID)
	if historical.FinalChannel != id || !historical.HasLedgerTx {
		t.Fatalf("business delete removed historical settlement: %+v", historical)
	}
	e.assertZeroSum()
}

func TestHomeAndModelListShowTodayNumbers(t *testing.T) {
	e := newGroupEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay})
	_, secret := e.defaultKey(consumer)
	e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), map[string]string{"X-AIHub-Tag": "nightly"})

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
	models := e.apiJSON(consumer, http.StatusOK, http.MethodGet, "/api/market/models", nil)["items"].([]any)
	var found map[string]any
	for _, item := range models {
		if item.(map[string]any)["id"] == "gpt-test" {
			found = item.(map[string]any)
		}
	}
	if found["online_channels"] != float64(1) || len(found["formats"].([]any)) != 4 {
		t.Fatalf("model list entry = %v", found)
	}
	detail := e.apiJSON(consumer, 200, "GET", "/api/market/models/gpt-test", nil)
	if rows := detail["routing_channels"].([]any); len(rows) != 1 || rows[0].(map[string]any)["multiplier"] != "2" {
		t.Fatal(detail)
	}
	var quoted bool
	for _, raw := range detail["offers"].([]any) {
		offer := raw.(map[string]any)
		if offer["quote"] != nil {
			q := offer["quote"].(map[string]any)
			if q["currency"] != "CNY" || q["prices"].(map[string]any)["input"] != "10" {
				t.Fatal("independent quote changed", q)
			}
			quoted = true
		}
	}
	if !quoted {
		t.Fatal("missing approved quote", detail)
	}
}
