package gateway

import (
	"math"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestUsageAndAuthorizationShareTwoContextUpperBound(t *testing.T) {
	lease := channel.RoutingLease{
		ContextWindow:   1000,
		InputPrice:      money.FromNano(2 * money.Scale),
		OutputPrice:     money.FromNano(5 * money.Scale),
		CacheWritePrice: money.FromNano(3 * money.Scale),
		CacheReadPrice:  money.FromNano(money.Scale),
		Multiplier:      money.FromNano(money.Scale),
	}
	upper, err := ConservativeNetDebitUpperBound(lease, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	priced, err := ledger.CalculatePriceV1(
		ledger.UsageV1{InputTokens: 1000, OutputTokens: 1000},
		OfficialPrices(lease),
		lease.Multiplier.Nano(),
		0,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateUsage(ledger.UsageV1{InputTokens: 1000, OutputTokens: 1000}, lease.ContextWindow); err != nil {
		t.Fatalf("input at the catalog limit plus output at the catalog limit must remain billable: %v", err)
	}
	if upper < priced.ProviderCharge {
		t.Fatalf("authorization %d is below legal maximum charge %d", upper, priced.ProviderCharge)
	}
	if err := ValidateUsage(ledger.UsageV1{InputTokens: 1000, OutputTokens: 1001}, lease.ContextWindow); err == nil {
		t.Fatal("usage above two context windows was accepted")
	}
}

func TestTwoContextUpperBoundRejectsIntegerOverflow(t *testing.T) {
	lease := channel.RoutingLease{
		ContextWindow: math.MaxInt64/2 + 1,
		InputPrice:    money.FromNano(money.Scale),
		Multiplier:    money.FromNano(money.Scale),
	}
	if _, err := ConservativeNetDebitUpperBound(lease, 0, false); err == nil {
		t.Fatal("overflowing authorization token count was accepted")
	}
	if err := ValidateUsage(ledger.UsageV1{}, lease.ContextWindow); err == nil {
		t.Fatal("overflowing validation token count was accepted")
	}
	selfLease := channel.RoutingLease{
		ContextWindow: math.MaxInt64 / 2,
		InputPrice:    money.FromNano(100_000 * money.Scale),
		Multiplier:    money.FromNano(1000 * money.Scale),
	}
	if _, err := ConservativeNetDebitUpperBound(selfLease, 0, true); err == nil {
		t.Fatal("self-channel nominal provider charge overflow was accepted")
	}
}
