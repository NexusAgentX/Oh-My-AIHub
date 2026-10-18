package postgres_test

import (
	"context"
	"encoding/json"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"net/http"
	"strings"
	"testing"
)

func TestTokenPricesPersistSettleAndKeepSnapshot(t *testing.T) {
	e := newGroupEnv(t)
	ctx := context.Background()
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	specific := map[string]money.Amount{"input_audio": 0, "output_audio": mustAmount(t, "40")}
	tiers := []market.Tier{{Name: "actual standard", Conditions: market.Conditions{ServiceTier: "openai:default"}, Prices: market.TokenPrices{Input: mustAmount(t, "10"), Output: mustAmount(t, "20"), Details: specific}}}
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"service_tier":"default","choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":500,"prompt_tokens_details":{"audio_tokens":500},"completion_tokens_details":{"audio_tokens":100}}}`))
	})
	channelID := e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	saved := e.reviseGroupedQuote(channelID, "gpt-test", func(q *market.Quote) { q.Prices.Details = specific; q.Tiers = tiers })
	if saved.Prices.Details["output_audio"] != specific["output_audio"] {
		t.Fatal("modality price lost", saved)
	}
	if value, present := saved.Tiers[0].Prices.Details["input_audio"]; !present || value != 0 {
		t.Fatal("explicit zero lost")
	}
	e.declareGroupedCapabilities(channelID, "upstream_protocol/openai-chat-completions.cache_tier/service_tier/openai:fast")
	keyID, secret := e.defaultKey(consumer)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"budget_total": "0.017"})
	rec := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test","service_tier":"priority"}`), nil)
	if rec.Code != 200 {
		t.Fatalf("call %d %s", rec.Code, rec.Body.String())
	}
	id := rec.Header().Get("X-AIHub-Request-Id")
	row := e.callRow(id)
	// 500 generic input *10 +500 audio input *0 +400 generic output *20 +100 audio output *40 = .017.
	if row.CostNano != 17_000_000 {
		t.Fatalf("cost %+v", row)
	}
	if e.balance(consumer) != "-0.017017" {
		t.Fatalf("balance %s", e.balance(consumer))
	}
	second := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if second.Code != http.StatusPaymentRequired {
		t.Fatalf("new token charges did not exhaust budget: %d", second.Code)
	}
	var before []byte
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var snap struct {
		Detail ledger.UsageDetail `json:"detail"`
		Prices map[string]string  `json:"token_prices"`
	}
	if json.Unmarshal(before, &snap) != nil || snap.Detail.ServiceTier != "openai:default" || snap.Prices["input_audio"] != "0" {
		t.Fatal(string(before))
	}
	saved = e.reviseGroupedQuote(channelID, "gpt-test", func(q *market.Quote) { q.Prices.Details = map[string]money.Amount{}; q.Tiers = nil })
	if len(saved.Prices.Details) != 0 {
		t.Fatal("detail prices did not clear")
	}
	var after []byte
	_ = e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, id).Scan(&after)
	if string(before) != string(after) {
		t.Fatal("historical snapshot changed")
	}
}

// The new alias/partial-frame shapes must reach the same atomic persistence,
// budget accounting and immutable snapshot path as native usage.
func TestCompatibleUsagePersistsSettlesAndExhaustsBudget(t *testing.T) {
	e := newGroupEnv(t)
	ctx := context.Background()
	consumer := e.member("compat-consumer", "100")
	sharer := e.member("compat-sharer", "0")
	input, output, write, read := mustAmount(t, "10"), mustAmount(t, "20"), mustAmount(t, "30"), mustAmount(t, "40")
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + `{"usage":{"prompt_tokens":1000,"prompt_tokens_details":{"cached_tokens":300,"cache_creation_tokens":100,"cache_creation_token_details":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":60}}}}` + "\n\ndata: " + `{"usage":{"completion_tokens":500}}` + "\n\ndata: [DONE]\n\n"))
	})
	channelID := e.createChannel(sharer, channelSpec{name: "compat-relay", upstream: relay, multiplier: "1"})
	e.reviseGroupedQuote(channelID, "gpt-test", func(q *market.Quote) {
		q.Prices = market.TokenPrices{Input: input, Output: output, CacheWrite: write, CacheRead: read}
	})
	keyID, secret := e.defaultKey(consumer)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"budget_total": "0.031"})
	rec := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test","stream":true,"stream_options":{"include_usage":true}}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("call %d %s", rec.Code, rec.Body.String())
	}
	id := rec.Header().Get("X-AIHub-Request-Id")
	row := e.callRow(id)
	// .006 fresh input + .010 output + .003 write + .012 read.
	if row.CostNano != 31_000_000 || row.Input != 600 || row.Output != 500 || !row.HasLedgerTx {
		t.Fatalf("normalized cost %+v", row)
	}
	if got := e.balance(consumer); got != "-0.031031" {
		t.Fatalf("balance %s", got)
	}
	second := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if second.Code != http.StatusPaymentRequired {
		t.Fatalf("budget was not exhausted: %d", second.Code)
	}
	var before []byte
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var snap struct {
		Detail ledger.UsageDetail `json:"detail"`
	}
	if err := json.Unmarshal(before, &snap); err != nil || snap.Detail.Tokens["cache_write_5m"] != 40 || snap.Detail.Tokens["cache_write_1h"] != 60 {
		t.Fatalf("snapshot %s err=%v", before, err)
	}
	input = mustAmount(t, "100")
	e.reviseGroupedQuote(channelID, "gpt-test", func(q *market.Quote) { q.Prices.Input = input })
	var after []byte
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("historical snapshot recalculated")
	}
}

