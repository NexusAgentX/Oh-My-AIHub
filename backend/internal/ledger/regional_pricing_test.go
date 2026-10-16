package ledger

import (
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestRegionalPricesUseWholeTierGroupAndAllTokenBuckets(t *testing.T) {
	for _, tc := range []struct {
		name, region string
		base, tier   map[string]int64
		useTier      bool
		want         string
	}{
		{"unknown", "", map[string]int64{"us": 1_100_000_000}, nil, false, "10"},
		{"unconfigured", "eu", map[string]int64{"us": 1_100_000_000}, nil, false, "10"},
		{"base", "us", map[string]int64{"us": 1_100_000_000}, nil, false, "11"},
		{"inherit whole group", "us", map[string]int64{"us": 1_100_000_000}, nil, true, "11"},
		{"replace whole group", "eu", map[string]int64{"eu": 1_100_000_000}, map[string]int64{"us": 1_200_000_000}, true, "10"},
		{"override", "us", map[string]int64{"us": 1_100_000_000}, map[string]int64{"us": 1_200_000_000}, true, "12"},
		{"explicit free", "us", map[string]int64{"us": 0}, nil, false, "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prices := Prices{InputPerMillion: money.FromNano(1e9), OutputPerMillion: money.FromNano(2e9), CacheWritePerMillion: money.FromNano(3e9), CacheReadPerMillion: money.FromNano(4e9), GeoMultipliers: tc.base}
			var tiers []PriceTier
			if tc.useTier {
				tiers = []PriceTier{{ServiceTier: "priority", InputPrice: prices.InputPerMillion, OutputPrice: prices.OutputPerMillion, CacheWritePrice: prices.CacheWritePerMillion, CacheReadPrice: prices.CacheReadPerMillion, GeoMultipliers: tc.tier}}
			}
			u := Usage{InputTokens: 1e6, OutputTokens: 1e6, CacheWriteTokens: 1e6, CacheReadTokens: 1e6, Detail: &UsageDetail{InferenceGeo: tc.region, ServiceTier: "priority"}}
			got, err := CalculatePriceAt(u, prices, tiers, pricingTime, FixedPointScale, UnitRate, 1_000_000)
			if err != nil || got.Cost.String() != tc.want {
				t.Fatalf("cost=%s want=%s err=%v", got.Cost.String(), tc.want, err)
			}
			var parts []PriceResult
			for range 2 {
				part, err := CalculatePriceComponentAt(u, prices, tiers, pricingTime, FixedPointScale, UnitRate)
				if err != nil {
					t.Fatal(err)
				}
				parts = append(parts, part)
			}
			combined, err := CombinePrices(parts, 1_000_000)
			if err != nil || combined.Cost != got.Cost*2 || combined.Fee != got.Fee*2 {
				t.Fatalf("component multiplier or fees applied twice: single=%+v combined=%+v err=%v", got, combined, err)
			}
		})
	}
}
