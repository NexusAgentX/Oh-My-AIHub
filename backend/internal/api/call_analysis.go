package api

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"net/http"
	"strconv"
	"time"
)

type analysisValuesJSON struct {
	FallbackP95       *float64 `json:"fallback_p95"`
	FallbackP50       *float64 `json:"fallback_p50"`
	StreamInterrupted int64    `json:"stream_interrupted"`
	StreamFinished    int64    `json:"stream_finished"`
	Attempted         int64    `json:"attempted"`
	Calls             int64    `json:"calls"`
	Finished          int64    `json:"finished"`
	Succeeded         int64    `json:"succeeded"`
	FirstSucceeded    int64    `json:"first_succeeded"`
	Retried           int64    `json:"retried"`
	Recovered         int64    `json:"recovered"`
	Attempts          int64    `json:"attempts"`
	AttemptSucceeded  int64    `json:"attempt_succeeded"`
	Delivered         int64    `json:"delivered"`
	DeliveryKnown     int64    `json:"delivery_known"`
	Billed            int64    `json:"billed"`
	UsageReported     int64    `json:"usage_reported"`
	UsageMissing      int64    `json:"usage_missing"`
	Streamed          int64    `json:"streamed"`
	InputTokens       int64    `json:"input_tokens"`
	OutputTokens      int64    `json:"output_tokens"`
	CacheReadTokens   int64    `json:"cache_read_tokens"`
	CacheWriteTokens  int64    `json:"cache_write_tokens"`
	Charged           string   `json:"charged"`
	Fee               string   `json:"fee"`
	Interrupted       int64    `json:"interrupted"`
	Disconnected      int64    `json:"disconnected"`
	Unbilled          int64    `json:"unbilled"`
	TtftP50           *float64 `json:"ttft_p50"`
	TtftP95           *float64 `json:"ttft_p95"`
	TtftP99           *float64 `json:"ttft_p99"`
	DurationP50       *float64 `json:"duration_p50"`
	DurationP95       *float64 `json:"duration_p95"`
	DurationP99       *float64 `json:"duration_p99"`
	SpeedP10          *float64 `json:"speed_p10"`
	SpeedP50          *float64 `json:"speed_p50"`
	SpeedP95          *float64 `json:"speed_p95"`
	GapP50            *float64 `json:"gap_p50"`
	GapP95            *float64 `json:"gap_p95"`
	GapP99            *float64 `json:"gap_p99"`
	MaxGapP95         *float64 `json:"max_gap_p95"`
	CostP50           *float64 `json:"cost_p50"`
	CostP95           *float64 `json:"cost_p95"`
	CostP99           *float64 `json:"cost_p99"`
	RetryP50          *float64 `json:"retry_p50"`
	RetryP95          *float64 `json:"retry_p95"`
	OverheadP50       *float64 `json:"overhead_p50"`
	OverheadP95       *float64 `json:"overhead_p95"`
	AuthenticationP95 *float64 `json:"authentication_p95"`
	RequestP95        *float64 `json:"request_p95"`
	OpenCallP95       *float64 `json:"open_call_p95"`
	AdmissionP95      *float64 `json:"admission_p95"`
	RoutingP95        *float64 `json:"routing_p95"`
	PricingP95        *float64 `json:"pricing_p95"`
	SettlementP95     *float64 `json:"settlement_p95"`
	ClientWriteP95    *float64 `json:"client_write_p95"`
}
type analysisRowJSON struct {
	Key        string             `json:"key"`
	Label      string             `json:"label"`
	SplitKey   string             `json:"split_key"`
	SplitLabel string             `json:"split_label"`
	Values     analysisValuesJSON `json:"values"`
}
type analysisReportJSON struct {
	From          time.Time          `json:"from"`
	To            time.Time          `json:"to"`
	PreviousFrom  time.Time          `json:"previous_from"`
	PreviousTo    time.Time          `json:"previous_to"`
	GroupBy       string             `json:"group_by"`
	SplitBy       string             `json:"split_by"`
	BucketSeconds int                `json:"bucket_seconds"`
	Current       analysisValuesJSON `json:"current"`
	Previous      analysisValuesJSON `json:"previous"`
	Groups        []analysisRowJSON  `json:"groups"`
	Trend         []analysisRowJSON  `json:"trend"`
}

