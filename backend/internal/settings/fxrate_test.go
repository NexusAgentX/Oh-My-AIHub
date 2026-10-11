package settings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
)

func TestSyncDueOncePerDayAfterTheSyncMinute(t *testing.T) {
	day := time.Date(2026, 10, 11, 0, 0, 0, 0, localtime.Zone)
	at := func(hour, minute int) time.Time {
		return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
	}
	yesterday := at(-14, 0)
	today := at(9, 31)
	value := Settings{FXAutoSync: true, FXSyncMinute: 9*60 + 30}
	cases := []struct {
		name      string
		auto      bool
		attempted *time.Time
		now       time.Time
		want      bool
	}{
		{"disabled", false, nil, at(10, 0), false},
		{"before the sync minute", true, &yesterday, at(9, 29), false},
		{"first attempt after the minute", true, nil, at(9, 30), true},
		{"last attempt was yesterday", true, &yesterday, at(12, 0), true},
		{"already attempted today", true, &today, at(23, 59), false},
	}
	for _, tc := range cases {
		value.FXAutoSync, value.FXSyncAttemptedAt = tc.auto, tc.attempted
		if got := SyncDue(value, tc.now); got != tc.want {
			t.Errorf("%s: due = %v, want %v", tc.name, got, tc.want)
		}
	}
}

type memoryRates struct {
	value  Settings
	synced int
}

func (m *memoryRates) GetSettings(context.Context) (Settings, error) { return m.value, nil }

func (m *memoryRates) SaveSyncedRate(_ context.Context, _ string, rate int64, at time.Time) (Settings, error) {
	m.synced++
	m.value.USDCNYRateNano, m.value.USDCNYRateSource, m.value.FXSyncAttemptedAt, m.value.FXSyncError = rate, RateSourceAuto, &at, nil
	return m.value, nil
}

func (m *memoryRates) SaveSyncFailure(_ context.Context, message string, at time.Time) (Settings, error) {
	m.value.FXSyncAttemptedAt, m.value.FXSyncError = &at, &message
	return m.value, nil
}

type fixedRate struct {
	rate int64
	err  error
}

func (f fixedRate) FetchUSDCNY(context.Context) (int64, error) { return f.rate, f.err }

func TestSyncRejectsImplausibleRates(t *testing.T) {
	for name, source := range map[string]fixedRate{
		"fetch failed":  {err: errors.New("offline")},
		"inverted rate": {rate: 147_000_000},
		"jump over 20%": {rate: 9_000_000_000},
		"drop over 20%": {rate: 5_000_000_000},
		"absurdly high": {rate: 60_000_000_000},
	} {
		store := &memoryRates{value: Settings{USDCNYRateNano: 7_000_000_000}}
		if _, err := NewRateSyncer(store, source).Sync(context.Background(), ""); err == nil {
			t.Errorf("%s: sync succeeded", name)
		}
		if store.synced != 0 || store.value.USDCNYRateNano != 7_000_000_000 || store.value.FXSyncError == nil {
			t.Errorf("%s: store = %+v", name, store.value)
		}
	}
	store := &memoryRates{value: Settings{USDCNYRateNano: 7_000_000_000}}
	if _, err := NewRateSyncer(store, fixedRate{rate: 7_120_000_000}).Sync(context.Background(), ""); err != nil || store.value.USDCNYRateNano != 7_120_000_000 {
		t.Fatalf("plausible rate: %v %+v", err, store.value)
	}
}

func TestSyncIfDueRunsOnlyWhenDue(t *testing.T) {
	store := &memoryRates{value: Settings{USDCNYRateNano: 7_000_000_000, FXAutoSync: true, FXSyncMinute: 0}}
	syncer := NewRateSyncer(store, fixedRate{rate: 7_100_000_000})
	if ran, err := syncer.SyncIfDue(context.Background()); !ran || err != nil {
		t.Fatalf("first run = %v %v", ran, err)
	}
	if ran, _ := syncer.SyncIfDue(context.Background()); ran {
		t.Fatal("ran twice on the same day")
	}
}

func TestFrankfurterSourceParsesTheRate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte(`{"amount":1.0,"base":"USD","date":"2026-10-09","rates":{"CNY":6.6921}}`))
		case "/missing":
			_, _ = w.Write([]byte(`{"amount":1.0,"base":"USD","rates":{"EUR":0.9}}`))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	source := &FrankfurterSource{URL: server.URL + "/ok", Client: server.Client()}
	if rate, err := source.FetchUSDCNY(context.Background()); err != nil || rate != 6_692_100_000 {
		t.Fatalf("rate = %d, %v", rate, err)
	}
	for _, path := range []string{"/missing", "/down"} {
		source.URL = server.URL + path
		if _, err := source.FetchUSDCNY(context.Background()); err == nil {
			t.Errorf("%s: no error", path)
		}
	}
}
