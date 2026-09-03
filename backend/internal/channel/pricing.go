package channel

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type BenchmarkPrices struct {
	Input      money.Amount
	Output     money.Amount
	CacheWrite money.Amount
	CacheRead  money.Amount
}

func CalculateBenchmarkPrices(offer Offer) (BenchmarkPrices, error) {
	calculate := func(price money.Amount) (money.Amount, error) {
		return EffectivePrice(offer.Multiplier, price)
	}

	values := make([]money.Amount, 4)
	for index, input := range []money.Amount{offer.InputPrice, offer.OutputPrice, offer.CacheWritePrice, offer.CacheReadPrice} {
		value, err := calculate(input)
		if err != nil {
			return BenchmarkPrices{}, err
		}
		values[index] = value
	}
	return BenchmarkPrices{Input: values[0], Output: values[1], CacheWrite: values[2], CacheRead: values[3]}, nil
}

// EffectivePrice applies the channel multiplier to one per-million benchmark
// price with the same ceiling rounding used everywhere else a display price
// is derived from catalog prices.
func EffectivePrice(multiplier, price money.Amount) (money.Amount, error) {
	result, err := ledger.CalculatePriceV1(
		ledger.UsageV1{InputTokens: 1_000_000},
		ledger.OfficialPricesV1{InputPerMillion: price},
		multiplier.Nano(), 0, true,
	)
	if err != nil {
		return 0, err
	}
	return result.ProviderCharge, nil
}

// EffectivePriceTiers keeps every tier predicate and replaces the four
// benchmark prices with multiplier-applied prices, so tier-aware eligibility
// bounds and display projections share one rounding rule.
func EffectivePriceTiers(multiplier money.Amount, tiers []ledger.PriceTier) ([]ledger.PriceTier, error) {
	result := make([]ledger.PriceTier, 0, len(tiers))
	for _, tier := range tiers {
		var err error
		if tier.InputPrice, err = EffectivePrice(multiplier, tier.InputPrice); err != nil {
			return nil, err
		}
		if tier.OutputPrice, err = EffectivePrice(multiplier, tier.OutputPrice); err != nil {
			return nil, err
		}
		if tier.CacheWritePrice, err = EffectivePrice(multiplier, tier.CacheWritePrice); err != nil {
			return nil, err
		}
		if tier.CacheReadPrice, err = EffectivePrice(multiplier, tier.CacheReadPrice); err != nil {
			return nil, err
		}
		result = append(result, tier)
	}
	return result, nil
}
