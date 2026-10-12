package metrics

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"github.com/prometheus/client_golang/prometheus"
	"strconv"
	"time"
)

type detailMetrics struct {
	tokens, fees, booked                            *prometheus.CounterVec
	bytes, cost, fallback                           *prometheus.HistogramVec
	httpRequests                                    *prometheus.CounterVec
	duration, retries, speed, gap, overhead, stages *prometheus.HistogramVec
	failures, delivery, usage, firstAttempt         *prometheus.CounterVec
	lastRefresh                                     prometheus.Gauge
	refreshFailures                                 prometheus.Counter
}

func newDetailMetrics(r *prometheus.Registry) *detailMetrics {
	labels := []string{"format", "model", "channel", "stream"}
	counter := func(name, help string, labels []string) *prometheus.CounterVec {
		v := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
		r.MustRegister(v)
		return v
	}
	hist := func(name, help string, buckets []float64, labels []string) *prometheus.HistogramVec {
		v := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets}, labels)
		r.MustRegister(v)
		return v
	}
	d := &detailMetrics{
		tokens:          counter("aihub_channel_tokens_total", "Observed tokens attributed to the final channel.", append(append([]string{}, labels...), "kind")),
		fees:            counter("aihub_channel_fee_points_total", "Successfully booked fees by final channel.", labels),
		booked:          counter("aihub_accounting_total", "Accounting result after completion; not delivery success.", []string{"format", "result"}),
		bytes:           hist("aihub_response_bytes", "Upstream bytes and bytes accepted by the client writer.", prometheus.ExponentialBuckets(256, 4, 10), append(append([]string{}, labels...), "direction")),
		cost:            hist("aihub_call_cost_points", "Actual charged points per finished request, zero when not booked.", prometheus.ExponentialBuckets(.000001, 10, 10), labels),
		fallback:        hist("aihub_fallback_duration_seconds", "Time spent in attempts before the final attempt, for retried calls.", prometheus.ExponentialBuckets(.001, 4, 10), labels),
		httpRequests:    counter("aihub_http_requests_total", "Inference HTTP requests, including authentication and persistence failures.", []string{"format", "status"}),
		duration:        hist("aihub_request_duration_seconds", "End-to-end handler duration, including settlement and retries.", prometheus.ExponentialBuckets(.01, 3, 12), labels),
		retries:         hist("aihub_request_retries", "Upstream attempts after the first, one observation per request.", []float64{0, 1, 2, 3, 5, 10}, labels),
		speed:           hist("aihub_output_tokens_per_second", "Streaming average generation speed; inspect lower quantiles for slow output.", []float64{1, 5, 10, 20, 40, 80, 160, 320}, labels),
		gap:             hist("aihub_max_frame_gap_seconds", "Largest observed data-frame gap of each stream; not token latency.", prometheus.ExponentialBuckets(.01, 3, 9), labels),
		overhead:        hist("aihub_gateway_overhead_seconds", "Handler duration minus cumulative upstream attempts; includes settlement.", prometheus.ExponentialBuckets(.0001, 4, 10), labels),
		stages:          hist("aihub_stage_duration_seconds", "Measured gateway phase duration. Client writes overlap upstream exchange; do not add all phases.", prometheus.ExponentialBuckets(.0001, 4, 10), []string{"format", "stage"}),
		failures:        counter("aihub_attempt_errors_total", "Attempt failures by channel, model, HTTP status and normalized reason.", []string{"format", "model", "channel", "status", "reason"}),
		delivery:        counter("aihub_delivery_total", "Observed client delivery result; independent from billing.", []string{"format", "result"}),
		usage:           counter("aihub_usage_observation_total", "Upstream usage observation quality.", []string{"format", "result"}),
		firstAttempt:    counter("aihub_first_attempt_total", "Whether the first upstream attempt completed.", []string{"model", "channel", "result"}),
		lastRefresh:     prometheus.NewGauge(prometheus.GaugeOpts{Name: "aihub_metrics_last_refresh_timestamp_seconds", Help: "Unix timestamp of the last successful periodic refresh; zero until first success."}),
		refreshFailures: prometheus.NewCounter(prometheus.CounterOpts{Name: "aihub_metrics_refresh_failures_total", Help: "Failed periodic metrics refreshes; previous values remain until next success."}),
	}
	r.MustRegister(d.lastRefresh, d.refreshFailures)
	return d
}

