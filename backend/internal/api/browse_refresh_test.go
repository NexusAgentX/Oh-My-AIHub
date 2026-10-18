package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity/identitytest"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

const browseTestChannelID = "00000000-0000-4000-8000-0000000000f1"

type oneOnlineChannel struct{ gateway.Browse }

func (oneOnlineChannel) OnlineChannels(context.Context) ([]gateway.OnlineChannel, error) {
	return []gateway.OnlineChannel{{ModelID: "deepseek-chat", ChannelID: browseTestChannelID, ChannelName: "渠道一", OwnerID: "00000000-0000-4000-8000-0000000000f2", OwnerName: "车主", Formats: []channel.Format{channel.FormatOpenAIResponses}, MultiplierNano: 1_000_000_000}}, nil
}

type noRoutingPreferences struct{ routing.Store }

func (noRoutingPreferences) Get(context.Context, string, string, string) (routing.Pref, error) {
	return routing.Pref{}, routing.ErrNotFound
}

// The model page reads the channel statistics the gateway counts in memory:
// attempts recorded after the page was first served show up once the engine
// republishes them (RunRefresh does it every few seconds), and serving the page
// never waits on a refresh.
func TestModelPageReadsOnlyVersionedModelStatistics(t *testing.T) {
	spec := loadOpenAPI(t)
	store := newFakeStore()
	policy, err := channel.NewOutboundPolicy(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	book := gateway.NewStatsBook()
	engine := gateway.NewEngine(gateway.Dependencies{Settings: settings.NewService(store), Outbound: policy, StatsBook: book})
	identityService, err := identitytest.NewService(store, time.Hour)
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

	successRate := func() any {
		channels := admin.expect(t, http.StatusOK, http.MethodGet, "/api/models/deepseek-chat", nil)["channels"].([]any)
		if len(channels) != 1 {
			t.Fatalf("channels = %v", channels)
		}
		return channels[0].(map[string]any)["success_rate_24h"]
	}
	now := time.Now()
	for range 3 {
		book.Record(browseTestChannelID, "deepseek-chat", false, gateway.Attempt{EndReason: gateway.EndCompleted}, now)
	}
	book.Record(browseTestChannelID, "deepseek-chat", false, gateway.Attempt{EndReason: gateway.EndUpstreamError}, now)
	if got := successRate(); got != nil {
		t.Fatalf("legacy history invented model statistics: %v", got)
	}
	engine.Store = browseMetricHistory{rows: []gateway.ModelAttemptHistoryRow{{ChannelID: browseTestChannelID, ModelID: "deepseek-chat", Format: channel.FormatOpenAIResponses, Bucket: now.Unix() / 300, Counted: 4, Successes: 3, LatencyBin: -1, SpeedBin: -1}}}
	if err := engine.LoadStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := successRate(); got != "0.750000" {
		t.Fatalf("success rate = %v, want 0.750000", got)
	}

	// Legacy channel-only observations cannot contaminate a model scope,
	// including after the next snapshot refresh.
	book.Record(browseTestChannelID, "deepseek-chat", false, gateway.Attempt{EndReason: gateway.EndUpstreamError}, now)
	if got := successRate(); got != "0.750000" {
		t.Fatalf("success rate = %v before the refresh, want the published 0.750000", got)
	}
	engine.RefreshStats()
	if got := successRate(); got != "0.750000" {
		t.Fatalf("success rate = %v after the refresh, want model-only 0.750000", got)
	}
}

type browseMetricHistory struct {
	gateway.Store
	rows []gateway.ModelAttemptHistoryRow
}

func (s browseMetricHistory) AttemptHistory(context.Context, time.Time) ([]gateway.AttemptHistoryRow, error) {
	return nil, nil
}
func (s browseMetricHistory) ModelAttemptHistory(context.Context, time.Time, int32) ([]gateway.ModelAttemptHistoryRow, error) {
	return s.rows, nil
}
