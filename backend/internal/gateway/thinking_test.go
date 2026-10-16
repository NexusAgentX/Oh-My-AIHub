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

func TestThinkingObservationsAndExplicitPricing(t *testing.T) {
	off, on := false, true
	for _, tc := range []struct {
		name, events, mode string
		enabled            bool
		requested          *bool
		observed           *bool
	}{
		{name: "generic content", events: `{"choices":[{"delta":{"reasoning_content":"private"}}]}`, observed: &on},
		{name: "generic alias", events: `{"choices":[{"delta":{"reasoning":"private"}}]}`, observed: &on},
		{name: "generic message", events: `{"choices":[{"message":{"reasoning_content":"private"}}]}`, observed: &on},
		{name: "empty", events: `{"choices":[{"delta":{"reasoning_content":""}}]}`, observed: &off},
		{name: "null unknown", events: `{"choices":[{"delta":{"reasoning_content":null}}]}`},
		{name: "invalid unknown", events: `{"choices":[{"delta":{"reasoning_content":42}}]}`},
		{name: "request is not observation", requested: &off},
		{name: "multiple choices", events: `{"choices":[{"delta":{"reasoning_content":""}},{"delta":{"reasoning":"private"}}]}`, observed: &on},
		{name: "both keys", events: `{"choices":[{"delta":{"reasoning_content":"","reasoning":"private"}}]}`, observed: &on},
		{name: "sticky nonempty", events: "{\"choices\":[{\"delta\":{\"reasoning\":\"private\"}}]}\n{\"choices\":[{\"delta\":{\"reasoning\":\"\"}}]}", observed: &on},
		{name: "configured actual", enabled: true, events: `{"choices":[{"delta":{"reasoning_content":"private"}}]}`, observed: &on, mode: "qwen_thinking"},
		{name: "configured empty", enabled: true, events: `{"choices":[{"delta":{"reasoning_content":""}}]}`, observed: &off, mode: "qwen_non_thinking"},
		{name: "configured null", enabled: true, events: `{"choices":[{"delta":{"reasoning_content":null}}]}`, mode: "qwen_non_thinking"},
		{name: "configured invalid", enabled: true, events: `{"choices":[{"delta":{"reasoning_content":42}}]}`},
		{name: "configured alias remains generic", enabled: true, events: `{"choices":[{"delta":{"reasoning":"private"}}]}`, observed: &on},
		{name: "configured numeric tokens remain generic", enabled: true, events: `{"usage":{"prompt_tokens":1,"completion_tokens":20,"completion_tokens_details":{"reasoning_tokens":10}}}`},
		{name: "configured request off", enabled: true, requested: &off, mode: "qwen_non_thinking"},
		{name: "configured request on unknown", enabled: true, requested: &on},
		{name: "response overrides request", enabled: true, requested: &off, events: `{"choices":[{"delta":{"reasoning_content":"private"}}]}`, observed: &on, mode: "qwen_thinking"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := NewObserver(channel.FormatOpenAIChat, true, "text/event-stream")
			model := catalog.Model{OutputPrice: money.FromNano(2)}
			if tc.enabled {
				model.PriceTiers = []ledger.PriceTier{{ThinkingMode: "qwen_thinking", OutputPrice: money.FromNano(4)}, {ThinkingMode: "qwen_non_thinking", OutputPrice: money.FromNano(3)}}
			}
			o.configureThinkingPricing(model.PriceTiers, tc.requested)
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
			if d.ThinkingMode != tc.mode {
				t.Fatalf("mode=%q want=%q", d.ThinkingMode, tc.mode)
			}
			if (d.ThinkingObserved == nil) != (tc.observed == nil) || (tc.observed != nil && *d.ThinkingObserved != *tc.observed) {
				t.Fatalf("observation=%v want=%v", d.ThinkingObserved, tc.observed)
			}
			wantBasis := ""
			if tc.mode != "" {
				wantBasis = "bailian_condition"
			}
			if d.ThinkingModeBasis != wantBasis {
				t.Fatalf("basis=%q want=%q", d.ThinkingModeBasis, wantBasis)
			}
			priced, err := ledger.CalculatePriceV2(got, model.BasePrices(), model.PriceTiers, time.Now(), ledger.FixedPointScale, 0)
			if err != nil {
				t.Fatal(err)
			}
			wantTier := 0
			if tc.mode == "qwen_thinking" {
				wantTier = 1
			}
			if tc.mode == "qwen_non_thinking" {
				wantTier = 2
			}
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
			if saved.Detail.ThinkingModeBasis != wantBasis || saved.Detail.ThinkingMode != tc.mode {
				t.Fatalf("snapshot: %s", snapshot)
			}
		})
	}
}

func TestBailianConditionsDoNotApplyToOtherProtocols(t *testing.T) {
	off := false
	for _, format := range []channel.Format{channel.FormatOpenAIResponses, channel.FormatAnthropic, channel.FormatGemini} {
		o := NewObserver(format, false, "application/json")
		o.configureThinkingPricing([]ledger.PriceTier{{ThinkingMode: "qwen_thinking"}}, &off)
		if o.bailianPricing || o.qwenObserved {
			t.Fatalf("enabled Bailian pricing for %s", format)
		}
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
