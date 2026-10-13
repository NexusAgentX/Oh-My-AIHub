package observepg

import (
	"context"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"time"
)

func (s *Store) DirectoryAnalysis(ctx context.Context, f observe.DirectoryFilter) (observe.DirectoryReport, error) {
	report := observe.DirectoryReport{From: f.From, To: f.To, PreviousFrom: f.From.Add(-f.To.Sub(f.From)), PreviousTo: f.From, BucketSeconds: f.Bucket, GroupBy: f.Group, Groups: []observe.DirectoryRow{}, Trend: []observe.DirectoryRow{}}
	err := s.withAnalysisBudget(ctx, func(ctx context.Context) error {
		return s.snapshot(ctx, func(q *Queries) error {
			visible, err := q.DirectoryTargetExists(ctx, DirectoryTargetExistsParams{Supplier: f.Supplier, TargetID: f.TargetID})
			if err != nil {
				return err
			}
			if !visible {
				return observe.ErrNotFound
			}
			rows, err := q.AnalyzeDirectory(ctx, AnalyzeDirectoryParams{Supplier: f.Supplier, TargetID: f.TargetID, PreviousFrom: report.PreviousFrom, FromTime: f.From, ToTime: f.To, GroupBy: f.Group, BucketSeconds: int32(f.Bucket), Model: f.Model, Format: f.Format, Channel: f.Channel, Stream: f.Stream, Result: f.Result, Status: f.Status})
			if err != nil {
				return err
			}
			// Never silently truncate a report: the UI can narrow its filters.
			if len(rows) >= 2002 {
				return observe.ErrInvalidInput
			}
			for _, row := range rows {
				v := observe.DirectoryValues{Attempts: row.Attempts, Counted: row.Counted, Succeeded: row.Succeeded, Failed: row.Failed, Excluded: row.Excluded, Streamed: row.Streamed, TtftSamples: row.TtftSamples, DurationSamples: row.DurationSamples, SpeedSamples: row.SpeedSamples, GapSamples: row.GapSamples, TtftP50: nonNegative(row.TtftP50), TtftP95: nonNegative(row.TtftP95), TtftP99: nonNegative(row.TtftP99), DurationP50: nonNegative(row.DurationP50), DurationP95: nonNegative(row.DurationP95), DurationP99: nonNegative(row.DurationP99), SpeedP10: nonNegative(row.SpeedP10), SpeedP50: nonNegative(row.SpeedP50), SpeedP95: nonNegative(row.SpeedP95), GapP50: nonNegative(row.GapP50), GapP95: nonNegative(row.GapP95), GapP99: nonNegative(row.GapP99)}
				switch row.Kind {
				case "summary":
					if row.Key == "current" {
						report.Current = v
					} else {
						report.Previous = v
					}
				case "group":
					report.Groups = append(report.Groups, observe.DirectoryRow{Key: row.Key, Label: row.Label, Values: v})
				case "time":
					report.Trend = append(report.Trend, observe.DirectoryRow{Key: row.Key, Label: row.Key, Values: v})
				}
			}
			if len(report.Groups) > 1000 {
				return observe.ErrInvalidInput
			}
			return nil
		})
	})
	if err == nil {
		rows := map[string]observe.DirectoryRow{}
		for _, row := range report.Trend {
			rows[row.Key] = row
		}
		report.Trend = []observe.DirectoryRow{}
		for at := time.Unix(f.From.Unix()/int64(f.Bucket)*int64(f.Bucket), 0).UTC(); at.Before(f.To); at = at.Add(time.Duration(f.Bucket) * time.Second) {
			key := at.Format(time.RFC3339)
			row, ok := rows[key]
			if !ok {
				row = observe.DirectoryRow{Key: key, Label: key}
			}
			report.Trend = append(report.Trend, row)
		}
	}
	return report, err
}
