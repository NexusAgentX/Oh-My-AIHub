package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

type c2cFixture struct {
	t       *testing.T
	pool    *pgxpool.Pool
	store   *storepg.Store
	service *c2c.Service
	admin   identity.Account
	members []identity.AdminAccount
	funding int
}

// newC2CFixture creates an administrator and members with the given credit
// limits; every member has balance 0 until fund is called.
func newC2CFixture(t *testing.T, credits ...string) *c2cFixture {
	t.Helper()
	pool, store := isolatedDatabase(t)
	_, admin, members := accounts(t, store, credits...)
	keyring, err := c2c.ParseKeyring("k1=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", "k1")
	if err != nil {
		t.Fatal(err)
	}
	return &c2cFixture{t: t, pool: pool, store: store, service: c2c.NewService(store.C2C, keyring), admin: admin, members: members}
}

func (f *c2cFixture) fund(member int, amount string) {
	f.t.Helper()
	f.funding++
	_, err := f.store.Ledger.Adjust(context.Background(), ledger.Adjustment{
		ActorID: f.admin.ID, AccountID: f.members[member].ID, Amount: mustAmount(f.t, amount), Reason: "充值",
		IdempotencyKey: fmt.Sprintf("fund-%d", f.funding),
	})
	if err != nil {
		f.t.Fatalf("fund: %v", err)
	}
}

func (f *c2cFixture) balance(member int) money.Amount {
	return balanceOf(f.t, f.pool, ledger.User(f.members[member].ID))
}

func (f *c2cFixture) escrow() money.Amount {
	return balanceOf(f.t, f.pool, ledger.System(ledger.SystemC2CEscrow))
}

var alipay = []c2c.PaymentMethod{{Channel: "支付宝", Account: "seller@example.com 备注 AIHub"}, {Channel: "微信", Account: "wx-seller"}}

func (f *c2cFixture) list(seller int, amount string, priceFen int64) c2c.MyOrder {
	f.t.Helper()
	min := mustAmount(f.t, "1")
	order, err := f.service.CreateOrder(context.Background(), c2c.CreateOrderInput{
		SellerID: f.members[seller].ID, Amount: mustAmount(f.t, amount), UnitPriceFen: priceFen, MinPerTrade: &min, PaymentMethods: alipay,
	})
	if err != nil {
		f.t.Fatalf("list: %v", err)
	}
	return order
}

func (f *c2cFixture) buy(buyer int, orderID, amount string) (c2c.TradeView, error) {
	return f.service.CreateTrade(context.Background(), orderID, f.members[buyer].ID, mustAmount(f.t, amount), "")
}

func (f *c2cFixture) mustBuy(buyer int, orderID, amount string) c2c.TradeView {
	f.t.Helper()
	trade, err := f.buy(buyer, orderID, amount)
	if err != nil {
		f.t.Fatalf("buy: %v", err)
	}
	return trade
}

func (f *c2cFixture) order(orderID string) c2c.Order {
	f.t.Helper()
	var seller string
	if err := f.pool.QueryRow(context.Background(), `SELECT seller_id FROM c2c_orders WHERE id = $1`, orderID).Scan(&seller); err != nil {
		f.t.Fatal(err)
	}
	orders, err := f.store.C2C.ListSellerOrders(context.Background(), seller, "", nil, 100)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, order := range orders {
		if order.ID == orderID {
			return order
		}
	}
	f.t.Fatalf("order %s not found", orderID)
	return c2c.Order{}
}

// assertInvariants checks the properties that must hold after every path:
// all ledger accounts sum to zero, the escrow balance equals the open
// quantities of all orders, and in_trade equals the unfinished trades.
func (f *c2cFixture) assertInvariants() {
	f.t.Helper()
	ctx := context.Background()
	var sum, escrow, held, inTrade, unfinished int64
	if err := f.pool.QueryRow(ctx, `SELECT COALESCE(sum(balance_nano), 0) FROM ledger_accounts`).Scan(&sum); err != nil {
		f.t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT balance_nano FROM ledger_accounts WHERE system_code = 'c2c_escrow'`).Scan(&escrow); err != nil {
		f.t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT COALESCE(sum(available_nano + in_trade_nano), 0), COALESCE(sum(in_trade_nano), 0) FROM c2c_orders`).Scan(&held, &inTrade); err != nil {
		f.t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT COALESCE(sum(amount_nano), 0) FROM c2c_trades WHERE status IN ('awaiting_payment', 'paid', 'disputed')`).Scan(&unfinished); err != nil {
		f.t.Fatal(err)
	}
	if sum != 0 {
		f.t.Fatalf("ledger accounts sum to %d, want 0", sum)
	}
	if escrow != held {
		f.t.Fatalf("escrow %d != available + in_trade of all orders %d", escrow, held)
	}
	if inTrade != unfinished {
		f.t.Fatalf("in_trade %d != unfinished trades %d", inTrade, unfinished)
	}
}

func (f *c2cFixture) ledgerTypes(relatedID string) []string {
	f.t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT type FROM ledger_transactions WHERE related_id = $1 ORDER BY created_at, id`, relatedID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var types []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			f.t.Fatal(err)
		}
		types = append(types, kind)
	}
	return types
}