func analysisValues(v observe.AnalysisValues) analysisValuesJSON {
	return analysisValuesJSON{FallbackP95: v.FallbackP95, FallbackP50: v.FallbackP50, StreamInterrupted: v.StreamInterrupted, StreamFinished: v.StreamFinished, Attempted: v.Attempted, Calls: v.Calls, Finished: v.Finished, Succeeded: v.Succeeded, FirstSucceeded: v.FirstSucceeded, Retried: v.Retried, Recovered: v.Recovered, Attempts: v.Attempts, AttemptSucceeded: v.AttemptSucceeded, Delivered: v.Delivered, DeliveryKnown: v.DeliveryKnown, Billed: v.Billed, UsageReported: v.UsageReported, UsageMissing: v.UsageMissing, Streamed: v.Streamed, InputTokens: v.InputTokens, OutputTokens: v.OutputTokens, CacheReadTokens: v.CacheReadTokens, CacheWriteTokens: v.CacheWriteTokens, Charged: money.FromNano(v.ChargedNano).String(), Fee: money.FromNano(v.FeeNano).String(), Interrupted: v.Interrupted, Disconnected: v.Disconnected, Unbilled: v.Unbilled, TtftP50: v.TtftP50, TtftP95: v.TtftP95, TtftP99: v.TtftP99, DurationP50: v.DurationP50, DurationP95: v.DurationP95, DurationP99: v.DurationP99, SpeedP10: v.SpeedP10, SpeedP50: v.SpeedP50, SpeedP95: v.SpeedP95, GapP50: v.GapP50, GapP95: v.GapP95, GapP99: v.GapP99, MaxGapP95: v.MaxGapP95, CostP50: v.CostP50, CostP95: v.CostP95, CostP99: v.CostP99, RetryP50: v.RetryP50, RetryP95: v.RetryP95, OverheadP50: v.OverheadP50, OverheadP95: v.OverheadP95, AuthenticationP95: v.AuthenticationP95, RequestP95: v.RequestP95, OpenCallP95: v.OpenCallP95, AdmissionP95: v.AdmissionP95, RoutingP95: v.RoutingP95, PricingP95: v.PricingP95, SettlementP95: v.SettlementP95, ClientWriteP95: v.ClientWriteP95}
}
func (a *app) analyzeMyCalls(w http.ResponseWriter, r *http.Request)    { a.analyzeCalls(w, r, false) }
func (a *app) analyzeAdminCalls(w http.ResponseWriter, r *http.Request) { a.analyzeCalls(w, r, true) }
func (a *app) analyzeCalls(w http.ResponseWriter, r *http.Request, admin bool) {
	f, ok := parseCallFilter(r)
	q := r.URL.Query()
	if !ok {
		writeInvalidQuery(w)
		return
	}
	if !admin {
		f.AccountID = accountFromContext(r.Context()).ID
		if q.Get("group_by") == "account" || q.Get("split_by") == "account" {
			writeInvalidQuery(w)
			return
		}
	}
	bucket := 0
	if raw := q.Get("bucket_seconds"); raw != "" {
		var err error
		bucket, err = strconv.Atoi(raw)
		if err != nil {
			writeInvalidQuery(w)
			return
		}
	}
	report, err := a.observe.Analysis(r.Context(), f, q.Get("group_by"), q.Get("split_by"), bucket)
	if err != nil {
		writeObserveError(w, err)
		return
	}
	rows := func(input []observe.AnalysisRow) []analysisRowJSON {
		result := make([]analysisRowJSON, 0, len(input))
		for _, v := range input {
			result = append(result, analysisRowJSON{v.Key, v.Label, v.SplitKey, v.SplitLabel, analysisValues(v.Values)})
		}
		return result
	}
	writeJSON(w, 200, analysisReportJSON{report.From, report.To, report.PreviousFrom, report.PreviousTo, report.GroupBy, report.SplitBy, report.BucketSeconds, analysisValues(report.Current), analysisValues(report.Previous), rows(report.Groups), rows(report.Trend)})
}
