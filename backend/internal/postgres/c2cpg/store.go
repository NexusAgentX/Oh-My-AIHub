// Package c2cpg is the PostgreSQL persistence of the C2C sell-order market
// (ADR-0025). Each state transition runs in one database transaction that
// locks the order row, then the trade row, then the ledger accounts, always in
// that order so concurrent requests cannot deadlock; the pure rules live in
// internal/c2c and the points move through ledgerpg.Post in the same
// transaction.
package c2cpg

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

var _ c2c.Store = (*Store)(nil)

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: New(pool)} }

func (s *Store) inTx(ctx context.Context, fn func(q *Queries, tx pgx.Tx) error) error {
	return pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error { return fn(s.q.WithTx(tx), tx) })
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return c2c.ErrNotFound
	}
	return err
}

// --- mapping ---

type orderRow = GetOrderRow

func orderOf(row orderRow) c2c.Order {
	o := row.C2cOrder
	return c2c.Order{
		ID: o.ID, Seller: c2c.Party{ID: o.SellerID, DisplayName: row.SellerName},
		Total: o.TotalNano, Available: o.AvailableNano, InTrade: o.InTradeNano, Sold: o.SoldNano, Closed: o.ClosedNano,
		UnitPriceFen: o.UnitPriceFen, MinPerTrade: o.MinPerTradeNano, MaxPerTrade: o.MaxPerTradeNano,
		Methods: c2c.EncryptedValue{KeyID: o.PaymentMethodsKeyID, Nonce: o.PaymentMethodsNonce, Ciphertext: o.PaymentMethodsCiphertext},
		Status:  c2c.OrderStatus(o.Status), CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt, ClosedAt: o.ClosedAt,
	}
}

type tradeRow = GetTradeRow

func tradeOf(row tradeRow) c2c.Trade {
	t := row.C2cTrade
	return c2c.Trade{
		ID: t.ID, OrderID: t.OrderID,
		Buyer: c2c.Party{ID: t.BuyerID, DisplayName: row.BuyerName}, Seller: c2c.Party{ID: t.SellerID, DisplayName: row.SellerName},
		Amount: t.AmountNano, UnitPriceFen: t.UnitPriceFen, TotalFen: t.TotalFen, Status: c2c.TradeStatus(t.Status),
		PaymentDeadline: t.PaymentDeadline,
		Methods:         c2c.EncryptedValue{KeyID: row.PaymentMethodsKeyID, Nonce: row.PaymentMethodsNonce, Ciphertext: row.PaymentMethodsCiphertext},
		BuyerNote:       t.BuyerNote, DisputeOpenedBy: t.DisputeOpenedBy, BuyerStatement: t.BuyerStatement,
		SellerStatement: t.SellerStatement, ResolutionReason: t.ResolutionReason, CreatedAt: t.CreatedAt,
		PaidAt: t.PaidAt, ReleasedAt: t.ReleasedAt, CancelledAt: t.CancelledAt, DisputedAt: t.DisputedAt, ResolvedAt: t.ResolvedAt,
	}
}

func cursorArgs(after *c2c.Cursor) (has bool, price int64, at time.Time, id string) {
	if after == nil {
		return false, 0, time.Time{}, "00000000-0000-0000-0000-000000000000"
	}
	return true, after.Price, after.Time, after.ID
}

// --- orders ---

