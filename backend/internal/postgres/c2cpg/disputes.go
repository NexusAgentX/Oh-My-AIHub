package c2cpg

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
)

func tradeParticipant(command c2c.Command, trade c2c.Trade) bool {
	return command.Actor.ID == trade.BuyerAccountID || command.Actor.ID == trade.SellerAccountID
}

func (x *session) insertDisputeStatement(ctx context.Context, command c2c.Command, tradeID string, statement c2c.NewStatement) error {
	return mapError(x.q.InsertDisputeStatement(ctx, InsertDisputeStatementParams{
		ID: statement.ID, TradeID: tradeID, ActorAccountID: command.Actor.ID,
		CharacterCount: int32(statement.CharacterCount),
		KeyID:          &statement.Encrypted.KeyID, Nonce: statement.Encrypted.Nonce, Ciphertext: statement.Encrypted.Ciphertext,
		CreatedAt: command.Now,
	}))
}

// OpenDispute moves a paid trade into dispute and stores the participant's
// first (encrypted) statement.
func (s *Store) OpenDispute(ctx context.Context, command c2c.Command, tradeID string, statement c2c.NewStatement) (c2c.Trade, error) {
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
		if !tradeParticipant(command, trade) {
			return c2c.ErrNotFound
		}
		if trade.Status != c2c.TradePaid {
			return c2c.ErrConflict
		}
		if err := x.insertDisputeStatement(ctx, command, trade.ID, statement); err != nil {
			return err
		}
		reviewDue := command.Now.Add(c2c.ReviewExtension)
		affected, err := x.q.OpenTradeDispute(ctx, OpenTradeDisputeParams{ID: trade.ID, ReviewDueAt: &reviewDue, UpdatedAt: command.Now})
		if err != nil {
			return mapError(err)
		}
		if affected != 1 {
			return c2c.ErrConflict
		}
		if err := x.insertEvent(ctx, command, order.ID, trade.ID, "trade.disputed", "participant opened C2C dispute", "", ""); err != nil {
			return err
		}
		result, err = loadTrade(ctx, x.q, trade.ID, false, false)
		if err != nil {
			return err
		}
		return x.completeCommand(ctx, command, cleanTradeSnapshot(result))
	})
	return result, mapError(err)
}

// AddDisputeStatement appends a participant statement to a disputed trade.
func (s *Store) AddDisputeStatement(ctx context.Context, command c2c.Command, tradeID string, statement c2c.NewStatement) (c2c.Trade, error) {
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
		if !tradeParticipant(command, trade) {
			return c2c.ErrNotFound
		}
		if trade.Status != c2c.TradeDisputed {
			return c2c.ErrConflict
		}
		if err := x.insertDisputeStatement(ctx, command, trade.ID, statement); err != nil {
			return err
		}
		if err := x.q.TouchTrade(ctx, TouchTradeParams{ID: trade.ID, UpdatedAt: command.Now}); err != nil {
			return mapError(err)
		}
		if err := x.insertEvent(ctx, command, order.ID, trade.ID, "trade.statement_added", "participant added C2C dispute statement", "", ""); err != nil {
			return err
		}
		result, err = loadTrade(ctx, x.q, trade.ID, false, false)
		if err != nil {
			return err
		}
		return x.completeCommand(ctx, command, cleanTradeSnapshot(result))
	})
	return result, mapError(err)
}

