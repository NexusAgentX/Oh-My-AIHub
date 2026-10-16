package ledger

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"testing"
)

func TestObservedRegionDoesNotChangeAnyTokenBucketOrComponent(t *testing.T) {
	for _, region := range []string{"", "us", "eu", "unknown"} {
		t.Run(region, func(t *testing.T) {
			prices := Prices{InputPerMillion: money.FromNano(1e9), OutputPerMillion: money.FromNano(2e9), CacheWritePerMillion: money.FromNano(3e9), CacheReadPerMillion: money.FromNano(4e9)}
			usage := Usage{InputTokens: 1e6, OutputTokens: 1e6, CacheWriteTokens: 1e6, CacheReadTokens: 1e6, Detail: &UsageDetail{InferenceGeo: region}}
			got, err := CalculatePriceAt(usage, prices, nil, pricingTime, FixedPointScale, UnitRate, 1_000_000)
			if err != nil || got.Cost.String() != "10" || got.Fee.String() != "0.01" {
				t.Fatalf("price=%+v err=%v", got, err)
			}
			var parts []PriceResult
			for range 2 {
				part, err := CalculatePriceComponentAt(usage, prices, nil, pricingTime, FixedPointScale, UnitRate)
				if err != nil {
					t.Fatal(err)
				}
				parts = append(parts, part)
			}
			combined, err := CombinePrices(parts, 1_000_000)
			if err != nil || combined.Cost != got.Cost*2 || combined.Fee != got.Fee*2 {
				t.Fatalf("combined=%+v err=%v", combined, err)
			}
		})
	}
}
