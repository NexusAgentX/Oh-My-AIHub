package ledger

// Currencies of model quotes. Points are advertised as 1 point ≈ 1 CNY,
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
