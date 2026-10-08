package c2c

import (
	"errors"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func points(t *testing.T, value string) money.Amount {
	t.Helper()
	amount, err := money.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return amount
}

func TestTotalFenRoundsUpToWholeFen(t *testing.T) {
	for _, tc := range []struct {
		amount string
		price  int64
		want   int64
	}{
		{"30", 92, 2760},
		{"1", 1, 1},
		{"0.000000001", 1, 1},    // the smallest amount still costs one fen
		{"5.000000001", 33, 166}, // 165.000000033 fen rounds up
		{"10", 33, 330},          // exact
		{"0.5", 3, 2},            // 1.5 fen rounds up
		{"1000000", 10000, 1e10}, // large but within range
	} {
		got, err := TotalFen(points(t, tc.amount), tc.price)
		if err != nil || got != tc.want {
			t.Errorf("TotalFen(%s, %d) = %d, %v; want %d", tc.amount, tc.price, got, err, tc.want)
		}
	}
	if _, err := TotalFen(money.Amount(1<<62), 1<<40); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("overflow error = %v", err)
	}
	if _, err := TotalFen(0, 5); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("zero amount error = %v", err)
	}
}

func identityHolds(o Order) bool { return o.Total == o.Available+o.InTrade+o.Sold+o.Closed }

func TestOrderQuantitiesAlwaysAddUp(t *testing.T) {
	max := points(t, "20")
	order := Order{
		ID: "o", Seller: Party{ID: "seller"}, Total: points(t, "40"), Available: points(t, "40"), UnitPriceFen: 90,
		MinPerTrade: points(t, "5"), MaxPerTrade: &max, Status: OrderOpen,
	}
	ten := points(t, "10")

	reserved := order.Reserve(ten)
	if reserved.Available != points(t, "30") || reserved.InTrade != ten || !identityHolds(reserved) {
		t.Fatalf("reserve = %+v", reserved)
	}
	settled := reserved.Settle(ten)
	if settled.InTrade != 0 || settled.Sold != ten || settled.Status != OrderOpen || !identityHolds(settled) {
		t.Fatalf("settle = %+v", settled)
	}
	back, refund := reserved.Unlock(ten)
	if refund || back.Available != points(t, "40") || back.InTrade != 0 || !identityHolds(back) {
		t.Fatalf("unlock on open order = %+v refund %v", back, refund)
	}

	closed, returned := reserved.Close()
	if returned != points(t, "30") || closed.Available != 0 || closed.Closed != points(t, "30") || closed.Status != OrderClosed || !identityHolds(closed) {
		t.Fatalf("close = %+v returned %s", closed, returned)
	}
	// A trade ending on a closed order refunds the seller and never reopens it.
	ended, refund := closed.Unlock(ten)
	if !refund || ended.Status != OrderClosed || ended.Available != 0 || ended.Closed != points(t, "40") || !identityHolds(ended) {
		t.Fatalf("unlock on closed order = %+v refund %v", ended, refund)
	}
	soldClosed := closed.Settle(ten)
	if soldClosed.Status != OrderClosed || soldClosed.Sold != ten || !identityHolds(soldClosed) {
		t.Fatalf("settle on closed order = %+v", soldClosed)
	}

	// Selling out an open order fills it.
	last := Order{Total: ten, InTrade: ten, Status: OrderOpen}
	if filled := last.Settle(ten); filled.Status != OrderFilled || !identityHolds(filled) {
		t.Fatalf("filled = %+v", filled)
	}
}

func TestCheckBuy(t *testing.T) {
	max := points(t, "20")
	order := Order{Seller: Party{ID: "seller"}, Available: points(t, "25"), MinPerTrade: points(t, "5"), MaxPerTrade: &max, Status: OrderOpen}
	for name, tc := range map[string]struct {
		order  Order
		buyer  string
		amount string
		want   error
	}{
		"ok":            {order, "buyer", "20", nil},
		"minimum":       {order, "buyer", "5", nil},
		"below minimum": {order, "buyer", "4.999999999", ErrAmountOutOfRange},
		"above maximum": {order, "buyer", "20.000000001", ErrAmountOutOfRange},
		"unavailable":   {Order{Seller: order.Seller, Available: points(t, "3"), MinPerTrade: 1, Status: OrderOpen}, "buyer", "5", ErrAmountUnavailable},
		"own order":     {order, "seller", "5", ErrOwnOrder},
		"closed":        {Order{Seller: order.Seller, Available: 1, MinPerTrade: 1, Status: OrderClosed}, "buyer", "1", ErrOrderNotOpen},
	} {
		if err := tc.order.CheckBuy(tc.buyer, points(t, tc.amount)); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tc.want)
		}
	}
}

