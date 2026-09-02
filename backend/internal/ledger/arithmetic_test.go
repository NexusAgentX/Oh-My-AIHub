package ledger

import (
	"math"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestSpendableCapacityUsesCanonicalComponents(t *testing.T) {
	posted := money.FromNano(8)
	credit := money.FromNano(10)
	assets := money.FromNano(3)
	authorized := money.FromNano(4)
	if got := SpendableCapacity(posted, credit, assets, authorized); got != money.FromNano(11) {
		t.Fatalf("capacity = %s, want 0.000000011", got)
	}
	if IsOverLimit(money.FromNano(-8), money.FromNano(5), 0, money.FromNano(4)) != true {
		t.Fatal("expected lowered credit with an existing authorization to be over limit")
	}
	if got := SpendableCapacity(money.FromNano(math.MaxInt64), money.FromNano(math.MaxInt64), 0, 0); got.Nano() != math.MaxInt64 {
		t.Fatalf("saturated capacity = %d, want MaxInt64", got.Nano())
	}
}

func TestCheckedArithmeticRejectsOverflow(t *testing.T) {
	if _, err := Add(money.FromNano(math.MaxInt64), 1); err != ErrAmountOverflow {
		t.Fatalf("Add overflow error = %v", err)
	}
	if _, err := Negate(money.FromNano(math.MinInt64)); err != ErrAmountOverflow {
		t.Fatalf("Negate overflow error = %v", err)
	}
}
