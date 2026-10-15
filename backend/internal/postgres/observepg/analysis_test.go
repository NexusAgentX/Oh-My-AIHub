package observepg

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"testing"
	"time"
)

func TestTrendPreservesEmptyBucketsAndNullPercentiles(t *testing.T) {
	from := time.Date(2026, 1, 1, 10, 15, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)
	rows := fillTrend(observe.AnalysisReport{From: from, To: to, BucketSeconds: 3600, Trend: []observe.AnalysisRow{{Key: "2026-01-01T11:00:00Z", Values: observe.AnalysisValues{Calls: 3}}}})
	if len(rows) != 3 || rows[0].Key != "2026-01-01T10:00:00Z" || rows[1].Values.Calls != 3 || rows[2].Values.Calls != 0 || rows[2].Values.DurationP95 != nil {
		t.Fatalf("trend=%+v", rows)
	}
}

func TestAnalysisRowsPreserveSectionsAndUnknownValues(t *testing.T) {
	var report observe.AnalysisReport
	rows := []AnalyzeCallsRow{
		{Kind: "group", GroupKey: "model", SplitKey: "channel", Label: "Model", SplitLabel: "Channel", Calls: 2, DurationP95: 95, SettlementP95: -1},
		{Kind: "summary", GroupKey: "current", Calls: 3, DurationP95: 97, SettlementP95: -1},
		{Kind: "summary", GroupKey: "previous", Calls: 7, DurationP95: 40},
		{Kind: "time", GroupKey: "2026-01-01T10:00:00Z", Calls: 3, DurationP95: 97},
	}
	if err := setAnalysisRows(&report, rows); err != nil {
		t.Fatal(err)
	}
	if report.Current.Calls != 3 || report.Previous.Calls != 7 || *report.Current.DurationP95 != 97 || report.Current.SettlementP95 != nil {
		t.Fatalf("summary=%+v previous=%+v", report.Current, report.Previous)
	}
	if len(report.Groups) != 1 || report.Groups[0].SplitKey != "channel" || report.Groups[0].Label != "Model" || report.Groups[0].SplitLabel != "Channel" || report.Groups[0].Values.SettlementP95 != nil {
		t.Fatalf("groups=%+v", report.Groups)
	}
	if len(report.Trend) != 1 || report.Trend[0].Label != report.Trend[0].Key || report.Trend[0].Values.Calls != 3 {
		t.Fatalf("trend=%+v", report.Trend)
	}
}

func TestAnalysisRowsRejectOverflowWithoutReturningPartialSuccess(t *testing.T) {
	for _, kind := range []string{"group", "time"} {
		rows := make([]AnalyzeCallsRow, 1001)
		for i := range rows {
			rows[i].Kind = kind
		}
		var report observe.AnalysisReport
		if err := setAnalysisRows(&report, rows); err != observe.ErrInvalidInput {
			t.Fatalf("%s overflow: %v", kind, err)
		}
	}
	var report observe.AnalysisReport
	if err := setAnalysisRows(&report, make([]AnalyzeCallsRow, 2003)); err != observe.ErrInvalidInput {
		t.Fatalf("SQL sentinel: %v", err)
	}
	rows := []AnalyzeCallsRow{{Kind: "summary", GroupKey: "current"}, {Kind: "summary", GroupKey: "previous"}}
	for range 1000 {
		rows = append(rows, AnalyzeCallsRow{Kind: "group"}, AnalyzeCallsRow{Kind: "time"})
	}
	if err := setAnalysisRows(&report, rows); err != nil || len(report.Groups) != 1000 || len(report.Trend) != 1000 {
		t.Fatalf("valid boundary: groups=%d trend=%d error=%v", len(report.Groups), len(report.Trend), err)
	}
}
