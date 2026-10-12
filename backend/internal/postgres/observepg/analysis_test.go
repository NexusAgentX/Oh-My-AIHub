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
