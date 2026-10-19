package ledger

import (
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestUSDModelPricing(t *testing.T) {
	const rate = 6_800_000_000 // 6.8 CNY per USD
	usage := Usage{InputTokens: 1_000_000}
	prices := Prices{InputPerMillion: mustPriceAmount(t, "1")} // $1 per million tokens
	cases := []struct {
		name       string
		currency   string
		multiplier int64
		want       string
	}{
		{"usd at the converted price", CurrencyUSD, FixedPointScale, "6.8"},
		{"usd with a channel discount", CurrencyUSD, 130_000_000, "0.884"},
		{"cny at the point price", CurrencyCNY, FixedPointScale, "1"},
		{"cny with a channel discount", CurrencyCNY, 130_000_000, "0.13"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := CalculatePriceAt(usage, prices, nil, pricingTime, tc.multiplier, ModelRate(tc.currency, rate), 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := result.Cost.String(); got != tc.want {
				t.Fatalf("cost = %s, want %s", got, tc.want)
			}
			shown := ScalePriceAt(prices.InputPerMillion, tc.multiplier, ModelRate(tc.currency, rate))
			if shown.String() != tc.want {
				t.Fatalf("display price = %s, want %s", shown, tc.want)
			}
		})
	}
}

func TestUSDModelRoundsUpOnce(t *testing.T) {
	// One million tokens at 1 nano-dollar per million × 6.8 = 6.8 nano points:
	// one rounding on the converted total.
	result, err := CalculatePriceAt(Usage{InputTokens: 1_000_000}, Prices{InputPerMillion: money.FromNano(1)}, nil, pricingTime, FixedPointScale, 6_800_000_000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cost != money.FromNano(7) {
		t.Fatalf("cost = %d nano, want 7", result.Cost.Nano())
	}
}
