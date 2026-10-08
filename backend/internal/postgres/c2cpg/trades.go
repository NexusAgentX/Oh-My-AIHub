package c2cpg

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

// returnAllocation gives a trade's quantity back to its order and ends the
// trade (cancelled, expired or returned to the seller). The quantity is released
// from the parent hold in the same transaction when the order is already
// cancelled; otherwise it stays inside the order's parent hold.
func (x *session) returnAllocation(ctx context.Context, command c2c.Command, order c2c.Order, trade c2c.Trade, terminal c2c.TradeStatus, action, reason string) (c2c.Trade, error) {
	if trade.Status != c2c.TradeAwaitingPayment && trade.Status != c2c.TradePaid && trade.Status != c2c.TradeDisputed {
		return c2c.Trade{}, c2c.ErrConflict
	}
	if (trade.Status == c2c.TradePaid || trade.Status == c2c.TradeDisputed) && terminal != c2c.TradeReturnedToSeller {
		return c2c.Trade{}, c2c.ErrConflict
	}
	if trade.Status == c2c.TradeAwaitingPayment && terminal != c2c.TradeCancelled && terminal != c2c.TradeExpired {
		return c2c.Trade{}, c2c.ErrConflict
	}
	if order.Allocated < trade.Quantity {
		return c2c.Trade{}, c2c.ErrConflict
	}

	releasedHold := false
	if order.Status == c2c.OrderCancelled {
		if _, err := x.ledger.ReleaseHold(ctx, ledger.MutateHoldRequest{
			IdempotencyKey: derivedLedgerKey(command, "trade-release"),
			HoldID:         trade.HoldID,
			BusinessID:     trade.ID,
			Amount:         ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: trade.Quantity},
			Reason:         reason,
		}); err != nil {
			return c2c.Trade{}, err
		}
		releasedHold = true
	}
	order.Allocated -= trade.Quantity
	if order.Status == c2c.OrderCancelled {
		order.Closed += trade.Quantity
	} else {
		order.Available += trade.Quantity
	}
	if err := x.persistOrderAmounts(ctx, order, command.Now); err != nil {
		return c2c.Trade{}, err
	}
	affected, err := x.q.ReturnTrade(ctx, ReturnTradeParams{
		ID: trade.ID, NewStatus: string(terminal), ResolvedAt: &command.Now, ExpectedStatus: string(trade.Status),
	})
	if err != nil {
		return c2c.Trade{}, mapError(err)
	}
	if affected != 1 {
		return c2c.Trade{}, c2c.ErrConflict
	}
	holdBusinessID := ""
	if releasedHold {
		holdBusinessID = trade.ID
	}
	if err := x.insertEvent(ctx, command, order.ID, trade.ID, action, reason, "", holdBusinessID); err != nil {
		return c2c.Trade{}, err
	}
	return loadTrade(ctx, x.q, trade.ID, false, false)
}

// captureTrade settles a paid or disputed trade: the hold is captured to the
// buyer and the order's allocated quantity becomes settled, atomically.
func (x *session) captureTrade(ctx context.Context, command c2c.Command, order c2c.Order, trade c2c.Trade, action, reason string) (c2c.Trade, error) {
	if trade.Status != c2c.TradePaid && trade.Status != c2c.TradeDisputed {
		return c2c.Trade{}, c2c.ErrConflict
	}
	if order.Allocated < trade.Quantity {
		return c2c.Trade{}, c2c.ErrConflict
	}
	captured, err := x.ledger.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{
			IdempotencyKey: derivedLedgerKey(command, "trade-capture"),
			HoldID:         trade.HoldID,
			BusinessID:     trade.ID,
			Amount:         ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: trade.Quantity},
			Reason:         reason,
		},
		Credits: []ledger.Posting{{
			Account: ledger.UserAccount(trade.BuyerAccountID), BusinessRole: ledger.EntryRoleBuyer,
			Amount: trade.Quantity,
		}},
		ReferenceType: "c2c_trade",
		ReferenceID:   trade.ID,
	})
	if err != nil {
		return c2c.Trade{}, err
	}
	order.Allocated -= trade.Quantity
	order.Settled += trade.Quantity
	if err := x.persistOrderAmounts(ctx, order, command.Now); err != nil {
		return c2c.Trade{}, err
	}
	transactionID := captured.Transaction.ID
	affected, err := x.q.ReleaseTrade(ctx, ReleaseTradeParams{
		ID: trade.ID, LedgerTransactionID: &transactionID, ResolvedAt: &command.Now, ExpectedStatus: string(trade.Status),
	})
	if err != nil {
		return c2c.Trade{}, mapError(err)
	}
	if affected != 1 {
		return c2c.Trade{}, c2c.ErrConflict
	}
	if err := x.insertEvent(ctx, command, order.ID, trade.ID, action, reason, transactionID, trade.ID); err != nil {
		return c2c.Trade{}, err
	}
	return loadTrade(ctx, x.q, trade.ID, false, false)
}

