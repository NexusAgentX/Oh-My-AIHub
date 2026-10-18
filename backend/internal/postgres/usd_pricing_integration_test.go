package postgres_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func TestGroupUSDQuotesAlwaysConvertAndApplyWholeModelDiscount(t *testing.T) {
	e := newGroupEnv(t)
	ctx := context.Background()
	consumer, sharer := e.member("consumer", "100"), e.member("sharer", "0")
	relay := okUpstream(t)
	early := e.createChannel(sharer, channelSpec{name: "early", upstream: relay, multiplier: "2"})
	_, secret := e.defaultKey(consumer)
	choose := func(id string) {
		t.Helper()
		e.apiJSON(consumer, 200, "PUT", "/api/routing/gpt-test", map[string]any{"mode": "manual", "order": []string{id}})
	}
	send := func() (*httptest.ResponseRecorder, callRow) {
		t.Helper()
		reply := e.call(secret, "POST", "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
		if reply.Code != 200 {
			t.Fatal(reply.Code, reply.Body.String())
		}
		return reply, e.callRow(reply.Header().Get("X-AIHub-Request-Id"))
	}
	choose(early)
	original, originalRow := send()
	if originalRow.CostNano != 40_000_000 {
		t.Fatal("initial CNY quote", originalRow)
	}
	var before []byte
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, original.Header().Get("X-AIHub-Request-Id")).Scan(&before); err != nil {
		t.Fatal(err)
	}
	e.reviseGroupedQuote(early, "gpt-test", func(q *market.Quote) { q.Currency = "USD" })
	_, converted := send()
	if converted.CostNano != 272_000_000 {
		t.Fatal("changing quote currency must not invent an FX exemption", converted)
	}
	view := e.apiJSON(sharer, 200, "GET", "/api/group-channels/"+early, nil)
	if view["models"].([]any)[0].(map[string]any)["discount"] != "2" {
		t.Fatal("quote update changed channel discount", view)
	}
	e.patchGroupedChannel(sharer, early, map[string]any{"status": "unlisted"})
	discounted := e.createChannel(sharer, channelSpec{name: "discounted", upstream: relay, multiplier: "0.13"})
	plain := e.createChannel(sharer, channelSpec{name: "plain", upstream: relay, multiplier: "1"})
	for _, id := range []string{discounted, plain} {
		e.reviseGroupedQuote(id, "gpt-test", func(q *market.Quote) { q.Currency = "USD" })
	}
	// The fixture has no USD comparison workload; select explicitly rather than
	// fabricating a price ranking. Actual billing always uses the frozen quote.
	choose(discounted)
	first, row := send()
	if row.CostNano != 17_680_000 {
		t.Fatal("USD cost must be .02 * .13 * 6.8", row)
	}
	var raw []byte
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, first.Header().Get("X-AIHub-Request-Id")).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Currency            string            `json:"currency"`
		Rate                *string           `json:"usd_cny_rate"`
		FXDiscount          bool              `json:"fx_discount"`
		EffectiveMultiplier string            `json:"effective_multiplier"`
		Prices              map[string]string `json:"prices"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.Currency != "USD" || snapshot.Rate == nil || *snapshot.Rate != "6.8" || snapshot.FXDiscount || snapshot.EffectiveMultiplier != "0.13" || snapshot.Prices["input"] != "8.84" {
		t.Fatalf("USD snapshot=%s err=%v", raw, err)
	}
	e.patchGroupedChannel(sharer, discounted, map[string]any{"status": "unlisted"})
	choose(plain)
	_, row = send()
	if row.CostNano != 136_000_000 {
		t.Fatal("USD cost must be .02 * 6.8", row)
	}
	var after []byte
	if err := e.pool.QueryRow(ctx, `SELECT price_snapshot FROM calls WHERE id=$1`, original.Header().Get("X-AIHub-Request-Id")).Scan(&after); err != nil || string(before) != string(after) {
		t.Fatal("currency update rewrote historical CNY pricing", err)
	}
	e.assertZeroSum()
}

type fixedRate int64

func (f fixedRate) FetchUSDCNY(context.Context) (int64, error) { return int64(f), nil }

func TestExchangeRateSourceFollowsManualEditsAndSyncs(t *testing.T) {
	e := newGroupEnv(t)
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
