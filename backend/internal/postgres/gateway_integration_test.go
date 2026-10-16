package postgres_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestGatewayForwardsResponsesAndCodexByteForByteAndBooksEachCall(t *testing.T) {
	e := newEnv(t)
	consumer, sharer := e.member("consumer", "100"), e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, upstreamAs: "gpt-up", formats: []string{"openai_responses", "codex"}})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	for _, path := range []string{"/v1/responses", "/backend-api/codex/responses"} {
		for _, stream := range []bool{false, true} {
			raw := `{ "input":[{"role":"user","content":"café \"model\": 1"}], "model" : "gpt-test", "store":false, "stream":` + map[bool]string{false: "false", true: "true"}[stream] + ` }`
			result := e.call(secret, http.MethodPost, path, []byte(raw), map[string]string{"Cookie": "private", "X-AIHub-Tag": "suite"})
			expected := responsesBody
			if stream {
				expected = responsesStreamBody
			}
			if result.Code != 200 || result.Body.String() != expected {
				t.Fatalf("%s stream=%v: %d %s", path, stream, result.Code, result.Body.String())
			}
			requests := relay.inference()
			seen := requests[len(requests)-1]
			if string(seen.Body) != strings.Replace(raw, `"gpt-test"`, `"gpt-up"`, 1) || seen.Path != path || seen.Header.Get("Cookie") != "" {
				t.Fatalf("forwarding changed: %+v", seen)
			}
			row := e.callRow(result.Header().Get("X-AIHub-Request-Id"))
			if row.Outcome != "succeeded" || row.CostNano != 40_000_000 || row.FeeNano != 40_000 || !row.HasLedgerTx {
				t.Fatal(row)
			}
		}
	}
	if e.balance(consumer) != "-0.16016" || e.balance(sharer) != "0.16" {
		t.Fatal("wrong four-call settlement")
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
	e.selectFixtureChannels(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
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
	e.selectFixtureChannels(consumer)

	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
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
	e.selectFixtureChannels(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
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
	e.selectFixtureChannels(consumer)
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
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
	e.selectFixtureChannels(consumer)
	for range 2 {
		if code := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusBadGateway {
			t.Fatalf("failing call = %d", code)
		}
	}
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
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

func TestGatewayRateLimitWithRetryAfterRestsTheChannelForThatLong(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	var limited atomic.Bool
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if !limited.Swap(true) {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, "slow down")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatBody)
	})
	// The default threshold is three failures in a row: the single 429 alone would not cool the channel.
	id := e.createChannel(sharer, channelSpec{name: "limited", upstream: relay, advanced: map[string]any{"cooldown_seconds": 60}})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	send := func() *httptest.ResponseRecorder {
		return e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	}

	started := time.Now()
	if first := send(); first.Code != http.StatusTooManyRequests || first.Body.String() != "slow down" {
		t.Fatalf("limited call = %d %q", first.Code, first.Body.String())
	}
	// The channel is skipped while the upstream asked for quiet...
	if second := send(); second.Code != http.StatusServiceUnavailable || !strings.Contains(second.Body.String(), `"no_channel_available"`) {
		t.Fatalf("call during the wait = %d %s", second.Code, second.Body.String())
	}
	if hits := len(relay.inference()); hits != 1 {
		t.Fatalf("upstream hit %d times during the wait", hits)
	}
	// ...and available again once the two seconds are over, long before the 60 second cooldown.
	deadline := time.Now().Add(10 * time.Second)
	for {
		recorder := send()
		if recorder.Code == http.StatusOK {
			break
		}
		if recorder.Code != http.StatusServiceUnavailable || time.Now().After(deadline) {
			t.Fatalf("call after the wait = %d %s (%s since the 429)", recorder.Code, recorder.Body.String(), time.Since(started))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if waited := time.Since(started); waited < 2*time.Second {
		t.Fatalf("channel came back after %s, before the Retry-After of 2s", waited)
	}
	if hits := len(relay.inference()); hits != 2 {
		t.Fatalf("upstream hit %d times, want the 429 and the recovered call", hits)
	}

	var reasons []string
	rows, err := e.pool.Query(context.Background(), `SELECT reason FROM channel_events WHERE channel_id = $1 AND kind = $2`, id, channel.EventCooldownStarted)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var reason string
		_ = rows.Scan(&reason)
		reasons = append(reasons, reason)
	}
	if len(reasons) != 1 || reasons[0] != gateway.CooldownRateLimited {
		t.Fatalf("cooldown_started reasons = %v", reasons)
	}
	if events := e.channelEvents(id); events[len(events)-1] != channel.EventCooldownEnded {
		t.Fatalf("events = %v", events)
	}
}

func TestGatewayRateLimitWithoutRetryAfterIsOnlyCounted(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	var limited atomic.Bool
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if !limited.Swap(true) {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatBody)
	})
	id := e.createChannel(sharer, channelSpec{name: "limited", upstream: relay})
	_, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	body := []byte(`{"model":"gpt-test"}`)
	if first := e.call(secret, http.MethodPost, "/v1/responses", body, nil); first.Code != http.StatusTooManyRequests {
		t.Fatalf("limited call = %d", first.Code)
	}
	if second := e.call(secret, http.MethodPost, "/v1/responses", body, nil); second.Code != http.StatusOK {
		t.Fatalf("call right after a 429 without a hint = %d %s", second.Code, second.Body.String())
	}
	for _, kind := range e.channelEvents(id) {
		if kind == channel.EventCooldownStarted {
			t.Fatal("a 429 without Retry-After started a cooldown")
		}
	}
}

func TestGatewayChecksBalanceAndKeyBudgetWithoutReserving(t *testing.T) {
	e := newEnv(t)
	sharer := e.member("sharer", "0")
	consumer := e.member("consumer", "0.05") // credit limit: one call overdraws, the next is refused
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay})
	keyID, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)

	first := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first call = %d %s", first.Code, first.Body.String())
	}
	if got := e.balance(consumer); got != "-0.04004" {
		t.Fatalf("balance = %s", got)
	}
	second := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	if second.Code != http.StatusOK || e.balance(consumer) != "-0.08008" {
		t.Fatalf("overdrawing call = %d, balance %s", second.Code, e.balance(consumer))
	}
	third := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
	if third.Code != http.StatusPaymentRequired || !strings.Contains(third.Body.String(), `"insufficient_balance"`) || third.Header().Get("X-AIHub-Request-Id") == "" {
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
	e.selectFixtureChannels(rich)
	e.apiJSON(rich, http.StatusOK, http.MethodPatch, "/api/keys/"+budgetKeyID, map[string]any{"budget_daily": "0.04"})
	if code := e.call(budgetSecret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil).Code; code != http.StatusOK {
		t.Fatalf("within budget = %d", code)
	}
	refused := e.call(budgetSecret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
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
	e.selectFixtureChannels(owner)
	recorder := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
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
