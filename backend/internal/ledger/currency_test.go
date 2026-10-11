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
		discount   bool
		multiplier int64
		want       string
	}{
		{"usd without discount pays the converted price", CurrencyUSD, false, FixedPointScale, "6.8"},
		{"usd with discount pays the dollar figure", CurrencyUSD, true, FixedPointScale, "1"},
		{"discount keeps the relay multiplier", CurrencyUSD, true, 130_000_000, "0.13"},
		{"cny ignores the discount", CurrencyCNY, true, FixedPointScale, "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := CalculatePriceAt(usage, prices, nil, pricingTime, tc.multiplier, ChannelRate(tc.currency, tc.discount, rate), 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := result.Cost.String(); got != tc.want {
				t.Fatalf("cost = %s, want %s", got, tc.want)
			}
			shown := ScalePriceAt(prices.InputPerMillion, tc.multiplier, ChannelRate(tc.currency, tc.discount, rate))
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

func TestEffectiveMultiplier(t *testing.T) {
	const rate = 6_800_000_000
	if got := EffectiveMultiplier(130_000_000, ChannelRate(CurrencyUSD, true, rate), ModelRate(CurrencyUSD, rate)); got != 19_117_647 {
		t.Fatalf("effective = %d, want 19117647 (0.13 / 6.8)", got)
	}
	if got := EffectiveMultiplier(130_000_000, ChannelRate(CurrencyUSD, false, rate), ModelRate(CurrencyUSD, rate)); got != 130_000_000 {
		t.Fatalf("effective without discount = %d", got)
	}
	if got := EffectiveMultiplier(2_000_000_000, ChannelRate(CurrencyCNY, true, rate), ModelRate(CurrencyCNY, rate)); got != 2_000_000_000 {
		t.Fatalf("cny effective = %d", got)
	}
}
