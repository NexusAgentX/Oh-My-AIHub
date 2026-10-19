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

func TestPlatformExchangeRateSyncStates(t *testing.T) {
	t.Parallel()
	p := newPlatform(t)
	admin := p.admin(t)
	alice, _ := p.member(t, "alice")

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