func TestGenericThinkingObservationKeepsBillingBudgetAndSnapshot(t *testing.T) {
	e := newGroupEnv(t)
	ctx := context.Background()
	consumer := e.member("thinking-consumer", "100")
	sharer := e.member("thinking-sharer", "0")
	input, output := mustAmount(t, "10"), mustAmount(t, "20")
	response := `{"choices":[{"message":{"reasoning_content":"private","reasoning":"private"}}],"usage":{"prompt_tokens":1000,"completion_tokens":500,"completion_tokens_details":{"reasoning_tokens":300}}}`
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	})
	channelID := e.createChannel(sharer, channelSpec{name: "thinking-relay", upstream: relay, multiplier: "1"})
	e.reviseGroupedQuote(channelID, "gpt-test", func(q *market.Quote) { q.Prices.Input = input; q.Prices.Output = output })
	keyID, secret := e.defaultKey(consumer)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"budget_total": "0.02"})
	rec := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test","enable_thinking":false}`), nil)
	if rec.Code != http.StatusOK || rec.Body.String() != response {
		t.Fatalf("transparent response: %d %s", rec.Code, rec.Body.String())
	}
	id := rec.Header().Get("X-AIHub-Request-Id")
	row := e.callRow(id)
	if row.CostNano != 20_000_000 || row.Output != 500 || !row.HasLedgerTx {
		t.Fatalf("bill changed: %+v", row)
	}
	if balance := e.balance(consumer); balance != "-0.02002" {
		t.Fatal("balance", balance)
	}
	second := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if second.Code != http.StatusPaymentRequired {
		t.Fatal("budget", second.Code)
	}
	var before []byte
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Detail ledger.UsageDetail `json:"detail"`
	}
	if err := json.Unmarshal(before, &snapshot); err != nil {
		t.Fatal(err)
	}
	d := snapshot.Detail
	if d.ThinkingMode != "" || d.ThinkingModeBasis != "" || d.ThinkingObserved == nil || !*d.ThinkingObserved {
		t.Fatalf("generic snapshot: %s", before)
	}
	if strings.Contains(string(before), "private") {
		t.Fatal("snapshot retained content")
	}
	threshold := int64(0)
	e.reviseGroupedQuote(channelID, "gpt-test", func(q *market.Quote) {
		q.Tiers = []market.Tier{{Conditions: market.Conditions{MinPromptTokens: &threshold}, Prices: market.TokenPrices{Input: input, Output: mustAmount(t, "40")}}}
	})
	var after []byte
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("historical snapshot changed after group quote update")
	}
	// A later call sees the new token tier; thinking remains a generic observation.
	next := e.member("next-consumer", "100")
	_, nextSecret := e.defaultKey(next)
	configured := e.call(nextSecret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if configured.Code != http.StatusOK {
		t.Fatalf("configured call %d %s", configured.Code, configured.Body.String())
	}
	id = configured.Header().Get("X-AIHub-Request-Id")
	if row := e.callRow(id); row.CostNano != 30_000_000 {
		t.Fatalf("configured cost %+v", row)
	}
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Detail.ThinkingMode != "" || snapshot.Detail.ThinkingModeBasis != "" || snapshot.Detail.ThinkingObserved == nil || !*snapshot.Detail.ThinkingObserved {
		t.Fatalf("configured snapshot %s", after)
	}
}