// MarkPaid records the buyer's external payment declaration. A declaration made
// after the deadline instead expires the trade and returns c2c.ErrExpired; the
// expiry itself is committed.
//
// Lock order for all trade commands: actor, buyer and seller advisory keys
// (sorted), command row, order row, trade row, then ledger rows.
func (s *Store) MarkPaid(ctx context.Context, command c2c.Command, tradeID string, paymentReference *c2c.EncryptedValue, paymentReferenceChars int) (c2c.Trade, error) {
	var result c2c.Trade
	expired := false
	err := s.inTx(ctx, func(x *session) error {
		if err := x.lockTradeKeys(ctx, command.Actor.ID, tradeID); err != nil {
			return err
		}
		if err := x.lockActor(ctx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := x.reserveCommand(ctx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeSnapshot[c2c.Trade](snapshot)
			expired = err == nil && result.Status == c2c.TradeExpired
			return err
		}
		order, trade, err := x.lockOrderAndTrade(ctx, tradeID)
		if err != nil {
			return err
		}
		if trade.BuyerAccountID != command.Actor.ID {
			return c2c.ErrForbidden
		}
		if trade.Status != c2c.TradeAwaitingPayment {
			return c2c.ErrConflict
		}
		if !command.Now.Before(trade.PaymentDeadline) {
			result, err = x.returnAllocation(ctx, command, order, trade, c2c.TradeExpired, "trade.expired", "C2C payment deadline expired")
			if err != nil {
				return err
			}
			expired = true
			return x.completeCommand(ctx, command, cleanTradeSnapshot(result))
		}
		params := MarkTradePaidParams{ID: trade.ID, PaymentReferenceChars: int32(paymentReferenceChars), PaidAt: &command.Now}
		if paymentReference != nil {
			params.PaymentReferenceKeyID = &paymentReference.KeyID
			params.PaymentReferenceNonce = paymentReference.Nonce
			params.PaymentReferenceCiphertext = paymentReference.Ciphertext
		}
		affected, err := x.q.MarkTradePaid(ctx, params)
		if err != nil {
			return mapError(err)
		}
		if affected != 1 {
			return c2c.ErrConflict
		}
		if err := x.insertEvent(ctx, command, order.ID, trade.ID, "trade.paid", "buyer declared external payment", "", ""); err != nil {
			return err
		}
		result, err = loadTrade(ctx, x.q, trade.ID, false, false)
		if err != nil {
			return err
		}
		return x.completeCommand(ctx, command, cleanTradeSnapshot(result))
	})
	if err != nil {
		return result, mapError(err)
	}
	if expired {
		return result, c2c.ErrExpired
	}
	return result, nil
}

// CancelTrade lets the buyer cancel before paying; a cancellation after the
// deadline, or by the system, expires the trade instead.
func (s *Store) CancelTrade(ctx context.Context, command c2c.Command, tradeID string, system bool) (c2c.Trade, error) {
	var result c2c.Trade
	expired := false
	err := s.inTx(ctx, func(x *session) error {
		if err := x.lockTradeKeys(ctx, command.Actor.ID, tradeID); err != nil {
			return err
		}
		if err := x.lockActor(ctx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := x.reserveCommand(ctx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeSnapshot[c2c.Trade](snapshot)
			expired = err == nil && result.Status == c2c.TradeExpired
			return err
		}
		order, trade, err := x.lockOrderAndTrade(ctx, tradeID)
		if err != nil {
			return err
		}
		if !system && trade.BuyerAccountID != command.Actor.ID {
			return c2c.ErrForbidden
		}
		if trade.Status != c2c.TradeAwaitingPayment {
			return c2c.ErrConflict
		}
		terminal, action, reason := c2c.TradeCancelled, "trade.cancelled", "buyer cancelled C2C trade before payment"
		if system || !command.Now.Before(trade.PaymentDeadline) {
			terminal, action, reason = c2c.TradeExpired, "trade.expired", "C2C payment deadline expired"
			expired = !system
		}
		result, err = x.returnAllocation(ctx, command, order, trade, terminal, action, reason)
		if err != nil {
			return err
		}
		return x.completeCommand(ctx, command, cleanTradeSnapshot(result))
	})
	if err != nil {
		return result, mapError(err)
	}
	if expired {
		return result, c2c.ErrExpired
	}
	return result, nil
}

// ConfirmReceipt lets the seller confirm the external payment, which releases
// the points to the buyer.
func (s *Store) ConfirmReceipt(ctx context.Context, command c2c.Command, tradeID string) (c2c.Trade, error) {
	var result c2c.Trade
	err := s.inTx(ctx, func(x *session) error {
		if err := x.lockTradeKeys(ctx, command.Actor.ID, tradeID); err != nil {
			return err
		}
		if err := x.lockActor(ctx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := x.reserveCommand(ctx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeSnapshot[c2c.Trade](snapshot)
			return err
		}
		order, trade, err := x.lockOrderAndTrade(ctx, tradeID)
		if err != nil {
			return err
		}
		if trade.SellerAccountID != command.Actor.ID {
			return c2c.ErrForbidden
		}
		if trade.Status != c2c.TradePaid {
			return c2c.ErrConflict
		}
		result, err = x.captureTrade(ctx, command, order, trade, "trade.released", "seller confirmed external payment and released points")
		if err != nil {
			return err
		}
		return x.completeCommand(ctx, command, cleanTradeSnapshot(result))
	})
	return result, mapError(err)
}

func expiryCommand(tradeID string, now time.Time) c2c.Command {
	payloadHash := sha256.Sum256([]byte("c2c.trade.expire\x00" + tradeID))
	return c2c.Command{
		Operation: "c2c.trade.expire", IdempotencyKey: tradeID,
		PayloadHash: payloadHash, Now: now,
	}
}

func (s *Store) expireTrade(ctx context.Context, tradeID string, now time.Time) error {
	command := expiryCommand(tradeID, now)
	return mapError(s.inTx(ctx, func(x *session) error {
		if err := x.lockTradeKeys(ctx, command.Actor.ID, tradeID); err != nil {
			return err
		}
		if err := x.lockActor(ctx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := x.reserveCommand(ctx, command)
		if err != nil {
			return err
		}
		if replay {
			_, err = decodeSnapshot[c2c.Trade](snapshot)
			return err
		}
		order, trade, err := x.lockOrderAndTrade(ctx, tradeID)
		if err != nil {
			return err
		}
		if trade.Status != c2c.TradeAwaitingPayment || now.Before(trade.PaymentDeadline) {
			return c2c.ErrConflict
		}
		result, err := x.returnAllocation(ctx, command, order, trade, c2c.TradeExpired, "trade.expired", "C2C payment deadline expired")
		if err != nil {
			return err
		}
		return x.completeCommand(ctx, command, cleanTradeSnapshot(result))
	}))
}

// ExpireDue expires up to limit trades whose payment deadline has passed, each
// in its own transaction. Trades another command already settled are skipped.
func (s *Store) ExpireDue(ctx context.Context, now time.Time, limit int) (int, error) {
	ids, err := s.q.ListDueTradeIDs(ctx, ListDueTradeIDsParams{Now: now, BatchLimit: batchLimit(limit)})
	if err != nil {
		return 0, mapError(err)
	}
	expired := 0
	for _, id := range ids {
		err := s.expireTrade(ctx, id, now)
		if err == nil {
			expired++
			continue
		}
		if errors.Is(err, c2c.ErrConflict) || errors.Is(err, c2c.ErrNotFound) {
			continue
		}
		return expired, err
	}
	return expired, nil
}
