package postgres_test

import (
	"context"
	"encoding/json"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"net/http"
	"testing"
)

func TestTokenPricesPersistSettleAndKeepSnapshot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	service := catalog.NewService(e.store.Catalog)
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
		_, _ = w.Write([]byte(`{"service_tier":"default","choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":500,"prompt_tokens_details":{"audio_tokens":500},"completion_tokens_details":{"audio_tokens":100}}}`))
	})
	e.createChannel(sharer, channelSpec{name: "relay", upstream: relay, multiplier: "1"})
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
