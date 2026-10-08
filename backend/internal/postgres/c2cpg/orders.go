package c2cpg

import (
	"context"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
)

// CreateOrder publishes a sell order and freezes its total in a parent hold
// inside the same transaction.
//
// Lock order: actor advisory key, command row, ledger account (inside the hold).
func (s *Store) CreateOrder(ctx context.Context, command c2c.Command, input c2c.NewOrder) (c2c.Order, error) {
	var result c2c.Order
	err := s.inTx(ctx, func(x *session) error {
		if err := x.lockActor(ctx, command, false); err != nil {
			return err
		}
		snapshot, replay, err := x.reserveCommand(ctx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeSnapshot[c2c.Order](snapshot)
			return err
		}
		if err := x.ensureCreditActive(ctx, command.Actor.ID); err != nil {
			return err
		}
		hold, err := x.ledger.CreateHold(ctx, ledger.CreateHoldRequest{
			IdempotencyKey: derivedLedgerKey(command, "parent-hold"),
			AccountID:      command.Actor.ID,
			Amount:         input.Total,
			FundingPolicy:  ledger.HoldFundingSettledBalanceOnly,
			Purpose:        ledger.HoldPurposeAssetReservation,
			Reason:         "reserve points for C2C sell order",
			BusinessType:   "c2c_sell_order",
			BusinessID:     input.ID,
		})
		if err != nil {
			return err
		}
		if err := x.q.InsertOrder(ctx, InsertOrderParams{
			ID: input.ID, OwnerAccountID: command.Actor.ID,
			UnitPriceFen: input.UnitPriceFen, TotalNano: input.Total,
			MinimumNano: input.Minimum, MaximumNano: input.Maximum,
			ParentHoldID: &hold.ID, CreatedAt: command.Now,
		}); err != nil {
			return mapError(err)
		}
		for _, method := range input.PaymentMethods {
			if err := x.q.InsertPaymentMethod(ctx, InsertPaymentMethodParams{
				ID: method.ID, OrderID: input.ID, MethodType: string(method.Type),
				Position: int32(method.Position), QrAvailable: method.QRAvailable,
				KeyID: method.Private.KeyID, Nonce: method.Private.Nonce, Ciphertext: method.Private.Ciphertext,
				CreatedAt: command.Now,
			}); err != nil {
				return mapError(err)
			}
		}
		if err := x.insertEvent(ctx, command, input.ID, "", "order.created", "C2C order published", "", input.ID); err != nil {
			return err
		}
		result, err = loadOrder(ctx, x.q, input.ID, false)
		if err != nil {
			return err
		}
		return x.completeCommand(ctx, command, cleanOrderSnapshot(result))
	})
	return result, mapError(err)
}

