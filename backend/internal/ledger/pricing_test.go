package ledger

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var pricingTime = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

func TestCalculatePriceV2UsesAllUsageClassesAndSeparateCeilings(t *testing.T) {
	result, err := CalculatePriceV2(
		Usage{InputTokens: 1_000_000, OutputTokens: 2_000_000, CacheWriteTokens: 3_000_000, CacheReadTokens: 4_000_000},
		Prices{
			InputPerMillion: mustPriceAmount(t, "1"), OutputPerMillion: mustPriceAmount(t, "2"),
			CacheWritePerMillion: mustPriceAmount(t, "0.5"), CacheReadPerMillion: mustPriceAmount(t, "0.25"),
		},
		nil, pricingTime, 1_500_000_000, 1_000_000,
	)
	if err != nil {
		t.Fatalf("CalculatePriceV2: %v", err)
	}
	if result.Cost != mustPriceAmount(t, "11.25") || result.Fee != mustPriceAmount(t, "0.01125") || result.TierSeq != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestCalculatePriceV2RoundsTinyNonzeroAmountsToOneNano(t *testing.T) {
	result, err := CalculatePriceV2(Usage{InputTokens: 1}, Prices{InputPerMillion: money.FromNano(1)}, nil, pricingTime, 1, 1)
	if err != nil {
		t.Fatalf("CalculatePriceV2: %v", err)
	}
	if result.Cost.Nano() != 1 || result.Fee.Nano() != 1 {
		t.Fatalf("tiny result = %+v, want one nano for each nonzero charge", result)
	}
}

func TestCalculatePriceV2ZeroFeeRateChargesNoFee(t *testing.T) {
	result, err := CalculatePriceV2(Usage{InputTokens: 1_000_000}, Prices{InputPerMillion: mustPriceAmount(t, "2")}, nil, pricingTime, FixedPointScale, 0)
	if err != nil || result.Cost != mustPriceAmount(t, "2") || result.Fee != 0 {
		t.Fatalf("result = %+v, err %v", result, err)
	}
}

func TestCalculatePriceV2RejectsInvalidAndOverflowingInputs(t *testing.T) {
	if _, err := CalculatePriceV2(Usage{InputTokens: -1}, Prices{}, nil, pricingTime, FixedPointScale, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative usage error = %v", err)
	}
	if _, err := CalculatePriceV2(Usage{}, Prices{}, nil, pricingTime, FixedPointScale, FixedPointScale+1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("fee rate above 100%% error = %v", err)
	}
	if _, err := CalculatePriceV2(
		Usage{InputTokens: math.MaxInt64},
		Prices{InputPerMillion: money.FromNano(math.MaxInt64)},
		nil, pricingTime, math.MaxInt64, 0,
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