func (s *Store) CreateOrder(ctx context.Context, n c2c.NewOrder) (c2c.Order, error) {
	var result c2c.Order
	err := s.inTx(ctx, func(q *Queries, tx pgx.Tx) error {
		if existing, err := q.GetOrder(ctx, n.ID); err == nil {
			if existing.C2cOrder.SellerID != n.SellerID {
				return c2c.ErrNotFound
			}
			result = orderOf(existing)
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		status, err := q.AccountStatus(ctx, n.SellerID)
		if err != nil {
			return notFound(err)
		}
		if status != "active" {
			return c2c.ErrAccountInactive
		}
		// Only a positive balance can be sold, never credit: lock the ledger
		// account before checking so two listings cannot both pass.
		balance, err := ledgerpg.LockBalance(ctx, tx, n.SellerID)
		if err != nil {
			return err
		}
		if balance < n.Total {
			return c2c.ErrInsufficientBalance
		}
		if _, err := q.InsertOrder(ctx, InsertOrderParams{
			ID: n.ID, SellerID: n.SellerID, TotalNano: n.Total, UnitPriceFen: n.UnitPriceFen, MinPerTradeNano: n.MinPerTrade,
			MaxPerTradeNano: n.MaxPerTrade, PaymentMethodsCiphertext: n.Methods.Ciphertext, PaymentMethodsNonce: n.Methods.Nonce,
			PaymentMethodsKeyID: n.Methods.KeyID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return c2c.ErrInvalidState
			}
			return err
		}
		if _, err := ledgerpg.Post(ctx, tx, ledger.Transaction{
			Type: ledger.TypeC2CList, IdempotencyKey: "c2c:order:" + n.ID + ":list",
			Related: &ledger.Related{Type: "c2c_order", ID: n.ID}, ActorID: n.SellerID, Reason: "C2C 挂卖单",
			Entries: []ledger.Line{
				{Account: ledger.User(n.SellerID), Amount: -n.Total},
				{Account: ledger.System(ledger.SystemC2CEscrow), Amount: n.Total},
			},
		}); err != nil {
			return err
		}
		created, err := q.GetOrder(ctx, n.ID)
		result = orderOf(created)
		return err
	})
	return result, err
}

func (s *Store) ListMarket(ctx context.Context, viewerID string, after *c2c.Cursor, limit int) ([]c2c.Order, error) {
	has, price, at, id := cursorArgs(after)
	rows, err := s.q.ListMarketOrders(ctx, ListMarketOrdersParams{
		ViewerID: viewerID, HasCursor: has, AfterPrice: price, AfterTime: at, AfterID: id, RowLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	orders := make([]c2c.Order, 0, len(rows))
	for _, row := range rows {
		orders = append(orders, orderOf(orderRow(row)))
	}
	return orders, nil
}

func (s *Store) ListSellerOrders(ctx context.Context, sellerID string, status c2c.OrderStatus, after *c2c.Cursor, limit int) ([]c2c.Order, error) {
	has, _, at, id := cursorArgs(after)
	rows, err := s.q.ListSellerOrders(ctx, ListSellerOrdersParams{
		SellerID: sellerID, Status: string(status), HasCursor: has, AfterTime: at, AfterID: id, RowLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	orders := make([]c2c.Order, 0, len(rows))
	for _, row := range rows {
		orders = append(orders, orderOf(orderRow(row)))
	}
	return orders, nil
}

func (s *Store) CloseOrder(ctx context.Context, orderID, sellerID string) (c2c.Order, error) {
	var result c2c.Order
	err := s.inTx(ctx, func(q *Queries, tx pgx.Tx) error {
		locked, err := q.LockOrder(ctx, orderID)
		if err != nil {
			return notFound(err)
		}
		order := orderOf(orderRow(locked))
		switch {
		case order.Seller.ID != sellerID:
			return c2c.ErrForbidden
		case order.Status == c2c.OrderClosed: // repeated request
			result = order
			return nil
		case order.Status != c2c.OrderOpen:
			return c2c.ErrInvalidState
		}
		closed, refund := order.Close()
		if refund > 0 {
			if _, err := postReturn(ctx, tx, "c2c:order:"+orderID+":close", "c2c_order", orderID, sellerID, refund, "C2C 关闭卖单，剩余积分退回"); err != nil {
				return err
			}
		}
		if err := saveOrder(ctx, q, closed); err != nil {
			return err
		}
		reloaded, err := q.GetOrder(ctx, orderID)
		result = orderOf(reloaded)
		return err
	})
	return result, err
}

func saveOrder(ctx context.Context, q *Queries, o c2c.Order) error {
	return q.UpdateOrder(ctx, UpdateOrderParams{
		ID: o.ID, AvailableNano: o.Available, InTradeNano: o.InTrade, SoldNano: o.Sold, ClosedNano: o.Closed, Status: string(o.Status),
	})
}

// postReturn books escrow -> seller.
func postReturn(ctx context.Context, tx pgx.Tx, key, relatedType, relatedID, sellerID string, amount money.Amount, reason string) (string, error) {
	posted, err := ledgerpg.Post(ctx, tx, ledger.Transaction{
		Type: ledger.TypeC2CReturn, IdempotencyKey: key, Related: &ledger.Related{Type: relatedType, ID: relatedID},
		ActorID: sellerID, Reason: reason,
		Entries: []ledger.Line{
			{Account: ledger.System(ledger.SystemC2CEscrow), Amount: -amount},
			{Account: ledger.User(sellerID), Amount: amount},
		},
	})
	return posted.ID, err
}

// --- trades ---

func (s *Store) CreateTrade(ctx context.Context, n c2c.NewTrade) (c2c.Trade, error) {
	var result c2c.Trade
	err := s.inTx(ctx, func(q *Queries, tx pgx.Tx) error {
		locked, err := q.LockOrder(ctx, n.OrderID)
		if err != nil {
			return notFound(err)
		}
		if existing, err := q.GetTrade(ctx, n.ID); err == nil { // repeated request
			if existing.C2cTrade.BuyerID != n.BuyerID || existing.C2cTrade.OrderID != n.OrderID {
				return c2c.ErrNotFound
			}
			result = tradeOf(existing)
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		status, err := q.AccountStatus(ctx, n.BuyerID)
		if err != nil {
			return notFound(err)
		}
		if status != "active" {
			return c2c.ErrAccountInactive
		}
		order := orderOf(orderRow(locked))
		if err := order.CheckBuy(n.BuyerID, n.Amount); err != nil {
			return err
		}
		open, err := q.CountUnfinishedTrades(ctx, CountUnfinishedTradesParams{OrderID: n.OrderID, BuyerID: n.BuyerID})
		if err != nil {
			return err
		}
		if open > 0 {
			return c2c.ErrDuplicateTrade
		}
		totalFen, err := c2c.TotalFen(n.Amount, order.UnitPriceFen)
		if err != nil {
			return err
		}
		if _, err := q.InsertTrade(ctx, InsertTradeParams{
			ID: n.ID, OrderID: n.OrderID, BuyerID: n.BuyerID, SellerID: order.Seller.ID, AmountNano: n.Amount,
			UnitPriceFen: order.UnitPriceFen, TotalFen: totalFen,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return c2c.ErrInvalidState
			}
			return err
		}
		if err := saveOrder(ctx, q, order.Reserve(n.Amount)); err != nil {
			return err
		}
		created, err := q.GetTrade(ctx, n.ID)
		result = tradeOf(created)
		return err
	})
	return result, err
}

func (s *Store) GetTrade(ctx context.Context, tradeID string) (c2c.Trade, error) {
	row, err := s.q.GetTrade(ctx, tradeID)
	if err != nil {
		return c2c.Trade{}, notFound(err)
	}
	return tradeOf(row), nil
}

func (s *Store) ListTrades(ctx context.Context, f c2c.TradeFilter) ([]c2c.Trade, error) {
	has, _, at, id := cursorArgs(f.After)
	rows, err := s.q.ListUserTrades(ctx, ListUserTradesParams{
		Role: f.Role, UserID: f.UserID, Status: string(f.Status), Pending: f.Pending,
		HasCursor: has, AfterTime: at, AfterID: id, RowLimit: int32(f.Limit),
	})
	if err != nil {
		return nil, err
	}
	trades := make([]c2c.Trade, 0, len(rows))
	for _, row := range rows {
		trades = append(trades, tradeOf(tradeRow(row)))
	}
	return trades, nil
}

func (s *Store) ListDisputes(ctx context.Context, status c2c.TradeStatus, after *c2c.Cursor, limit int) ([]c2c.Trade, error) {
	has, _, at, id := cursorArgs(after)
	rows, err := s.q.ListDisputedTrades(ctx, ListDisputedTradesParams{
		Status: string(status), HasCursor: has, AfterTime: at, AfterID: id, RowLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	trades := make([]c2c.Trade, 0, len(rows))
	for _, row := range rows {
		trades = append(trades, tradeOf(tradeRow(row)))
	}
	return trades, nil
}

func (s *Store) DueTrades(ctx context.Context, limit int) ([]string, error) {
	return s.q.ListDueTrades(ctx, int32(limit))
}

// lockedTrade takes the order lock before the trade lock, then reloads both.
func lockedTrade(ctx context.Context, q *Queries, tradeID string) (c2c.Order, c2c.Trade, error) {
	orderID, err := q.OrderIDOfTrade(ctx, tradeID)
	if err != nil {
		return c2c.Order{}, c2c.Trade{}, notFound(err)
	}
	lockedOrder, err := q.LockOrder(ctx, orderID)
	if err != nil {
		return c2c.Order{}, c2c.Trade{}, notFound(err)
	}
	if _, err := q.LockTrade(ctx, tradeID); err != nil {
		return c2c.Order{}, c2c.Trade{}, notFound(err)
	}
	row, err := q.GetTrade(ctx, tradeID)
	if err != nil {
		return c2c.Order{}, c2c.Trade{}, notFound(err)
	}
	return orderOf(orderRow(lockedOrder)), tradeOf(row), nil
}

func (s *Store) transition(ctx context.Context, tradeID string, apply func(q *Queries, tx pgx.Tx, order c2c.Order, trade c2c.Trade) (done bool, err error)) (c2c.Trade, error) {
	var result c2c.Trade
	err := s.inTx(ctx, func(q *Queries, tx pgx.Tx) error {
		order, trade, err := lockedTrade(ctx, q, tradeID)
		if err != nil {
			return err
		}
		changed, err := apply(q, tx, order, trade)
		if err != nil {
			return err
		}
		if !changed {
			result = trade
			return nil
		}
		reloaded, err := q.GetTrade(ctx, tradeID)
		result = tradeOf(reloaded)
		return err
	})
	return result, err
}

func (s *Store) MarkPaid(ctx context.Context, tradeID, buyerID, note string) (c2c.Trade, error) {
	return s.transition(ctx, tradeID, func(q *Queries, _ pgx.Tx, _ c2c.Order, trade c2c.Trade) (bool, error) {
		noop, err := trade.CheckPaid(buyerID, time.Now())
		if err != nil || noop {
			return false, err
		}
		var text *string
		if note != "" {
			text = &note
		}
		return true, q.MarkTradePaid(ctx, MarkTradePaidParams{ID: tradeID, Note: text})
	})
}

func (s *Store) Release(ctx context.Context, tradeID, sellerID string) (c2c.Trade, error) {
	return s.transition(ctx, tradeID, func(q *Queries, tx pgx.Tx, order c2c.Order, trade c2c.Trade) (bool, error) {
		noop, err := trade.CheckRelease(sellerID)
		if err != nil || noop {
			return false, err
		}
		return true, settle(ctx, q, tx, order, trade, c2c.TradeReleased, "", "", "C2C 卖家放行")
	})
}

func (s *Store) Cancel(ctx context.Context, tradeID, actorID string) (c2c.Trade, error) {
	return s.transition(ctx, tradeID, func(q *Queries, tx pgx.Tx, order c2c.Order, trade c2c.Trade) (bool, error) {
		noop, err := trade.CheckCancel(actorID, time.Now())
		if err != nil || noop {
			return false, err
		}
		return true, unlock(ctx, q, tx, order, trade, c2c.TradeCancelled, "", "", "C2C 交易取消，积分退回")
	})
}

func (s *Store) Dispute(ctx context.Context, tradeID, actorID, statement string) (c2c.Trade, error) {
	return s.transition(ctx, tradeID, func(q *Queries, _ pgx.Tx, _ c2c.Order, trade c2c.Trade) (bool, error) {
		if err := trade.CheckDispute(actorID); err != nil {
			return false, err
		}
		return true, q.SetDisputeStatement(ctx, SetDisputeStatementParams{
			ID: tradeID, ActorID: &actorID, IsBuyer: actorID == trade.Buyer.ID, Statement: statement,
		})
	})
}

func (s *Store) Resolve(ctx context.Context, r c2c.Resolution) (c2c.Trade, error) {
	return s.transition(ctx, r.TradeID, func(q *Queries, tx pgx.Tx, order c2c.Order, trade c2c.Trade) (bool, error) {
		noop, err := trade.CheckResolve(r.ToBuyer)
		if err != nil || noop {
			return false, err
		}
		result := "to_seller"
		if r.ToBuyer {
			result = "to_buyer"
			err = settle(ctx, q, tx, order, trade, c2c.TradeResolvedBuyer, r.AdminID, r.Reason, "C2C 仲裁：判给买家")
		} else {
			err = unlock(ctx, q, tx, order, trade, c2c.TradeResolvedSeller, r.AdminID, r.Reason, "C2C 仲裁：退回卖家")
		}
		if err != nil {
			return false, err
		}
		return true, auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: r.AdminID, Action: audit.ActionC2CResolve, TargetType: "c2c_trade", TargetID: trade.ID, Reason: r.Reason,
			Detail: map[string]any{"result": result, "order_id": trade.OrderID, "amount": trade.Amount.String(), "buyer_id": trade.Buyer.ID, "seller_id": trade.Seller.ID},
		})
	})
}

// settle hands the escrowed points of a trade to the buyer.
func settle(ctx context.Context, q *Queries, tx pgx.Tx, order c2c.Order, trade c2c.Trade, status c2c.TradeStatus, resolvedBy, reason, ledgerReason string) error {
	posted, err := ledgerpg.Post(ctx, tx, ledger.Transaction{
		Type: ledger.TypeC2CRelease, IdempotencyKey: "c2c:trade:" + trade.ID + ":release",
		Related: &ledger.Related{Type: "c2c_trade", ID: trade.ID}, ActorID: actorOr(resolvedBy, trade.Seller.ID), Reason: ledgerReason,
		Entries: []ledger.Line{
			{Account: ledger.System(ledger.SystemC2CEscrow), Amount: -trade.Amount},
			{Account: ledger.User(trade.Buyer.ID), Amount: trade.Amount},
		},
	})
	if err != nil {
		return err
	}
	if err := saveOrder(ctx, q, order.Settle(trade.Amount)); err != nil {
		return err
	}
	return finish(ctx, q, trade.ID, status, &posted.ID, resolvedBy, reason)
}

// unlock puts the points of a trade back: into the order while it is open,
// otherwise straight back to the seller.
func unlock(ctx context.Context, q *Queries, tx pgx.Tx, order c2c.Order, trade c2c.Trade, status c2c.TradeStatus, resolvedBy, reason, ledgerReason string) error {
	updated, refund := order.Unlock(trade.Amount)
	var ledgerTx *string
	if refund {
		id, err := postReturn(ctx, tx, "c2c:trade:"+trade.ID+":return", "c2c_trade", trade.ID, trade.Seller.ID, trade.Amount, ledgerReason)
		if err != nil {
			return err
		}
		ledgerTx = &id
	}
	if err := saveOrder(ctx, q, updated); err != nil {
		return err
	}
	return finish(ctx, q, trade.ID, status, ledgerTx, resolvedBy, reason)
}

func finish(ctx context.Context, q *Queries, tradeID string, status c2c.TradeStatus, ledgerTx *string, resolvedBy, reason string) error {
	params := FinishTradeParams{ID: tradeID, Status: string(status), LedgerTxID: ledgerTx}
	if resolvedBy != "" {
		params.ResolvedBy = &resolvedBy
		params.ResolutionReason = &reason
	}
	return q.FinishTrade(ctx, params)
}

func actorOr(actor, fallback string) string {
	if actor != "" {
		return actor
	}
	return fallback
}