// TakeOrder allocates part of an open sell order to a new trade; the quantity
// stays inside the order's parent hold.
//
// Lock order: actor and owner advisory keys (sorted), command row, order row,
// ledger account (inside the hold).
func (s *Store) TakeOrder(ctx context.Context, command c2c.Command, orderID string, input c2c.NewTrade) (c2c.Trade, error) {
	var result c2c.Trade
	err := s.inTx(ctx, func(x *session) error {
		ownerID, err := x.q.GetOrderOwnerID(ctx, orderID)
		if err != nil {
			return mapError(err)
		}
		if err := x.lockAccountKeys(ctx, command.Actor.ID, ownerID); err != nil {
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
		order, err := loadOrder(ctx, x.q, orderID, true)
		if err != nil {
			return err
		}
		if order.OwnerAccountID == command.Actor.ID {
			return c2c.ErrForbidden
		}
		if err := x.ensureCreditActive(ctx, command.Actor.ID); err != nil {
			return err
		}
		if err := x.ensureOrderOwnerReady(ctx, order); err != nil {
			return err
		}
		if order.Status != c2c.OrderOpen || order.Available <= 0 || input.Quantity <= 0 || input.Quantity > order.Available || input.Quantity > order.Maximum || (input.Quantity < order.Minimum && input.Quantity != order.Available) {
			return c2c.ErrConflict
		}
		methodFound := false
		for _, method := range order.PaymentMethods {
			if method.ID == input.PaymentMethodID {
				methodFound = true
				break
			}
		}
		if !methodFound {
			return c2c.ErrInvalidInput
		}
		fiatAmount, err := c2c.FiatAmountFen(input.Quantity, order.UnitPriceFen)
		if err != nil {
			return err
		}

		order.Available -= input.Quantity
		order.Allocated += input.Quantity
		if err := x.persistOrderAmounts(ctx, order, command.Now); err != nil {
			return err
		}
		if err := x.q.InsertTrade(ctx, InsertTradeParams{
			ID: input.ID, OrderID: order.ID, BuyerAccountID: command.Actor.ID, SellerAccountID: order.OwnerAccountID,
			QuantityNano: input.Quantity, UnitPriceFen: order.UnitPriceFen, FiatAmountFen: fiatAmount,
			HoldID: order.ParentHoldID, SelectedPaymentMethodID: input.PaymentMethodID,
			PaymentDeadline: input.PaymentDeadline, CreatedAt: command.Now,
		}); err != nil {
			return mapError(err)
		}
		if err := x.insertEvent(ctx, command, order.ID, input.ID, "trade.created", "order quantity allocated to C2C trade", "", input.ID); err != nil {
			return err
		}
		result, err = loadTrade(ctx, x.q, input.ID, false, false)
		if err != nil {
			return err
		}
		return x.completeCommand(ctx, command, cleanTradeSnapshot(result))
	})
	return result, mapError(err)
}

// CancelOrder lets the owner cancel an open or partially allocated order.
func (s *Store) CancelOrder(ctx context.Context, command c2c.Command, orderID string) (c2c.Order, error) {
	return s.cancelOrder(ctx, command, orderID, false,
		"order.cancelled", "C2C order cancelled", "release unallocated points from cancelled C2C sell order")
}

// AdminCancelOrder lets an administrator cancel an order with a recorded reason.
func (s *Store) AdminCancelOrder(ctx context.Context, command c2c.Command, orderID, reason string) (c2c.Order, error) {
	return s.cancelOrder(ctx, command, orderID, true, "order.admin_cancelled", reason, reason)
}

// cancelOrder closes the unallocated quantity of an order and releases that
// quantity from the parent hold. Trades already allocated
// keep their own holds.
//
// Lock order: actor and owner advisory keys (sorted), command row, order row,
// ledger account (inside the release).
func (s *Store) cancelOrder(ctx context.Context, command c2c.Command, orderID string, admin bool, action, eventReason, releaseReason string) (c2c.Order, error) {
	var result c2c.Order
	err := s.inTx(ctx, func(x *session) error {
		if err := x.lockOrderKeys(ctx, command.Actor.ID, orderID); err != nil {
			return err
		}
		if err := x.lockActor(ctx, command, admin); err != nil {
			return err
		}
		snapshot, replay, err := x.reserveCommand(ctx, command)
		if err != nil {
			return err
		}
		if replay {
			result, err = decodeSnapshot[c2c.Order](snapshot)
			return err
		}
		order, err := loadOrder(ctx, x.q, orderID, true)
		if err != nil {
			return err
		}
		if !admin && order.OwnerAccountID != command.Actor.ID {
			return c2c.ErrForbidden
		}
		if order.Status != c2c.OrderOpen && order.Status != c2c.OrderAllocated {
			return c2c.ErrConflict
		}
		closing := order.Available
		if closing > 0 {
			if _, err := x.ledger.ReleaseHold(ctx, ledger.MutateHoldRequest{
				IdempotencyKey: derivedLedgerKey(command, "parent-release"),
				HoldID:         order.ParentHoldID,
				BusinessID:     order.ID,
				Amount:         ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: closing},
				Reason:         releaseReason,
			}); err != nil {
				return err
			}
		}
		affected, err := x.q.CancelOrder(ctx, CancelOrderParams{ID: order.ID, ClosingNano: closing, CancelledAt: &command.Now})
		if err != nil {
			return mapError(err)
		}
		if affected != 1 {
			return c2c.ErrConflict
		}
		if err := x.insertEvent(ctx, command, order.ID, "", action, eventReason, "", order.ID); err != nil {
			return err
		}
		result, err = loadOrder(ctx, x.q, order.ID, false)
		if err != nil {
			return err
		}
		return x.completeCommand(ctx, command, cleanOrderSnapshot(result))
	})
	return result, mapError(err)
}
