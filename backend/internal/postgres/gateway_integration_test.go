package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// 1000 input + 500 output tokens at 10/20 points per million, times a 2x channel
// multiplier: (1000*10 + 500*20) / 1e6 * 2 = 0.04, and a 0.1% fee of 0.00004.
const (
	expectedCost   = "0.04"
	expectedFee    = "0.00004"
	expectedCharge = "0.04004"
)

func TestGatewayForwardsAllFourFormatsByteForByteAndBooksEachCall(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, upstreamAs: "gpt-up"})
	_, secret := e.defaultKey(consumer)

	// Whitespace, key order, escapes and numbers must reach the upstream untouched.
	chat := `{ "messages":[{"role":"user","content":"café \"model\": 1"}],  "temperature": 0.70, "model" : "gpt-test" }`
	chatStream := `{"model":"gpt-test","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	responses := `{"input" : "hi","model":"gpt-test","max_output_tokens":16}`
	anthropic := `{"model":"gpt-test","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`
	cases := []struct {
		name, path, body, wantUpstreamBody, wantResponse, contentType string
		headers                                                       map[string]string
	}{
		{"chat", "/v1/chat/completions", chat, strings.Replace(chat, `"gpt-test"`, `"gpt-up"`, 1), chatBody, "application/json", nil},
		{"chat stream", "/v1/chat/completions", chatStream, `{"model":"gpt-up","stream":true,"messages":[{"role":"user","content":"hi"}],"stream_options":{"include_usage":true}}`, chatStreamBody, "text/event-stream", nil},
		{"responses", "/v1/responses", responses, strings.Replace(responses, `"gpt-test"`, `"gpt-up"`, 1), responsesBody, "application/json", nil},
		{"anthropic", "/v1/messages", anthropic, strings.Replace(anthropic, `"gpt-test"`, `"gpt-up"`, 1), anthropicBody, "application/json",
			map[string]string{"anthropic-version": "2023-06-01"}},
	}
	before := len(relay.inference())
	charged := money.Amount(0)
	for index, tc := range cases {
		headers := tc.headers
		if headers == nil {
			headers = map[string]string{}
		}
		headers["Cookie"] = "session=secret"
		headers["X-AIHub-Tag"] = "suite"
		recorder := e.call(secret, http.MethodPost, tc.path, []byte(tc.body), headers)
		if recorder.Code != http.StatusOK || recorder.Body.String() != tc.wantResponse {
			t.Fatalf("%s: %d %q", tc.name, recorder.Code, recorder.Body.String())
		}
		if got := recorder.Header().Get("Content-Type"); got != tc.contentType {
			t.Fatalf("%s: content type %q", tc.name, got)
		}
		id := recorder.Header().Get("X-AIHub-Request-Id")
		if id == "" {
			t.Fatalf("%s: missing request id", tc.name)
		}
		seen := relay.inference()[before+index]
		if string(seen.Body) != tc.wantUpstreamBody {
			t.Fatalf("%s: upstream body\n got %s\nwant %s", tc.name, seen.Body, tc.wantUpstreamBody)
		}
		if seen.Header.Get("Authorization") != "Bearer upstream-key-relay" || seen.Header.Get("Cookie") != "" || seen.Header.Get("X-Aihub-Tag") != "" {
			t.Fatalf("%s: upstream headers %v", tc.name, seen.Header)
		}
		row := e.callRow(id)
		if row.Outcome != "succeeded" || row.Input != 1000 || row.Output != 500 || row.CostNano != 40_000_000 || row.FeeNano != 40_000 || !row.HasLedgerTx || row.Attempts != 1 {
			t.Fatalf("%s: call = %+v", tc.name, row)
		}
		charged += money.FromNano(row.CostNano + row.FeeNano)
	}
	if charged.String() != "0.16016" {
		t.Fatalf("charged = %s", charged)
	}
	if got := e.balance(consumer); got != "-0.16016" {
		t.Fatalf("consumer balance = %s", got)
	}
	if got := e.balance(sharer); got != "0.16" {
		t.Fatalf("sharer balance = %s", got)
	}
	if got := e.systemBalance("platform_revenue"); got != "160000" {
		t.Fatalf("platform revenue nano = %s", got)
	}
	e.assertZeroSum()

	// Gemini carries the model in the path and the key in a query parameter.
	gemini := `{"contents":[{"parts":[{"text":"hi"}]}]}`
	recorder := e.call("", http.MethodPost, "/v1beta/models/gpt-test:generateContent?key="+secret+"&alt=json", []byte(gemini), nil)
	if recorder.Code != http.StatusOK || recorder.Body.String() != geminiBody {
		t.Fatalf("gemini: %d %q", recorder.Code, recorder.Body.String())
	}
	seen := relay.inference()[before+len(cases)]
	if seen.Path != "/v1beta/models/gpt-up:generateContent" || seen.Query != "alt=json&key=upstream-key-relay" || string(seen.Body) != gemini || seen.Header.Get("Authorization") != "" {
		t.Fatalf("gemini upstream request = %+v", seen)
	}
	streamed := e.call(secret, http.MethodPost, "/v1beta/models/gpt-test:streamGenerateContent?alt=sse", []byte(gemini), map[string]string{"x-goog-api-key": secret, "Authorization": ""})
	if streamed.Code != http.StatusOK {
		t.Fatalf("gemini stream: %d %s", streamed.Code, streamed.Body.String())
	}
	if seen := relay.inference()[before+len(cases)+1]; seen.Path != "/v1beta/models/gpt-up:streamGenerateContent" || seen.Header.Get("X-Goog-Api-Key") != "upstream-key-relay" {
		t.Fatalf("gemini stream upstream request = %+v", seen)
	}
	e.assertZeroSum()
}

