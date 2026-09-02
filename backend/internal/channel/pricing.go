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
		result, err := ledger.CalculatePriceV1(
			ledger.UsageV1{InputTokens: 1_000_000},
			ledger.OfficialPricesV1{InputPerMillion: price},
			offer.Multiplier.Nano(), 0, true,
		)
		if err != nil {
			return 0, err
		}
		return result.ProviderCharge, nil
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
