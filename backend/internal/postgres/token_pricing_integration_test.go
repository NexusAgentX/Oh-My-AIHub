package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"net/http"
	"strings"
	"testing"
)

func TestTokenPricesPersistSettleAndKeepSnapshot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	service := e.catalog
	specific := map[string]money.Amount{"input_audio": 0, "output_audio": mustAmount(t, "40")}
	tiers := []ledger.PriceTier{{Name: "actual standard", ServiceTier: "openai:default", InputPrice: mustAmount(t, "10"), OutputPrice: mustAmount(t, "20"), TokenPrices: specific}}
	saved, err := service.Update(ctx, e.admin.id, "gpt-test", catalog.ModelPatch{TokenPrices: &specific, PriceTiers: &tiers})
	if err != nil || saved.TokenPrices["output_audio"] != specific["output_audio"] {
		t.Fatalf("save %+v %v", saved, err)
	}
	if value, present := saved.PriceTiers[0].TokenPrices["input_audio"]; !present || value != 0 {
		t.Fatal("explicit zero lost")
	}
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"service_tier":"default","choices":[],"usage":{"input_tokens":1000,"output_tokens":500,"input_tokens_details":{"audio_tokens":500},"output_tokens_details":{"audio_tokens":100}}}`))
	})
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
	keyID, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"budget_total": "0.017"})
	rec := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","service_tier":"priority"}`), nil)
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
	second := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
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
	empty := map[string]money.Amount{}
	saved, err = service.Update(ctx, e.admin.id, "gpt-test", catalog.ModelPatch{TokenPrices: &empty, PriceTiers: &[]ledger.PriceTier{}})
	if err != nil || len(saved.TokenPrices) != 0 {
		t.Fatal("clear", err)
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
	e := newEnv(t)
	ctx := context.Background()
	consumer := e.member("compat-consumer", "100")
	sharer := e.member("compat-sharer", "0")
	input, output, write, read := mustAmount(t, "10"), mustAmount(t, "20"), mustAmount(t, "30"), mustAmount(t, "40")
	if _, err := e.catalog.Update(ctx, e.admin.id, "gpt-test", catalog.ModelPatch{InputPrice: &input, OutputPrice: &output, CacheWritePrice: &write, CacheReadPrice: &read}); err != nil {
		t.Fatal(err)
	}
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + `{"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":300,"cache_creation_tokens":100,"cache_creation_token_details":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":60}}}}` + "\n\ndata: " + `{"usage":{"output_tokens":500}}` + "\n\ndata: [DONE]\n\n"))
	})
	e.createChannel(sharer, channelSpec{name: "compat-relay", upstream: relay, multiplier: "1"})
	keyID, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"budget_total": "0.031"})
	rec := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","stream":true,"stream_options":{"include_usage":true}}`), nil)
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
	second := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
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
	if _, err := e.catalog.Update(ctx, e.admin.id, "gpt-test", catalog.ModelPatch{InputPrice: &input}); err != nil {
		t.Fatal(err)
	}
	var after []byte
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("historical snapshot recalculated")
	}
}

func TestResponsesReasoningDoesNotCreateBailianPriceFacts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	consumer := e.member("thinking-consumer", "100")
	sharer := e.member("thinking-sharer", "0")
	input, output := mustAmount(t, "10"), mustAmount(t, "20")
	if _, err := e.catalog.Update(ctx, e.admin.id, "gpt-test", catalog.ModelPatch{InputPrice: &input, OutputPrice: &output}); err != nil {
		t.Fatal(err)
	}
	response := `{"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"private"}]}],"usage":{"input_tokens":1000,"output_tokens":500,"output_tokens_details":{"reasoning_tokens":300}}}`
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	})
	e.createChannel(sharer, channelSpec{name: "thinking-relay", upstream: relay, multiplier: "1"})
	keyID, secret := e.defaultKey(consumer)
	e.selectFixtureChannels(consumer)
	e.apiJSON(consumer, http.StatusOK, http.MethodPatch, "/api/keys/"+keyID, map[string]any{"budget_total": "0.02"})
	rec := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","enable_thinking":false}`), nil)
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
	second := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test"}`), nil)
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
	if d.ThinkingMode != "" || d.ThinkingModeBasis != "" || d.ThinkingObserved != nil {
		t.Fatalf("generic snapshot: %s", before)
	}
	if strings.Contains(string(before), "private") {
		t.Fatal("snapshot retained content")
	}
	tiers := []ledger.PriceTier{{ThinkingMode: "qwen_thinking", InputPrice: input, OutputPrice: mustAmount(t, "40")}}
	if _, err := e.catalog.Update(ctx, e.admin.id, "gpt-test", catalog.ModelPatch{PriceTiers: &tiers}); !errors.Is(err, catalog.ErrInvalidInput) {
		t.Fatalf("removed pricing predicate accepted: %v", err)
	}
	var after []byte
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("historical snapshot changed after rejected configuration")
	}

}