func TestGatewayResponseIsUnchangedWhenUsageCannotBeRead(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "relay=1")
		_, _ = io.WriteString(w, noUsageBody)
	})
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay})
	_, secret := e.defaultKey(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if recorder.Code != http.StatusOK || recorder.Body.String() != noUsageBody || recorder.Header().Get("Set-Cookie") != "" {
		t.Fatalf("response = %d %q %v", recorder.Code, recorder.Body.String(), recorder.Header())
	}
	row := e.callRow(recorder.Header().Get("X-AIHub-Request-Id"))
	if row.Outcome != "succeeded_unbilled" || row.CostNano != 0 || row.HasLedgerTx {
		t.Fatalf("call = %+v", row)
	}
	if e.balance(consumer) != "0" || e.balance(sharer) != "0" {
		t.Fatalf("balances changed: %s %s", e.balance(consumer), e.balance(sharer))
	}
}

func TestGatewayFallsBackBeforeAnyByteIsWritten(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	broken := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"boom"}`)
	})
	good := okUpstream(t)
	brokenID := e.createChannel(sharer, channelSpec{name: "broken", upstream: broken, multiplier: "1"})
	goodID := e.createChannel(sharer, channelSpec{name: "good", upstream: good, multiplier: "2"})
	_, secret := e.defaultKey(consumer)

	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if recorder.Code != http.StatusOK || recorder.Body.String() != chatBody {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	row := e.callRow(recorder.Header().Get("X-AIHub-Request-Id"))
	if row.Outcome != "succeeded" || row.Attempts != 2 || row.FinalChannel != goodID {
		t.Fatalf("call = %+v (broken=%s good=%s)", row, brokenID, goodID)
	}
	if len(broken.inference()) != 1 || len(good.inference()) != 1 {
		t.Fatalf("attempts: broken %d good %d", len(broken.inference()), len(good.inference()))
	}
}

func TestGatewayReturnsClientErrorsUnchangedWithoutFallbackOrCharge(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	rejecting := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("X-Upstream", "kept")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"bad param"}}`)
	})
	second := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "strict", upstream: rejecting, multiplier: "1"})
	e.createChannel(sharer, channelSpec{name: "other", upstream: second, multiplier: "2"})
	_, secret := e.defaultKey(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if recorder.Code != http.StatusBadRequest || recorder.Body.String() != `{"error":{"message":"bad param"}}` || recorder.Header().Get("X-Upstream") != "kept" {
		t.Fatalf("response = %d %s %v", recorder.Code, recorder.Body.String(), recorder.Header())
	}
	if len(second.inference()) != 0 {
		t.Fatal("a request-level error must not try another channel")
	}
	row := e.callRow(recorder.Header().Get("X-AIHub-Request-Id"))
	if row.Outcome != "upstream_failed" || row.HasLedgerTx || row.CostNano != 0 {
		t.Fatalf("call = %+v", row)
	}
}

