package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
)

// fakeRateSource is the exchange-rate feed of the platform tests.
type fakeRateSource struct {
	mu       sync.Mutex
	rateNano int64
	err      error
}

func (f *fakeRateSource) FetchUSDCNY(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rateNano, f.err
}

func (f *fakeRateSource) set(rateNano int64, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rateNano, f.err = rateNano, err
}

func TestUSDModelsShowPointsAndFXDiscount(t *testing.T) {
	t.Parallel()
	p := newPlatform(t)
	admin := p.admin(t)
	alice, _ := p.member(t, "alice")

	// The seed rate is 6.8; a $1/M model shows 6.8 points/M to users.
	created := admin.expect(t, http.StatusCreated, http.MethodPost, "/api/admin/models", map[string]any{
		"id": "gpt-x", "display_name": "GPT X", "price_currency": "USD",
		"base_prices": map[string]string{"input": "1", "output": "4", "cache_write": "0", "cache_read": "0.1"},
	})["model"].(map[string]any)
	if created["price_currency"] != "USD" || created["base_prices"].(map[string]any)["input"] != "1" {
		t.Fatalf("created = %v", created)
	}
	if recorder := admin.call(t, http.MethodPost, "/api/admin/models", map[string]any{
		"id": "bad", "display_name": "Bad", "price_currency": "EUR",
		"base_prices": map[string]string{"input": "1", "output": "1", "cache_write": "0", "cache_read": "0"},
	}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown currency = %d", recorder.Code)
	}

	channelBody := func(fxDiscount bool, multiplier string) map[string]any {
		return map[string]any{
			"name": "中转", "base_url": "https://relay.example/v1", "api_key": "sk-upstream-test-key-0001",
			"advanced": map[string]any{"compatibility": map[string]any{"responses": []string{"inference", "stream"}, "codex": []string{"inference"}}}, "models": []map[string]any{{"model_id": "gpt-x", "multiplier": multiplier, "fx_discount": fxDiscount}},
		}
	}
	// 1 CNY = 1 USD relay with group multiplier 0.13: effective 0.13 / 6.8.
	relay := alice.expect(t, http.StatusCreated, http.MethodPost, "/api/channels", channelBody(true, "0.13"))["channel"].(map[string]any)
	row := relay["models"].([]any)[0].(map[string]any)
	if row["price_currency"] != "USD" || row["fx_discount"] != true || row["multiplier"] != "0.13" || row["effective_multiplier"] != "0.019117647" ||
		row["current_prices"].(map[string]any)["input"] != "0.13" {
		t.Fatalf("discounted channel model = %v", row)
	}
	// A relay that sells USD at the real rate: same multiplier, no discount.
	plain := alice.expect(t, http.StatusCreated, http.MethodPost, "/api/channels", channelBody(false, "1"))["channel"].(map[string]any)
	if row := plain["models"].([]any)[0].(map[string]any); row["effective_multiplier"] != "1" || row["current_prices"].(map[string]any)["input"] != "6.8" {
		t.Fatalf("plain channel model = %v", row)
	}

	page := alice.expect(t, http.StatusOK, http.MethodGet, "/api/models/gpt-x", nil)
	model := page["model"].(map[string]any)
	if model["price_currency"] != "USD" || model["usd_cny_rate"] != "6.8" || model["base_prices"].(map[string]any)["output"] != "27.2" || model["lowest_prices"].(map[string]any)["input"] != "0.13" {
		t.Fatalf("model page = %v", model)
	}
	for _, item := range page["channels"].([]any) {
		channel := item.(map[string]any)
		if channel["fx_discount"] == true && channel["effective_multiplier"] != "0.019117647" {
			t.Fatalf("discounted channel on model page = %v", channel)
		}
	}

	// A CNY model shows its figures as points unchanged.
	admin.expect(t, http.StatusOK, http.MethodPatch, "/api/admin/models/gpt-x", map[string]any{"price_currency": "CNY"})
	if listed := alice.expect(t, http.StatusOK, http.MethodGet, "/api/models", nil)["items"].([]any); listed[0].(map[string]any)["base_prices"].(map[string]any)["input"] != "1" {
		t.Fatalf("cny model = %v", listed)
	}

	// Exchange rate: manual edit, sync now, rejected and failed syncs.
	current := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/settings", nil)["settings"].(map[string]any)
	if current["usd_cny_rate"] != "6.8" || current["usd_cny_rate_source"] != "manual" || current["fx_auto_sync"] != false {
		t.Fatalf("settings = %v", current)
	}
	synced := admin.expect(t, http.StatusOK, http.MethodPost, "/api/admin/settings/exchange-rate/sync", nil)["settings"].(map[string]any)
	if synced["usd_cny_rate"] != "7.1" || synced["usd_cny_rate_source"] != "auto" || synced["fx_sync_error"] != nil || synced["fx_sync_attempted_at"] == nil {
		t.Fatalf("synced = %v", synced)
	}
	p.rates.set(140_000_000, nil) // an inverted feed (CNY per USD read upside down)
	if recorder := admin.call(t, http.MethodPost, "/api/admin/settings/exchange-rate/sync", nil); recorder.Code != http.StatusBadGateway {
		t.Fatalf("implausible rate = %d", recorder.Code)
	}
	p.rates.set(0, errors.New("offline"))
	if recorder := admin.call(t, http.MethodPost, "/api/admin/settings/exchange-rate/sync", nil); recorder.Code != http.StatusBadGateway {
		t.Fatalf("failed fetch = %d", recorder.Code)
	}
	after := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/settings", nil)["settings"].(map[string]any)
	if after["usd_cny_rate"] != "7.1" || after["fx_sync_error"] == nil {
		t.Fatalf("settings after failed sync = %v", after)
	}
	if recorder := alice.call(t, http.MethodPost, "/api/admin/settings/exchange-rate/sync", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("member sync = %d", recorder.Code)
	}
}
