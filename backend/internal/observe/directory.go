package observe

import (
	"context"
	"slices"
	"time"
)

// DirectoryValues is a deliberately public, aggregate-only projection. Never add
// account identifiers, billing, raw upstream errors or call diagnostics here.
type DirectoryValues struct {
	CacheSamples    int64    `json:"cache_samples"`
	CacheReadRatio  *float64 `json:"cache_read_ratio"`
	CacheHitRate    *float64 `json:"cache_hit_rate"`
	Attempts        int64    `json:"attempts"`
	Counted         int64    `json:"counted"`
	Succeeded       int64    `json:"succeeded"`
	Failed          int64    `json:"failed"`
	Excluded        int64    `json:"excluded"`
	Streamed        int64    `json:"streamed"`
	TtftSamples     int64    `json:"ttft_samples"`
	DurationSamples int64    `json:"duration_samples"`
	SpeedSamples    int64    `json:"speed_samples"`
	GapSamples      int64    `json:"gap_samples"`
	TtftP50         *float64 `json:"ttft_p50"`
	TtftP95         *float64 `json:"ttft_p95"`
	TtftP99         *float64 `json:"ttft_p99"`
	DurationP50     *float64 `json:"duration_p50"`
	DurationP95     *float64 `json:"duration_p95"`
	DurationP99     *float64 `json:"duration_p99"`
	SpeedP10        *float64 `json:"speed_p10"`
	SpeedP50        *float64 `json:"speed_p50"`
	SpeedP95        *float64 `json:"speed_p95"`
	GapP50          *float64 `json:"gap_p50"`
	GapP95          *float64 `json:"gap_p95"`
	GapP99          *float64 `json:"gap_p99"`
}
type DirectoryRow struct {
	Key    string          `json:"key"`
	Label  string          `json:"label"`
	Values DirectoryValues `json:"values"`
}
type DirectoryReport struct {
	From          time.Time       `json:"from"`
	To            time.Time       `json:"to"`
	PreviousFrom  time.Time       `json:"previous_from"`
	PreviousTo    time.Time       `json:"previous_to"`
	BucketSeconds int             `json:"bucket_seconds"`
	GroupBy       string          `json:"group_by"`
	Current       DirectoryValues `json:"current"`
	Previous      DirectoryValues `json:"previous"`
	Groups        []DirectoryRow  `json:"groups"`
	Trend         []DirectoryRow  `json:"trend"`
}
type DirectoryFilter struct {
	TargetID                                       string
	Supplier                                       bool
	From, To                                       time.Time
	Model, Format, Channel, Stream, Result, Status string
	Group                                          string
	Bucket                                         int
}

var DirectoryDimensions = []string{"model", "channel", "format", "stream", "result", "status"}
var DirectoryResults = []string{"completed", "upstream_error", "connect_error", "timeout_ttft", "timeout_total", "client_error", "client_disconnected", "interrupted", "unknown"}

func (s *Service) DirectoryAnalysis(ctx context.Context, f DirectoryFilter) (DirectoryReport, error) {
	if f.Group == "" {
		f.Group = "model"
	}
	if !f.From.Before(f.To) || f.To.Sub(f.From) > maxStatsWindow || !slices.Contains(DirectoryDimensions, f.Group) ||
		(f.Format != "" && !slices.Contains(formats, f.Format)) ||
		(f.Stream != "" && f.Stream != "true" && f.Stream != "false") ||
		(f.Result != "" && !slices.Contains(DirectoryResults, f.Result)) || len(f.Model) > 200 {
		return DirectoryReport{}, ErrInvalidInput
	}
	if f.Bucket == 0 {
		f.Bucket = 3600
		if f.To.Sub(f.From) > 7*24*time.Hour {
			f.Bucket = 86400
		}
	}
	if !slices.Contains([]int{300, 900, 3600, 21600, 86400}, f.Bucket) || f.To.Sub(f.From)/time.Duration(f.Bucket)/time.Second > 998 {
		return DirectoryReport{}, ErrInvalidInput
	}
	return s.store.DirectoryAnalysis(ctx, f)
}
