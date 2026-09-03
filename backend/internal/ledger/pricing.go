package ledger

import (
	"math"
	"math/big"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

const FixedPointScale int64 = 1_000_000_000

type UsageV1 struct {
	InputTokens      int64
	OutputTokens     int64
	CacheWriteTokens int64
	CacheReadTokens  int64
}

type OfficialPricesV1 struct {
	InputPerMillion      money.Amount
	OutputPerMillion     money.Amount
	CacheWritePerMillion money.Amount
	CacheReadPerMillion  money.Amount
}

type PriceV1Result struct {
	ProviderCharge money.Amount
	PlatformFee    money.Amount
}

// PriceV2Result extends the v1 settlement with the tier sequence that won
// selection, 0 meaning the model's unconditional prices.
type PriceV2Result struct {
	PriceV1Result
	TierSeq int
}

func CalculatePriceV1(usage UsageV1, prices OfficialPricesV1, multiplierNano, feeRateNano int64, selfChannel bool) (PriceV1Result, error) {
	return calculateWeightedUsage(usage, prices, multiplierNano, feeRateNano, selfChannel)
}

// CalculatePriceV2 prices one call under formula-v2: it selects the first
// conditional tier whose predicates match the prompt-side token volume and the
// call start time, then charges the whole request (all four usage buckets)
// with that tier's prices. Models without conditional tiers settle to exactly
// the CalculatePriceV1 amounts.
func CalculatePriceV2(usage UsageV1, defaultPrices OfficialPricesV1, tiers []PriceTier, at time.Time, multiplierNano, feeRateNano int64, selfChannel bool) (PriceV2Result, error) {
	promptTokens, err := PromptSideTokens(usage)
	if err != nil {
		return PriceV2Result{}, err
	}
	prices, tierSeq := SelectPriceTier(defaultPrices, tiers, promptTokens, at)
	priced, err := calculateWeightedUsage(usage, prices, multiplierNano, feeRateNano, selfChannel)
	if err != nil {
		return PriceV2Result{}, err
	}
	return PriceV2Result{PriceV1Result: priced, TierSeq: tierSeq}, nil
}

func calculateWeightedUsage(usage UsageV1, prices OfficialPricesV1, multiplierNano, feeRateNano int64, selfChannel bool) (PriceV1Result, error) {
	usageValues := []int64{usage.InputTokens, usage.OutputTokens, usage.CacheWriteTokens, usage.CacheReadTokens}
	priceValues := []money.Amount{prices.InputPerMillion, prices.OutputPerMillion, prices.CacheWritePerMillion, prices.CacheReadPerMillion}
	if multiplierNano < 0 || feeRateNano < 0 || feeRateNano > FixedPointScale {
		return PriceV1Result{}, ErrInvalidInput
	}

	weightedUsage := new(big.Int)
	for index, tokenCount := range usageValues {
		if tokenCount < 0 || priceValues[index] < 0 {
			return PriceV1Result{}, ErrInvalidInput
		}
		term := new(big.Int).Mul(big.NewInt(tokenCount), big.NewInt(priceValues[index].Nano()))
		weightedUsage.Add(weightedUsage, term)
	}

	providerNumerator := new(big.Int).Mul(weightedUsage, big.NewInt(multiplierNano))
	providerDenominator := new(big.Int).Mul(big.NewInt(1_000_000), big.NewInt(FixedPointScale))
	providerCharge, err := ceilNonNegative(providerNumerator, providerDenominator)
	if err != nil {
		return PriceV1Result{}, err
	}

	fee := int64(0)
	if !selfChannel && providerCharge > 0 && feeRateNano > 0 {
		fee, err = ceilNonNegative(
			new(big.Int).Mul(big.NewInt(providerCharge), big.NewInt(feeRateNano)),
			big.NewInt(FixedPointScale),
		)
		if err != nil {
			return PriceV1Result{}, err
		}
	}

	return PriceV1Result{
		ProviderCharge: money.FromNano(providerCharge),
		PlatformFee:    money.FromNano(fee),
	}, nil
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
	if !quotient.IsInt64() || quotient.Cmp(big.NewInt(math.MaxInt64)) > 0 {
		return 0, ErrAmountOverflow
	}
	return quotient.Int64(), nil
}