// ResolveDispute applies an administrator decision to a paid or disputed trade:
// release to the buyer, return to the seller, extend the review window, or
// restrict one party's credit.
func (s *Store) ResolveDispute(ctx context.Context, command c2c.Command, tradeID string, action c2c.ResolutionAction, reason string, now time.Time) (c2c.Trade, error) {
	var result c2c.Trade
	err := s.inTx(ctx, func(x *session) error {
		if err := x.lockTradeKeys(ctx, command.Actor.ID, tradeID); err != nil {
			return err
		}
		if err := x.lockActor(ctx, command, true); err != nil {
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
		if (action == c2c.ResolutionExtend && trade.Status != c2c.TradeDisputed) ||
			(action != c2c.ResolutionExtend && trade.Status != c2c.TradePaid && trade.Status != c2c.TradeDisputed) {
			return c2c.ErrConflict
		}
		switch action {
		case c2c.ResolutionRelease:
			result, err = x.captureTrade(ctx, command, order, trade, "dispute.released", reason)
		case c2c.ResolutionReturn:
			result, err = x.returnAllocation(ctx, command, order, trade, c2c.TradeReturnedToSeller, "dispute.returned", reason)
		case c2c.ResolutionExtend:
			base := now
			if trade.ReviewDueAt != nil && trade.ReviewDueAt.After(base) {
				base = *trade.ReviewDueAt
			}
			reviewDue := base.Add(c2c.ReviewExtension)
			affected, updateErr := x.q.ExtendTradeReview(ctx, ExtendTradeReviewParams{ID: trade.ID, ReviewDueAt: &reviewDue, UpdatedAt: command.Now})
			if updateErr != nil {
				return mapError(updateErr)
			}
			if affected != 1 {
				return c2c.ErrConflict
			}
			if err := x.insertEvent(ctx, command, order.ID, trade.ID, "dispute.extended", reason, "", ""); err != nil {
				return err
			}
			result, err = loadTrade(ctx, x.q, trade.ID, false, false)
		case c2c.ResolutionRestrictBuyer:
			result, err = x.restrictParty(ctx, command, order, trade, "buyer", trade.BuyerAccountID, reason)
		case c2c.ResolutionRestrictSeller:
			result, err = x.restrictParty(ctx, command, order, trade, "seller", trade.SellerAccountID, reason)
		default:
			return c2c.ErrInvalidInput
		}
		if err != nil {
			return err
		}
		return x.completeCommand(ctx, command, cleanTradeSnapshot(result))
	})
	return result, mapError(err)
}

// restrictParty freezes one trade party's credit as part of dispute
// handling. The caller already holds the stable per-account advisory keys for
// the actor, buyer, and seller plus the order and trade row locks; this then
// follows the account-policy order (ledger account row, then identity row) used
// by UpdateAccount. The trade row, its status, and its ledger holds are left
// untouched. An already frozen party is not updated again, but the decision is
// still recorded on the trade timeline and in the audit log.
func (x *session) restrictParty(ctx context.Context, command c2c.Command, order c2c.Order, trade c2c.Trade, party, accountID, reason string) (c2c.Trade, error) {
	if _, err := x.q.LockLedgerAccountByIdentity(ctx, accountID); err != nil {
		return c2c.Trade{}, mapError(err)
	}
	account, err := x.q.LockAccountForUpdate(ctx, accountID)
	if err != nil {
		return c2c.Trade{}, mapError(err)
	}
	version := account.Version
	if !account.CreditFrozen {
		version, err = x.q.FreezeAccountCredit(ctx, FreezeAccountCreditParams{ID: accountID, Version: version, UpdatedAt: command.Now})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return c2c.Trade{}, c2c.ErrConflict
			}
			return c2c.Trade{}, mapError(err)
		}
	}
	details := map[string]any{
		"trade_id": trade.ID, "party": party, "credit_frozen": true,
		"changed": !account.CreditFrozen, "version": version,
	}
	if err := auditpg.Record(ctx, x.tx, auditpg.Event{
		ActorID: command.Actor.ID, Action: "c2c.dispute.party_restricted", TargetType: "account",
		TargetID: accountID, Reason: reason, Details: details,
	}); err != nil {
		return c2c.Trade{}, err
	}
	if err := x.insertEvent(ctx, command, order.ID, trade.ID, "dispute."+party+"_restricted", reason, "", ""); err != nil {
		return c2c.Trade{}, err
	}
	return loadTrade(ctx, x.q, trade.ID, false, false)
}
