package gateway

import (
	"math"
	"math/big"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func ConservativeNetDebitUpperBound(lease channel.RoutingLease, feeRateNano int64, selfChannel bool) (money.Amount, error) {
	if lease.ContextWindow <= 0 || lease.ContextWindow > math.MaxInt64/2 || feeRateNano < 0 || feeRateNano > ledger.FixedPointScale {
		return 0, ErrInvalidInput
	}
	bound, err := conservativeBoundForPrices(lease.ContextWindow, OfficialPrices(lease), lease.Multiplier.Nano(), feeRateNano, selfChannel)
	if err != nil {
		return 0, err
	}
	// Tiered models may settle on any conditional tier, so the authorization
	// must dominate the worst tier as well as the default prices.
	for _, tier := range lease.PriceTiers {
		tierBound, tierErr := conservativeBoundForPrices(lease.ContextWindow, tier.Prices(), lease.Multiplier.Nano(), feeRateNano, selfChannel)
		if tierErr != nil {
			return 0, tierErr
		}
		if tierBound > bound {
			bound = tierBound
		}
	}
	return bound, nil
}

func conservativeBoundForPrices(contextWindow int64, prices ledger.OfficialPricesV1, multiplierNano, feeRateNano int64, selfChannel bool) (money.Amount, error) {
	priceValues := []money.Amount{prices.InputPerMillion, prices.OutputPerMillion, prices.CacheWritePerMillion, prices.CacheReadPerMillion}
	maxIndex := 0
	for index := 1; index < len(priceValues); index++ {
		if priceValues[index] > priceValues[maxIndex] {
			maxIndex = index
		}
	}
	usage := ledger.UsageV1{}
	// One request can consume up to one context window of input/cache tokens and
	// still produce up to one context window of output tokens. Keeping this
	// limit identical to ValidateUsage makes the authorization a real upper
	// bound instead of a post-paid validation trap.
	maximumBillableTokens := contextWindow * 2
	switch maxIndex {
	case 0:
		usage.InputTokens = maximumBillableTokens
	case 1:
		usage.OutputTokens = maximumBillableTokens
	case 2:
		usage.CacheWriteTokens = maximumBillableTokens
	case 3:
		usage.CacheReadTokens = maximumBillableTokens
	}
	priced, err := ledger.CalculatePriceV1(usage, prices, multiplierNano, feeRateNano, selfChannel)
	if err != nil {
		return 0, err
	}
	if selfChannel {
		// Self use has zero net debit and no platform fee, but its provider-side
		// usage fact still records the nominal charge. Validate that amount now so
		// an overflowing self candidate never reaches an upstream before failing
		// settlement.
		return 0, nil
	}
	return ledger.Add(priced.ProviderCharge, priced.PlatformFee)
}

func OfficialPrices(lease channel.RoutingLease) ledger.OfficialPricesV1 {
	return ledger.OfficialPricesV1{
		InputPerMillion: lease.InputPrice, OutputPerMillion: lease.OutputPrice,
		CacheWritePerMillion: lease.CacheWritePrice, CacheReadPerMillion: lease.CacheReadPrice,
	}
}

func ValidateUsage(usage ledger.UsageV1, contextWindow int64) error {
	if contextWindow <= 0 || contextWindow > math.MaxInt64/2 || usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheWriteTokens < 0 || usage.CacheReadTokens < 0 {
		return ErrNoUsage
	}
	total := new(big.Int)
	for _, value := range []int64{usage.InputTokens, usage.OutputTokens, usage.CacheWriteTokens, usage.CacheReadTokens} {
		total.Add(total, big.NewInt(value))
	}
	maximumBillableTokens := new(big.Int).Mul(big.NewInt(contextWindow), big.NewInt(2))
	if total.Cmp(maximumBillableTokens) > 0 {
		return ErrNoUsage
	}
	return nil
}
