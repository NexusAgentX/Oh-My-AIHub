// Package metrics exposes the Prometheus metrics of the platform on a
// separate internal listener (Feature G). The metrics carry no user, account
// or API key labels: only model, format, channel and outcome.
package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

// staleActive is how long a started call may stay in the active gauge before
// it is dropped as a missed finish event (the event bus drops under load).
const staleActive = 2 * time.Hour

// ReconciliationChecks are the label values of aihub_reconciliation_failed.
var ReconciliationChecks = []string{"zero_sum", "account_balances", "escrow", "billing_calls", "released_trades"}

// Source is what the periodic refresh reads.
type Source interface {
	Checks(ctx context.Context) (observe.Checks, observe.Balances, error)
	LedgerTotals(ctx context.Context) ([]observe.LedgerTotals, error)
	Channels(ctx context.Context) ([]observe.ChannelState, error)
}

// Metrics owns the registry. It implements observe.Observer.
type Metrics struct {
	registry *prometheus.Registry

	activeRequests *prometheus.GaugeVec
	requests       *prometheus.CounterVec
	attempts       *prometheus.CounterVec
	upstream       *prometheus.HistogramVec
	firstToken     *prometheus.HistogramVec
	interToken     *prometheus.HistogramVec
	tokens         *prometheus.CounterVec
	cost           *prometheus.CounterVec
	cooldowns      *prometheus.CounterVec
	settlement     prometheus.Counter

	mutex      sync.Mutex
	active     map[string]activeCall
	models     map[string]bool
	snapshot   snapshot
	inCooldown func(channelID string, now time.Time) bool
}

type activeCall struct {
	format string
	at     time.Time
}

type snapshot struct {
	ready    bool
	balances observe.Balances
	failed   map[string]bool
	totals   []observe.LedgerTotals
	channels []observe.ChannelState
}

// New creates the registry with every metric registered. inCooldown tells
// whether a channel is cooling down right now (may be nil).
func New(inCooldown func(channelID string, now time.Time) bool) *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(), active: map[string]activeCall{}, models: map[string]bool{}, inCooldown: inCooldown,
		activeRequests: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "aihub_active_requests", Help: "Gateway requests in flight, by API format."}, []string{"format"}),
		requests:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "aihub_requests_total", Help: "Finished gateway requests."}, []string{"format", "model", "outcome"}),
		attempts:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "aihub_upstream_attempts_total", Help: "Upstream attempts per channel."}, []string{"channel", "status_class", "result"}),
		upstream: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "aihub_upstream_latency_seconds", Help: "Total duration of an upstream attempt.", Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, []string{"format", "model"}),
		firstToken: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "aihub_first_token_latency_seconds", Help: "Time from request start to the first response byte.", Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
		}, []string{"format", "model"}),
		interToken: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "aihub_inter_token_latency_seconds", Help: "Median gap between streamed frames of a call (one observation per streamed call).", Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		}, []string{"format", "model"}),
		tokens:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "aihub_tokens_total", Help: "Tokens read from upstream usage, by kind."}, []string{"model", "kind"}),
		cost:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "aihub_cost_points_total", Help: "Points debited from callers (cost plus fee)."}, []string{"model"}),
		cooldowns:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "aihub_channel_cooldowns_total", Help: "Channel cooldowns started."}, []string{"channel", "reason"}),
		settlement: prometheus.NewCounter(prometheus.CounterOpts{Name: "aihub_settlement_failures_total", Help: "Calls whose ledger booking failed and needs repair."}),
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.activeRequests, m.requests, m.attempts, m.upstream, m.firstToken, m.interToken, m.tokens, m.cost, m.cooldowns, m.settlement,
		&snapshotCollector{m: m},
	)
	return m
}

// Handler serves /metrics.
func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return mux
}

// Registry exposes the registry for tests.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// SetModels replaces the set of catalog model IDs allowed as the model label;
// anything else is reported as "other" to bound the label cardinality.
func (m *Metrics) SetModels(ids []string) {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	m.mutex.Lock()
	m.models = set
	m.mutex.Unlock()
}

