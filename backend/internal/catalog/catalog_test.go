package catalog

import (
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestValidateModelIDRejectsAmbiguousPathSegments(t *testing.T) {
	base := Model{
		Name:             "Test Model",
		Provider:         "Provider",
		ContextWindow:    1,
		InputModalities:  []string{"text"},
		OutputModalities: []string{"text"},
		Status:           StatusActive,
	}
	for _, id := range []string{"provider//model", "provider/./model", "provider/../model", "/model", "provider/model/", "provider model"} {
		model := base
		model.ID = id
		if err := validate(model); err == nil {
			t.Fatalf("validate model ID %q unexpectedly succeeded", id)
		}
	}

	for _, id := range []string{"gpt-5", "openai/gpt-5.2", "vendor/family/model:latest"} {
		model := base
		model.ID = id
		if err := validate(model); err != nil {
			t.Fatalf("validate model ID %q: %v", id, err)
		}
	}
}

func TestValidateModelPriceCeilingProtectsChannelPriceProjection(t *testing.T) {
	base := Model{
		ID: "provider/model", Name: "Test Model", Provider: "Provider", ContextWindow: 1,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"}, Status: StatusActive,
		InputPrice: MaxPriceNanoPerMillion, OutputPrice: MaxPriceNanoPerMillion,
		CacheWritePrice: MaxPriceNanoPerMillion, CacheReadPrice: MaxPriceNanoPerMillion,
	}
	if err := validate(base); err != nil {
		t.Fatalf("maximum catalog price was rejected: %v", err)
	}
	base.InputPrice = money.FromNano(MaxPriceNanoPerMillion.Nano() + 1)
	if err := validate(base); err == nil {
		t.Fatal("catalog price above the representable channel ceiling was accepted")
	}
}
