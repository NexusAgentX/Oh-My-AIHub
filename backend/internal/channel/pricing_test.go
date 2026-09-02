package channel

import (
	"errors"
	"math"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestCalculateBenchmarkPricesPreservesMaximumCatalogPriceAtMaximumMultiplier(t *testing.T) {
	offer := Offer{
		Multiplier: money.FromNano(maxMultiplierNano),
		InputPrice: catalog.MaxPriceNanoPerMillion, OutputPrice: catalog.MaxPriceNanoPerMillion,
		CacheWritePrice: catalog.MaxPriceNanoPerMillion, CacheReadPrice: catalog.MaxPriceNanoPerMillion,
	}
	prices, err := CalculateBenchmarkPrices(offer)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]money.Amount{
		"input": prices.Input, "output": prices.Output, "cache write": prices.CacheWrite, "cache read": prices.CacheRead,
	} {
		if value.String() != "100000000" {
			t.Fatalf("%s maximum benchmark price = %s", name, value.String())
		}
	}
}

func TestCalculateBenchmarkPricesDoesNotTurnOverflowIntoFreeUsage(t *testing.T) {
	_, err := CalculateBenchmarkPrices(Offer{
		Multiplier: money.FromNano(maxMultiplierNano),
		InputPrice: money.FromNano(math.MaxInt64),
	})
	if !errors.Is(err, ledger.ErrAmountOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
}
