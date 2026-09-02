package ledger

import (
	"errors"
	"math"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestCalculatePriceV1UsesAllUsageClassesAndSeparateCeilings(t *testing.T) {
	result, err := CalculatePriceV1(
		UsageV1{InputTokens: 1_000_000, OutputTokens: 2_000_000, CacheWriteTokens: 3_000_000, CacheReadTokens: 4_000_000},
		OfficialPricesV1{
			InputPerMillion: mustPriceAmount(t, "1"), OutputPerMillion: mustPriceAmount(t, "2"),
			CacheWritePerMillion: mustPriceAmount(t, "0.5"), CacheReadPerMillion: mustPriceAmount(t, "0.25"),
		},
		1_500_000_000,
		1_000_000,
		false,
	)
	if err != nil {
		t.Fatalf("CalculatePriceV1: %v", err)
	}
	if result.ProviderCharge != mustPriceAmount(t, "11.25") || result.PlatformFee != mustPriceAmount(t, "0.01125") {
		t.Fatalf("result = %+v", result)
	}
}

func TestCalculatePriceV1RoundsTinyNonzeroAmountsToOneNano(t *testing.T) {
	result, err := CalculatePriceV1(
		UsageV1{InputTokens: 1},
		OfficialPricesV1{InputPerMillion: money.FromNano(1)},
		1,
		1,
		false,
	)
	if err != nil {
		t.Fatalf("CalculatePriceV1: %v", err)
	}
	if result.ProviderCharge.Nano() != 1 || result.PlatformFee.Nano() != 1 {
		t.Fatalf("tiny result = %+v, want one nano for each nonzero charge", result)
	}
}

func TestCalculatePriceV1SelfChannelHasNoPlatformFee(t *testing.T) {
	result, err := CalculatePriceV1(
		UsageV1{InputTokens: 1_000_000},
		OfficialPricesV1{InputPerMillion: mustPriceAmount(t, "2")},
		FixedPointScale,
		FixedPointScale,
		true,
	)
	if err != nil || result.ProviderCharge != mustPriceAmount(t, "2") || result.PlatformFee != 0 {
		t.Fatalf("self result = %+v, err %v", result, err)
	}
}

func TestCalculatePriceV1RejectsInvalidAndOverflowingInputs(t *testing.T) {
	if _, err := CalculatePriceV1(UsageV1{InputTokens: -1}, OfficialPricesV1{}, FixedPointScale, 0, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative usage error = %v", err)
	}
	if _, err := CalculatePriceV1(
		UsageV1{InputTokens: math.MaxInt64},
		OfficialPricesV1{InputPerMillion: money.FromNano(math.MaxInt64)},
		math.MaxInt64,
		0,
		false,
	); !errors.Is(err, ErrAmountOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
}

func mustPriceAmount(t *testing.T, value string) money.Amount {
	t.Helper()
	amount, err := money.Parse(value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return amount
}
