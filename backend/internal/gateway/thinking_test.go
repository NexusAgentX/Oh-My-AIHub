package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestThinkingObservationsDoNotChangePricing(t *testing.T) {
	off, on := false, true
	for _, tc := range []struct {
		name, events string
		observed     *bool
	}{
		{name: "generic content", events: `{"choices":[{"delta":{"reasoning_content":"private"}}]}`, observed: &on},
		{name: "generic alias", events: `{"choices":[{"delta":{"reasoning":"private"}}]}`, observed: &on},
		{name: "generic message", events: `{"choices":[{"message":{"reasoning_content":"private"}}]}`, observed: &on},
		{name: "empty", events: `{"choices":[{"delta":{"reasoning_content":""}}]}`, observed: &off},
		{name: "null unknown", events: `{"choices":[{"delta":{"reasoning_content":null}}]}`},
		{name: "invalid unknown", events: `{"choices":[{"delta":{"reasoning_content":42}}]}`},
		{name: "multiple choices", events: `{"choices":[{"delta":{"reasoning_content":""}},{"delta":{"reasoning":"private"}}]}`, observed: &on},
		{name: "both keys", events: `{"choices":[{"delta":{"reasoning_content":"","reasoning":"private"}}]}`, observed: &on},
		{name: "sticky nonempty", events: "{\"choices\":[{\"delta\":{\"reasoning\":\"private\"}}]}\n{\"choices\":[{\"delta\":{\"reasoning\":\"\"}}]}", observed: &on},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := NewObserver(channel.FormatOpenAIChat, true, "text/event-stream")
			model := catalog.Model{OutputPrice: money.FromNano(2)}
			for _, event := range bytes.Split([]byte(tc.events), []byte("\n")) {
				o.absorb(event)
			}
			// Token totals are reported independently of observed text, including repeated cumulative usage.
			for range 2 {
				o.absorb([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":20,"completion_tokens_details":{"reasoning_tokens":10}}}`))
			}
			got := o.Finish().Usage
			if got.OutputTokens != 20 {
				t.Fatalf("reasoning changed total: %+v", got)
			}
			d := got.Detail
			if d == nil {
				d = &ledger.UsageDetail{}
			}
			if d.ThinkingMode != "" {
				t.Fatalf("retired mode generated: %q", d.ThinkingMode)
			}
			if (d.ThinkingObserved == nil) != (tc.observed == nil) || (tc.observed != nil && *d.ThinkingObserved != *tc.observed) {
				t.Fatalf("observation=%v want=%v", d.ThinkingObserved, tc.observed)
			}
			wantBasis := ""
			if d.ThinkingModeBasis != wantBasis {
				t.Fatalf("basis=%q want=%q", d.ThinkingModeBasis, wantBasis)
			}
			priced, err := ledger.CalculatePriceV2(got, model.BasePrices(), model.PriceTiers, time.Now(), ledger.FixedPointScale, 0)
			if err != nil {
				t.Fatal(err)
			}
			wantTier := 0
			if priced.TierSeq != wantTier {
				t.Fatalf("price=%+v want tier=%d", priced, wantTier)
			}
			snapshot := priceSnapshot(model, priced, Candidate{MultiplierNano: ledger.FixedPointScale, RateNano: ledger.UnitRate}, 0, 0, got, time.Now())
			if bytes.Contains(snapshot, []byte("private")) {
				t.Fatal("snapshot retained reasoning text")
			}
			var saved struct {
				Detail ledger.UsageDetail `json:"detail"`
			}
			if err := json.Unmarshal(snapshot, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Detail.ThinkingModeBasis != wantBasis || saved.Detail.ThinkingMode != "" {
				t.Fatalf("snapshot: %s", snapshot)
			}
		})
	}
}

func BenchmarkThinkingObservation(b *testing.B) {
	raw := []byte(`{"choices":[{"delta":{"reasoning_content":"private reasoning that must not be copied or retained"}}]}`)
	for _, frames := range []int{1, 1000} {
		b.Run(fmt.Sprint(frames), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				o := NewObserver(channel.FormatOpenAIChat, true, "text/event-stream")
				for range frames {
					o.absorb(raw)
				}
				_ = o.Finish()
			}
		})
	}
}
