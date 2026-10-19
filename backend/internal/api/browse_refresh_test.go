package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
)

const browseTestChannelID = "00000000-0000-4000-8000-0000000000f1"

// The model page reads the channel statistics the gateway counts in memory:
// attempts recorded after the page was first served show up once the engine
// republishes them (RunRefresh does it every few seconds), and serving the page
// never waits on a refresh.
func TestModelPageReadsOnlyVersionedModelStatistics(t *testing.T) {
	p, engine := marketModelFixture(t, "deepseek-chat", []groupchannel.Record{{ID: browseTestChannelID, Name: "Synthetic channel"}})
	admin := p.admin(t)
	book := p.stats

	successRate := func() any {
		channels := admin.expect(t, http.StatusOK, http.MethodGet, "/api/market/models/deepseek-chat", nil)["routing_channels"].([]any)
		if len(channels) != 1 {
			t.Fatalf("channels=%v", channels)
		}
		metrics := channels[0].(map[string]any)["routing_metrics"].([]any)
		if len(metrics) == 0 {
			return nil
		}
		if len(metrics) != 1 {
			t.Fatalf("metric scopes=%v", metrics)
		}
		metric := metrics[0].(map[string]any)
		if metric["protocol"] != "openai-responses" || metric["format"] != "openai_responses" || metric["stream"] != false {
			t.Fatal("scope lost", metric)
		}
		return metric["success_rate"]
	}
	now := time.Now()
	for range 3 {
		book.Record(browseTestChannelID, "deepseek-chat", false, gateway.Attempt{EndReason: gateway.EndCompleted}, now)
	}
	book.Record(browseTestChannelID, "deepseek-chat", false, gateway.Attempt{EndReason: gateway.EndUpstreamError}, now)
	if got := successRate(); got != nil {
		t.Fatalf("legacy history invented model statistics: %v", got)
	}
	engine.Store = browseMetricHistory{rows: []gateway.ModelAttemptHistoryRow{{ChannelID: browseTestChannelID, ModelID: "deepseek-chat", Protocol: "openai-responses", Format: channel.FormatOpenAIResponses, Bucket: now.Unix() / 300, Counted: 4, Successes: 3, LatencyBin: -1, SpeedBin: -1}}}
	if err := engine.LoadStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := successRate(); got != float64(0.75) {
		t.Fatalf("success rate = %v, want 0.75", got)
	}

	// Legacy channel-only observations cannot contaminate a model scope,
	// including after the next snapshot refresh.
	book.Record(browseTestChannelID, "deepseek-chat", false, gateway.Attempt{EndReason: gateway.EndUpstreamError}, now)
	if got := successRate(); got != float64(0.75) {
		t.Fatalf("success rate = %v before the refresh, want the published 0.75", got)
	}
	engine.RefreshStats()
	if got := successRate(); got != float64(0.75) {
		t.Fatalf("success rate = %v after the refresh, want model-only 0.75", got)
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
