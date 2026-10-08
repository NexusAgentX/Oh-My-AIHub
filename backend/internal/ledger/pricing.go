package ledger

import (
	"math"
	"math/big"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// FixedPointScale is the nano scale of multipliers and fee rates (1e9 = 1x / 100%).
const FixedPointScale int64 = 1_000_000_000

// Usage is the four normalized token buckets of one call.
type Usage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheWriteTokens int64
	CacheReadTokens  int64
}

// Prices are four per-million-token prices in nano-points.
type Prices struct {
	InputPerMillion      money.Amount
	OutputPerMillion     money.Amount
	CacheWritePerMillion money.Amount
	CacheReadPerMillion  money.Amount
}

// PriceResult is the settlement of one call: the channel cost paid to the
// provider, the platform fee on top of it, and the tier that won selection
// (0 meaning the model's base prices).
type PriceResult struct {
	Cost    money.Amount
	Fee     money.Amount
	TierSeq int
}

// CalculatePriceV2 prices one call under formula v2 (ADR-0012): it selects the
// first conditional tier whose predicates match the prompt-side token volume
// and the call start time, charges all four usage buckets at that tier's
// prices times the channel multiplier, and rounds the cost and the fee up to
// a whole nano separately.
func CalculatePriceV2(usage Usage, basePrices Prices, tiers []PriceTier, at time.Time, multiplierNano, feeRateNano int64) (PriceResult, error) {
	promptTokens, err := PromptSideTokens(usage)
	if err != nil {
		return PriceResult{}, err
	}
	prices, tierSeq := SelectPriceTier(basePrices, tiers, promptTokens, at)
	usageValues := []int64{usage.InputTokens, usage.OutputTokens, usage.CacheWriteTokens, usage.CacheReadTokens}
	priceValues := []money.Amount{prices.InputPerMillion, prices.OutputPerMillion, prices.CacheWritePerMillion, prices.CacheReadPerMillion}
	if multiplierNano < 0 || feeRateNano < 0 || feeRateNano > FixedPointScale {
		return PriceResult{}, ErrInvalidInput
	}

	weightedUsage := new(big.Int)
	for index, tokenCount := range usageValues {
		if tokenCount < 0 || priceValues[index] < 0 {
			return PriceResult{}, ErrInvalidInput
		}
		weightedUsage.Add(weightedUsage, new(big.Int).Mul(big.NewInt(tokenCount), big.NewInt(priceValues[index].Nano())))
	}
	cost, err := ceilNonNegative(
		new(big.Int).Mul(weightedUsage, big.NewInt(multiplierNano)),
		new(big.Int).Mul(big.NewInt(1_000_000), big.NewInt(FixedPointScale)),
	)
	if err != nil {
		return PriceResult{}, err
	}
	fee := int64(0)
	if cost > 0 && feeRateNano > 0 {
		fee, err = ceilNonNegative(new(big.Int).Mul(big.NewInt(cost), big.NewInt(feeRateNano)), big.NewInt(FixedPointScale))
		if err != nil {
			return PriceResult{}, err
		}
	}
	if cost > math.MaxInt64-fee {
		return PriceResult{}, ErrAmountOverflow
	}
	return PriceResult{Cost: money.FromNano(cost), Fee: money.FromNano(fee), TierSeq: tierSeq}, nil
}

func ceilNonNegative(numerator, denominator *big.Int) (int64, error) {
	if numerator.Sign() < 0 || denominator.Sign() <= 0 {
		return 0, ErrInvalidInput
	}
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, ErrAmountOverflow
	}
	return quotient.Int64(), nil
}
