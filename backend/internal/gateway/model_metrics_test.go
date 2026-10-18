package gateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func TestModelMetricsSeparateModelsProtocolsAndActualTiers(t *testing.T) {
	book := newModelMetricsBook()
	now := time.Now()
	latency, tps := 100, 80.0
	base := modelMetricKey{channel: "channel", model: "text-one", format: channel.FormatOpenAIResponses}
	for _, key := range []modelMetricKey{base, {channel: "channel", model: "text-two", format: base.format}, {channel: "channel", model: base.model, format: channel.FormatAnthropic}, {channel: "channel", model: base.model, format: base.format, tier: "openai:priority"}} {
		if !book.record(key, modelMetricSample{end: EndCompleted, firstOutputMS: &latency, outputTPS: &tps}, now) {
			t.Fatal("valid observation dropped")
		}
		latency *= 2
		tps /= 2
	}
	book.record(base, modelMetricSample{end: EndClientError}, now)
	book.record(base, modelMetricSample{end: EndClientDisconnected}, now)
	view, dropped := book.view(now)
	if len(view) != 4 || dropped != 0 || view[base].attempts != 1 || view[base].excluded != 2 || view[base].clientErrors != 1 {
		t.Fatal("scope/excluded counters mixed", view[base])
	}
	first := view[base]
	if first.latencySamples != 1 || first.speedSamples != 1 || first.firstOutputP50MS == nil || *first.firstOutputP50MS < 100 || *first.firstOutputP50MS > 110 || first.outputTPSP50 == nil || *first.outputTPSP50 < 75 || *first.outputTPSP50 > 85 {
		t.Fatal("reliable histogram sample lost", first)
	}
}

func TestModelMetricsSaturateWithoutUnboundedAllocationAndExpire(t *testing.T) {
	book := newModelMetricsBook()
	now := time.Now()
	for i := range maxMetricSeries {
		if !book.record(modelMetricKey{channel: "channel", model: fmt.Sprintf("approved-%d", i), format: channel.FormatOpenAIResponses}, modelMetricSample{end: EndCompleted}, now) {
			t.Fatal("series budget smaller than declared")
		}
	}
	if book.record(modelMetricKey{channel: "another-channel", model: "another-approved-model"}, modelMetricSample{end: EndCompleted}, now) || len(book.series) != maxMetricSeries {
		t.Fatal("series saturation allocated more state")
	}
	_, dropped := book.view(now)
	if dropped != 1 {
		t.Fatal("saturation did not retain diagnostic count")
	}
	view, _ := book.view(now.Add(25 * time.Hour))
	if len(view) != 0 || len(book.series) != 0 || book.buckets != 0 {
		t.Fatal("expired model/protocol series leaked")
	}
	key := modelMetricKey{channel: "channel", model: "approved-model", format: channel.FormatOpenAIResponses}
	for i := range maxMetricBuckets {
		if !book.record(key, modelMetricSample{end: EndCompleted}, now.Add(time.Duration(i)*5*time.Minute)) {
			t.Fatal("bucket budget smaller than declared")
		}
	}
	if book.record(key, modelMetricSample{end: EndCompleted}, now.Add(time.Duration(maxMetricBuckets)*5*time.Minute)) || book.buckets != maxMetricBuckets {
		t.Fatal("bucket saturation allocated more state")
	}
}

func TestModelMetricsUnknownOutputDoesNotInventTPSOrLatency(t *testing.T) {
	book := newModelMetricsBook()
	now := time.Now()
	key := modelMetricKey{channel: "channel", model: "image", format: channel.FormatOpenAIImages}
	book.record(key, modelMetricSample{end: EndCompleted}, now)
	view, _ := book.view(now)
	stats := view[key]
	if stats.attempts != 1 || stats.successes != 1 || stats.outputTPSP50 != nil || stats.firstOutputP50MS != nil {
		t.Fatal("unknown image measurement invented a value")
	}
	zero := 0
	book.record(key, modelMetricSample{end: EndCompleted, firstOutputMS: &zero}, now)
	view, _ = book.view(now)
	if view[key].firstOutputP50MS == nil || *view[key].firstOutputP50MS != 0 {
		t.Fatal("observed zero latency became unknown")
	}
}

func BenchmarkModelMetricRecordExistingSeries(b *testing.B) {
	book := newModelMetricsBook()
	now := time.Now()
	key := modelMetricKey{channel: "channel", model: "model", format: channel.FormatOpenAIResponses}
	latency, tps := 100, 80.0
	sample := modelMetricSample{end: EndCompleted, firstOutputMS: &latency, outputTPS: &tps}
	book.record(key, sample, now)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		book.record(key, sample, now)
	}
}

func BenchmarkModelMetricSnapshotSaturated(b *testing.B) {
	now := time.Now()
	e := NewEngine(Dependencies{Now: func() time.Time { return now }})
	latency, tps := 100, 80.0
	for i := range maxMetricSeries {
		key := modelMetricKey{channel: fmt.Sprint(i), model: "approved-model", format: channel.FormatOpenAIResponses, stream: true}
		for _, at := range []time.Time{now.Add(-5 * time.Minute), now} {
			e.modelMetrics.record(key, modelMetricSample{end: EndCompleted, firstOutputMS: &latency, outputTPS: &tps}, at)
		}
	}
	e.RefreshStats()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		e.RefreshStats()
	}
	b.ReportMetric(maxMetricSeries, "series")
	b.ReportMetric(maxMetricBuckets, "buckets")
}
