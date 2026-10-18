package ledger

import (
	"math/big"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// CalculateUnitComponent preserves the exact cost until token, image and tool
// components are combined. Quantities, discount and currency rate use 1e9.
func CalculateUnitComponent(quantityNano int64, price money.Amount, discountNano, rateNano int64) (PriceResult, error) {
	if quantityNano < 0 || price < 0 || discountNano < 0 || rateNano < 0 {
		return PriceResult{}, ErrInvalidInput
	}
	numerator := new(big.Int).Mul(big.NewInt(quantityNano), big.NewInt(price.Nano()))
	numerator.Mul(numerator, big.NewInt(discountNano))
	numerator.Mul(numerator, big.NewInt(rateNano))
	denominator := new(big.Int).Exp(big.NewInt(FixedPointScale), big.NewInt(3), nil)
	value, err := ceilNonNegative(numerator, denominator)
	if err != nil {
		return PriceResult{}, err
	}
	return PriceResult{Cost: money.FromNano(value), exact: new(big.Rat).SetFrac(numerator, denominator)}, nil
}