func (m *Metrics) modelLabel(call *observe.CallRecord, event gateway.Event) string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	candidates := []string{event.Model}
	if call != nil {
		if call.ModelID != nil {
			candidates = append([]string{*call.ModelID}, candidates...)
		}
		candidates = append(candidates, call.RequestedModel)
	}
	for _, name := range candidates {
		if m.models[name] {
			return name
		}
	}
	return "other"
}

// Observe implements observe.Observer.
func (m *Metrics) Observe(event gateway.Event, call *observe.CallRecord) {
	switch event.Kind {
	case gateway.EventCallStarted:
		m.mutex.Lock()
		if _, exists := m.active[event.CallID]; !exists && event.CallID != "" {
			m.active[event.CallID] = activeCall{format: event.Format, at: event.At}
			m.activeRequests.WithLabelValues(event.Format).Inc()
		}
		m.mutex.Unlock()
	case gateway.EventCallFinished:
		m.mutex.Lock()
		if started, exists := m.active[event.CallID]; exists {
			delete(m.active, event.CallID)
			m.activeRequests.WithLabelValues(started.format).Dec()
		}
		m.mutex.Unlock()
		m.finished(event, call)
	case gateway.EventChannelCooldown:
		m.cooldowns.WithLabelValues(event.ChannelID, event.Detail).Inc()
	case gateway.EventSettlementFailed:
		m.settlement.Inc()
	}
}

func (m *Metrics) finished(event gateway.Event, call *observe.CallRecord) {
	format, outcome := event.Format, event.Outcome
	if call != nil {
		format, outcome = call.Format, call.Outcome
	}
	model := m.modelLabel(call, event)
	m.requests.WithLabelValues(format, model, outcome).Inc()
	if call == nil {
		return
	}
	for _, attempt := range call.Attempts {
		if attempt.Channel == nil {
			continue
		}
		m.attempts.WithLabelValues(attempt.Channel.ID, statusClass(attempt.StatusCode), attemptResult(attempt.EndReason)).Inc()
		if attempt.DurationMS != nil {
			m.upstream.WithLabelValues(format, model).Observe(float64(*attempt.DurationMS) / 1000)
		}
	}
	if call.TTFTMS != nil {
		m.firstToken.WithLabelValues(format, model).Observe(float64(*call.TTFTMS) / 1000)
	}
	if call.IntervalP50MS != nil {
		m.interToken.WithLabelValues(format, model).Observe(float64(*call.IntervalP50MS) / 1000)
	}
	for kind, value := range map[string]int64{
		"input": call.Usage.InputTokens, "output": call.Usage.OutputTokens,
		"cache_write": call.Usage.CacheWriteTokens, "cache_read": call.Usage.CacheReadTokens,
	} {
		if value > 0 {
			m.tokens.WithLabelValues(model, kind).Add(float64(value))
		}
	}
	if charged := call.Charged(); charged > 0 {
		m.cost.WithLabelValues(model).Add(points(charged))
	}
}

func statusClass(status *int) string {
	if status == nil || *status < 100 {
		return "none"
	}
	return strconv.Itoa(*status/100) + "xx"
}

func attemptResult(endReason string) string {
	switch endReason {
	case "completed":
		return "success"
	case "timeout_ttft", "timeout_total":
		return "timeout"
	case "client_disconnected", "client_error":
		return "client"
	}
	return "error"
}

func points(amount money.Amount) float64 { return float64(amount.Nano()) / 1e9 }

// Refresh reads the ledger gauges and the channel list; call it once a minute.
func (m *Metrics) Refresh(ctx context.Context, source Source) error {
	checks, balances, err := source.Checks(ctx)
	if err != nil {
		return err
	}
	totals, err := source.LedgerTotals(ctx)
	if err != nil {
		return err
	}
	channels, err := source.Channels(ctx)
	if err != nil {
		return err
	}
	failed := map[string]bool{}
	for _, name := range checks.FailedChecks() {
		failed[name] = true
	}
	now := time.Now()
	m.mutex.Lock()
	m.snapshot = snapshot{ready: true, balances: balances, failed: failed, totals: totals, channels: channels}
	for id, started := range m.active {
		if now.Sub(started.at) > staleActive {
			delete(m.active, id)
			m.activeRequests.WithLabelValues(started.format).Dec()
		}
	}
	m.mutex.Unlock()
	return nil
}