func (f *c2cFixture) expireDeadline(tradeID string) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(), `UPDATE c2c_trades SET payment_deadline = now() - interval '1 minute' WHERE id = $1`, tradeID); err != nil {
		f.t.Fatal(err)
	}
}

func amount(t *testing.T, value string) money.Amount { return mustAmount(t, value) }

func TestC2CFullRound(t *testing.T) {
	f := newC2CFixture(t, "0", "0", "0")
	ctx := context.Background()
	f.fund(0, "100")
	order := f.list(0, "40", 92)
	if f.balance(0) != amount(t, "60") || f.escrow() != amount(t, "40") || order.Available != amount(t, "40") || order.Status != c2c.OrderOpen {
		t.Fatalf("after listing: balance %s escrow %s order %+v", f.balance(0), f.escrow(), order)
	}
	f.assertInvariants()

	trade := f.mustBuy(1, order.ID, "30")
	if trade.Status != c2c.TradeAwaitingPayment || trade.ViewerRole != "buyer" || len(trade.PaymentMethods) != 2 || trade.PaymentMethods[0].Account != alipay[0].Account {
		t.Fatalf("trade = %+v", trade)
	}
	// 30 points at 0.92 yuan = 27.60 yuan = 2760 fen.
	if trade.TotalFen != 2760 || trade.UnitPriceFen != 92 {
		t.Fatalf("total fen = %d at %d", trade.TotalFen, trade.UnitPriceFen)
	}
	if got := f.order(order.ID); got.Available != amount(t, "10") || got.InTrade != amount(t, "30") {
		t.Fatalf("order after buy = %+v", got)
	}
	f.assertInvariants()

	paid, err := f.service.MarkPaid(ctx, trade.ID, f.members[1].ID, "已转账")
	if err != nil || paid.Status != c2c.TradePaid || paid.BuyerNote == nil || *paid.BuyerNote != "已转账" || paid.PaidAt == nil {
		t.Fatalf("paid = %+v, %v", paid, err)
	}
	released, err := f.service.Release(ctx, trade.ID, f.members[0].ID)
	if err != nil || released.Status != c2c.TradeReleased || released.ReleasedAt == nil || released.ViewerRole != "seller" || len(released.PaymentMethods) != 2 {
		t.Fatalf("released = %+v, %v", released, err)
	}
	if f.balance(1) != amount(t, "30") || f.balance(0) != amount(t, "60") || f.escrow() != amount(t, "10") {
		t.Fatalf("balances after release: buyer %s seller %s escrow %s", f.balance(1), f.balance(0), f.escrow())
	}
	got := f.order(order.ID)
	if got.Sold != amount(t, "30") || got.InTrade != 0 || got.Available != amount(t, "10") || got.Status != c2c.OrderOpen {
		t.Fatalf("order after release = %+v", got)
	}
	if types := f.ledgerTypes(trade.ID); len(types) != 1 || types[0] != "c2c_release" {
		t.Fatalf("trade ledger types = %v", types)
	}
	f.assertInvariants()

	// The remainder sells out: the order becomes filled and escrow empties.
	last := f.mustBuy(2, order.ID, "10")
	if _, err := f.service.Release(ctx, last.ID, f.members[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := f.order(order.ID); got.Status != c2c.OrderFilled || got.Sold != amount(t, "40") || f.escrow() != 0 {
		t.Fatalf("filled order = %+v escrow %s", got, f.escrow())
	}
	if _, err := f.buy(1, order.ID, "1"); !errors.Is(err, c2c.ErrOrderNotOpen) {
		t.Fatalf("buy on filled order error = %v", err)
	}
	f.assertInvariants()
}

func TestC2CListingRules(t *testing.T) {
	f := newC2CFixture(t, "100", "0")
	ctx := context.Background()
	input := func(seller int, value string) c2c.CreateOrderInput {
		return c2c.CreateOrderInput{SellerID: f.members[seller].ID, Amount: amount(t, value), UnitPriceFen: 90, PaymentMethods: alipay}
	}
	// A credit limit is not sellable: balance 0 with credit 100 cannot list.
	if _, err := f.service.CreateOrder(ctx, input(0, "10")); !errors.Is(err, c2c.ErrInsufficientBalance) {
		t.Fatalf("list with credit only error = %v", err)
	}
	f.fund(0, "10")
	if _, err := f.service.CreateOrder(ctx, input(0, "10.000000001")); !errors.Is(err, c2c.ErrInsufficientBalance) {
		t.Fatalf("list above balance error = %v", err)
	}
	// Overdrawn through credit: a negative balance cannot list anything.
	if _, err := f.store.Ledger.Adjust(ctx, ledger.Adjustment{ActorID: f.admin.ID, AccountID: f.members[1].ID, Amount: amount(t, "-5"), Reason: "透支", IdempotencyKey: "overdraw"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CreateOrder(ctx, input(1, "1")); !errors.Is(err, c2c.ErrInsufficientBalance) {
		t.Fatalf("list with negative balance error = %v", err)
	}
	// Listing the exact balance works and leaves the seller at zero.
	order, err := f.service.CreateOrder(ctx, input(0, "10"))
	if err != nil || f.balance(0) != 0 || order.MinPerTrade != 1 || order.MaxPerTrade != nil {
		t.Fatalf("exact balance listing = %+v, %v (balance %s)", order, err, f.balance(0))
	}
	f.assertInvariants()

	// Input validation and disabled accounts.
	f.fund(0, "5")
	bad := input(0, "1")
	bad.PaymentMethods = nil
	if _, err := f.service.CreateOrder(ctx, bad); !errors.Is(err, c2c.ErrInvalidInput) {
		t.Fatalf("no payment methods error = %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE accounts SET status = 'disabled' WHERE id = $1`, f.members[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CreateOrder(ctx, input(0, "1")); !errors.Is(err, c2c.ErrAccountInactive) {
		t.Fatalf("disabled seller error = %v", err)
	}
	if _, err := f.buy(0, order.ID, "1"); !errors.Is(err, c2c.ErrOwnOrder) && !errors.Is(err, c2c.ErrAccountInactive) {
		t.Fatalf("disabled buyer error = %v", err)
	}
	f.assertInvariants()
}

func TestC2CBuyRules(t *testing.T) {
	f := newC2CFixture(t, "0", "0", "0")
	ctx := context.Background()
	f.fund(0, "100")
	min, max := amount(t, "5"), amount(t, "20")
	order, err := f.service.CreateOrder(ctx, c2c.CreateOrderInput{
		SellerID: f.members[0].ID, Amount: amount(t, "50"), UnitPriceFen: 33, MinPerTrade: &min, MaxPerTrade: &max, PaymentMethods: alipay,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		buyer  int
		amount string
		want   error
	}{
		"below minimum": {1, "4.999999999", c2c.ErrAmountOutOfRange},
		"above maximum": {1, "20.000000001", c2c.ErrAmountOutOfRange},
		"own order":     {0, "5", c2c.ErrOwnOrder},
	} {
		if _, err := f.buy(tc.buyer, order.ID, tc.amount); !errors.Is(err, tc.want) {
			t.Fatalf("%s error = %v, want %v", name, err, tc.want)
		}
	}
	// 5.000000001 points at 0.33 yuan = 1.65000000033 yuan, rounded up to 166 fen.
	first := f.mustBuy(1, order.ID, "5.000000001")
	if first.TotalFen != 166 {
		t.Fatalf("total fen = %d, want 166", first.TotalFen)
	}
	if _, err := f.buy(1, order.ID, "5"); !errors.Is(err, c2c.ErrDuplicateTrade) {
		t.Fatalf("second unfinished trade error = %v", err)
	}
	f.mustBuy(2, order.ID, "20")
	if _, err := f.buy(2, order.ID, "5"); !errors.Is(err, c2c.ErrDuplicateTrade) {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := f.service.CreateTrade(ctx, order.ID, f.members[1].ID, amount(t, "5"), ""); !errors.Is(err, c2c.ErrDuplicateTrade) {
		t.Fatalf("duplicate error = %v", err)
	}
	if got := f.order(order.ID); got.Available != amount(t, "24.999999999") || got.InTrade != amount(t, "25.000000001") {
		t.Fatalf("order = %+v", got)
	}
	f.assertInvariants()
}

func TestC2CCancelTimeoutCloseAndReturn(t *testing.T) {
	f := newC2CFixture(t, "0", "0", "0")
	ctx := context.Background()
	f.fund(0, "100")
	order := f.list(0, "60", 90)

	// The buyer cancels: points go back to the open order, nothing hits the seller's balance.
	cancelled := f.mustBuy(1, order.ID, "10")
	view, err := f.service.Cancel(ctx, cancelled.ID, f.members[1].ID)
	if err != nil || view.Status != c2c.TradeCancelled || view.CancelledAt == nil || len(view.PaymentMethods) != 0 {
		t.Fatalf("cancel = %+v, %v", view, err)
	}
	if got := f.order(order.ID); got.Available != amount(t, "60") || got.InTrade != 0 {
		t.Fatalf("order after cancel = %+v", got)
	}
	if types := f.ledgerTypes(cancelled.ID); len(types) != 0 {
		t.Fatalf("cancel on an open order booked %v", types)
	}
	// A cancelled trade frees the buyer to buy again, and cancel is repeatable.
	if again, err := f.service.Cancel(ctx, cancelled.ID, f.members[1].ID); err != nil || again.Status != c2c.TradeCancelled {
		t.Fatalf("repeat cancel = %+v, %v", again, err)
	}
	if _, err := f.service.Cancel(ctx, cancelled.ID, f.members[2].ID); !errors.Is(err, c2c.ErrForbidden) {
		t.Fatalf("stranger cancel error = %v", err)
	}
	f.assertInvariants()

	// Timeout: only awaiting_payment trades past the deadline are cancelled by the job.
	late := f.mustBuy(1, order.ID, "10")
	paid := f.mustBuy(2, order.ID, "10")
	if _, err := f.service.MarkPaid(ctx, paid.ID, f.members[2].ID, ""); err != nil {
		t.Fatal(err)
	}
	f.expireDeadline(late.ID)
	f.expireDeadline(paid.ID)
	if expired, err := f.service.ExpireDue(ctx, 10); err != nil || expired != 1 {
		t.Fatalf("expire = %d, %v", expired, err)
	}
	if got, err := f.service.GetTrade(ctx, c2c.Viewer{ID: f.members[1].ID}, late.ID); err != nil || got.Status != c2c.TradeCancelled {
		t.Fatalf("late trade = %+v, %v", got, err)
	}
	if got, err := f.service.GetTrade(ctx, c2c.Viewer{ID: f.members[2].ID}, paid.ID); err != nil || got.Status != c2c.TradePaid {
		t.Fatalf("paid trade must survive the timeout job: %+v, %v", got, err)
	}
	if expired, err := f.service.ExpireDue(ctx, 10); err != nil || expired != 0 {
		t.Fatalf("second expire = %d, %v", expired, err)
	}
	// An expired trade can no longer be marked paid.
	stale := f.mustBuy(1, order.ID, "10")
	f.expireDeadline(stale.ID)
	if _, err := f.service.MarkPaid(ctx, stale.ID, f.members[1].ID, ""); !errors.Is(err, c2c.ErrPaymentExpired) {
		t.Fatalf("paid after deadline error = %v", err)
	}
	f.assertInvariants()

	// Closing returns the available part at once and keeps trades in progress.
	// Now: available 30, in trade 10 (paid) + 10 (stale) = 20, escrow 50 + 0 ... verify by numbers.
	closed, err := f.service.CloseOrder(ctx, order.ID, f.members[0].ID)
	if err != nil || closed.Status != c2c.OrderClosed || closed.Available != 0 || closed.Closed != amount(t, "40") || closed.InTrade != amount(t, "20") {
		t.Fatalf("closed order = %+v, %v", closed, err)
	}
	if f.balance(0) != amount(t, "80") || f.escrow() != amount(t, "20") {
		t.Fatalf("after close: seller %s escrow %s", f.balance(0), f.escrow())
	}
	if types := f.ledgerTypes(order.ID); len(types) != 2 || types[0] != "c2c_list" || types[1] != "c2c_return" {
		t.Fatalf("order ledger types = %v", types)
	}
	f.assertInvariants()
	if again, err := f.service.CloseOrder(ctx, order.ID, f.members[0].ID); err != nil || again.Status != c2c.OrderClosed {
		t.Fatalf("repeat close = %+v, %v", again, err)
	}
	if _, err := f.service.CloseOrder(ctx, order.ID, f.members[1].ID); !errors.Is(err, c2c.ErrForbidden) {
		t.Fatalf("close by stranger error = %v", err)
	}
	if _, err := f.buy(1, order.ID, "1"); !errors.Is(err, c2c.ErrOrderNotOpen) {
		t.Fatalf("buy on closed order error = %v", err)
	}
	// Trades finishing on a closed order never reopen it: a timeout refunds the seller directly.
	if view, err := f.service.Cancel(ctx, stale.ID, f.members[1].ID); err != nil || view.Status != c2c.TradeCancelled {
		t.Fatalf("cancel on closed order = %+v, %v", view, err)
	}
	if f.balance(0) != amount(t, "90") || f.escrow() != amount(t, "10") {
		t.Fatalf("after refund: seller %s escrow %s", f.balance(0), f.escrow())
	}
	if types := f.ledgerTypes(stale.ID); len(types) != 1 || types[0] != "c2c_return" {
		t.Fatalf("stale trade ledger types = %v", types)
	}
	// Releasing the remaining paid trade sells it and the order stays closed.
	if _, err := f.service.Release(ctx, paid.ID, f.members[0].ID); err != nil {
		t.Fatal(err)
	}
	final := f.order(order.ID)
	if final.Status != c2c.OrderClosed || final.Sold != amount(t, "10") || final.Closed != amount(t, "50") || final.InTrade != 0 || f.escrow() != 0 {
		t.Fatalf("final order = %+v escrow %s", final, f.escrow())
	}
	f.assertInvariants()
}

func TestC2CDisputeAndArbitration(t *testing.T) {
	f := newC2CFixture(t, "0", "0", "0")
	ctx := context.Background()
	f.fund(0, "100")
	order := f.list(0, "50", 90)

	toBuyer := f.mustBuy(1, order.ID, "20")
	if _, err := f.service.Dispute(ctx, toBuyer.ID, f.members[1].ID, "我还没付款"); !errors.Is(err, c2c.ErrInvalidState) {
		t.Fatalf("dispute before paid error = %v", err)
	}
	if _, err := f.service.MarkPaid(ctx, toBuyer.ID, f.members[1].ID, ""); err != nil {
		t.Fatal(err)
	}
	opened, err := f.service.Dispute(ctx, toBuyer.ID, f.members[1].ID, "已付款，卖家未放行")
	if err != nil || opened.Status != c2c.TradeDisputed || opened.DisputedAt == nil || opened.BuyerStatement == nil ||
		opened.DisputeOpenedBy == nil || *opened.DisputeOpenedBy != f.members[1].ID {
		t.Fatalf("dispute = %+v, %v", opened, err)
	}
	updated, err := f.service.Dispute(ctx, toBuyer.ID, f.members[0].ID, "没有收到款项")
	if err != nil || updated.SellerStatement == nil || *updated.SellerStatement != "没有收到款项" || *updated.DisputeOpenedBy != f.members[1].ID {
		t.Fatalf("seller statement = %+v, %v", updated, err)
	}
	again, err := f.service.Dispute(ctx, toBuyer.ID, f.members[1].ID, "补充：转账截图编号 123")
	if err != nil || *again.BuyerStatement != "补充：转账截图编号 123" {
		t.Fatalf("buyer update = %+v, %v", again, err)
	}
	if _, err := f.service.Dispute(ctx, toBuyer.ID, f.admin.ID, "我是管理员"); !errors.Is(err, c2c.ErrForbidden) {
		t.Fatalf("outsider dispute error = %v", err)
	}
	// While disputed neither side can release or cancel, and the timeout job ignores it.
	if _, err := f.service.Release(ctx, toBuyer.ID, f.members[0].ID); !errors.Is(err, c2c.ErrInvalidState) {
		t.Fatalf("release while disputed error = %v", err)
	}
	if _, err := f.service.Cancel(ctx, toBuyer.ID, f.members[1].ID); !errors.Is(err, c2c.ErrInvalidState) {
		t.Fatalf("cancel while disputed error = %v", err)
	}
	f.expireDeadline(toBuyer.ID)
	if expired, err := f.service.ExpireDue(ctx, 10); err != nil || expired != 0 {
		t.Fatalf("expire disputed = %d, %v", expired, err)
	}
	if f.escrow() != amount(t, "50") {
		t.Fatalf("escrow while disputed = %s", f.escrow())
	}
	f.assertInvariants()

	// A second dispute that the administrator returns to the seller.
	toSeller := f.mustBuy(2, order.ID, "10")
	f.mustPaidAndDispute(toSeller.ID, 2, "对方没有给我账号")

	queue, err := f.service.ListDisputes(ctx, c2c.Viewer{ID: f.admin.ID, IsAdmin: true}, "", "", 10)
	if err != nil || len(queue.Items) != 2 || queue.Items[0].ID != toBuyer.ID || queue.Items[1].ID != toSeller.ID || queue.Items[0].ViewerRole != "admin" ||
		len(queue.Items[0].PaymentMethods) != 2 {
		t.Fatalf("disputes = %+v, %v", queue, err)
	}
	if _, err := f.service.Resolve(ctx, c2c.Resolution{TradeID: toBuyer.ID, AdminID: f.admin.ID, ToBuyer: true, Reason: ""}); !errors.Is(err, c2c.ErrInvalidInput) {
		t.Fatalf("resolve without reason error = %v", err)
	}
	resolved, err := f.service.Resolve(ctx, c2c.Resolution{TradeID: toBuyer.ID, AdminID: f.admin.ID, ToBuyer: true, Reason: "转账记录属实"})
	if err != nil || resolved.Status != c2c.TradeResolvedBuyer || resolved.ResolvedAt == nil || resolved.ResolutionReason == nil || *resolved.ResolutionReason != "转账记录属实" {
		t.Fatalf("resolve to buyer = %+v, %v", resolved, err)
	}
	if f.balance(1) != amount(t, "20") || f.escrow() != amount(t, "30") {
		t.Fatalf("after ruling for buyer: buyer %s escrow %s", f.balance(1), f.escrow())
	}
	// Repeating the same ruling is a no-op; the opposite ruling is a conflict.
	if repeat, err := f.service.Resolve(ctx, c2c.Resolution{TradeID: toBuyer.ID, AdminID: f.admin.ID, ToBuyer: true, Reason: "重复"}); err != nil || repeat.Status != c2c.TradeResolvedBuyer {
		t.Fatalf("repeat resolve = %+v, %v", repeat, err)
	}
	if _, err := f.service.Resolve(ctx, c2c.Resolution{TradeID: toBuyer.ID, AdminID: f.admin.ID, ToBuyer: false, Reason: "反悔"}); !errors.Is(err, c2c.ErrInvalidState) {
		t.Fatalf("opposite ruling error = %v", err)
	}
	back, err := f.service.Resolve(ctx, c2c.Resolution{TradeID: toSeller.ID, AdminID: f.admin.ID, ToBuyer: false, Reason: "买家未能证明付款"})
	if err != nil || back.Status != c2c.TradeResolvedSeller {
		t.Fatalf("resolve to seller = %+v, %v", back, err)
	}
	// The order is still open, so the points returned to its available part.
	if got := f.order(order.ID); got.Available != amount(t, "30") || got.InTrade != 0 || got.Sold != amount(t, "20") {
		t.Fatalf("order after rulings = %+v", got)
	}
	if f.balance(0) != amount(t, "50") || f.escrow() != amount(t, "30") {
		t.Fatalf("seller %s escrow %s", f.balance(0), f.escrow())
	}
	if types := f.ledgerTypes(toSeller.ID); len(types) != 0 {
		t.Fatalf("ruling for the seller on an open order booked %v", types)
	}
	var audits int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'c2c.resolve' AND actor_id = $1 AND reason <> ''`, f.admin.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("resolve audit rows = %d, %v", audits, err)
	}
	empty, err := f.service.ListDisputes(ctx, c2c.Viewer{ID: f.admin.ID, IsAdmin: true}, "", "", 10)
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("remaining disputes = %+v, %v", empty, err)
	}
	f.assertInvariants()

	// A ruling for the seller on a closed order refunds the seller directly.
	third := f.mustBuy(1, order.ID, "10")
	f.mustPaidAndDispute(third.ID, 1, "争议")
	if _, err := f.service.CloseOrder(ctx, order.ID, f.members[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Resolve(ctx, c2c.Resolution{TradeID: third.ID, AdminID: f.admin.ID, ToBuyer: false, Reason: "退回"}); err != nil {
		t.Fatal(err)
	}
	if f.escrow() != 0 || f.balance(0) != amount(t, "80") {
		t.Fatalf("closed-order ruling: seller %s escrow %s", f.balance(0), f.escrow())
	}
	if types := f.ledgerTypes(third.ID); len(types) != 1 || types[0] != "c2c_return" {
		t.Fatalf("ledger types = %v", types)
	}
	f.assertInvariants()
}

func (f *c2cFixture) mustPaidAndDispute(tradeID string, buyer int, statement string) {
	f.t.Helper()
	ctx := context.Background()
	if _, err := f.service.MarkPaid(ctx, tradeID, f.members[buyer].ID, ""); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.service.Dispute(ctx, tradeID, f.members[buyer].ID, statement); err != nil {
		f.t.Fatal(err)
	}
}

func TestC2CVisibilityAndListings(t *testing.T) {
	f := newC2CFixture(t, "0", "0", "0", "0")
	ctx := context.Background()
	f.fund(0, "100")
	f.fund(1, "100")
	cheap := f.list(0, "10", 80)
	dear := f.list(0, "10", 95)
	middle := f.list(1, "10", 90)
	same := f.list(1, "10", 90)

	// Market: price ascending, then oldest first; own orders are not buyable; accounts stay hidden.
	page, err := f.service.ListMarket(ctx, f.members[2].ID, "", 3)
	if err != nil || len(page.Items) != 3 || page.Next == "" {
		t.Fatalf("market page = %+v, %v", page, err)
	}
	if page.Items[0].ID != cheap.ID || page.Items[1].ID != middle.ID || page.Items[2].ID != same.ID {
		t.Fatalf("market order = %s %s %s", page.Items[0].ID, page.Items[1].ID, page.Items[2].ID)
	}
	if got := page.Items[0].Channels; len(got) != 2 || got[0] != "支付宝" || got[1] != "微信" {
		t.Fatalf("channels = %v", got)
	}
	rest, err := f.service.ListMarket(ctx, f.members[2].ID, page.Next, 3)
	if err != nil || len(rest.Items) != 1 || rest.Items[0].ID != dear.ID || rest.Next != "" {
		t.Fatalf("market rest = %+v, %v", rest, err)
	}
	seller0, err := f.service.ListMarket(ctx, f.members[0].ID, "", 10)
	if err != nil || len(seller0.Items) != 2 || seller0.Items[0].ID != middle.ID {
		t.Fatalf("own orders must not be buyable: %+v, %v", seller0, err)
	}
	if _, err := f.service.ListMarket(ctx, f.members[2].ID, "not-a-cursor", 3); !errors.Is(err, c2c.ErrInvalidCursor) {
		t.Fatalf("bad cursor error = %v", err)
	}
	// A fully booked order leaves the market.
	f.mustBuy(2, cheap.ID, "10")
	if market, _ := f.service.ListMarket(ctx, f.members[3].ID, "", 10); len(market.Items) != 3 || market.Items[0].ID != middle.ID {
		t.Fatalf("market after sell-out = %+v", market)
	}

	// My orders by status.
	if _, err := f.service.CloseOrder(ctx, dear.ID, f.members[0].ID); err != nil {
		t.Fatal(err)
	}
	open, err := f.service.ListMyOrders(ctx, f.members[0].ID, c2c.OrderOpen, "", 10)
	if err != nil || len(open.Items) != 1 || open.Items[0].ID != cheap.ID || len(open.Items[0].PaymentMethods) != 2 {
		t.Fatalf("open orders = %+v, %v", open, err)
	}
	closed, err := f.service.ListMyOrders(ctx, f.members[0].ID, c2c.OrderClosed, "", 10)
	if err != nil || len(closed.Items) != 1 || closed.Items[0].ID != dear.ID {
		t.Fatalf("closed orders = %+v, %v", closed, err)
	}
	if _, err := f.service.ListMyOrders(ctx, f.members[0].ID, "bogus", "", 10); !errors.Is(err, c2c.ErrInvalidInput) {
		t.Fatalf("bogus status error = %v", err)
	}

	// Trade visibility: parties and administrators only; the buyer loses the accounts once finished.
	trade, err := f.service.GetTrade(ctx, c2c.Viewer{ID: f.members[2].ID}, cheapTradeID(t, f, cheap.ID))
	if err != nil || trade.ViewerRole != "buyer" || len(trade.PaymentMethods) != 2 {
		t.Fatalf("buyer view = %+v, %v", trade, err)
	}
	if _, err := f.service.GetTrade(ctx, c2c.Viewer{ID: f.members[3].ID}, trade.ID); !errors.Is(err, c2c.ErrNotFound) {
		t.Fatalf("stranger view error = %v", err)
	}
	if view, err := f.service.GetTrade(ctx, c2c.Viewer{ID: f.admin.ID, IsAdmin: true}, trade.ID); err != nil || view.ViewerRole != "admin" || len(view.PaymentMethods) != 2 {
		t.Fatalf("admin view = %+v, %v", view, err)
	}
	if _, err := f.service.Release(ctx, trade.ID, f.members[2].ID); !errors.Is(err, c2c.ErrForbidden) {
		t.Fatalf("buyer release error = %v", err)
	}
	if _, err := f.service.Release(ctx, trade.ID, f.members[0].ID); err != nil {
		t.Fatal(err)
	}
	if view, _ := f.service.GetTrade(ctx, c2c.Viewer{ID: f.members[2].ID}, trade.ID); len(view.PaymentMethods) != 0 {
		t.Fatalf("buyer still sees accounts after release: %+v", view.PaymentMethods)
	}
	if view, _ := f.service.GetTrade(ctx, c2c.Viewer{ID: f.members[0].ID}, trade.ID); len(view.PaymentMethods) != 2 {
		t.Fatalf("seller must keep seeing accounts: %+v", view.PaymentMethods)
	}

	// "Pending on me": buyer awaiting payment, or seller with a paid trade.
	waiting := f.mustBuy(3, middle.ID, "5")
	toRelease := f.mustBuy(2, middle.ID, "5")
	if _, err := f.service.MarkPaid(ctx, toRelease.ID, f.members[2].ID, ""); err != nil {
		t.Fatal(err)
	}
	pendingBuyer, err := f.service.ListMyTrades(ctx, c2c.TradeFilter{UserID: f.members[3].ID, Pending: true, Limit: 10}, "")
	if err != nil || len(pendingBuyer.Items) != 1 || pendingBuyer.Items[0].ID != waiting.ID {
		t.Fatalf("buyer pending = %+v, %v", pendingBuyer, err)
	}
	pendingSeller, err := f.service.ListMyTrades(ctx, c2c.TradeFilter{UserID: f.members[1].ID, Pending: true, Limit: 10}, "")
	if err != nil || len(pendingSeller.Items) != 1 || pendingSeller.Items[0].ID != toRelease.ID {
		t.Fatalf("seller pending = %+v, %v", pendingSeller, err)
	}
	asBuyer, err := f.service.ListMyTrades(ctx, c2c.TradeFilter{UserID: f.members[2].ID, Role: "buyer", Limit: 1}, "")
	if err != nil || len(asBuyer.Items) != 1 || asBuyer.Next == "" || asBuyer.Items[0].ID != toRelease.ID {
		t.Fatalf("buyer trades page 1 = %+v, %v", asBuyer, err)
	}
	more, err := f.service.ListMyTrades(ctx, c2c.TradeFilter{UserID: f.members[2].ID, Role: "buyer", Status: c2c.TradeReleased, Limit: 5}, "")
	if err != nil || len(more.Items) != 1 || more.Items[0].Status != c2c.TradeReleased {
		t.Fatalf("released trades = %+v, %v", more, err)
	}
	f.assertInvariants()
}

func cheapTradeID(t *testing.T, f *c2cFixture, orderID string) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM c2c_trades WHERE order_id = $1`, orderID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestC2CIdempotentRequests(t *testing.T) {
	f := newC2CFixture(t, "0", "0")
	ctx := context.Background()
	f.fund(0, "100")
	min := amount(t, "1")
	input := c2c.CreateOrderInput{
		SellerID: f.members[0].ID, Amount: amount(t, "30"), UnitPriceFen: 90, MinPerTrade: &min, PaymentMethods: alipay, IdempotencyKey: "list-1",
	}
	first, err := f.service.CreateOrder(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.CreateOrder(ctx, input)
	if err != nil || second.ID != first.ID || f.balance(0) != amount(t, "70") || f.escrow() != amount(t, "30") {
		t.Fatalf("repeat listing = %+v, %v (balance %s, escrow %s)", second, err, f.balance(0), f.escrow())
	}

	trade, err := f.service.CreateTrade(ctx, first.ID, f.members[1].ID, amount(t, "10"), "buy-1")
	if err != nil {
		t.Fatal(err)
	}
	if repeat, err := f.service.CreateTrade(ctx, first.ID, f.members[1].ID, amount(t, "10"), "buy-1"); err != nil || repeat.ID != trade.ID {
		t.Fatalf("repeat buy = %+v, %v", repeat, err)
	}
	if got := f.order(first.ID); got.Available != amount(t, "20") || got.InTrade != amount(t, "10") {
		t.Fatalf("order after repeated buy = %+v", got)
	}

	transactions := func() int {
		var count int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_transactions WHERE type LIKE 'c2c_%'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if _, err := f.service.MarkPaid(ctx, trade.ID, f.members[1].ID, "x"); err != nil {
		t.Fatal(err)
	}
	if again, err := f.service.MarkPaid(ctx, trade.ID, f.members[1].ID, "y"); err != nil || again.BuyerNote == nil || *again.BuyerNote != "x" {
		t.Fatalf("repeat paid = %+v, %v", again, err)
	}
	released, err := f.service.Release(ctx, trade.ID, f.members[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	before := transactions()
	repeated, err := f.service.Release(ctx, trade.ID, f.members[0].ID)
	if err != nil || repeated.Status != c2c.TradeReleased || repeated.ReleasedAt == nil || !repeated.ReleasedAt.Equal(*released.ReleasedAt) {
		t.Fatalf("repeat release = %+v, %v", repeated, err)
	}
	if transactions() != before || f.balance(1) != amount(t, "10") || f.escrow() != amount(t, "20") {
		t.Fatalf("repeat release re-booked: %d vs %d transactions, buyer %s, escrow %s", transactions(), before, f.balance(1), f.escrow())
	}
	f.assertInvariants()
}

func TestC2CConcurrency(t *testing.T) {
	buyers := 16
	credits := make([]string, buyers+1)
	for i := range credits {
		credits[i] = "0"
	}
	f := newC2CFixture(t, credits...)
	ctx := context.Background()
	f.fund(0, "100")
	order := f.list(0, "10", 90)

	t.Run("concurrent buys never oversell", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make(chan error, buyers)
		for buyer := 1; buyer <= buyers; buyer++ {
			wg.Add(1)
			go func(buyer int) {
				defer wg.Done()
				_, err := f.buy(buyer, order.ID, "1")
				results <- err
			}(buyer)
		}
		wg.Wait()
		close(results)
		succeeded := 0
		for err := range results {
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, c2c.ErrAmountUnavailable):
			default:
				t.Errorf("unexpected buy error: %v", err)
			}
		}
		if succeeded != 10 {
			t.Fatalf("%d buys succeeded, want exactly the 10 points on offer", succeeded)
		}
		if got := f.order(order.ID); got.Available != 0 || got.InTrade != amount(t, "10") {
			t.Fatalf("order = %+v", got)
		}
		f.assertInvariants()
	})

	t.Run("one buyer cannot hold two trades", func(t *testing.T) {
		f2 := newC2CFixture(t, "0", "0")
		f2.fund(0, "50")
		order := f2.list(0, "30", 90)
		var wg sync.WaitGroup
		results := make(chan error, 6)
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := f2.buy(1, order.ID, "2")
				results <- err
			}()
		}
		wg.Wait()
		close(results)
		succeeded := 0
		for err := range results {
			if err == nil {
				succeeded++
			} else if !errors.Is(err, c2c.ErrDuplicateTrade) {
				t.Errorf("unexpected error: %v", err)
			}
		}
		if succeeded != 1 {
			t.Fatalf("%d trades created for one buyer, want 1", succeeded)
		}
		f2.assertInvariants()
	})

	t.Run("release racing cancel books exactly one outcome", func(t *testing.T) {
		f3 := newC2CFixture(t, "0", "0")
		f3.fund(0, "50")
		order := f3.list(0, "20", 90)
		for round := 0; round < 8; round++ {
			trade := f3.mustBuy(1, order.ID, "2")
			var wg sync.WaitGroup
			var releaseErr, cancelErr error
			wg.Add(2)
			go func() { defer wg.Done(); _, releaseErr = f3.service.Release(ctx, trade.ID, f3.members[0].ID) }()
			go func() { defer wg.Done(); _, cancelErr = f3.service.Cancel(ctx, trade.ID, f3.members[1].ID) }()
			wg.Wait()
			if (releaseErr == nil) == (cancelErr == nil) {
				t.Fatalf("round %d: release error %v, cancel error %v; exactly one must win", round, releaseErr, cancelErr)
			}
			if err := errors.Join(releaseErr, cancelErr); err != nil && !errors.Is(err, c2c.ErrInvalidState) {
				t.Fatalf("round %d: loser error = %v", round, err)
			}
			f3.assertInvariants()
		}
	})

	t.Run("concurrent listings cannot spend the same balance twice", func(t *testing.T) {
		f4 := newC2CFixture(t, "0")
		f4.fund(0, "10")
		var wg sync.WaitGroup
		results := make(chan error, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				min := amount(t, "1")
				_, err := f4.service.CreateOrder(ctx, c2c.CreateOrderInput{
					SellerID: f4.members[0].ID, Amount: amount(t, "10"), UnitPriceFen: 90, MinPerTrade: &min, PaymentMethods: alipay,
				})
				results <- err
			}()
		}
		wg.Wait()
		close(results)
		succeeded := 0
		for err := range results {
			if err == nil {
				succeeded++
			} else if !errors.Is(err, c2c.ErrInsufficientBalance) {
				t.Errorf("unexpected error: %v", err)
			}
		}
		if succeeded != 1 || f4.balance(0) != 0 {
			t.Fatalf("%d listings succeeded, balance %s", succeeded, f4.balance(0))
		}
		f4.assertInvariants()
	})
}

func TestC2CSchemaEnforcesTheOrderIdentity(t *testing.T) {
	f := newC2CFixture(t, "0")
	f.fund(0, "10")
	order := f.list(0, "10", 90)
	_, err := f.pool.Exec(context.Background(), `UPDATE c2c_orders SET available_nano = available_nano + 1 WHERE id = $1`, order.ID)
	if !isSQLState(err, "23514") {
		t.Fatalf("breaking total = available + in_trade + sold + closed error = %v, want check violation", err)
	}
}
