package api

import (
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestParseModelRequestRoundTripsPriceTiers(t *testing.T) {
	minPrompt := int64(200_000)
	start, end := int16(540), int16(720)
	request := modelRequest{
		ID: "vendor/model", Name: "Model", Provider: "Vendor", ContextWindow: 1000,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		InputPrice: "1", OutputPrice: "2", CacheWritePrice: "0.5", CacheReadPrice: "0.25",
		PriceTiers: []priceTierRequest{{
			Name: "peak", MinPromptTokens: &minPrompt, Timezone: "Asia/Shanghai",
			Weekdays: []int{1, 2, 3, 4, 5}, StartMinute: &start, EndMinute: &end,
			InputPrice: "3", OutputPrice: "9", CacheWritePrice: "4.5", CacheReadPrice: "0.3",
		}},
		Status: "active",
	}
	model, err := parseModelRequest(request)
	if err != nil {
		t.Fatalf("parseModelRequest: %v", err)
	}
	if len(model.PriceTiers) != 1 {
		t.Fatalf("parsed %d tiers, want 1", len(model.PriceTiers))
	}
	tier := model.PriceTiers[0]
	if tier.Timezone != "Asia/Shanghai" || tier.MinPromptTokens == nil || *tier.MinPromptTokens != 200_000 ||
		tier.StartMinute == nil || *tier.StartMinute != 540 || tier.OutputPrice != money.FromNano(9_000_000_000) {
		t.Fatalf("parsed tier = %+v", tier)
	}

	response := modelResponse(model)
	tiers, ok := response["price_tiers"].([]map[string]any)
	if !ok || len(tiers) != 1 {
		t.Fatalf("modelResponse price_tiers = %#v", response["price_tiers"])
	}
	if tiers[0]["timezone"] != "Asia/Shanghai" || tiers[0]["input_price"] != "3" || tiers[0]["price_unit"] != "points_per_million_tokens" {
		t.Fatalf("tier response = %#v", tiers[0])
	}
	if tiers[0]["weekdays"].([]int) == nil || len(tiers[0]["weekdays"].([]int)) != 5 {
		t.Fatalf("tier weekdays = %#v", tiers[0]["weekdays"])
	}
}

func TestParseModelRequestRejectsInvalidTierPrices(t *testing.T) {
	request := modelRequest{
		ID: "vendor/model", Name: "Model", Provider: "Vendor", ContextWindow: 1000,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		InputPrice: "1", OutputPrice: "2", CacheWritePrice: "0.5", CacheReadPrice: "0.25",
		PriceTiers: []priceTierRequest{{InputPrice: "100001", OutputPrice: "1", CacheWritePrice: "1", CacheReadPrice: "1"}},
		Status:     "active",
	}
	if _, err := parseModelRequest(request); err == nil {
		t.Fatal("tier price above the catalog ceiling was accepted")
	}
}

func TestEffectivePriceTierResponsesApplyMultiplierWithCeilingRounding(t *testing.T) {
	tiers := []ledger.PriceTier{{
		Name: "peak", Timezone: "UTC",
		InputPrice: money.FromNano(1_500_000_000), OutputPrice: money.FromNano(4_500_000_000),
		CacheWritePrice: money.FromNano(1_000_000_000), CacheReadPrice: money.FromNano(100_000_000),
	}}
	responses := effectivePriceTierResponses(money.FromNano(money.Scale), tiers)
	if len(responses) != 1 {
		t.Fatalf("responses = %#v", responses)
	}
	if responses[0]["input_price"] != "1.5" || responses[0]["output_price"] != "4.5" {
		t.Fatalf("effective prices = %#v", responses[0])
	}
	doubled := effectivePriceTierResponses(money.FromNano(2*money.Scale), tiers)
	if doubled[0]["input_price"] != "3" || doubled[0]["cache_read_price"] != "0.2" {
		t.Fatalf("multiplied prices = %#v", doubled[0])
	}
}
