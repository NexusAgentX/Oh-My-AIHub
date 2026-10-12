package metrics

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"strings"
	"testing"
	"time"
)

func TestCompletionMetricsDoNotDependOnFeedOrPersistence(t *testing.T) {
	m := New(nil)
	m.SetFeedHealth(func() uint64 { return 7 }, func() uint64 { return 3 }, func() uint64 { return 2 })
	ms := 120
	status := 200
	slow := 2.5
	f := gateway.CallFinish{ModelID: "untrusted-label", ChannelID: "c1", Outcome: gateway.OutcomeClientDisconnect, Cost: money.FromNano(1000000000), TTFTMS: &ms, TokensPerSecond: &slow, Attempts: []gateway.Attempt{{Channel: &gateway.AttemptChannel{ID: "c1"}, StatusCode: &status, EndReason: gateway.EndClientDisconnected, DurationMS: &ms}}, Diagnostics: &gateway.Diagnostics{UsageStatus: "reported", Delivery: "client_disconnected", MaxFrameGapMS: &ms}}
	m.Complete(gateway.Completion{Format: "openai_chat", Status: 200, Stream: true, Duration: time.Second, Finish: f, Booked: false})
	m.Complete(gateway.Completion{Format: "openai_chat", Status: 401, Duration: time.Millisecond, Finish: gateway.CallFinish{Outcome: gateway.OutcomeRejectedKey}})
	body := scrape(t, m)
	for _, want := range []string{`aihub_http_requests_total{format="openai_chat",status="401"} 1`, `aihub_accounting_total{format="openai_chat",result="not_booked"} 2`, `aihub_delivery_total{format="openai_chat",result="client_disconnected"} 1`, `aihub_gateway_events_dropped_total 7`, `aihub_live_read_failures_total 2`, `aihub_first_token_latency_seconds_count{channel="c1",format="openai_chat",model="other",stream="true"} 1`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, `model="untrusted-label"`) || strings.Contains(body, `aihub_cost_points_total{model="other"} 1`) {
		t.Fatal("unbounded label or unbooked cost counted")
	}
}
