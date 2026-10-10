package api

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

func TestCallDetailOutputSpeed(t *testing.T) {
	total, zero, negative := 2380, 0, -1
	for _, tt := range []struct {
		name     string
		stream   bool
		duration *int
		want     float64
	}{
		{"nonstream sample", false, &total, 6.0 / 2.38},
		{"zero duration", false, &zero, 0},
		{"missing duration", false, nil, 0},
		{"negative duration", false, &negative, 0},
		{"stream unchanged", true, &total, 2000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, first := range []int{2377, 100} {
				stored := 2000.0
				detail := observe.CallDetail{CallRecord: observe.CallRecord{
					CallRow:         observe.CallRow{Stream: tt.stream, DurationMS: tt.duration, TTFTMS: &first, Usage: observe.Usage{OutputTokens: 6}},
					TokensPerSecond: &stored,
				}}
				result := newCallDetailJSON(detail)
				if _, err := json.Marshal(result); err != nil {
					t.Fatal(err)
				}
				speed := result.OutputTokensPerSecond
				if tt.want == 0 {
					if speed != nil {
						t.Fatalf("want unavailable, got %v", *speed)
					}
				} else if speed == nil || math.Abs(*speed-tt.want) > 1e-9 {
					t.Fatalf("want %v, got %v", tt.want, speed)
				}
			}
		})
	}
}
