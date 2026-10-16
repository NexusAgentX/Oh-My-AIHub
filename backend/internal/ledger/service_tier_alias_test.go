package ledger

import "testing"

func TestServiceTierAliasesAffectSelectionWithoutChangingObservedFacts(t *testing.T) {
	tiers := []PriceTier{
		{ServiceTier: "openai:fast", InputPrice: 3},
		{ServiceTier: "openai:ultrafast", InputPrice: 5},
		{ServiceTier: "anthropic:priority", InputPrice: 7},
		{ServiceTier: "gemini:priority", InputPrice: 9},
	}
	for _, tc := range []struct {
		actual string
		seq    int
		cost   int64
	}{
		{"openai:priority", 1, 3}, {"openai:fast", 1, 3},
		{"openai:ultrafast", 2, 5}, {"anthropic:priority", 3, 7},
		{"gemini:priority", 4, 9}, {"openai:default", 0, 1}, {"", 0, 1},
	} {
		t.Run(tc.actual, func(t *testing.T) {
			detail := &UsageDetail{ServiceTier: tc.actual}
			got, err := CalculatePriceAt(Usage{InputTokens: 1_000_000, Detail: detail}, Prices{InputPerMillion: 1}, tiers, pricingTime, FixedPointScale, UnitRate, 0)
			if err != nil || got.TierSeq != tc.seq || got.Cost.Nano() != tc.cost {
				t.Fatalf("price=%+v err=%v", got, err)
			}
			if detail.ServiceTier != tc.actual {
				t.Fatal("pricing rewrote observed service tier")
			}
		})
	}
	minimum := int64(100)
	if (PriceTier{ServiceTier: "openai:fast", MinPromptTokens: &minimum}).Matches(99, pricingTime, &UsageDetail{ServiceTier: "openai:priority"}) {
		t.Fatal("priority alias bypassed the token predicate")
	}
	if (PriceTier{ServiceTier: "openai:fast"}).Matches(100, pricingTime) {
		t.Fatal("unknown service tier matched fast")
	}
}
