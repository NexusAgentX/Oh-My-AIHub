package catalog

import (
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func tieredModel(tiers []ledger.PriceTier) Model {
	return Model{
		ID: "provider/model", Name: "Test Model", Provider: "Provider", ContextWindow: 1,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"}, Status: StatusActive,
		PriceTiers: tiers,
	}
}

func validTier() ledger.PriceTier {
	minPrompt := int64(200_000)
	return ledger.PriceTier{
		Name: "long-context", MinPromptTokens: &minPrompt, Timezone: "UTC",
		InputPrice: money.FromNano(2_000_000_000), OutputPrice: money.FromNano(15_000_000_000),
	}
}

func TestValidateModelPriceTiersAcceptsRealisticSchedules(t *testing.T) {
	tier := validTier()
	model := tieredModel([]ledger.PriceTier{tier})
	if err := validate(normalize(model)); err != nil {
		t.Fatalf("realistic tier rejected: %v", err)
	}

	morning := ledger.PriceTier{
		Timezone: "Asia/Shanghai", Weekdays: []int{1, 2, 3, 4, 5},
		StartMinute: int16Pointer(540), EndMinute: int16Pointer(720),
		InputPrice: money.FromNano(3_000_000_000), OutputPrice: money.FromNano(9_000_000_000),
	}
	afternoon := morning
	afternoon.StartMinute, afternoon.EndMinute = int16Pointer(840), int16Pointer(1080)
	model = tieredModel([]ledger.PriceTier{morning, afternoon})
	if err := validate(normalize(model)); err != nil {
		t.Fatalf("DeepSeek-style peak schedule rejected: %v", err)
	}
}

func TestValidateModelPriceTiersRejectsInvalidShapes(t *testing.T) {
	cases := map[string]func(*ledger.PriceTier){
		"no predicate":       func(tier *ledger.PriceTier) { tier.MinPromptTokens = nil },
		"inverted range":     func(tier *ledger.PriceTier) { max := int64(1); tier.MaxPromptTokens = &max },
		"negative minimum":   func(tier *ledger.PriceTier) { min := int64(-1); tier.MinPromptTokens = &min },
		"start without end":  func(tier *ledger.PriceTier) { tier.StartMinute = int16Pointer(60) },
		"zero-length window": func(tier *ledger.PriceTier) { tier.StartMinute, tier.EndMinute = int16Pointer(60), int16Pointer(60) },
		"start out of range": func(tier *ledger.PriceTier) { tier.StartMinute, tier.EndMinute = int16Pointer(1440), int16Pointer(10) },
		"end out of range":   func(tier *ledger.PriceTier) { tier.StartMinute, tier.EndMinute = int16Pointer(60), int16Pointer(1441) },
		"weekday zero":       func(tier *ledger.PriceTier) { tier.Weekdays = []int{0} },
		"weekday eight":      func(tier *ledger.PriceTier) { tier.Weekdays = []int{8} },
		"invalid timezone":   func(tier *ledger.PriceTier) { tier.Timezone = "Not/AZone"; tier.StartMinute, tier.EndMinute = int16Pointer(60), int16Pointer(120) },
		"name too long":      func(tier *ledger.PriceTier) { tier.Name = string(make([]byte, 65)) },
	}
	for note, mutate := range cases {
		tier := validTier()
		mutate(&tier)
		if err := validate(tieredModel([]ledger.PriceTier{tier})); err == nil {
			t.Fatalf("%s: tier accepted", note)
		}
	}

	tier := validTier()
	tier.InputPrice = money.FromNano(MaxPriceNanoPerMillion.Nano() + 1)
	if err := validate(tieredModel([]ledger.PriceTier{tier})); err == nil {
		t.Fatal("tier price above the catalog ceiling accepted")
	}

	tiers := make([]ledger.PriceTier, MaxPriceTiers+1)
	for index := range tiers {
		tiers[index] = validTier()
	}
	if err := validate(tieredModel(tiers)); err == nil {
		t.Fatal("more tiers than the storage bound accepted")
	}
}

func TestNormalizePriceTiersDefaultsTimezoneAndDeduplicatesWeekdays(t *testing.T) {
	tier := validTier()
	tier.Timezone = ""
	tier.Weekdays = []int{3, 3, 1, 7, 7}
	model := normalize(tieredModel([]ledger.PriceTier{tier}))
	normalized := model.PriceTiers[0]
	if normalized.Timezone != "UTC" {
		t.Fatalf("timezone default = %q, want UTC", normalized.Timezone)
	}
	if len(normalized.Weekdays) != 3 || normalized.Weekdays[0] != 1 || normalized.Weekdays[1] != 3 || normalized.Weekdays[2] != 7 {
		t.Fatalf("weekdays not deduplicated and sorted: %v", normalized.Weekdays)
	}
	if err := validate(model); err != nil {
		t.Fatalf("normalized tier rejected: %v", err)
	}
}

func int16Pointer(value int16) *int16 { return &value }
