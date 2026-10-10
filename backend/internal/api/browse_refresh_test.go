package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

const browseTestChannelID = "00000000-0000-4000-8000-0000000000f1"

// stuckStatsStore answers ChannelStats immediately until stick() is called,
// after which every query parks until its context ends (a slow 24h query).
type stuckStatsStore struct {
	gateway.Store
	mutex   sync.Mutex
	stuck   bool
	entered chan struct{}
}

func (s *stuckStatsStore) stick() {
	s.mutex.Lock()
	s.stuck = true
	s.mutex.Unlock()
}

func (s *stuckStatsStore) ChannelStats(ctx context.Context) (map[string]gateway.ChannelStats, error) {
	s.mutex.Lock()
	stuck := s.stuck
	s.mutex.Unlock()
	if stuck {
		select {
		case s.entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return map[string]gateway.ChannelStats{browseTestChannelID: {Attempts: 4, Successes: 3}}, nil
}

type oneOnlineChannel struct{ gateway.Browse }

func (oneOnlineChannel) OnlineChannels(context.Context) ([]gateway.OnlineChannel, error) {
	return []gateway.OnlineChannel{{ModelID: "deepseek-chat", ChannelID: browseTestChannelID, ChannelName: "渠道一", OwnerID: "00000000-0000-4000-8000-0000000000f2", OwnerName: "车主", Formats: []channel.Format{channel.FormatOpenAIResponses}, MultiplierNano: 1_000_000_000}}, nil
}

type noRoutingPreferences struct{ routing.Store }

func (noRoutingPreferences) Get(context.Context, string, string, string) (routing.Pref, error) {
	return routing.Pref{}, routing.ErrNotFound
}

// A stuck background refresh of the 24h channel stats must not hold up the
// model page: it keeps serving the previous stats, and the refresh goroutine
// ends with its context.
func TestModelPageServesPreviousStatsWhileRefreshIsStuck(t *testing.T) {
	spec := loadOpenAPI(t)
	store := newFakeStore()
	stats := &stuckStatsStore{entered: make(chan struct{}, 1)}
	policy, err := channel.NewOutboundPolicy(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	engine := gateway.NewEngine(gateway.Dependencies{Store: stats, Settings: settings.NewService(store), Outbound: policy})
	identityService, err := identity.NewService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Dependencies{
		Identity: identityService, Catalog: catalog.NewService(store), Ledger: ledger.NewService(store),
		Settings: settings.NewService(store), Audit: audit.NewService(store), CookieSecure: true,
		Gateway: engine, Browse: oneOnlineChannel{}, Routing: noRoutingPreferences{},
	})
	admin := bootstrap(t, spec, handler)
	admin.expect(t, http.StatusCreated, http.MethodPost, "/api/admin/models", map[string]any{
		"id": "deepseek-chat", "display_name": "DeepSeek Chat",
		"base_prices": map[string]string{"input": "1.5", "output": "4.5", "cache_write": "0", "cache_read": "0.15"},
	})

	successRate := func(decoded map[string]any) any {
		channels := decoded["channels"].([]any)
		if len(channels) != 1 {
			t.Fatalf("channels = %v", channels)
		}
		return channels[0].(map[string]any)["success_rate_24h"]
	}
	// The first request loads the stats itself (no refresh loop is running yet).
	if got := successRate(admin.expect(t, http.StatusOK, http.MethodGet, "/api/models/deepseek-chat", nil)); got != "0.750000" {
		t.Fatalf("success rate = %v, want 0.750000", got)
	}

	// Start the background refresh against a database whose stats query hangs.
	stats.stick()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		engine.RunRefresh(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	select {
	case <-stats.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh never reached the stats query")
	}

	type outcome struct {
		code int
		body []byte
	}
	served := make(chan outcome, 1)
	go func() {
		request := httptest.NewRequest(http.MethodGet, "https://hub.example/api/models/deepseek-chat", nil)
		request.Header.Set("Origin", "https://hub.example")
		request.AddCookie(admin.cookie)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		served <- outcome{recorder.Code, recorder.Body.Bytes()}
	}()
	select {
	case got := <-served:
		var decoded map[string]any
		if err := json.Unmarshal(got.body, &decoded); err != nil || got.code != http.StatusOK || successRate(decoded) != "0.750000" {
			t.Fatalf("model page = %d %s", got.code, got.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the model page waited for the stuck stats refresh")
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("RunRefresh did not return after its context ended")
	}
}
