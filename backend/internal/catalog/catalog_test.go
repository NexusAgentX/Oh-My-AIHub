package catalog

import (
	"strings"

	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestValidateModelIDFitsOnePathSegment(t *testing.T) {
	base := Model{
		DisplayName: "Test Model",
	}
	for _, id := range []string{"", "openai/gpt-5", "gemini-2.5-flash:generateContent", "-model", ".model", "provider model", strings.Repeat("a", 129)} {
		model := base
		model.ID = id
		if err := Validate(model); err == nil {
			t.Fatalf("validate model ID %q unexpectedly succeeded", id)
		}
	}

	for _, id := range []string{"gpt-5", "claude-sonnet-4-5", "gemini-2.5-flash", "Qwen3_235B"} {
		model := base
		model.ID = id
		if err := Validate(model); err != nil {
			t.Fatalf("validate model ID %q: %v", id, err)
		}
	}
}

func TestValidateModelPriceCeilingProtectsChannelPriceProjection(t *testing.T) {
	base := Model{
		ID: "provider-model", DisplayName: "Test Model",
		InputPrice: MaxPriceNanoPerMillion, OutputPrice: MaxPriceNanoPerMillion,
		CacheWritePrice: MaxPriceNanoPerMillion, CacheReadPrice: MaxPriceNanoPerMillion,
	}
	if err := Validate(base); err != nil {
		t.Fatalf("maximum catalog price was rejected: %v", err)
	}
	base.InputPrice = money.FromNano(MaxPriceNanoPerMillion.Nano() + 1)
	if err := Validate(base); err == nil {
		t.Fatal("catalog price above the representable channel ceiling was accepted")
	}
}
