package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

// fakeC2CStore is an in-memory c2c.Store built on the real state machine, so
// handler tests exercise permissions, visibility and response shapes. Ledger
// bookkeeping is covered by the PostgreSQL integration tests.
type fakeC2CStore struct {
	mu       sync.Mutex
	accounts *fakeStore
	balances map[string]money.Amount
	orders   map[string]c2c.Order
	trades   map[string]c2c.Trade
	clock    time.Time
}

func newFakeC2CStore(accounts *fakeStore) *fakeC2CStore {
	return &fakeC2CStore{
		accounts: accounts, balances: map[string]money.Amount{}, orders: map[string]c2c.Order{}, trades: map[string]c2c.Trade{},
		clock: time.Now(),
	}
}

func (s *fakeC2CStore) party(id string) c2c.Party {
	s.accounts.mu.Lock()
	defer s.accounts.mu.Unlock()
	return c2c.Party{ID: id, DisplayName: s.accounts.accounts[id].DisplayName}
}

func (s *fakeC2CStore) tick() time.Time {
	s.clock = s.clock.Add(time.Second)
	return s.clock
}

func (s *fakeC2CStore) CreateOrder(_ context.Context, n c2c.NewOrder) (c2c.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.orders[n.ID]; ok {
		return existing, nil
	}
	if s.balances[n.SellerID] < n.Total {
		return c2c.Order{}, c2c.ErrInsufficientBalance
	}
	s.balances[n.SellerID] -= n.Total
	order := c2c.Order{
		ID: n.ID, Seller: s.party(n.SellerID), Total: n.Total, Available: n.Total, UnitPriceFen: n.UnitPriceFen, MinPerTrade: n.MinPerTrade,
		MaxPerTrade: n.MaxPerTrade, Methods: n.Methods, Status: c2c.OrderOpen, CreatedAt: s.tick(), UpdatedAt: s.clock,
	}
	s.orders[n.ID] = order
	return order, nil
}

func (s *fakeC2CStore) sortedOrders(keep func(c2c.Order) bool, less func(a, b c2c.Order) bool) []c2c.Order {
	var orders []c2c.Order
	for _, order := range s.orders {
		if keep(order) {
			orders = append(orders, order)
		}
	}
	sort.Slice(orders, func(i, j int) bool { return less(orders[i], orders[j]) })
	return orders
}

func (s *fakeC2CStore) ListMarket(_ context.Context, viewerID string, _ *c2c.Cursor, limit int) ([]c2c.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	orders := s.sortedOrders(func(o c2c.Order) bool {
		return o.Status == c2c.OrderOpen && o.Available > 0 && o.Seller.ID != viewerID
	}, func(a, b c2c.Order) bool { return a.UnitPriceFen < b.UnitPriceFen })
	if len(orders) > limit {
		orders = orders[:limit]
	}
	return orders, nil
}

func (s *fakeC2CStore) ListSellerOrders(_ context.Context, sellerID string, status c2c.OrderStatus, _ *c2c.Cursor, limit int) ([]c2c.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	orders := s.sortedOrders(func(o c2c.Order) bool {
		return o.Seller.ID == sellerID && (status == "" || o.Status == status)
	}, func(a, b c2c.Order) bool { return a.CreatedAt.After(b.CreatedAt) })
	if len(orders) > limit {
		orders = orders[:limit]
	}
	return orders, nil
}

func (s *fakeC2CStore) CloseOrder(_ context.Context, orderID, sellerID string) (c2c.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	order, ok := s.orders[orderID]
	switch {
	case !ok:
		return c2c.Order{}, c2c.ErrNotFound
	case order.Seller.ID != sellerID:
		return c2c.Order{}, c2c.ErrForbidden
	case order.Status != c2c.OrderOpen:
		return c2c.Order{}, c2c.ErrInvalidState
	}
	closed, refund := order.Close()
	s.balances[sellerID] += refund
	s.orders[orderID] = closed
	return closed, nil
}

