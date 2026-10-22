package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
)

// The model page still reports the channel's published health state without
// inventing per-model routing metrics: the engine republishes channel
// statistics every few seconds, and serving the page never waits on a refresh.
func TestModelPageReadsPublishedChannelStatistics(t *testing.T) {
	p, engine := marketModelFixture(t, "deepseek-chat", []groupchannel.Record{{ID: browseTestChannelID, Name: "Synthetic channel"}})
	admin := p.admin(t)
	book := p.stats

	state := func() any {
		channels := admin.expect(t, http.StatusOK, http.MethodGet, "/api/market/models/deepseek-chat", nil)["routing_channels"].([]any)
		if len(channels) != 1 {
			t.Fatalf("channels=%v", channels)
		}
		channel := channels[0].(map[string]any)
		if _, present := channel["routing_metrics"]; present {
			t.Fatalf("routing metrics are no longer part of the contract: %v", channel)
		}
		return channel["state"]
	}
	now := time.Now()
	for range 3 {
		book.Record(browseTestChannelID, "deepseek-chat", false, gateway.Attempt{EndReason: gateway.EndCompleted}, now)
	}
	book.Record(browseTestChannelID, "deepseek-chat", false, gateway.Attempt{EndReason: gateway.EndUpstreamError}, now)
	if got := state(); got != "available" {
		t.Fatalf("state = %v, want available", got)
	}
	book.Record(browseTestChannelID, "deepseek-chat", false, gateway.Attempt{EndReason: gateway.EndUpstreamError}, now)
	engine.RefreshStats()
	if got := state(); got != "available" {
		t.Fatalf("state after refresh = %v", got)
	}
}

const browseTestChannelID = "00000000-0000-4000-8000-0000000000f1"
