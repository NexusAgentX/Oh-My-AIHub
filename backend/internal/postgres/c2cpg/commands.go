package c2cpg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
)

// session is one open command transaction. The queries and the ledger service
// are bound to the same pgx.Tx, so C2C rows and ledger holds/transfers commit
// together (ADR-0020).
type session struct {
	tx     pgx.Tx
	q      *Queries
	ledger *ledger.Service
}

// inTx runs work in one database transaction with the pool's default isolation
// level. A commit failure is mapped through the ledger error mapping first (so
// deferred ledger and C2C constraint violations keep their meaning) and then to
// a C2C error; the work error is mapped by the caller.
func (s *Store) inTx(ctx context.Context, work func(*session) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := work(&session{tx: tx, q: s.q.WithTx(tx), ledger: ledger.NewService(ledgerpg.NewTx(tx))}); err != nil {
		return err
	}
	return ledgerpg.MapError(tx.Commit(ctx))
}

// ---------------------------------------------------------------------------
// Command bookkeeping
// ---------------------------------------------------------------------------

func actorKey(command c2c.Command) (string, *string) {
	if command.Actor.ID == "" {
		return "system:timeout", nil
	}
	return command.Actor.ID, &command.Actor.ID
}

func (x *session) reserveCommand(ctx context.Context, command c2c.Command) (json.RawMessage, bool, error) {
	key, actorID := actorKey(command)
	inserted, err := x.q.ReserveCommand(ctx, ReserveCommandParams{
		ActorKey: key, ActorAccountID: actorID, Operation: command.Operation,
		IdempotencyKey: command.IdempotencyKey, PayloadHash: command.PayloadHash[:], CreatedAt: command.Now,
	})
	if err != nil {
		return nil, false, mapError(err)
	}
	if inserted == 1 {
		return nil, false, nil
	}
	existing, err := x.q.GetCommandForUpdate(ctx, GetCommandForUpdateParams{
		ActorKey: key, Operation: command.Operation, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return nil, false, mapError(err)
	}
	if !bytes.Equal(existing.PayloadHash, command.PayloadHash[:]) || existing.CompletedAt == nil || len(existing.ResultPayload) == 0 {
		return nil, false, c2c.ErrConflict
	}
	return existing.ResultPayload, true, nil
}

func (x *session) completeCommand(ctx context.Context, command c2c.Command, snapshot any) error {
	key, _ := actorKey(command)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	affected, err := x.q.CompleteCommand(ctx, CompleteCommandParams{
		ResultPayload: encoded, CompletedAt: &command.Now,
		ActorKey: key, Operation: command.Operation, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return mapError(err)
	}
	if affected != 1 {
		return c2c.ErrConflict
	}
	return nil
}

func decodeSnapshot[T any](snapshot json.RawMessage) (T, error) {
	var result T
	if err := json.Unmarshal(snapshot, &result); err != nil {
		return result, fmt.Errorf("decode C2C command snapshot: %w", err)
	}
	return result, nil
}

// derivedLedgerKey makes the ledger idempotency key of one step deterministic
// from the C2C command, so a replayed command cannot post twice.
func derivedLedgerKey(command c2c.Command, suffix string) string {
	key, _ := actorKey(command)
	digest := sha256.Sum256([]byte(strings.Join([]string{
		key, command.Operation, command.IdempotencyKey, suffix,
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func (x *session) insertEvent(ctx context.Context, command c2c.Command, orderID, tradeID, action, reason, ledgerTransactionID, holdBusinessID string) error {
	params := InsertEventParams{OrderID: orderID, Action: action, Reason: reason, CreatedAt: command.Now}
	if command.Actor.ID != "" {
		params.ActorAccountID = &command.Actor.ID
	}
	if tradeID != "" {
		params.TradeID = &tradeID
	}
	if ledgerTransactionID != "" {
		params.LedgerTransactionID = &ledgerTransactionID
	}
	if holdBusinessID != "" {
		params.HoldBusinessID = &holdBusinessID
	}
	return mapError(x.q.InsertEvent(ctx, params))
}

// ---------------------------------------------------------------------------
// Locks and gates
// ---------------------------------------------------------------------------

// lockActor takes the actor's per-account advisory key and checks that the
// actor may issue commands. The system timeout actor needs no account.
func (x *session) lockActor(ctx context.Context, command c2c.Command, requireAdmin bool) error {
	if command.Actor.ID == "" {
		if command.Operation == "c2c.trade.expire" && !requireAdmin {
			return nil
		}
		return c2c.ErrForbidden
	}
	if err := x.lockAccountKeys(ctx, command.Actor.ID); err != nil {
		return err
	}
	gate, err := x.q.GetAccountGate(ctx, command.Actor.ID)
	if err != nil {
		return mapError(err)
	}
	if gate.Status != string(identity.StatusActive) || gate.MustChangePassword || (requireAdmin && !gate.IsAdmin) {
		return c2c.ErrForbidden
	}
	return nil
}

// lockAccountKeys takes the per-account advisory keys in sorted order so
// concurrent commands over the same accounts cannot deadlock.
func (x *session) lockAccountKeys(ctx context.Context, accountIDs ...string) error {
	ordered := make([]string, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID != "" {
			ordered = append(ordered, accountID)
		}
	}
	slices.Sort(ordered)
	ordered = slices.Compact(ordered)
	for _, accountID := range ordered {
		if err := x.q.LockAccountMutationKey(ctx, accountID); err != nil {
			return err
		}
	}
	return nil
}

func (x *session) lockOrderKeys(ctx context.Context, actorID, orderID string) error {
	ownerID, err := x.q.GetOrderOwnerID(ctx, orderID)
	if err != nil {
		return mapError(err)
	}
	return x.lockAccountKeys(ctx, actorID, ownerID)
}

func (x *session) lockTradeKeys(ctx context.Context, actorID, tradeID string) error {
	parties, err := x.q.GetTradeParties(ctx, tradeID)
	if err != nil {
		return mapError(err)
	}
	return x.lockAccountKeys(ctx, actorID, parties.BuyerAccountID, parties.SellerAccountID)
}

// lockOrderAndTrade locks the order row, then the trade row.
func (x *session) lockOrderAndTrade(ctx context.Context, tradeID string) (c2c.Order, c2c.Trade, error) {
	orderID, err := x.q.GetTradeOrderID(ctx, tradeID)
	if err != nil {
		return c2c.Order{}, c2c.Trade{}, mapError(err)
	}
	order, err := loadOrder(ctx, x.q, orderID, true)
	if err != nil {
		return c2c.Order{}, c2c.Trade{}, err
	}
	trade, err := loadTrade(ctx, x.q, tradeID, true, false)
	if err != nil {
		return c2c.Order{}, c2c.Trade{}, err
	}
	if trade.OrderID != order.ID {
		return c2c.Order{}, c2c.Trade{}, c2c.ErrConflict
	}
	return order, trade, nil
}

// ensureCreditActive blocks new C2C exposure (publishing or taking an order)
// for an account whose credit is frozen. Commands on existing trades do not
// call it, so a restricted party can still finish or dispute them.
func (x *session) ensureCreditActive(ctx context.Context, accountID string) error {
	gate, err := x.q.GetAccountGate(ctx, accountID)
	if err != nil {
		return mapError(err)
	}
	if gate.CreditFrozen {
		return ledger.ErrCreditFrozen
	}
	return nil
}

// ensureOrderOwnerReady must match the takeable condition in queries.sql.
func (x *session) ensureOrderOwnerReady(ctx context.Context, order c2c.Order) error {
	gate, err := x.q.GetAccountGate(ctx, order.OwnerAccountID)
	if err != nil {
		return mapError(err)
	}
	if gate.Status != string(identity.StatusActive) || gate.MustChangePassword || gate.CreditFrozen {
		return c2c.ErrConflict
	}
	return nil
}

// ---------------------------------------------------------------------------
// Order and trade state
// ---------------------------------------------------------------------------

func orderStatusAfter(order c2c.Order) (c2c.OrderStatus, error) {
	if order.Status == c2c.OrderCancelled {
		return c2c.OrderCancelled, nil
	}
	if order.Available > 0 {
		return c2c.OrderOpen, nil
	}
	if order.Allocated > 0 {
		return c2c.OrderAllocated, nil
	}
	if order.Settled == order.Total {
		return c2c.OrderFilled, nil
	}
	return "", c2c.ErrConflict
}

func (x *session) persistOrderAmounts(ctx context.Context, order c2c.Order, now time.Time) error {
	status, err := orderStatusAfter(order)
	if err != nil {
		return err
	}
	affected, err := x.q.UpdateOrderAmounts(ctx, UpdateOrderAmountsParams{
		ID: order.ID, AvailableNano: order.Available, AllocatedNano: order.Allocated,
		SettledNano: order.Settled, ClosedNano: order.Closed, Status: string(status), UpdatedAt: now,
	})
	if err != nil {
		return mapError(err)
	}
	if affected != 1 {
		return c2c.ErrConflict
	}
	return nil
}