func (s *fakeC2CStore) CreateTrade(_ context.Context, n c2c.NewTrade) (c2c.Trade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	order, ok := s.orders[n.OrderID]
	if !ok {
		return c2c.Trade{}, c2c.ErrNotFound
	}
	if err := order.CheckBuy(n.BuyerID, n.Amount); err != nil {
		return c2c.Trade{}, err
	}
	for _, trade := range s.trades {
		if trade.OrderID == n.OrderID && trade.Buyer.ID == n.BuyerID && trade.Status.Unfinished() {
			return c2c.Trade{}, c2c.ErrDuplicateTrade
		}
	}
	total, err := c2c.TotalFen(n.Amount, order.UnitPriceFen)
	if err != nil {
		return c2c.Trade{}, err
	}
	trade := c2c.Trade{
		ID: n.ID, OrderID: n.OrderID, Buyer: s.party(n.BuyerID), Seller: order.Seller, Amount: n.Amount, UnitPriceFen: order.UnitPriceFen,
		TotalFen: total, Status: c2c.TradeAwaitingPayment, PaymentDeadline: s.clock.Add(30 * time.Minute), Methods: order.Methods, CreatedAt: s.tick(),
	}
	s.orders[n.OrderID] = order.Reserve(n.Amount)
	s.trades[n.ID] = trade
	return trade, nil
}

func (s *fakeC2CStore) GetTrade(_ context.Context, id string) (c2c.Trade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	trade, ok := s.trades[id]
	if !ok {
		return c2c.Trade{}, c2c.ErrNotFound
	}
	return trade, nil
}

func (s *fakeC2CStore) ListTrades(_ context.Context, f c2c.TradeFilter) ([]c2c.Trade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var trades []c2c.Trade
	for _, trade := range s.trades {
		mine := trade.Buyer.ID == f.UserID || trade.Seller.ID == f.UserID
		if f.Role == "buyer" {
			mine = trade.Buyer.ID == f.UserID
		} else if f.Role == "seller" {
			mine = trade.Seller.ID == f.UserID
		}
		pending := (trade.Buyer.ID == f.UserID && trade.Status == c2c.TradeAwaitingPayment) || (trade.Seller.ID == f.UserID && trade.Status == c2c.TradePaid)
		if mine && (f.Status == "" || trade.Status == f.Status) && (!f.Pending || pending) {
			trades = append(trades, trade)
		}
	}
	sort.Slice(trades, func(i, j int) bool { return trades[i].CreatedAt.After(trades[j].CreatedAt) })
	if len(trades) > f.Limit {
		trades = trades[:f.Limit]
	}
	return trades, nil
}

func (s *fakeC2CStore) ListDisputes(_ context.Context, status c2c.TradeStatus, _ *c2c.Cursor, limit int) ([]c2c.Trade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var trades []c2c.Trade
	for _, trade := range s.trades {
		if trade.Status == status {
			trades = append(trades, trade)
		}
	}
	sort.Slice(trades, func(i, j int) bool { return trades[i].DisputedAt.Before(*trades[j].DisputedAt) })
	if len(trades) > limit {
		trades = trades[:limit]
	}
	return trades, nil
}

func (s *fakeC2CStore) DueTrades(context.Context, int) ([]string, error) { return nil, nil }

func (s *fakeC2CStore) apply(id string, step func(order c2c.Order, trade c2c.Trade) (c2c.Trade, error)) (c2c.Trade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	trade, ok := s.trades[id]
	if !ok {
		return c2c.Trade{}, c2c.ErrNotFound
	}
	updated, err := step(s.orders[trade.OrderID], trade)
	if err == nil {
		s.trades[id] = updated
	}
	return updated, err
}

