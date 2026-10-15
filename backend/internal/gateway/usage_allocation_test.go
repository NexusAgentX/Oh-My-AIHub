//go:build !race

package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

// The race instrumenter changes escape analysis and adds per-frame heap costs.
// Measure the production build here; cumulative correctness is tested in all builds.
func TestGeminiCumulativeUsageHasBoundedAllocation(t *testing.T) {
	body := []byte(strings.Repeat(geminiCumulativeUsageFrame, 1000))
	allocations := testing.AllocsPerRun(5, func() {
		o := NewObserver(channel.FormatGemini, true, "text/event-stream")
		o.Write(body, time.Unix(0, 0))
		o.Finish()
	})
	// Fixed scratch, line, histogram and detail storage; never one decoded usage
	// struct per frame. Leave headroom for bounded buffer capacity growth.
	if allocations > 40 {
		t.Fatalf("1000 cumulative usage frames allocated %.0f times", allocations)
	}
}
