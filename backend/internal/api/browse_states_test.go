package api

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

type channelStatsFixture struct {
	gateway.Store
	stats map[string]gateway.ChannelStats
}

func (s channelStatsFixture) ChannelStats(context.Context) (map[string]gateway.ChannelStats, error) {
	return s.stats, nil
}

type onlineChannelsFixture struct {
	gateway.Browse
	online  []gateway.OnlineChannel
	revenue money.Amount
}

func (b onlineChannelsFixture) OnlineChannels(context.Context) ([]gateway.OnlineChannel, error) {
	return b.online, nil
}

func (b onlineChannelsFixture) ChannelRevenue(context.Context, string, time.Time) (money.Amount, error) {
	return b.revenue, nil
}

// 模型页的渠道行把成功率、首字延迟、冷却剩余和每日上限剩余写成 null 或字符串；
// 这些分支由处理器内联拼装，门禁只校验形状，这里钉住各状态下的取值。
func TestModelPageReportsEachChannelsState(t *testing.T) {
	const idPrefix = "00000000-0000-4000-8000-00000000"
	store := newFakeStore()
	policy, err := channel.NewOutboundPolicy(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	median := 420.7
	engine := gateway.NewEngine(gateway.Dependencies{
		Store: channelStatsFixture{stats: map[string]gateway.ChannelStats{
			idPrefix + "a002": {Attempts: 4, Successes: 3},
			idPrefix + "a003": {Attempts: 10, Successes: 9, TTFTP50MS: &median},
		}},
		Settings: settings.NewService(store), Outbound: policy,
	})
	identityService, err := identity.NewService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	one := int32(1)
	capped := money.FromNano(10 * money.Scale)
	line := func(suffix, name string, advanced channel.Advanced) gateway.OnlineChannel {
		return gateway.OnlineChannel{
			ModelID: "m1", ChannelID: idPrefix + suffix, ChannelName: name, OwnerID: idPrefix + "00ff", OwnerName: "车主",
			Formats: []channel.Format{channel.FormatAnthropic, channel.FormatOpenAIChat}, MultiplierNano: 1_250_000_000, Advanced: advanced,
		}
	}
	browse := onlineChannelsFixture{revenue: money.FromNano(7_500_000_000), online: []gateway.OnlineChannel{
		line("a001", "无统计", channel.Advanced{}),
		line("a002", "无首字延迟", channel.Advanced{}),
		line("a003", "有首字延迟", channel.Advanced{}),
		line("a004", "冷却中", channel.Advanced{CooldownFailures: &one}),
		line("a005", "并发已满", channel.Advanced{ConcurrencyLimit: &one}),
		line("a006", "有上限", channel.Advanced{DailyRevenueCap: &capped}),
	}}
	handler := NewHandler(Dependencies{
		Identity: identityService, Catalog: catalog.NewService(store), Ledger: ledger.NewService(store),
		Settings: settings.NewService(store), Audit: audit.NewService(store), CookieSecure: true,
		Gateway: engine, Browse: browse, Routing: noRoutingPreferences{},
	})
	admin := bootstrap(t, loadOpenAPI(t), handler)
	admin.expect(t, http.StatusCreated, http.MethodPost, "/api/admin/models", map[string]any{
		"id": "m1", "display_name": "模型一",
		"base_prices": map[string]string{"input": "1.5", "output": "4.5", "cache_write": "0", "cache_read": "0.15"},
	})

	// A cooldown and a held concurrency slot give the "cooldown" and "limited" states.
	engine.State().Failure(idPrefix+"a004", gateway.Limits{CooldownAfter: 1, CooldownFor: time.Hour}, time.Now(), "test")
	release, ok, _ := engine.State().Begin(idPrefix+"a005", gateway.Limits{Concurrency: 1}, time.Now())
	if !ok {
		t.Fatal("could not hold a concurrency slot")
	}
	defer release()

	page := admin.expect(t, http.StatusOK, http.MethodGet, "/api/models/m1", nil)
	type row struct {
		state, successRate string
		ttft, cooldown     any
		capRemaining       any
	}
	want := map[string]row{
		"无统计":   {state: "available"},
		"无首字延迟": {state: "available", successRate: "0.750000"},
		"有首字延迟": {state: "available", successRate: "0.900000", ttft: float64(420)},
		"冷却中":   {state: "cooldown"},
		"并发已满":  {state: "limited"},
		"有上限":   {state: "available", capRemaining: "0.250000"},
	}
	got := map[string]row{}
	for _, entry := range page["channels"].([]any) {
		channelRow := entry.(map[string]any)
		successRate, _ := channelRow["success_rate_24h"].(string)
		got[channelRow["name"].(string)] = row{
			state: channelRow["state"].(string), successRate: successRate, ttft: channelRow["ttft_p50_ms"],
			cooldown: channelRow["cooldown_remaining_seconds"], capRemaining: channelRow["daily_cap_remaining"],
		}
	}
	// The cooldown row also reports the seconds left, which depends on the clock.
	if seconds, _ := got["冷却中"].cooldown.(float64); seconds < 3590 || seconds > 3601 {
		t.Fatalf("cooldown remaining = %v", got["冷却中"].cooldown)
	}
	for name, entry := range got {
		entry.cooldown = nil
		got[name] = entry
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("channel rows = %+v, want %+v", got, want)
	}
}