func (s *fakeC2CStore) MarkPaid(_ context.Context, id, buyerID, note string) (c2c.Trade, error) {
	return s.apply(id, func(_ c2c.Order, trade c2c.Trade) (c2c.Trade, error) {
		noop, err := trade.CheckPaid(buyerID, time.Now())
		if err != nil || noop {
			return trade, err
		}
		now := s.tick()
		trade.Status, trade.PaidAt = c2c.TradePaid, &now
		if note != "" {
			trade.BuyerNote = &note
		}
		return trade, nil
	})
}

func (s *fakeC2CStore) finish(trade c2c.Trade, order c2c.Order, toBuyer bool, status c2c.TradeStatus) c2c.Trade {
	if toBuyer {
		s.orders[order.ID] = order.Settle(trade.Amount)
	} else {
		updated, refund := order.Unlock(trade.Amount)
		if refund {
			s.balances[trade.Seller.ID] += trade.Amount
		}
		s.orders[order.ID] = updated
	}
	now := s.tick()
	trade.Status, trade.ReleasedAt = status, &now
	return trade
}

func (s *fakeC2CStore) Release(_ context.Context, id, sellerID string) (c2c.Trade, error) {
	return s.apply(id, func(order c2c.Order, trade c2c.Trade) (c2c.Trade, error) {
		if noop, err := trade.CheckRelease(sellerID); err != nil || noop {
			return trade, err
		}
		return s.finish(trade, order, true, c2c.TradeReleased), nil
	})
}

func (s *fakeC2CStore) Cancel(_ context.Context, id, actorID string) (c2c.Trade, error) {
	return s.apply(id, func(order c2c.Order, trade c2c.Trade) (c2c.Trade, error) {
		if noop, err := trade.CheckCancel(actorID, time.Now()); err != nil || noop {
			return trade, err
		}
		return s.finish(trade, order, false, c2c.TradeCancelled), nil
	})
}

func (s *fakeC2CStore) Dispute(_ context.Context, id, actorID, statement string) (c2c.Trade, error) {
	return s.apply(id, func(_ c2c.Order, trade c2c.Trade) (c2c.Trade, error) {
		if err := trade.CheckDispute(actorID); err != nil {
			return trade, err
		}
		now := s.tick()
		if trade.DisputedAt == nil {
			trade.DisputedAt, trade.DisputeOpenedBy = &now, &actorID
		}
		trade.Status = c2c.TradeDisputed
		if actorID == trade.Buyer.ID {
			trade.BuyerStatement = &statement
		} else {
			trade.SellerStatement = &statement
		}
		return trade, nil
	})
}

func (s *fakeC2CStore) Resolve(_ context.Context, r c2c.Resolution) (c2c.Trade, error) {
	return s.apply(r.TradeID, func(order c2c.Order, trade c2c.Trade) (c2c.Trade, error) {
		if noop, err := trade.CheckResolve(r.ToBuyer); err != nil || noop {
			return trade, err
		}
		status := c2c.TradeResolvedSeller
		if r.ToBuyer {
			status = c2c.TradeResolvedBuyer
		}
		trade = s.finish(trade, order, r.ToBuyer, status)
		trade.ReleasedAt, trade.ResolutionReason = nil, &r.Reason
		now := s.tick()
		trade.ResolvedAt = &now
		return trade, nil
	})
}

func newC2CHandler(store *fakeStore, c2cStore *fakeC2CStore) http.Handler {
	identityService, err := identity.NewService(store, time.Hour)
	if err != nil {
		panic(err)
	}
	keyring, err := c2c.ParseKeyring("k1=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", "k1")
	if err != nil {
		panic(err)
	}
	return NewHandler(Dependencies{
		Identity: identityService, Catalog: catalog.NewService(store), Ledger: ledger.NewService(store),
		Settings: settings.NewService(store), Audit: audit.NewService(store), C2C: c2c.NewService(c2cStore, keyring), CookieSecure: true,
		Observe: observe.NewService(&fakeObserveStore{}),
	})
}