func TestGatewayAnswersWithTheLastUpstreamFailureWhenEveryChannelFails(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	status := func(code int, body string) *upstream {
		return newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
			w.WriteHeader(code)
			_, _ = io.WriteString(w, body)
		})
	}
	e.createChannel(sharer, channelSpec{name: "limited", upstream: status(429, "slow down"), multiplier: "1"})
	e.createChannel(sharer, channelSpec{name: "unauthorized", upstream: status(401, "bad key"), multiplier: "2"})
	_, secret := e.defaultKey(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if recorder.Code != http.StatusUnauthorized || recorder.Body.String() != "bad key" {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
	row := e.callRow(recorder.Header().Get("X-AIHub-Request-Id"))
	if row.Outcome != "upstream_failed" || row.Attempts != 2 || row.HasLedgerTx {
		t.Fatalf("call = %+v", row)
	}
	e.assertZeroSum()
}

func TestGatewayCooldownTakesAChannelOutAfterConsecutiveFailures(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	failing := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) { w.WriteHeader(http.StatusBadGateway) })
	id := e.createChannel(sharer, channelSpec{name: "flaky", upstream: failing, advanced: map[string]any{"cooldown_failures": 2, "cooldown_seconds": 60}})
	_, secret := e.defaultKey(consumer)
	for range 2 {
		if code := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusBadGateway {
			t.Fatalf("failing call = %d", code)
		}
	}
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"no_channel_available"`) {
		t.Fatalf("call during cooldown = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(failing.inference()) != 2 {
		t.Fatalf("upstream hit %d times", len(failing.inference()))
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		kinds := e.channelEvents(id)
		if len(kinds) >= 2 && kinds[len(kinds)-1] == channel.EventCooldownStarted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("events = %v", kinds)
		}
		time.Sleep(20 * time.Millisecond)
	}
	model := e.apiJSON(consumer, http.StatusOK, http.MethodGet, "/api/models/gpt-test", nil)
	listed := model["channels"].([]any)[0].(map[string]any)
	if listed["state"] != "cooldown" || listed["cooldown_remaining_seconds"] == nil {
		t.Fatalf("channel state = %v", listed)
	}
}

func TestGatewayChecksBalanceAndKeyBudgetWithoutReserving(t *testing.T) {
	e := newEnv(t)
	sharer := e.member("sharer", "0")
	consumer := e.member("consumer", "0.05") // credit limit: one call overdraws, the next is refused
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay})
	keyID, secret := e.defaultKey(consumer)

	first := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first call = %d %s", first.Code, first.Body.String())
	}
	if got := e.balance(consumer); got != "-0.04004" {
		t.Fatalf("balance = %s", got)
	}
	second := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if second.Code != http.StatusOK || e.balance(consumer) != "-0.08008" {
		t.Fatalf("overdrawing call = %d, balance %s", second.Code, e.balance(consumer))
	}
	third := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if third.Code != http.StatusPaymentRequired || !strings.Contains(third.Body.String(), `"insufficient_balance"`) || !strings.Contains(third.Body.String(), `"request_id"`) {
		t.Fatalf("refused call = %d %s", third.Code, third.Body.String())
	}
	if row := e.callRow(third.Header().Get("X-AIHub-Request-Id")); row.Outcome != "rejected_balance" {
		t.Fatalf("call = %+v", row)
	}
	if len(relay.inference()) != 2 {
		t.Fatalf("a refused call must not reach the upstream: %d", len(relay.inference()))
	}

	// A key budget refuses once its window spend has reached the limit.
	rich := e.member("rich", "1000")
	budgetKeyID, budgetSecret := e.defaultKey(rich)
	e.apiJSON(rich, http.StatusOK, http.MethodPatch, "/api/keys/"+budgetKeyID, map[string]any{"budget_daily": "0.04"})
	if code := e.call(budgetSecret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusOK {
		t.Fatalf("within budget = %d", code)
	}
	refused := e.call(budgetSecret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if refused.Code != http.StatusPaymentRequired || !strings.Contains(refused.Body.String(), `"key_budget_exceeded"`) {
		t.Fatalf("over budget = %d %s", refused.Code, refused.Body.String())
	}
	keys := e.apiJSON(rich, http.StatusOK, http.MethodGet, "/api/keys", nil)["items"].([]any)
	spend := keys[0].(map[string]any)["spend"].(map[string]any)
	if spend["today"] != expectedCharge || spend["total"] != expectedCharge {
		t.Fatalf("spend = %v", spend)
	}
	_ = keyID
}

func TestGatewayOwnChannelNetsToZeroWithoutFee(t *testing.T) {
	e := newEnv(t)
	owner := e.member("owner", "10")
	relay := okUpstream(t)
	e.createChannel(owner, channelSpec{name: "mine", upstream: relay})
	_, secret := e.defaultKey(owner)
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("call = %d", recorder.Code)
	}
	row := e.callRow(recorder.Header().Get("X-AIHub-Request-Id"))
	if row.Outcome != "succeeded" || row.FeeNano != 0 || row.CostNano != 40_000_000 {
		t.Fatalf("call = %+v", row)
	}
	if e.balance(owner) != "0" || e.systemBalance("platform_revenue") != "0" {
		t.Fatalf("own-channel call moved points: %s %s", e.balance(owner), e.systemBalance("platform_revenue"))
	}
	e.assertZeroSum()
}

func TestFinishingACallTwiceBooksItOnce(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	id, err := e.store.Gateway.InsertCall(context.Background(), gateway.CallInsert{
		AccountID: consumer.id, RequestedModel: "gpt-test", Format: channel.FormatOpenAIChat, Outcome: gateway.OutcomeInProgress,
	})
	if err != nil {
		t.Fatal(err)
	}
	finish := gateway.CallFinish{
		ID: id, AccountID: consumer.id, Outcome: gateway.OutcomeSucceeded, ModelID: "gpt-test", Cost: money.FromNano(40_000_000), Fee: money.FromNano(40_000),
		Bill: &gateway.Billing{ConsumerID: consumer.id, ProviderID: sharer.id, Cost: money.FromNano(40_000_000), Fee: money.FromNano(40_000)},
	}
	first, err := e.store.Gateway.FinishCall(context.Background(), finish)
	if err != nil || !first.Finished || first.TxID == "" {
		t.Fatalf("first finish = %+v, %v", first, err)
	}
	second, err := e.store.Gateway.FinishCall(context.Background(), finish)
	if err != nil || second.Finished {
		t.Fatalf("second finish = %+v, %v", second, err)
	}
	var transactions int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM ledger_transactions WHERE idempotency_key = $1`, "call:"+id).Scan(&transactions)
	if transactions != 1 || e.balance(consumer) != "-0.04004" || e.balance(sharer) != "0.04" {
		t.Fatalf("transactions=%d balances %s %s", transactions, e.balance(consumer), e.balance(sharer))
	}
	e.assertZeroSum()
}

func TestStaleCallsAreInterruptedWithoutCharge(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	id, err := e.store.Gateway.InsertCall(context.Background(), gateway.CallInsert{
		AccountID: consumer.id, RequestedModel: "gpt-test", Format: channel.FormatOpenAIChat, Outcome: gateway.OutcomeInProgress,
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := e.store.Gateway.InsertCall(context.Background(), gateway.CallInsert{AccountID: consumer.id, RequestedModel: "gpt-test", Format: channel.FormatOpenAIChat, Outcome: gateway.OutcomeInProgress})
	if _, err := e.pool.Exec(context.Background(), `UPDATE calls SET created_at = now() - interval '2 hours' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	swept, err := e.store.Gateway.SweepStaleCalls(context.Background(), time.Now().Add(-time.Hour))
	if err != nil || swept != 1 {
		t.Fatalf("swept = %d, %v", swept, err)
	}
	if e.callRow(id).Outcome != "interrupted" || e.callRow(fresh).Outcome != "in_progress" || e.balance(consumer) != "0" {
		t.Fatalf("stale %+v fresh %+v", e.callRow(id), e.callRow(fresh))
	}
}

func jsonOf(t *testing.T, recorder interface{ Bytes() []byte }) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

var _ = bytes.MinRead
