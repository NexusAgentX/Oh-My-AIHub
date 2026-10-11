package ledger

import (
	"math"
	"math/big"
)

// Currencies of catalog base prices. Points are advertised as 1 point ≈ 1 CNY,
// so a CNY model's base prices are its point prices; a USD model's base prices
// are converted at the platform USD/CNY rate when they are charged or shown.
const (
	CurrencyCNY = "CNY"
	CurrencyUSD = "USD"
)

// UnitRate is the identity conversion rate (1e9 = 1).
const UnitRate = FixedPointScale

// ValidCurrency reports whether currency is a supported base-price currency.
func ValidCurrency(currency string) bool {
	return currency == CurrencyCNY || currency == CurrencyUSD
}

// ModelRate converts a model's base prices into points: the USD/CNY rate for
// USD models and 1 for CNY models.
func ModelRate(currency string, usdRateNano int64) int64 {
	if currency == CurrencyUSD {
		return usdRateNano
	}
	return UnitRate
}

// ChannelRate is the conversion a channel's multiplier is applied with. A
// channel with the FX discount sells a USD model at "USD price × multiplier"
// points, skipping the exchange rate, the way upstream relays sell one USD of
// quota for one CNY; it has no effect on CNY models.
func ChannelRate(currency string, fxDiscount bool, usdRateNano int64) int64 {
	if currency == CurrencyUSD && fxDiscount {
		return UnitRate
	}
	return ModelRate(currency, usdRateNano)
}

// EffectiveMultiplier is the multiplier the channel really applies to the
// model's point price: the set multiplier, divided by the exchange rate when
// the FX discount skips it. It is for display and truncates below one nano.
func EffectiveMultiplier(multiplierNano, channelRateNano, modelRateNano int64) int64 {
	if modelRateNano <= 0 {
		return multiplierNano
	}
	value := new(big.Int).Mul(big.NewInt(multiplierNano), big.NewInt(channelRateNano))
	value.Quo(value, big.NewInt(modelRateNano))
	if !value.IsInt64() {
		return math.MaxInt64
	}
	return value.Int64()
}