// newMember creates a ready member account and returns its client and ID.
func newMember(t *testing.T, spec *openAPISpec, handler http.Handler, admin *client, username string) (*client, string) {
	t.Helper()
	created := admin.expect(t, http.StatusCreated, http.MethodPost, "/api/admin/accounts", map[string]any{"username": username, "display_name": "用户" + username})
	initial := created["initial_password"].(string)
	member := &client{spec: spec, handler: handler}
	member.expect(t, http.StatusOK, http.MethodPost, "/api/auth/login", map[string]string{"username": username, "password": initial})
	member.expect(t, http.StatusOK, http.MethodPost, "/api/me/password", map[string]string{"current_password": initial, "new_password": "Member-password-2026"})
	return member, created["account"].(map[string]any)["id"].(string)
}

func TestC2CHandlers(t *testing.T) {
	spec := loadOpenAPI(t)
	store := newFakeStore()
	c2cStore := newFakeC2CStore(store)
	handler := newC2CHandler(store, c2cStore)
	admin := bootstrap(t, spec, handler)
	seller, sellerID := newMember(t, spec, handler, admin, "seller")
	buyer, buyerID := newMember(t, spec, handler, admin, "buyer")
	other, _ := newMember(t, spec, handler, admin, "other")
	c2cStore.balances[sellerID] = money.Amount(100 * money.Scale)

	listing := map[string]any{
		"amount": "40", "unit_price_fen": 92, "min_per_trade": "5", "max_per_trade": "30",
		"payment_methods": []map[string]string{{"channel": "支付宝", "account": "seller@example.com"}, {"channel": "银行卡", "account": "6222 0000 0000"}},
	}
	// Listing validation: balance, payment methods, amounts.
	poor := map[string]any{"amount": "1000", "unit_price_fen": 92, "payment_methods": listing["payment_methods"]}
	if recorder := seller.call(t, http.MethodPost, "/api/c2c/orders", poor); recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != "insufficient_balance" {
		t.Fatalf("over balance = %d %s", recorder.Code, recorder.Body.String())
	}
	for name, body := range map[string]map[string]any{
		"no methods":    {"amount": "10", "unit_price_fen": 90, "payment_methods": []map[string]string{}},
		"bad amount":    {"amount": "ten", "unit_price_fen": 90, "payment_methods": listing["payment_methods"]},
		"min above max": {"amount": "10", "unit_price_fen": 90, "min_per_trade": "8", "max_per_trade": "5", "payment_methods": listing["payment_methods"]},
	} {
		if recorder := seller.call(t, http.MethodPost, "/api/c2c/orders", body); recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d %s", name, recorder.Code, recorder.Body.String())
		}
	}
	if recorder := seller.call(t, http.MethodPost, "/api/c2c/orders", map[string]any{"amount": "1", "unit_price_fen": 90, "bogus": true}); recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", recorder.Code)
	}
	created := seller.expect(t, http.StatusCreated, http.MethodPost, "/api/c2c/orders", listing)["order"].(map[string]any)
	orderID := created["id"].(string)
	if created["total"] != "40" || created["available"] != "40" || created["status"] != "open" || len(created["payment_methods"].([]any)) != 2 {
		t.Fatalf("created order = %v", created)
	}

	// Market: other users see names of payment methods only, never accounts; the seller does not see it as buyable.
	market := buyer.expect(t, http.StatusOK, http.MethodGet, "/api/c2c/orders", nil)
	items := market["items"].([]any)
	if len(items) != 1 || strings.Contains(buyer.call(t, http.MethodGet, "/api/c2c/orders", nil).Body.String(), "seller@example.com") {
		t.Fatalf("market = %v", market)
	}
	if channels := items[0].(map[string]any)["payment_channels"].([]any); channels[0] != "支付宝" || channels[1] != "银行卡" {
		t.Fatalf("channels = %v", channels)
	}
	if own := seller.expect(t, http.StatusOK, http.MethodGet, "/api/c2c/orders", nil); len(own["items"].([]any)) != 0 {
		t.Fatalf("own order in market: %v", own)
	}
	if recorder := buyer.call(t, http.MethodGet, "/api/c2c/orders?cursor=bogus", nil); recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_cursor" {
		t.Fatalf("bad cursor = %d %s", recorder.Code, recorder.Body.String())
	}
	if mine := seller.expect(t, http.StatusOK, http.MethodGet, "/api/c2c/my/orders?status=open", nil); len(mine["items"].([]any)) != 1 {
		t.Fatalf("my orders = %v", mine)
	}
	if recorder := seller.call(t, http.MethodGet, "/api/c2c/my/orders?status=bogus", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad status filter = %d", recorder.Code)
	}

	// Buying: range, own order, duplicates.
	tradesPath := "/api/c2c/orders/" + orderID + "/trades"
	if recorder := buyer.call(t, http.MethodPost, tradesPath, map[string]string{"amount": "31"}); recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != "amount_out_of_range" {
		t.Fatalf("above max = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := seller.call(t, http.MethodPost, tradesPath, map[string]string{"amount": "10"}); recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != "own_order" {
		t.Fatalf("own order = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := buyer.call(t, http.MethodPost, "/api/c2c/orders/00000000-0000-4000-8000-00000000dead/trades", map[string]string{"amount": "10"}); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown order = %d", recorder.Code)
	}
	trade := buyer.expect(t, http.StatusCreated, http.MethodPost, tradesPath, map[string]string{"amount": "30"}, withHeader("Idempotency-Key", "buy-1"))["trade"].(map[string]any)
	tradeID := trade["id"].(string)
	if trade["viewer_role"] != "buyer" || trade["status"] != "awaiting_payment" || trade["total_fen"] != float64(2760) || len(trade["payment_methods"].([]any)) != 2 {
		t.Fatalf("trade = %v", trade)
	}
	if recorder := buyer.call(t, http.MethodPost, tradesPath, map[string]string{"amount": "5"}); recorder.Code != http.StatusConflict || errorCode(t, recorder) != "duplicate_trade" {
		t.Fatalf("duplicate = %d %s", recorder.Code, recorder.Body.String())
	}

	// Visibility: parties and administrators only.
	tradePath := "/api/c2c/trades/" + tradeID
	if recorder := other.call(t, http.MethodGet, tradePath, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("stranger = %d", recorder.Code)
	}
	if view := seller.expect(t, http.StatusOK, http.MethodGet, tradePath, nil)["trade"].(map[string]any); view["viewer_role"] != "seller" || len(view["payment_methods"].([]any)) != 2 {
		t.Fatalf("seller view = %v", view)
	}
	if view := admin.expect(t, http.StatusOK, http.MethodGet, tradePath, nil)["trade"].(map[string]any); view["viewer_role"] != "admin" {
		t.Fatalf("admin view = %v", view)
	}
	if recorder := buyer.call(t, http.MethodGet, "/api/c2c/trades/not-a-uuid", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("bad id = %d", recorder.Code)
	}

	// Pending lists, paid, wrong-actor release.
	if pending := buyer.expect(t, http.StatusOK, http.MethodGet, "/api/c2c/my/trades?pending=true", nil); len(pending["items"].([]any)) != 1 {
		t.Fatalf("buyer pending = %v", pending)
	}
	if pending := seller.expect(t, http.StatusOK, http.MethodGet, "/api/c2c/my/trades?pending=true", nil); len(pending["items"].([]any)) != 0 {
		t.Fatalf("seller pending before paid = %v", pending)
	}
	if recorder := seller.call(t, http.MethodPost, tradePath+"/paid", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("seller marks paid = %d", recorder.Code)
	}
	if recorder := buyer.call(t, http.MethodPost, tradePath+"/paid", map[string]string{"note": strings.Repeat("字", 501)}); recorder.Code != http.StatusBadRequest {
		t.Fatalf("long note = %d", recorder.Code)
	}
	paid := buyer.expect(t, http.StatusOK, http.MethodPost, tradePath+"/paid", map[string]string{"note": "已转账"})["trade"].(map[string]any)
	if paid["status"] != "paid" || paid["buyer_note"] != "已转账" {
		t.Fatalf("paid = %v", paid)
	}
	if pending := seller.expect(t, http.StatusOK, http.MethodGet, "/api/c2c/my/trades?pending=true&role=seller", nil); len(pending["items"].([]any)) != 1 {
		t.Fatalf("seller pending = %v", pending)
	}
	if recorder := buyer.call(t, http.MethodPost, tradePath+"/release", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("buyer release = %d", recorder.Code)
	}
	if recorder := buyer.call(t, http.MethodPost, tradePath+"/cancel", nil); recorder.Code != http.StatusConflict || errorCode(t, recorder) != "invalid_state" {
		t.Fatalf("cancel after paid = %d %s", recorder.Code, recorder.Body.String())
	}

	// Dispute and arbitration.
	if recorder := other.call(t, http.MethodPost, tradePath+"/dispute", map[string]string{"statement": "旁观者"}); recorder.Code != http.StatusForbidden {
		t.Fatalf("stranger dispute = %d", recorder.Code)
	}
	if recorder := seller.call(t, http.MethodPost, tradePath+"/dispute", map[string]string{"statement": strings.Repeat("字", 1001)}); recorder.Code != http.StatusBadRequest {
		t.Fatalf("long statement = %d", recorder.Code)
	}
	disputed := seller.expect(t, http.StatusOK, http.MethodPost, tradePath+"/dispute", map[string]string{"statement": "没有收到款项"})["trade"].(map[string]any)
	if disputed["status"] != "disputed" || disputed["seller_statement"] != "没有收到款项" || disputed["dispute_opened_by"] != sellerID {
		t.Fatalf("disputed = %v", disputed)
	}
	if recorder := seller.call(t, http.MethodPost, tradePath+"/release", nil); recorder.Code != http.StatusConflict {
		t.Fatalf("release while disputed = %d", recorder.Code)
	}
	if recorder := seller.call(t, http.MethodGet, "/api/admin/disputes", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("member disputes = %d", recorder.Code)
	}
	queue := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/disputes", nil)["items"].([]any)
	if len(queue) != 1 || queue[0].(map[string]any)["id"] != tradeID {
		t.Fatalf("dispute queue = %v", queue)
	}
	resolvePath := "/api/admin/c2c/trades/" + tradeID + "/resolve"
	if recorder := seller.call(t, http.MethodPost, resolvePath, map[string]string{"result": "seller", "reason": "x"}); recorder.Code != http.StatusForbidden {
		t.Fatalf("member resolve = %d", recorder.Code)
	}
	if recorder := admin.call(t, http.MethodPost, resolvePath, map[string]string{"result": "buyer"}); recorder.Code != http.StatusBadRequest && recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("resolve without reason = %d", recorder.Code)
	}
	resolved := admin.expect(t, http.StatusOK, http.MethodPost, resolvePath, map[string]string{"result": "buyer", "reason": "转账记录属实"})["trade"].(map[string]any)
	if resolved["status"] != "resolved_to_buyer" || resolved["resolution_reason"] != "转账记录属实" {
		t.Fatalf("resolved = %v", resolved)
	}
	if view := buyer.expect(t, http.StatusOK, http.MethodGet, tradePath, nil)["trade"].(map[string]any); len(view["payment_methods"].([]any)) != 0 {
		t.Fatalf("buyer still sees accounts after the ruling: %v", view["payment_methods"])
	}

	// Close the order: the unsold part returns, trades are untouched.
	if recorder := buyer.call(t, http.MethodPost, "/api/c2c/orders/"+orderID+"/close", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("close by buyer = %d", recorder.Code)
	}
	closed := seller.expect(t, http.StatusOK, http.MethodPost, "/api/c2c/orders/"+orderID+"/close", nil)["order"].(map[string]any)
	if closed["status"] != "closed" || closed["available"] != "0" || closed["closed"] != "10" || closed["sold"] != "30" {
		t.Fatalf("closed = %v", closed)
	}
	if recorder := buyer.call(t, http.MethodPost, tradesPath, map[string]string{"amount": "5"}); recorder.Code != http.StatusConflict || errorCode(t, recorder) != "order_not_open" {
		t.Fatalf("buy on closed = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := buyer.call(t, http.MethodGet, "/api/c2c/orders", nil, withoutCookie); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous market = %d", recorder.Code)
	}
	_ = buyerID
}

