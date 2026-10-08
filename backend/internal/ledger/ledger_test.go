package ledger

import (
	"errors"
	"math"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func validTransaction() Transaction {
	return Transaction{
		Type: TypeAPICall, IdempotencyKey: "call:1", Related: &Related{Type: "call", ID: "00000000-0000-4000-8000-000000000001"},
		Entries: []Line{
			{Account: User("consumer"), Amount: -30_030_000_000},
			{Account: User("provider"), Amount: 30_000_000_000},
			{Account: System(SystemPlatformRevenue), Amount: 30_000_000},
		},
	}
}

func TestTransactionValidateAcceptsBalancedEpicExample(t *testing.T) {
	if err := validTransaction().Validate(); err != nil {
		t.Fatalf("balanced transaction rejected: %v", err)
	}
}

func TestTransactionValidateRejectsMalformedTransactions(t *testing.T) {
	cases := map[string]func(*Transaction){
		"unknown type":     func(tx *Transaction) { tx.Type = "transfer" },
		"empty key":        func(tx *Transaction) { tx.IdempotencyKey = " " },
		"single entry":     func(tx *Transaction) { tx.Entries = tx.Entries[:1] },
		"zero amount":      func(tx *Transaction) { tx.Entries[2].Amount = 0 },
		"duplicate":        func(tx *Transaction) { tx.Entries[1].Account = User("consumer") },
		"both refs":        func(tx *Transaction) { tx.Entries[0].Account = AccountRef{UserID: "x", System: SystemBadDebt} },
		"unknown system":   func(tx *Transaction) { tx.Entries[2].Account = System("platform_loss") },
		"bad related type": func(tx *Transaction) { tx.Related.Type = "invoice" },
	}
	for name, mutate := range cases {
		tx := validTransaction()
		tx.Entries = append([]Line(nil), tx.Entries...)
		related := *tx.Related
		tx.Related = &related
		mutate(&tx)
		if err := tx.Validate(); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: error = %v, want ErrInvalidInput", name, err)
		}
	}
	unbalanced := validTransaction()
	unbalanced.Entries[2].Amount = 1
	if err := unbalanced.Validate(); !errors.Is(err, ErrUnbalanced) {
		t.Fatalf("unbalanced error = %v", err)
	}
	overflow := Transaction{Type: TypeAdminAdjust, IdempotencyKey: "k", Entries: []Line{
		{Account: User("a"), Amount: money.FromNano(math.MaxInt64)},
		{Account: User("b"), Amount: 1},
		{Account: System(SystemBadDebt), Amount: -1},
	}}
	if err := overflow.Validate(); !errors.Is(err, ErrAmountOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
}

func TestPointsAvailableIsBalancePlusCreditLimit(t *testing.T) {
	points := Points{Balance: -30_030_000_000, CreditLimit: 100_000_000_000}
	if points.Available() != 69_970_000_000 {
		t.Fatalf("available = %s", points.Available())
	}
	overdrawn := Points{Balance: -120_000_000_000, CreditLimit: 100_000_000_000}
	if overdrawn.Available() != -20_000_000_000 {
		t.Fatalf("overdrawn available = %s", overdrawn.Available())
	}
}

func TestAddBalanceRejectsOverflow(t *testing.T) {
	if _, err := AddBalance(money.FromNano(math.MaxInt64), 1); !errors.Is(err, ErrAmountOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
	if sum, err := AddBalance(5, -7); err != nil || sum != -2 {
		t.Fatalf("sum = %v, %v", sum, err)
	}
}
