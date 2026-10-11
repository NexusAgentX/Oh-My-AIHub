package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func TestUSDModelSettlesAtTheExchangeRateUnlessDiscounted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":500}}`))
	})
	channel := func(name, multiplier string, fxDiscount bool) string {
		created := e.apiJSON(sharer, http.StatusCreated, http.MethodPost, "/api/channels", map[string]any{
			"name": name, "base_url": relay.server.URL, "api_key": "upstream-key-" + name,
			"models": []any{map[string]any{"model_id": "gpt-test", "multiplier": multiplier, "fx_discount": fxDiscount, "formats": []string{"openai_chat"}}},
		})
		return created["channel"].(map[string]any)["id"].(string)
	}
	// A channel listed while gpt-test was still a CNY model: switching the
	// model to USD turns its discount on, so its price does not change.
	early := channel("early", "2", false)
	currency := ledger.CurrencyUSD
	if _, err := catalog.NewService(e.store.Catalog).Update(ctx, e.admin.id, "gpt-test", catalog.ModelPatch{PriceCurrency: &currency}); err != nil {
		t.Fatal(err)
	}
	var discount bool
	if err := e.pool.QueryRow(ctx, `SELECT fx_discount FROM channel_models WHERE channel_id = $1`, early).Scan(&discount); err != nil || !discount {
		t.Fatalf("early channel discount = %v %v", discount, err)
	}
	e.apiJSON(sharer, http.StatusOK, http.MethodPatch, "/api/channels/"+early, map[string]any{"status": "unlisted"})

	discounted := channel("discounted", "0.13", true)
	channel("plain", "1", false)
	_, secret := e.defaultKey(consumer)

	// gpt-test costs $10/M input and $20/M output: 1000 + 500 tokens are $0.02.
	// Cheapest first picks the discounted relay: $0.02 × 0.13 = 0.0026 points.
	first := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if first.Code != http.StatusOK {
		t.Fatalf("call %d %s", first.Code, first.Body.String())
	}
	row := e.callRow(first.Header().Get("X-AIHub-Request-Id"))
	if row.CostNano != 2_600_000 {
		t.Fatalf("discounted cost = %+v", row)
	}
	var raw []byte
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id = $1`, first.Header().Get("X-AIHub-Request-Id")).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Currency            string            `json:"currency"`
		Rate                *string           `json:"usd_cny_rate"`
		FXDiscount          bool              `json:"fx_discount"`
		EffectiveMultiplier string            `json:"effective_multiplier"`
		Prices              map[string]string `json:"prices"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.Currency != "USD" || snapshot.Rate == nil || *snapshot.Rate != "6.8" ||
		!snapshot.FXDiscount || snapshot.EffectiveMultiplier != "0.019117647" || snapshot.Prices["input"] != "1.3" {
		t.Fatalf("snapshot = %s", raw)
	}

	// Without the discount the dollar price is converted: $0.02 × 1 × 6.8 = 0.136.
	e.apiJSON(sharer, http.StatusOK, http.MethodPatch, "/api/channels/"+discounted, map[string]any{"status": "unlisted"})
	second := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	if second.Code != http.StatusOK {
		t.Fatalf("call %d %s", second.Code, second.Body.String())
	}
	if row := e.callRow(second.Header().Get("X-AIHub-Request-Id")); row.CostNano != 136_000_000 {
		t.Fatalf("converted cost = %+v", row)
	}
}

type fixedRate int64

func (f fixedRate) FetchUSDCNY(context.Context) (int64, error) { return int64(f), nil }

func TestExchangeRateSourceFollowsManualEditsAndSyncs(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	service := settings.NewService(e.store.Settings)
	current, err := service.Get(ctx)
	if err != nil || current.USDCNYRateNano != 6_800_000_000 || current.USDCNYRateSource != settings.RateSourceManual {
		t.Fatalf("default rate = %+v %v", current, err)
	}
	synced, err := settings.NewRateSyncer(e.store.Settings, fixedRate(7_100_000_000)).Sync(ctx, "")
	if err != nil || synced.USDCNYRateNano != 7_100_000_000 || synced.USDCNYRateSource != settings.RateSourceAuto || synced.FXSyncAttemptedAt == nil {
		t.Fatalf("synced = %+v %v", synced, err)
	}
	// Saving the form with the rate untouched keeps it automatic; a new value is manual.
	synced.FXAutoSync = true
	kept, err := service.Update(ctx, e.admin.id, synced)
	if err != nil || kept.USDCNYRateSource != settings.RateSourceAuto || !kept.FXAutoSync {
		t.Fatalf("kept = %+v %v", kept, err)
	}
	kept.USDCNYRateNano = 7_000_000_000
	manual, err := service.Update(ctx, e.admin.id, kept)
	if err != nil || manual.USDCNYRateSource != settings.RateSourceManual || manual.USDCNYRateNano != 7_000_000_000 {
		t.Fatalf("manual = %+v %v", manual, err)
	}
	var audits int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'settings.exchange_rate_synced' AND actor_id IS NULL`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("sync audits = %d %v", audits, err)
	}
}
