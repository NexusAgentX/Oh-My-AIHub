package observe

import (
	"context"
	"slices"
	"time"
)

type AnalysisValues struct {
	FallbackP95       *float64
	FallbackP50       *float64
	StreamInterrupted int64
	StreamFinished    int64
	Attempted         int64
	Calls             int64
	Finished          int64
	Succeeded         int64
	FirstSucceeded    int64
	Retried           int64
	Recovered         int64
	Attempts          int64
	AttemptSucceeded  int64
	Delivered         int64
	DeliveryKnown     int64
	Billed            int64
	UsageReported     int64
	UsageMissing      int64
	Streamed          int64
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheWriteTokens  int64
	ChargedNano       int64
	FeeNano           int64
	Interrupted       int64
	Disconnected      int64
	Unbilled          int64
	TtftP50           *float64
	TtftP95           *float64
	TtftP99           *float64
	DurationP50       *float64
	DurationP95       *float64
	DurationP99       *float64
	SpeedP10          *float64
	SpeedP50          *float64
	SpeedP95          *float64
	GapP50            *float64
	GapP95            *float64
	GapP99            *float64
	MaxGapP95         *float64
	CostP50           *float64
	CostP95           *float64
	CostP99           *float64
	RetryP50          *float64
	RetryP95          *float64
	OverheadP50       *float64
	OverheadP95       *float64
	AuthenticationP95 *float64
	RequestP95        *float64
	OpenCallP95       *float64
	AdmissionP95      *float64
	RoutingP95        *float64
	PricingP95        *float64
	SettlementP95     *float64
	ClientWriteP95    *float64
}
type AnalysisRow struct {
	Key, Label, SplitKey, SplitLabel string
	Values                           AnalysisValues
}
type AnalysisReport struct {
	From, To                 time.Time
	PreviousFrom, PreviousTo time.Time
	GroupBy, SplitBy         string
	BucketSeconds            int
	Current, Previous        AnalysisValues
	Groups, Trend            []AnalysisRow
}

var AnalysisDimensions = []string{"all", "model", "channel", "format", "stream", "key", "tag", "account", "outcome", "error", "error_code", "status", "stop", "service_tier"}

func (s *Service) Analysis(ctx context.Context, filter CallFilter, group, split string, bucket int) (AnalysisReport, error) {
	if group == "" {
		group = "model"
	}
	if split == "" {
		split = "all"
	}
	if !slices.Contains(AnalysisDimensions, group) || !slices.Contains(AnalysisDimensions, split) || filter.From == nil || filter.To == nil || !filter.From.Before(*filter.To) || filter.To.Sub(*filter.From) > maxUsageWindow {
		return AnalysisReport{}, ErrInvalidInput
	}
	if bucket == 0 {
		bucket = 3600
		if filter.To.Sub(*filter.From) > 7*24*time.Hour {
			bucket = 86400
		}
	}
	if !slices.Contains([]int{60, 300, 900, 3600, 21600, 86400}, bucket) || (filter.To.Unix()-filter.From.Unix())/int64(bucket) > 998 {
		return AnalysisReport{}, ErrInvalidInput
	}
	return s.store.Analyze(ctx, filter, group, split, bucket)
}
