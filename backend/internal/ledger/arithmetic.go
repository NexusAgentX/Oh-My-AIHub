package ledger

import (
	"math"
	"math/big"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func Add(left, right money.Amount) (money.Amount, error) {
	a, b := left.Nano(), right.Nano()
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, ErrAmountOverflow
	}
	return money.FromNano(a + b), nil
}

func Subtract(left, right money.Amount) (money.Amount, error) {
	a, b := left.Nano(), right.Nano()
	if (b > 0 && a < math.MinInt64+b) || (b < 0 && a > math.MaxInt64+b) {
		return 0, ErrAmountOverflow
	}
	return money.FromNano(a - b), nil
}

func Negate(value money.Amount) (money.Amount, error) {
	if value.Nano() == math.MinInt64 {
		return 0, ErrAmountOverflow
	}
	return money.FromNano(-value.Nano()), nil
}

func SpendableCapacity(postedBalance, effectiveCredit, assetReserved, spendAuthorized money.Amount) money.Amount {
	capacity := big.NewInt(postedBalance.Nano())
	capacity.Add(capacity, big.NewInt(effectiveCredit.Nano()))
	capacity.Sub(capacity, big.NewInt(assetReserved.Nano()))
	capacity.Sub(capacity, big.NewInt(spendAuthorized.Nano()))
	if capacity.Sign() <= 0 {
		return 0
	}
	if !capacity.IsInt64() {
		return money.FromNano(math.MaxInt64)
	}
	return money.FromNano(capacity.Int64())
}

func IsOverLimit(postedBalance, effectiveCredit, assetReserved, spendAuthorized money.Amount) bool {
	capacity := big.NewInt(postedBalance.Nano())
	capacity.Add(capacity, big.NewInt(effectiveCredit.Nano()))
	capacity.Sub(capacity, big.NewInt(assetReserved.Nano()))
	capacity.Sub(capacity, big.NewInt(spendAuthorized.Nano()))
	return capacity.Sign() < 0
}