func TestC2CReleaseAndCancel(t *testing.T) {
	t.Parallel()
	p := newPlatform(t)
	seller, sellerID := p.member(t, "seller")
	buyer, _ := p.member(t, "buyer")
	p.c2c.balances[sellerID] = money.Amount(100 * money.Scale)
	order := seller.expect(t, http.StatusCreated, http.MethodPost, "/api/c2c/orders", map[string]any{
		"amount": "40", "unit_price_fen": 92, "min_per_trade": "5", "max_per_trade": "30",
		"payment_methods": []map[string]string{{"channel": "支付宝", "account": "seller@example.com"}},
	})["order"].(map[string]any)
	tradesPath := "/api/c2c/orders/" + order["id"].(string) + "/trades"

	// 买家在付款前取消：锁定的数量回到卖单，重复取消没有副作用。
	cancelling := buyer.expect(t, http.StatusCreated, http.MethodPost, tradesPath, map[string]string{"amount": "10"})["trade"].(map[string]any)["id"].(string)
	if recorder := seller.call(t, http.MethodPost, "/api/c2c/trades/"+cancelling+"/cancel", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("seller cancels an unpaid trade = %d %s", recorder.Code, recorder.Body.String())
	}
	if again := buyer.expect(t, http.StatusOK, http.MethodPost, "/api/c2c/trades/"+cancelling+"/cancel", nil)["trade"].(map[string]any); again["status"] != "cancelled" {
		t.Fatalf("cancelled again = %v", again)
	}
	if mine := seller.expect(t, http.StatusOK, http.MethodGet, "/api/c2c/my/orders", nil)["items"].([]any); mine[0].(map[string]any)["available"] != "40" {
		t.Fatalf("order after cancel = %v", mine)
	}

	// 买家付款后卖家放行：重复放行同样没有副作用，已放行的交易不能再取消。
	releasing := buyer.expect(t, http.StatusCreated, http.MethodPost, tradesPath, map[string]string{"amount": "20"})["trade"].(map[string]any)["id"].(string)
	releasePath := "/api/c2c/trades/" + releasing + "/release"
	buyer.expect(t, http.StatusOK, http.MethodPost, "/api/c2c/trades/"+releasing+"/paid", nil)
	released := seller.expect(t, http.StatusOK, http.MethodPost, releasePath, nil)["trade"].(map[string]any)
	if released["status"] != "released" || released["viewer_role"] != "seller" {
		t.Fatalf("released = %v", released)
	}
	if again := seller.expect(t, http.StatusOK, http.MethodPost, releasePath, nil)["trade"].(map[string]any); again["status"] != "released" {
		t.Fatalf("released again = %v", again)
	}
	if recorder := buyer.call(t, http.MethodPost, "/api/c2c/trades/"+releasing+"/cancel", nil); recorder.Code != http.StatusConflict {
		t.Fatalf("cancel after release = %d %s", recorder.Code, recorder.Body.String())
	}
}
