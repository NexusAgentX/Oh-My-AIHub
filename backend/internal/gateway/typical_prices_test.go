package gateway

import (
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

type routingPriceFixture struct{ cost money.Amount }

func (f routingPriceFixture) LookupComparison(_ string, model, group, version string, discount, fx int64) (money.Amount, string, int64, bool) {
	return f.cost, "tokens", 1_000_000, model == "model" && group == "group" && version == "v1" && discount == 800_000_000 && fx == 7_000_000_000
}

func TestRoutingPricesTakeOneImmutableGenerationAndRejectStaleSelectors(t *testing.T) {
	reads := 0
	e := NewEngine(Dependencies{TypicalPrices: func() TypicalPriceLookup { reads++; return routingPriceFixture{cost: money.Amount(reads * 100)} }})
	pool := []Candidate{{ChannelID: "a", GroupID: "group", PricingVersion: "v1", MultiplierNano: 800_000_000}, {ChannelID: "b", GroupID: "group", PricingVersion: "v1", MultiplierNano: 800_000_000}, {ChannelID: "c", GroupID: "group", PricingVersion: "v0", MultiplierNano: 800_000_000}, {ChannelID: "d", GroupID: "group", PricingVersion: "v1", MultiplierNano: 700_000_000}}
	e.applyTypicalPrices(pool, "model", 7_000_000_000)
	if reads != 1 || !pool[0].TypicalCostKnown || !pool[1].TypicalCostKnown || pool[0].TypicalCost != 100 || pool[1].TypicalCost != 100 || pool[2].TypicalCostKnown || pool[3].TypicalCostKnown {
		t.Fatal("mixed generation or stale config price", reads, pool)
	}
	e.applyTypicalPrices(pool, "model", 6_800_000_000)
	for _, row := range pool {
		if row.TypicalCostKnown {
			t.Fatal("old FX estimate became a current price")
		}
	}
}

func TestRoutingDoesNotCompareTokenAndPerRequestDenominators(t *testing.T) {
	pool := []Candidate{{ChannelID: "a", GroupID: "group", OwnerID: "provider", TypicalCostKnown: true, TypicalCost: 100, TypicalUnit: "tokens", TypicalQuantity: 1_000_000}, {ChannelID: "b", GroupID: "group", OwnerID: "provider", TypicalCostKnown: true, TypicalCost: 1, TypicalUnit: "request", TypicalQuantity: 1}, {ChannelID: "c", GroupID: "group", OwnerID: "consumer"}}
	if got := ids(Rank(RankInput{Mode: routing.ModeCheapest, Candidates: pool, ConsumerID: "consumer"})); got != "cab" {
		t.Fatal("non-comparable amounts were treated as cheaper, or self net zero was lost", got)
	}
}
