package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

func TestReliableOutputTPSRequiresMeasuredVisibleIntervalAndDisjointCounts(t *testing.T) {
	base := time.Unix(1000, 0)
	for _, tc := range []struct {
		name   string
		mutate func(*callState, *Observation)
		want   bool
	}{
		{"text", func(_ *callState, _ *Observation) {}, true},
		{"reasoning disjoint", func(_ *callState, o *Observation) { n := int64(20); o.ReasoningOutput = true; o.ReasoningTokens = &n }, true},
		{"opaque without count", func(_ *callState, o *Observation) { o.OpaqueOutput = true }, false},
		{"thinking without count", func(_ *callState, o *Observation) { o.ReasoningOutput = true }, false},
		{"invalid thinking count", func(_ *callState, o *Observation) { n := int64(101); o.ReasoningTokens = &n }, false},
		{"short", func(_ *callState, o *Observation) { o.Usage.OutputTokens = 31 }, false},
		{"tiny interval", func(_ *callState, o *Observation) { o.OutputEndAt = o.FirstVisibleOutputAt.Add(49 * time.Millisecond) }, false},
		{"no terminal", func(_ *callState, o *Observation) { o.OutputEndAt = time.Time{} }, false},
		{"multi", func(_ *callState, o *Observation) { o.MultipleOutputs = true }, false},
		{"media", func(_ *callState, o *Observation) { o.MediaOutput = true }, false},
		{"nonstream", func(c *callState, _ *Observation) { c.stream = false }, false},
		{"polling", func(c *callState, _ *Observation) { c.polling = true }, false},
		{"iterations", func(_ *callState, o *Observation) {
			o.Usage.Detail = &ledger.UsageDetail{Iterations: []ledger.UsageIteration{{Type: "message"}}}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := &callState{stream: true, format: channel.FormatOpenAIResponses}
			o := Observation{Found: true, FirstVisibleOutputAt: base, OutputEndAt: base.Add(time.Second), Usage: ledger.Usage{OutputTokens: 100}}
			tc.mutate(call, &o)
			ms, tps := observedOutputTPS(call, o)
			if (tps != nil) != tc.want {
				t.Fatalf("TPS=%v, want measured=%v", tps, tc.want)
			}
			if tc.want && (ms == nil || *ms != 1000 || *tps <= 0 || *tps > 100) {
				t.Fatalf("invalid interval/count: %v %v", ms, tps)
			}
		})
	}
}

func TestMetricUsageCountsDoNotTrustMalformedOrInclusiveGeminiTotals(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":80,"thoughtsTokenCount":20,"totalTokenCount":110}}`, true},
		{`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":100,"thoughtsTokenCount":20,"totalTokenCount":110}}`, false},
		{`{"usageMetadata":{"candidatesTokenCount":80}}`, false},
		{`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":80,"totalTokenCount":90}}`, true},
		{`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":80,"totalTokenCount":90}`, false},
	} {
		o := NewObserver(channel.FormatGemini, true, "text/event-stream")
		o.observeMetricCounts([]byte(tc.raw))
		if o.metricVisibleKnown != tc.want {
			t.Fatalf("trusted=%v for %s", o.metricVisibleKnown, tc.raw)
		}
	}
}

func TestAttemptMetricsIncludeAddressFallbackButNotEarlierChannelsOrDowngradedTier(t *testing.T) {
	base := time.Unix(1000, 0)
	firstByte, selection := 100, 800
	call := &callState{stream: true, format: channel.FormatOpenAIResponses, requestedServiceTier: "priority"}
	o := Observation{FirstOutputAt: base.Add(200 * time.Millisecond), FirstVisibleOutputAt: base.Add(200 * time.Millisecond), OutputEndAt: base.Add(1200 * time.Millisecond), Found: true, Usage: ledger.Usage{OutputTokens: 100, Detail: &ledger.UsageDetail{ServiceTier: "openai:priority"}}}
	a := Attempt{EndReason: EndCompleted, TTFTMS: &firstByte, SelectionLatencyMS: &selection}
	populateAttemptMetrics(call, &a, o, base)
	if a.MetricsVersion != 2 || a.FirstOutputMS == nil || *a.FirstOutputMS != 200 || a.SelectionOutputMS == nil || *a.SelectionOutputMS != 900 || a.OutputTPS == nil || *a.OutputTPS != 100 {
		t.Fatalf("incorrect attempt metrics %+v", a)
	}
	o.Usage.Detail.ServiceTier = "openai:default"
	populateAttemptMetrics(call, &a, o, base)
	if a.SelectionOutputMS != nil || a.OutputTPS != nil || a.FirstOutputMS == nil || a.ActualServiceTier != "openai:default" {
		t.Fatalf("downgrade claimed priority timing: %+v", a)
	}
	for _, value := range []string{strings.Repeat("x", 10000), "openai:made-up", "anthropic:priority"} {
		if requestedMetricTier(call.format, value) != "unknown" {
			t.Fatal("unbounded tier label admitted")
		}
	}
}

func BenchmarkFinishedOutputTPS(b *testing.B) {
	base := time.Unix(1000, 0)
	call := &callState{stream: true, format: channel.FormatOpenAIResponses}
	o := Observation{Found: true, FirstVisibleOutputAt: base, OutputEndAt: base.Add(time.Second), Usage: ledger.Usage{OutputTokens: 100}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		ms, tps := observedOutputTPS(call, o)
		if ms == nil || tps == nil {
			b.Fatal("missing measured interval")
		}
	}
}
