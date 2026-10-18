package gateway

import (
	"cmp"
	"slices"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// ModelMetricView exposes observations and their scope, never upstream
// credentials or arbitrary response labels. Counts below MinSamples are real
// observations but do not yet participate in latency/TPS ranking.
type ModelMetricView struct {
	Format             channel.Format `json:"format"`
	Tier               string         `json:"tier"`
	Stream             bool           `json:"stream"`
	Attempts           int64          `json:"attempts"`
	Successes          int64          `json:"successes"`
	Excluded           int64          `json:"excluded"`
	SuccessRate        *float64       `json:"success_rate"`
	FirstOutputMS      *float64       `json:"first_output_p50_ms"`
	FirstOutputSamples int64          `json:"first_output_samples"`
	OutputTPS          *float64       `json:"output_tps_p50"`
	OutputSamples      int64          `json:"output_samples"`
	MinSamples         int            `json:"min_samples"`
}

// ModelMetrics is a management-page read of one immutable snapshot, grouped by
// channel. It cannot reconstruct a missing model from whole-channel history.
func (e *Engine) ModelMetrics(model string) map[string][]ModelMetricView {
	out := map[string][]ModelMetricView{}
	for scope, view := range e.statsView().models {
		if scope.model != model {
			continue
		}
		for id, stats := range view.channels {
			row := ModelMetricView{Format: scope.format, Tier: scope.tier, Stream: scope.stream, Attempts: stats.Attempts, Successes: stats.Successes, Excluded: stats.ExcludedSamples, FirstOutputMS: stats.FirstOutputP50MS, FirstOutputSamples: stats.FirstOutputSamples, OutputTPS: stats.OutputTPSP50, OutputSamples: stats.OutputSpeedSamples, MinSamples: minSamples}
			if stats.Attempts > 0 {
				rate := stats.SuccessRate()
				row.SuccessRate = &rate
			}
			out[id] = append(out[id], row)
		}
	}
	for _, rows := range out {
		slices.SortFunc(rows, func(a, b ModelMetricView) int {
			if n := cmp.Compare(a.Format, b.Format); n != 0 {
				return n
			}
			if n := cmp.Compare(a.Tier, b.Tier); n != 0 {
				return n
			}
			if a.Stream == b.Stream {
				return 0
			}
			if a.Stream {
				return -1
			}
			return 1
		})
	}
	return out
}