func TestTradeTransitionRules(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	trade := func(status TradeStatus, deadline time.Time) Trade {
		return Trade{Buyer: Party{ID: "buyer"}, Seller: Party{ID: "seller"}, Status: status, PaymentDeadline: deadline}
	}
	future, past := now.Add(time.Minute), now.Add(-time.Minute)

	if noop, err := trade(TradeAwaitingPayment, future).CheckPaid("buyer", now); noop || err != nil {
		t.Errorf("paid = %v, %v", noop, err)
	}
	if _, err := trade(TradeAwaitingPayment, future).CheckPaid("seller", now); !errors.Is(err, ErrForbidden) {
		t.Errorf("seller paid error = %v", err)
	}
	if _, err := trade(TradeAwaitingPayment, past).CheckPaid("buyer", now); !errors.Is(err, ErrPaymentExpired) {
		t.Errorf("late paid error = %v", err)
	}
	if noop, err := trade(TradePaid, past).CheckPaid("buyer", now); !noop || err != nil {
		t.Errorf("repeat paid = %v, %v", noop, err)
	}
	if _, err := trade(TradeDisputed, future).CheckPaid("buyer", now); !errors.Is(err, ErrInvalidState) {
		t.Errorf("paid while disputed error = %v", err)
	}

	for status, wantErr := range map[TradeStatus]error{
		TradeAwaitingPayment: nil, TradePaid: nil, TradeDisputed: ErrInvalidState, TradeCancelled: ErrInvalidState,
		TradeResolvedBuyer: ErrInvalidState, TradeResolvedSeller: ErrInvalidState,
	} {
		if _, err := trade(status, future).CheckRelease("seller"); !errors.Is(err, wantErr) {
			t.Errorf("release from %s error = %v, want %v", status, err, wantErr)
		}
	}
	if noop, _ := trade(TradeReleased, future).CheckRelease("seller"); !noop {
		t.Error("repeat release must be a no-op")
	}
	if _, err := trade(TradePaid, future).CheckRelease("buyer"); !errors.Is(err, ErrForbidden) {
		t.Errorf("buyer release error = %v", err)
	}

	if _, err := trade(TradePaid, future).CheckCancel("buyer", now); !errors.Is(err, ErrInvalidState) {
		t.Errorf("cancel after paid error = %v", err)
	}
	if _, err := trade(TradeAwaitingPayment, future).CheckCancel("", now); !errors.Is(err, ErrInvalidState) {
		t.Errorf("timeout job before the deadline error = %v", err)
	}
	if noop, err := trade(TradeAwaitingPayment, past).CheckCancel("", now); noop || err != nil {
		t.Errorf("timeout job after the deadline = %v, %v", noop, err)
	}
	if _, err := trade(TradePaid, past).CheckCancel("", now); !errors.Is(err, ErrInvalidState) {
		t.Errorf("timeout job must not touch a paid trade: %v", err)
	}
	if noop, _ := trade(TradeCancelled, past).CheckCancel("buyer", now); !noop {
		t.Error("repeat cancel must be a no-op")
	}

	for status, wantErr := range map[TradeStatus]error{
		TradePaid: nil, TradeDisputed: nil, TradeAwaitingPayment: ErrInvalidState, TradeReleased: ErrInvalidState,
	} {
		if err := trade(status, future).CheckDispute("seller"); !errors.Is(err, wantErr) {
			t.Errorf("dispute from %s error = %v, want %v", status, err, wantErr)
		}
	}
	if err := trade(TradePaid, future).CheckDispute("stranger"); !errors.Is(err, ErrForbidden) {
		t.Errorf("stranger dispute error = %v", err)
	}

	if noop, err := trade(TradeDisputed, future).CheckResolve(true); noop || err != nil {
		t.Errorf("resolve = %v, %v", noop, err)
	}
	if noop, _ := trade(TradeResolvedBuyer, future).CheckResolve(true); !noop {
		t.Error("repeat ruling must be a no-op")
	}
	if _, err := trade(TradeResolvedBuyer, future).CheckResolve(false); !errors.Is(err, ErrInvalidState) {
		t.Errorf("opposite ruling error = %v", err)
	}
	if _, err := trade(TradePaid, future).CheckResolve(true); !errors.Is(err, ErrInvalidState) {
		t.Errorf("resolve before dispute error = %v", err)
	}
}
