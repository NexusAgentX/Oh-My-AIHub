package observepg

import (
	"context"
	"errors"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"time"
)

func (s *Store) Analyze(ctx context.Context, f observe.CallFilter, group, split string, bucket int) (observe.AnalysisReport, error) {
	report := observe.AnalysisReport{From: *f.From, To: *f.To, PreviousFrom: f.From.Add(-f.To.Sub(*f.From)), PreviousTo: *f.From, GroupBy: group, SplitBy: split, BucketSeconds: bucket, Groups: []observe.AnalysisRow{}, Trend: []observe.AnalysisRow{}}
	err := s.withAnalysisBudget(ctx, func(ctx context.Context) error {
		return s.snapshot(ctx, func(q *Queries) error {
			read := func(filter observe.CallFilter, g, sp string) ([]observe.AnalysisRow, error) {
				p := callListParams(filter)
				rows, err := q.AnalyzeCalls(ctx, AnalyzeCallsParams{Dimension: p.Dimension, DimensionValue: p.DimensionValue, SecondDimension: p.SecondDimension, SecondValue: p.SecondValue, GroupBy: g, SplitBy: sp, BucketSeconds: int32(bucket), AccountID: p.AccountID, ScopeChannelID: p.ScopeChannelID, ScopeChannelText: p.ScopeChannelText, FinalChannelID: p.FinalChannelID, ApiKeyID: p.ApiKeyID, Model: p.Model, Format: p.Format, Outcome: p.Outcome, Tag: p.Tag, RequestID: p.RequestID, FromTime: p.FromTime, ToTime: p.ToTime, MinDuration: p.MinDuration, MaxDuration: p.MaxDuration, MinTokens: p.MinTokens, MaxTokens: p.MaxTokens, MinCharged: p.MinCharged, MaxCharged: p.MaxCharged, Stream: p.Stream, MinAttempts: p.MinAttempts, MaxAttempts: p.MaxAttempts, StatusCode: p.StatusCode, Billed: p.Billed, Delivery: p.Delivery, UsageStatus: p.UsageStatus, ErrorCode: p.ErrorCode, EndReason: p.EndReason, AttemptChannel: p.AttemptChannel})
				if err != nil {
					return nil, err
				}
				if len(rows) > 1000 {
					return nil, observe.ErrInvalidInput
				}
				result := make([]observe.AnalysisRow, 0, len(rows))
				for _, row := range rows {
					result = append(result, observe.AnalysisRow{Key: row.GroupKey, Label: row.Label, SplitKey: row.SplitKey, SplitLabel: row.SplitLabel, Values: observe.AnalysisValues{FallbackP95: nonNegative(row.FallbackP95), FallbackP50: nonNegative(row.FallbackP50), StreamInterrupted: row.StreamInterrupted, StreamFinished: row.StreamFinished, Attempted: row.Attempted, Calls: row.Calls, Finished: row.Finished, Succeeded: row.Succeeded, FirstSucceeded: row.FirstSucceeded, Retried: row.Retried, Recovered: row.Recovered, Attempts: row.Attempts, AttemptSucceeded: row.AttemptSucceeded, Delivered: row.Delivered, DeliveryKnown: row.DeliveryKnown, Billed: row.Billed, UsageReported: row.UsageReported, UsageMissing: row.UsageMissing, Streamed: row.Streamed, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens, ChargedNano: row.ChargedNano, FeeNano: row.FeeNano, Interrupted: row.Interrupted, Disconnected: row.Disconnected, Unbilled: row.Unbilled, TtftP50: nonNegative(row.TtftP50), TtftP95: nonNegative(row.TtftP95), TtftP99: nonNegative(row.TtftP99), DurationP50: nonNegative(row.DurationP50), DurationP95: nonNegative(row.DurationP95), DurationP99: nonNegative(row.DurationP99), SpeedP10: nonNegative(row.SpeedP10), SpeedP50: nonNegative(row.SpeedP50), SpeedP95: nonNegative(row.SpeedP95), GapP50: nonNegative(row.GapP50), GapP95: nonNegative(row.GapP95), GapP99: nonNegative(row.GapP99), MaxGapP95: nonNegative(row.MaxGapP95), CostP50: nonNegative(row.CostP50), CostP95: nonNegative(row.CostP95), CostP99: nonNegative(row.CostP99), RetryP50: nonNegative(row.RetryP50), RetryP95: nonNegative(row.RetryP95), OverheadP50: nonNegative(row.OverheadP50), OverheadP95: nonNegative(row.OverheadP95), AuthenticationP95: nonNegative(row.AuthenticationP95), RequestP95: nonNegative(row.RequestP95), OpenCallP95: nonNegative(row.OpenCallP95), AdmissionP95: nonNegative(row.AdmissionP95), RoutingP95: nonNegative(row.RoutingP95), PricingP95: nonNegative(row.PricingP95), SettlementP95: nonNegative(row.SettlementP95), ClientWriteP95: nonNegative(row.ClientWriteP95)}})
				}
				return result, nil
			}
			current, err := read(f, "all", "all")
			if err != nil {
				return err
			}
			if len(current) > 0 {
				report.Current = current[0].Values
			}
			previous := f
			previous.From = &report.PreviousFrom
			previous.To = &report.PreviousTo
			prev, err := read(previous, "all", "all")
			if err != nil {
				return err
			}
			if len(prev) > 0 {
				report.Previous = prev[0].Values
			}
			report.Groups, err = read(f, group, split)
			if err != nil {
				return err
			}
			report.Trend, err = read(f, "time", "all")
			return err
		})
	})
	if err == nil {
		report.Trend = fillTrend(report)
	}
	return report, err
}

func fillTrend(report observe.AnalysisReport) []observe.AnalysisRow {
	rows := make(map[string]observe.AnalysisRow, len(report.Trend))
	for _, row := range report.Trend {
		rows[row.Key] = row
	}
	step := time.Duration(report.BucketSeconds) * time.Second
	start := time.Unix(report.From.Unix()/int64(report.BucketSeconds)*int64(report.BucketSeconds), 0).UTC()
	result := []observe.AnalysisRow{}
	for at := start; at.Before(report.To); at = at.Add(step) {
		key := at.Format(time.RFC3339)
		row, ok := rows[key]
		if !ok {
			row = observe.AnalysisRow{Key: key, Label: key}
		}
		result = append(result, row)
	}
	return result
}

// Analytics shares the gateway pool. Admit at most two scans, with no waiting
// queue, and bound the entire repeatable-read transaction (not each query).
func (s *Store) withAnalysisBudget(ctx context.Context, read func(context.Context) error) error {
	select {
	case s.analysisSlots <- struct{}{}:
		defer func() { <-s.analysisSlots }()
	default:
		return observe.ErrAnalysisBusy
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := read(bounded)
	if errors.Is(bounded.Err(), context.DeadlineExceeded) {
		return observe.ErrAnalysisTimeout
	}
	return err
}
