package api

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"net/http"
	"testing"
	"time"
)

func TestHomePreservesHistoricalRecentCalls(t *testing.T) {
	t.Parallel()
	p := newPlatform(t)
	alice, _ := p.member(t, "alice")
	channelID := "30000000-0000-4000-8000-0000000000c1"
	keyName, keyID := "默认 Key", "30000000-0000-4000-8000-0000000000b1"
	finished, duration := time.Date(2026, 10, 8, 9, 0, 1, 0, time.UTC), int32(1200)
	p.browse.home = gateway.HomeStats{TodaySpend: money.FromNano(3_000_000_000), TodayCalls: 4, TodaySucceeded: 3, RevenueToday: money.FromNano(1_000_000_000), ChannelsOnline: 1, ChannelsTotal: 2, PendingTrades: 1}
	p.browse.recent = []gateway.CallSummary{{
		ID: "30000000-0000-4000-8000-000000000001", CreatedAt: finished.Add(-time.Second), CompletedAt: &finished, RequestedModel: "deepseek-chat",
		Format: channel.FormatOpenAIChat, Outcome: "succeeded", KeyID: &keyID, KeyName: &keyName, ChannelID: &channelID, ChannelName: &keyName, Cost: money.FromNano(1_000_000_000),
		Fee: money.FromNano(10_000_000), DurationMS: &duration, AttemptCount: 1, Booked: true,
	}}
	home := alice.expect(t, http.StatusOK, http.MethodGet, "/api/home", nil)
	if home["default_key"] == nil || len(home["recent_calls"].([]any)) != 1 || home["pending_c2c_trades"] != float64(1) {
		t.Fatalf("home = %v", home)
	}

}
