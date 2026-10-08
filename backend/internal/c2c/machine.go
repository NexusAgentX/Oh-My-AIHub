package c2c

import (
	"math/big"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// The functions below are the pure order/trade state machine. Persistence
// calls them on rows it has locked and then writes the result together with
// the ledger transaction, so the arithmetic exists in exactly one place.

// TotalFen is the price of amount at unitPriceFen per point, rounded up to
// whole fen.
func TotalFen(amount money.Amount, unitPriceFen int64) (int64, error) {
	if amount <= 0 || unitPriceFen <= 0 {
		return 0, ErrInvalidInput
	}
	product := new(big.Int).Mul(big.NewInt(amount.Nano()), big.NewInt(unitPriceFen))
	scale := big.NewInt(money.Scale)
	quotient, remainder := new(big.Int).QuoRem(product, scale, new(big.Int))
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, ErrInvalidInput
	}
	return quotient.Int64(), nil
}

// CheckBuy validates a buy against the order's current state. The caller
// holds the order row lock.
func (o Order) CheckBuy(buyerID string, amount money.Amount) error {
	switch {
	case o.Status != OrderOpen:
		return ErrOrderNotOpen
	case o.Seller.ID == buyerID:
		return ErrOwnOrder
	case amount <= 0 || amount < o.MinPerTrade || (o.MaxPerTrade != nil && amount > *o.MaxPerTrade):
		return ErrAmountOutOfRange
	case amount > o.Available:
		return ErrAmountUnavailable
	}
	return nil
}

// Reserve moves amount from available into a new trade.
func (o Order) Reserve(amount money.Amount) Order {
	o.Available -= amount
	o.InTrade += amount
	return o
}

// Settle books a released trade: the escrowed amount is sold. An open order
// with nothing left to sell or in flight becomes filled.
func (o Order) Settle(amount money.Amount) Order {
	o.InTrade -= amount
	o.Sold += amount
	if o.Status == OrderOpen && o.Available == 0 && o.InTrade == 0 {
		o.Status = OrderFilled
	}
	return o
}

// Unlock undoes a trade whose points did not change hands. While the order is
// open the amount becomes available again; once it is closed there is nothing
// to sell it to, so refund reports that the escrowed points must go back to
// the seller, which counts as closed.
func (o Order) Unlock(amount money.Amount) (updated Order, refund bool) {
	o.InTrade -= amount
	if o.Status == OrderOpen {
		o.Available += amount
		return o, false
	}
	o.Closed += amount
	return o, true
}

// Close ends the order: the unsold available part returns to the seller at
// once, trades in progress continue and never reopen the order.
func (o Order) Close() (updated Order, refund money.Amount) {
	refund = o.Available
	o.Closed += o.Available
	o.Available = 0
	o.Status = OrderClosed
	return o, refund
}

func (t Trade) role(actorID string) string {
	switch actorID {
	case t.Buyer.ID:
		return "buyer"
	case t.Seller.ID:
		return "seller"
	}
	return ""
}

// CheckPaid validates "I have paid". noop reports a repeated request.
func (t Trade) CheckPaid(actorID string, now time.Time) (noop bool, err error) {
	switch {
	case actorID != t.Buyer.ID:
		return false, ErrForbidden
	case t.Status == TradePaid:
		return true, nil
	case t.Status != TradeAwaitingPayment:
		return false, ErrInvalidState
	case !now.Before(t.PaymentDeadline):
		return false, ErrPaymentExpired
	}
	return false, nil
}

// CheckRelease validates the seller's release.
func (t Trade) CheckRelease(actorID string) (noop bool, err error) {
	switch {
	case actorID != t.Seller.ID:
		return false, ErrForbidden
	case t.Status == TradeReleased:
		return true, nil
	case t.Status != TradePaid && t.Status != TradeAwaitingPayment:
		return false, ErrInvalidState
	}
	return false, nil
}

// CheckCancel validates a cancel. An empty actorID is the timeout job, which
// may only cancel a trade whose deadline has passed.
func (t Trade) CheckCancel(actorID string, now time.Time) (noop bool, err error) {
	switch {
	case actorID != "" && actorID != t.Buyer.ID:
		return false, ErrForbidden
	case t.Status == TradeCancelled:
		return true, nil
	case t.Status != TradeAwaitingPayment, actorID == "" && now.Before(t.PaymentDeadline):
		return false, ErrInvalidState
	}
	return false, nil
}

// CheckDispute validates opening or updating a dispute statement.
func (t Trade) CheckDispute(actorID string) error {
	switch {
	case t.role(actorID) == "":
		return ErrForbidden
	case t.Status != TradePaid && t.Status != TradeDisputed:
		return ErrInvalidState
	}
	return nil
}

// CheckResolve validates an administrator's ruling.
func (t Trade) CheckResolve(toBuyer bool) (noop bool, err error) {
	want := TradeResolvedSeller
	if toBuyer {
		want = TradeResolvedBuyer
	}
	switch {
	case t.Status == want:
		return true, nil
	case t.Status != TradeDisputed:
		return false, ErrInvalidState
	}
	return false, nil
}
