package postgres_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
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
	bad := e.api(sharer, http.MethodPost, "/api/channels", map[string]any{
		"name": "bad", "base_url": relay.server.URL, "api_key": "k", "models": []any{map[string]any{"model_id": "gpt-test", "formats": []string{"openai_chat"}}},
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
	e := newEnv(t)
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
	model := e.apiJSON(consumer, http.StatusOK, http.MethodGet, "/api/models/gpt-test", nil)
	if model["routing"].(map[string]any)["mode"] != "manual" || len(model["channels"].([]any)) != 3 {
		t.Fatalf("model detail = %v", model)
	}
	if e.apiJSON(consumer, http.StatusOK, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "fastest"})["mode"] != "fastest" {
		t.Fatal("fastest mode not stored")
	}
	if recorder := e.api(consumer, http.MethodPut, "/api/routing/gpt-test", map[string]any{"mode": "manual", "max_attempts": 11}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("max_attempts 11 = %d", recorder.Code)
	}
}

func TestGatewayEnforcesRPMDailyRevenueAndConcurrencyLimits(t *testing.T) {
	e := newEnv(t)
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

	capped := e.member("capped", "0")
	capUpstream := okUpstream(t)
	capChannel := e.createChannel(capped, channelSpec{name: "capped", upstream: capUpstream, model: "claude-test", advanced: map[string]any{"daily_revenue_cap": "0.05"}})
	callClaude := func() *httptest.ResponseRecorder {
		return e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"claude-test"}`), nil)
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
	started := time.Now()
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if recorder.Code != http.StatusOK || time.Since(started) < time.Second {
		t.Fatalf("call = %d after %s", recorder.Code, time.Since(started))
	}
	var endReason string
	err := e.pool.QueryRow(context.Background(), `SELECT attempts->0->>'end_reason' FROM calls WHERE id = $1`, recorder.Header().Get("X-AIHub-Request-Id")).Scan(&endReason)
	if err != nil || endReason != "timeout_ttft" {
		t.Fatalf("first attempt end reason = %q, %v", endReason, err)
	}
}

func TestGatewayPricesWithTheTierMatchingTheRealRequest(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	minimum := int64(900)
	catalogService := catalog.NewService(e.store.Catalog)
	if _, err := catalogService.Update(context.Background(), e.admin.id, "gpt-test", catalog.ModelPatch{PriceTiers: &[]ledger.PriceTier{
		{Name: "long", MinPromptTokens: &minimum, InputPrice: mustAmount(t, "40"), OutputPrice: mustAmount(t, "20")},
	}}); err != nil {
		t.Fatal(err)
	}
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
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
	e := newEnv(t)
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
	gemini := e.call("", http.MethodGet, "/v1beta/models?key="+secret, nil, nil)
	if gemini.Code != http.StatusOK || !strings.Contains(gemini.Body.String(), `"name":"models/gpt-test"`) {
		t.Fatalf("gemini models = %d %s", gemini.Code, gemini.Body.String())
	}
}

func TestGatewayRejectsBadKeysAndDisabledAccounts(t *testing.T) {
	e := newEnv(t)
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
	if _, err := e.pool.Exec(context.Background(), `UPDATE accounts SET status = 'disabled' WHERE id = $1`, consumer.id); err != nil {
		t.Fatal(err)
	}
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

func TestGatewayMarksAbandonedAndBrokenStreamsWithoutChargingUnreadUsage(t *testing.T) {
	e := newEnv(t)
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
	if row.Outcome != "client_disconnected" || row.HasLedgerTx || e.balance(consumer) != "0" {
		t.Fatalf("abandoned stream = %+v, balance %s", row, e.balance(consumer))
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

func TestChannelDiscoveryMatchesCatalogAndFormatTestsCorrectFormats(t *testing.T) {
	e := newEnv(t)
	sharer := e.member("sharer", "0")
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"GPT-TEST"},{"id":"claude-test-20250929"},{"id":"unknown-model"},{"id":"gemini-test"}]}`)
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/chat/completions"), strings.HasSuffix(r.URL.Path, "/messages"):
			_, _ = io.WriteString(w, chatBody)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "no such route key=upstream-key-test")
		}
	})
	discovered := e.apiJSON(sharer, http.StatusOK, http.MethodPost, "/api/channels/discover", map[string]any{"base_url": relay.server.URL + "/", "api_key": "upstream-key-test"})
	byID := map[string]map[string]any{}
	for _, item := range discovered["upstream_models"].([]any) {
		entry := item.(map[string]any)
		byID[entry["id"].(string)] = entry
	}
	if byID["GPT-TEST"]["matched_model_id"] != "gpt-test" || byID["claude-test-20250929"]["matched_model_id"] != "claude-test" ||
		byID["unknown-model"]["matched_model_id"] != nil || len(byID) != 4 {
		t.Fatalf("discovered = %v", byID)
	}
	if fmt.Sprint(byID["GPT-TEST"]["suggested_formats"]) != "[openai_chat openai_responses]" ||
		fmt.Sprint(byID["claude-test-20250929"]["suggested_formats"]) != "[openai_chat anthropic]" ||
		fmt.Sprint(byID["gemini-test"]["suggested_formats"]) != "[openai_chat gemini]" ||
		fmt.Sprint(byID["unknown-model"]["suggested_formats"]) != "[openai_chat]" {
		t.Fatalf("suggested formats = %v", byID)
	}
	if empty := e.apiJSON(sharer, http.StatusOK, http.MethodPost, "/api/channels/discover", map[string]any{"base_url": "https://127.0.0.1:1", "api_key": "k"}); len(empty["upstream_models"].([]any)) != 0 {
		t.Fatalf("unreachable upstream must yield an empty list: %v", empty)
	}

	id := e.createChannel(sharer, channelSpec{name: "mixed", upstream: relay, key: "upstream-key-test"})
	tested := e.apiJSON(sharer, http.StatusOK, http.MethodPost, "/api/channels/"+id+"/test", map[string]any{"apply": true})
	passed := map[string]bool{}
	for _, item := range tested["results"].([]any) {
		result := item.(map[string]any)
		passed[result["format"].(string)] = result["ok"].(bool)
		if result["ok"] == false && strings.Contains(fmt.Sprint(result["error"]), "upstream-key-test") {
			t.Fatalf("test error leaks the upstream key: %v", result)
		}
	}
	if !passed["openai_chat"] || !passed["anthropic"] || passed["openai_responses"] || passed["gemini"] {
		t.Fatalf("format results = %v", passed)
	}
	model := tested["channel"].(map[string]any)["models"].([]any)[0].(map[string]any)
	if fmt.Sprint(model["formats"]) != "[openai_chat anthropic]" || len(model["format_tests"].(map[string]any)) != 4 {
		t.Fatalf("applied model = %v", model)
	}
	var calls int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM calls`).Scan(&calls)
	failed := 0
	for _, kind := range e.channelEvents(id) {
		if kind == "test_failed" {
			failed++
		}
	}
	if calls != 0 || failed != 1 {
		t.Fatalf("tests must not write calls (%d) and record one test_failed event (%d)", calls, failed)
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
	if code := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusServiceUnavailable {
		t.Fatalf("call to a suspended channel = %d", code)
	}
	e.apiJSON(e.admin, http.StatusOK, http.MethodPost, "/api/admin/channels/"+id+"/unsuspend", map[string]any{"reason": "reviewed"})
	if code := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"SECRET-PROMPT-TEXT"}]}`), nil).Code; code != http.StatusOK {
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
	if code := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusServiceUnavailable {
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
