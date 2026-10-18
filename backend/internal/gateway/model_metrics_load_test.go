package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

type modelHistoryStore struct {
	historyStore
	models     []ModelAttemptHistoryRow
	modelCalls int
}

func (s *modelHistoryStore) ModelAttemptHistory(_ context.Context, _ time.Time, limit int32) ([]ModelAttemptHistoryRow, error) {
	s.modelCalls++
	if limit != maxMetricBuckets+1 {
		panic("unbounded history limit")
	}
	return s.models, nil
}

func TestModelMetricsWarmupDoesNotProjectLegacyOrTruncatedHistory(t *testing.T) {
	now := time.Now()
	base := ModelAttemptHistoryRow{ChannelID: "channel", ModelID: "one", Protocol: "openai-responses", Format: channel.FormatOpenAIResponses, Stream: true, Bucket: statsEpoch(now), Counted: 30, Successes: 25, Excluded: 10, ClientErrors: 8, LatencyBin: metricBin(100, false), SpeedBin: metricBin(80, true)}
	for _, tc := range []struct {
		name string
		rows []ModelAttemptHistoryRow
		want int64
	}{
		{"measured", []ModelAttemptHistoryRow{base}, 30},
		{"legacy only", nil, 0},
		{"v2 without actual entrance", []ModelAttemptHistoryRow{func() ModelAttemptHistoryRow { v := base; v.Protocol = ""; return v }()}, 0},
		{"too many aggregates", make([]ModelAttemptHistoryRow, maxMetricBuckets+1), 0},
		{"too many calls", []ModelAttemptHistoryRow{func() ModelAttemptHistoryRow { v := base; v.HistoryTruncated = true; return v }()}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &modelHistoryStore{models: tc.rows, historyStore: historyStore{rows: []AttemptHistoryRow{{ChannelID: "channel", ModelID: "one", Bucket: statsEpoch(now), Counted: 999, Successes: 999, TTFTBin: ttftBinOf(1)}}}}
			e := NewEngine(Dependencies{Store: store, Now: func() time.Time { return now }})
			if err := e.LoadStats(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := e.ModelStats("one", base.Format, "", true)["channel"]
			if got.Attempts != tc.want || tc.want == 0 && got.FirstOutputP50MS != nil {
				t.Fatal("legacy/partial metrics used", got)
			}
			for range 3 {
				e.RefreshStats()
			}
			if store.modelCalls != 1 || store.calls != 1 {
				t.Fatal("refresh caused SQL")
			}
		})
	}
}

func TestModelMetricEditOnlyResetsScopedAnomaliesAndPreservesHealth(t *testing.T) {
	now := time.Now()
	book := newModelMetricsBook()
	base := ModelAttemptHistoryRow{ChannelID: "channel", ModelID: "one", Protocol: "openai-responses", Format: channel.FormatOpenAIResponses, Bucket: statsEpoch(now), Counted: 2, Successes: 2, Excluded: 38, ClientErrors: 38, LatencyBin: metricBin(100, false), SpeedBin: -1}
	book.load([]ModelAttemptHistoryRow{base}, now)
	key := modelMetricKey{channel: base.ChannelID, model: base.ModelID, format: base.Format}
	before, _ := book.view(now)
	book.resetAnomalies("channel")
	after, _ := book.view(now)
	if before[normalizeModelMetricKey(key)].anomalyClient != 38 || after[normalizeModelMetricKey(key)].anomalyAttempts != 0 || after[normalizeModelMetricKey(key)].attempts != 2 || after[normalizeModelMetricKey(key)].latencySamples != 2 {
		t.Fatal("edit erased health or retained anomaly", before[normalizeModelMetricKey(key)], after[normalizeModelMetricKey(key)])
	}
	base.BeforeEditAttempts, base.BeforeEditClientErrors = 40, 38
	fresh := newModelMetricsBook()
	fresh.load([]ModelAttemptHistoryRow{base}, now)
	view, _ := fresh.view(now)
	if view[normalizeModelMetricKey(key)].anomalyAttempts != 0 || view[normalizeModelMetricKey(key)].attempts != after[normalizeModelMetricKey(key)].attempts {
		t.Fatal("warmup restored pre-edit anomalies")
	}
}
