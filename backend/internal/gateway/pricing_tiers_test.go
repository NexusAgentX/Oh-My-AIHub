package gateway

import (
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func tierInt64(value int64) *int64 { return &value }

func TestConservativeNetDebitUpperBoundCoversEveryTier(t *testing.T) {
	lease := channel.RoutingLease{
		ContextWindow: 1_000, Multiplier: money.FromNano(ledger.FixedPointScale),
		InputPrice: mustPriceAmount(t, "1"), OutputPrice: mustPriceAmount(t, "1"),
		CacheWritePrice: mustPriceAmount(t, "1"), CacheReadPrice: mustPriceAmount(t, "1"),
		PriceTiers: []ledger.PriceTier{{
			MinPromptTokens: tierInt64(200_000),
			InputPrice:      mustPriceAmount(t, "1"), OutputPrice: mustPriceAmount(t, "1000"),
			CacheWritePrice: mustPriceAmount(t, "1"), CacheReadPrice: mustPriceAmount(t, "1"),
		}},
	}
	bound, err := ConservativeNetDebitUpperBound(lease, 0, false)
	if err != nil {
		t.Fatalf("ConservativeNetDebitUpperBound: %v", err)
	}
	// The worst tier bills up to 2x context window tokens at 1000 per million:
	// 2_000 * 1000 / 1_000_000 = 2 credits. The default prices only reach
	// 0.002, so the authorization must be dominated by the tier.
	if bound != mustPriceAmount(t, "2") {
		t.Fatalf("tier-aware bound = %s, want 2", bound.String())
	}
	withoutTiers := lease
	withoutTiers.PriceTiers = nil
	shrunk, err := ConservativeNetDebitUpperBound(withoutTiers, 0, false)
	if err != nil {
		t.Fatalf("default-only bound: %v", err)
	}
	if shrunk >= bound {
		t.Fatalf("default-only bound %s not below tier-aware bound %s", shrunk.String(), bound.String())
	}
}

func mustPriceAmount(t *testing.T, value string) money.Amount {
	t.Helper()
	amount, err := money.Parse(value)
	if err != nil {
		t.Fatalf("parse price %q: %v", value, err)
	}
	return amount
}