func (m *Metrics) Active(format string, delta float64) {
	m.activeRequests.WithLabelValues(format).Add(delta)
}
func (m *Metrics) Complete(c gateway.Completion) {
	f := c.Finish
	event := gateway.Event{Format: c.Format, Model: f.ModelID, Outcome: f.Outcome}
	call := &observe.CallRecord{CallRow: observe.CallRow{Format: c.Format, ModelID: &f.ModelID, Stream: c.Stream, Outcome: f.Outcome, Usage: observe.Usage{InputTokens: f.Usage.InputTokens, OutputTokens: f.Usage.OutputTokens, CacheReadTokens: f.Usage.CacheReadTokens, CacheWriteTokens: f.Usage.CacheWriteTokens}, Cost: f.Cost, Fee: f.Fee, TTFTMS: f.TTFTMS}, IntervalP50MS: f.IntervalP50MS}
	if c.Booked {
		id := "booked"
		call.LedgerTxID = &id
	}
	channel := "none"
	if f.ChannelID != "" {
		channel = f.ChannelID
		call.Channel = &observe.ChannelRef{ID: channel}
	}
	var upstream time.Duration
	model := m.modelLabel(call, event)
	for i, a := range f.Attempts {
		if a.Channel == nil {
			continue
		}
		call.Attempts = append(call.Attempts, observe.Attempt{Channel: &observe.ChannelRef{ID: a.Channel.ID}, StatusCode: a.StatusCode, EndReason: a.EndReason, DurationMS: a.DurationMS})
		if a.DurationMS != nil {
			upstream += time.Duration(*a.DurationMS) * time.Millisecond
		}
		result := "error"
		if a.EndReason == gateway.EndCompleted {
			result = "success"
		}
		if i == 0 {
			m.detail.firstAttempt.WithLabelValues(model, a.Channel.ID, result).Inc()
		}
		if result == "error" {
			status := "none"
			if a.StatusCode != nil {
				status = strconv.Itoa(*a.StatusCode)
			}
			m.detail.failures.WithLabelValues(c.Format, model, a.Channel.ID, status, a.EndReason).Inc()
		}
	}
	m.finished(event, call)
	labels := []string{c.Format, model, channel, strconv.FormatBool(c.Stream)}
	for kind, value := range map[string]int64{"input": f.Usage.InputTokens, "output": f.Usage.OutputTokens, "cache_read": f.Usage.CacheReadTokens, "cache_write": f.Usage.CacheWriteTokens} {
		if value > 0 {
			m.detail.tokens.WithLabelValues(append(append([]string{}, labels...), kind)...).Add(float64(value))
		}
	}
	charge := float64(0)
	accounting := "not_booked"
	if c.Booked {
		charge = points(f.Cost + f.Fee)
		m.detail.fees.WithLabelValues(labels...).Add(points(f.Fee))
		accounting = "booked"
	}
	m.detail.cost.WithLabelValues(labels...).Observe(charge)
	m.detail.booked.WithLabelValues(c.Format, accounting).Inc()
	m.detail.bytes.WithLabelValues(append(append([]string{}, labels...), "client")...).Observe(float64(f.ResponseBytes))
	if len(f.Attempts) > 1 {
		var before float64
		for _, a := range f.Attempts[:len(f.Attempts)-1] {
			if a.DurationMS != nil {
				before += float64(*a.DurationMS) / 1000
			}
		}
		m.detail.fallback.WithLabelValues(labels...).Observe(before)
	}
	m.detail.httpRequests.WithLabelValues(c.Format, strconv.Itoa(c.Status)).Inc()
	m.detail.duration.WithLabelValues(labels...).Observe(c.Duration.Seconds())
	m.detail.retries.WithLabelValues(labels...).Observe(float64(max(0, len(f.Attempts)-1)))
	m.detail.overhead.WithLabelValues(labels...).Observe(max(0, (c.Duration - upstream).Seconds()))
	if c.Stream && f.TokensPerSecond != nil {
		m.detail.speed.WithLabelValues(labels...).Observe(*f.TokensPerSecond)
	}
	if d := f.Diagnostics; d != nil {
		m.detail.bytes.WithLabelValues(append(append([]string{}, labels...), "upstream")...).Observe(float64(d.UpstreamBytes))
		m.detail.delivery.WithLabelValues(c.Format, d.Delivery).Inc()
		m.detail.usage.WithLabelValues(c.Format, d.UsageStatus).Inc()
		if c.Stream && d.MaxFrameGapMS != nil {
			m.detail.gap.WithLabelValues(labels...).Observe(float64(*d.MaxFrameGapMS) / 1000)
		}
		for _, s := range d.Stages {
			m.detail.stages.WithLabelValues(c.Format, s.Stage).Observe(s.DurationMS / 1000)
		}
	}
}

// Publish consumes only operational events synchronously, never call details.
func (m *Metrics) Publish(e gateway.Event) {
	switch e.Kind {
	case gateway.EventChannelCooldown:
		m.cooldowns.WithLabelValues(e.ChannelID, e.Detail).Inc()
	case gateway.EventSettlementFailed:
		m.settlement.Inc()
	}
}
func (m *Metrics) SetFeedHealth(busDropped, feedDropped, readFailures func() uint64) {
	for _, v := range []struct {
		name, help string
		read       func() uint64
	}{
		{"aihub_gateway_events_dropped_total", "Live event deliveries discarded because a subscriber buffer was full.", busDropped},
		{"aihub_live_messages_dropped_total", "Live UI messages discarded because a subscriber buffer was full.", feedDropped},
		{"aihub_live_read_failures_total", "Failed call-detail reads for the live UI stream.", readFailures},
	} {
		m.registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: v.name, Help: v.help}, func() float64 { return float64(v.read()) }))
	}
}