// Run refreshes every interval until ctx ends.
func (m *Metrics) Run(ctx context.Context, source Source, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		refreshContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := m.Refresh(refreshContext, source); err != nil && ctx.Err() == nil {
			logger.Error("metrics refresh failed", "error", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// snapshotCollector exposes the values computed by Refresh plus the live
// channel state, as constant metrics at scrape time.
type snapshotCollector struct{ m *Metrics }

var (
	descCirculating = prometheus.NewDesc("aihub_points_circulating", "Points held in positive user balances.", nil, nil)
	descCredit      = prometheus.NewDesc("aihub_points_credit_issued", "Credit in use: absolute sum of negative user balances.", nil, nil)
	descEscrow      = prometheus.NewDesc("aihub_points_escrow", "Points in the C2C escrow account.", nil, nil)
	descRevenue     = prometheus.NewDesc("aihub_points_platform_revenue", "Points in the platform revenue account.", nil, nil)
	descBadDebt     = prometheus.NewDesc("aihub_points_bad_debt", "Points in the bad debt account (negative: written off).", nil, nil)
	descImbalance   = prometheus.NewDesc("aihub_ledger_imbalance", "Sum of all ledger balances; always 0 when the books balance.", nil, nil)
	descLedgerTx    = prometheus.NewDesc("aihub_ledger_transactions_total", "Ledger transactions booked, by type.", []string{"type"}, nil)
	descLedgerSum   = prometheus.NewDesc("aihub_ledger_amount_total", "Points moved (sum of positive entries), by transaction type.", []string{"type"}, nil)
	descReconFailed = prometheus.NewDesc("aihub_reconciliation_failed", "1 when a live reconciliation check fails.", []string{"check"}, nil)
	descChannelUp   = prometheus.NewDesc("aihub_channel_up", "1 when the channel is listed and not cooling down.", []string{"channel"}, nil)
)

func (c *snapshotCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{descCirculating, descCredit, descEscrow, descRevenue, descBadDebt, descImbalance, descLedgerTx, descLedgerSum, descReconFailed, descChannelUp} {
		ch <- desc
	}
}

func (c *snapshotCollector) Collect(ch chan<- prometheus.Metric) {
	c.m.mutex.Lock()
	snap := c.m.snapshot
	inCooldown := c.m.inCooldown
	c.m.mutex.Unlock()
	if !snap.ready {
		return
	}
	gauge := func(desc *prometheus.Desc, value float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...)
	}
	balances := snap.balances
	gauge(descCirculating, points(balances.UserPositive))
	gauge(descCredit, points(balances.CreditIssued()))
	gauge(descEscrow, points(balances.Escrow))
	gauge(descRevenue, points(balances.PlatformRevenue))
	gauge(descBadDebt, points(balances.BadDebt))
	gauge(descImbalance, points(balances.Total))
	for _, total := range snap.totals {
		ch <- prometheus.MustNewConstMetric(descLedgerTx, prometheus.CounterValue, float64(total.Transactions), total.Type)
		ch <- prometheus.MustNewConstMetric(descLedgerSum, prometheus.CounterValue, points(total.Amount), total.Type)
	}
	for _, name := range ReconciliationChecks {
		value := 0.0
		if snap.failed[name] {
			value = 1
		}
		gauge(descReconFailed, value, name)
	}
	now := time.Now()
	for _, channel := range snap.channels {
		value := 0.0
		if channel.Listed && (inCooldown == nil || !inCooldown(channel.ID, now)) {
			value = 1
		}
		gauge(descChannelUp, value, channel.ID)
	}
}
