package api

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func partyResponse(p c2c.Party) map[string]any {
	return map[string]any{"id": p.ID, "display_name": p.DisplayName}
}

func methodsResponse(methods []c2c.PaymentMethod) []map[string]string {
	items := make([]map[string]string, 0, len(methods))
	for _, method := range methods {
		items = append(items, map[string]string{"channel": method.Channel, "account": method.Account})
	}
	return items
}

func marketOrderResponse(order c2c.MarketOrder) map[string]any {
	channels := order.Channels
	if channels == nil {
		channels = []string{}
	}
	return map[string]any{
		"id": order.ID, "seller": partyResponse(order.Seller), "available": order.Available.String(),
		"min_per_trade": order.MinPerTrade.String(), "max_per_trade": nullableAmount(order.MaxPerTrade),
		"unit_price_fen": order.UnitPriceFen, "payment_channels": channels, "created_at": order.CreatedAt,
	}
}

func myOrderResponse(order c2c.MyOrder) map[string]any {
	return map[string]any{
		"id": order.ID, "total": order.Total.String(), "available": order.Available.String(), "in_trade": order.InTrade.String(),
		"sold": order.Sold.String(), "closed": order.Closed.String(), "unit_price_fen": order.UnitPriceFen,
		"min_per_trade": order.MinPerTrade.String(), "max_per_trade": nullableAmount(order.MaxPerTrade),
		"payment_methods": methodsResponse(order.PaymentMethods), "status": order.Status,
		"created_at": order.CreatedAt, "updated_at": order.UpdatedAt, "closed_at": order.ClosedAt,
	}
}

func tradeResponse(view c2c.TradeView) map[string]any {
	return map[string]any{
		"id": view.ID, "order_id": view.OrderID, "viewer_role": view.ViewerRole,
		"buyer": partyResponse(view.Buyer), "seller": partyResponse(view.Seller),
		"amount": view.Amount.String(), "unit_price_fen": view.UnitPriceFen, "total_fen": view.TotalFen,
		"status": view.Status, "payment_deadline": view.PaymentDeadline, "payment_methods": methodsResponse(view.PaymentMethods),
		"buyer_note": view.BuyerNote, "dispute_opened_by": view.DisputeOpenedBy, "buyer_statement": view.BuyerStatement,
		"seller_statement": view.SellerStatement, "resolution_reason": view.ResolutionReason, "created_at": view.CreatedAt,
		"paid_at": view.PaidAt, "released_at": view.ReleasedAt, "cancelled_at": view.CancelledAt,
		"disputed_at": view.DisputedAt, "resolved_at": view.ResolvedAt,
	}
}

func writeTrade(w http.ResponseWriter, status int, view c2c.TradeView) {
	writeJSON(w, status, map[string]any{"trade": tradeResponse(view)})
}

func writePage[T any](w http.ResponseWriter, page c2c.Page[T], item func(T) map[string]any) {
	items := make([]map[string]any, 0, len(page.Items))
	for _, entry := range page.Items {
		items = append(items, item(entry))
	}
	var next *string
	if page.Next != "" {
		next = &page.Next
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func clientIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key 过长")
		return "", false
	}
	return key, true
}

func parseOptionalAmount(raw *string) (*money.Amount, error) {
	if raw == nil {
		return nil, nil
	}
	amount, err := money.Parse(*raw)
	if err != nil {
		return nil, err
	}
	return &amount, nil
}

func (a *app) listC2COrders(w http.ResponseWriter, r *http.Request) {
	limit, ok := pageLimit(r)
	if !ok {
		writeBadCursor(w)
		return
	}
	page, err := a.c2c.ListMarket(r.Context(), accountFromContext(r.Context()).ID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writePage(w, page, marketOrderResponse)
}

func (a *app) createC2COrder(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Amount       string  `json:"amount"`
		UnitPriceFen int64   `json:"unit_price_fen"`
		MinPerTrade  *string `json:"min_per_trade"`
		MaxPerTrade  *string `json:"max_per_trade"`
		Methods      []struct {
			Channel string `json:"channel"`
			Account string `json:"account"`
		} `json:"payment_methods"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	key, ok := clientIdempotencyKey(w, r)
	if !ok {
		return
	}
	amount, err := money.Parse(request.Amount)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	minPerTrade, err := parseOptionalAmount(request.MinPerTrade)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	maxPerTrade, err := parseOptionalAmount(request.MaxPerTrade)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	methods := make([]c2c.PaymentMethod, 0, len(request.Methods))
	for _, method := range request.Methods {
		methods = append(methods, c2c.PaymentMethod{Channel: method.Channel, Account: method.Account})
	}
	order, err := a.c2c.CreateOrder(r.Context(), c2c.CreateOrderInput{
		SellerID: accountFromContext(r.Context()).ID, Amount: amount, UnitPriceFen: request.UnitPriceFen,
		MinPerTrade: minPerTrade, MaxPerTrade: maxPerTrade, PaymentMethods: methods, IdempotencyKey: key,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"order": myOrderResponse(order)})
}

func (a *app) listMyC2COrders(w http.ResponseWriter, r *http.Request) {
	limit, ok := pageLimit(r)
	status := c2c.OrderStatus(r.URL.Query().Get("status"))
	if !ok || (status != "" && !status.Valid()) {
		writeError(w, http.StatusBadRequest, "invalid_request", "查询参数无效")
		return
	}
	page, err := a.c2c.ListMyOrders(r.Context(), accountFromContext(r.Context()).ID, status, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writePage(w, page, myOrderResponse)
}

func (a *app) closeC2COrder(w http.ResponseWriter, r *http.Request) {
	orderID, ok := pathUUID(w, r, "orderID")
	if !ok {
		return
	}
	order, err := a.c2c.CloseOrder(r.Context(), orderID, accountFromContext(r.Context()).ID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": myOrderResponse(order)})
}

func (a *app) createC2CTrade(w http.ResponseWriter, r *http.Request) {
	orderID, ok := pathUUID(w, r, "orderID")
	if !ok {
		return
	}
	var request struct {
		Amount string `json:"amount"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	key, ok := clientIdempotencyKey(w, r)
	if !ok {
		return
	}
	amount, err := money.Parse(request.Amount)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	view, err := a.c2c.CreateTrade(r.Context(), orderID, accountFromContext(r.Context()).ID, amount, key)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeTrade(w, http.StatusCreated, view)
}

func (a *app) listMyC2CTrades(w http.ResponseWriter, r *http.Request) {
	limit, ok := pageLimit(r)
	query := r.URL.Query()
	pending, role, status := query.Get("pending"), query.Get("role"), c2c.TradeStatus(query.Get("status"))
	if !ok || (pending != "" && pending != "true" && pending != "false") || (role != "" && role != "buyer" && role != "seller") ||
		(status != "" && !status.Valid()) {
		writeError(w, http.StatusBadRequest, "invalid_request", "查询参数无效")
		return
	}
	page, err := a.c2c.ListMyTrades(r.Context(), c2c.TradeFilter{
		UserID: accountFromContext(r.Context()).ID, Role: role, Status: status,
		Pending: pending == "true", Limit: limit,
	}, query.Get("cursor"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writePage(w, page, tradeResponse)
}

func (a *app) viewer(r *http.Request) c2c.Viewer {
	account := accountFromContext(r.Context())
	return c2c.Viewer{ID: account.ID, IsAdmin: account.IsAdmin}
}

func (a *app) getC2CTrade(w http.ResponseWriter, r *http.Request) {
	tradeID, ok := pathUUID(w, r, "tradeID")
	if !ok {
		return
	}
	view, err := a.c2c.GetTrade(r.Context(), a.viewer(r), tradeID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeTrade(w, http.StatusOK, view)
}

func (a *app) markC2CTradePaid(w http.ResponseWriter, r *http.Request) {
	tradeID, ok := pathUUID(w, r, "tradeID")
	if !ok {
		return
	}
	var request struct {
		Note string `json:"note"`
	}
	if err := decodeOptionalJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	if utf8.RuneCountInString(strings.TrimSpace(request.Note)) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_request", "付款说明不能超过 500 字")
		return
	}
	view, err := a.c2c.MarkPaid(r.Context(), tradeID, accountFromContext(r.Context()).ID, request.Note)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeTrade(w, http.StatusOK, view)
}

func (a *app) releaseC2CTrade(w http.ResponseWriter, r *http.Request) {
	tradeID, ok := pathUUID(w, r, "tradeID")
	if !ok {
		return
	}
	view, err := a.c2c.Release(r.Context(), tradeID, accountFromContext(r.Context()).ID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeTrade(w, http.StatusOK, view)
}

func (a *app) cancelC2CTrade(w http.ResponseWriter, r *http.Request) {
	tradeID, ok := pathUUID(w, r, "tradeID")
	if !ok {
		return
	}
	view, err := a.c2c.Cancel(r.Context(), tradeID, accountFromContext(r.Context()).ID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeTrade(w, http.StatusOK, view)
}

func (a *app) disputeC2CTrade(w http.ResponseWriter, r *http.Request) {
	tradeID, ok := pathUUID(w, r, "tradeID")
	if !ok {
		return
	}
	var request struct {
		Statement string `json:"statement"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	if statement := strings.TrimSpace(request.Statement); statement == "" || utf8.RuneCountInString(statement) > 1000 {
		writeError(w, http.StatusBadRequest, "invalid_request", "陈述须为 1 到 1000 字")
		return
	}
	view, err := a.c2c.Dispute(r.Context(), tradeID, accountFromContext(r.Context()).ID, request.Statement)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeTrade(w, http.StatusOK, view)
}

func (a *app) listAdminDisputes(w http.ResponseWriter, r *http.Request) {
	limit, ok := pageLimit(r)
	status := c2c.TradeStatus(r.URL.Query().Get("status"))
	switch status {
	case "", c2c.TradeDisputed, c2c.TradeResolvedBuyer, c2c.TradeResolvedSeller:
	default:
		ok = false
	}
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "查询参数无效")
		return
	}
	page, err := a.c2c.ListDisputes(r.Context(), a.viewer(r), status, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writePage(w, page, tradeResponse)
}

func (a *app) resolveAdminC2CTrade(w http.ResponseWriter, r *http.Request) {
	tradeID, ok := pathUUID(w, r, "tradeID")
	if !ok {
		return
	}
	var request struct {
		Result string `json:"result"`
		Reason string `json:"reason"`
	}
	if err := decodeJSON(w, r, &request); err != nil || (request.Result != "buyer" && request.Result != "seller") {
		writeInvalidJSON(w)
		return
	}
	view, err := a.c2c.Resolve(r.Context(), c2c.Resolution{
		TradeID: tradeID, AdminID: accountFromContext(r.Context()).ID, ToBuyer: request.Result == "buyer", Reason: request.Reason,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeTrade(w, http.StatusOK, view)
}

// registerC2CRoutes 注册 C2C 卖单市场与管理员仲裁（Feature C）。
func (a *app) registerC2CRoutes(r *router) {
	r.implement("C", "GET /api/c2c/orders", accessReady, a.listC2COrders)
	r.implement("C", "POST /api/c2c/orders", accessReady, a.createC2COrder)
	r.implement("C", "GET /api/c2c/my/orders", accessReady, a.listMyC2COrders)
	r.implement("C", "POST /api/c2c/orders/{orderID}/close", accessReady, a.closeC2COrder)
	r.implement("C", "POST /api/c2c/orders/{orderID}/trades", accessReady, a.createC2CTrade)
	r.implement("C", "GET /api/c2c/my/trades", accessReady, a.listMyC2CTrades)
	r.implement("C", "GET /api/c2c/trades/{tradeID}", accessReady, a.getC2CTrade)
	r.implement("C", "POST /api/c2c/trades/{tradeID}/paid", accessReady, a.markC2CTradePaid)
	r.implement("C", "POST /api/c2c/trades/{tradeID}/release", accessReady, a.releaseC2CTrade)
	r.implement("C", "POST /api/c2c/trades/{tradeID}/cancel", accessReady, a.cancelC2CTrade)
	r.implement("C", "POST /api/c2c/trades/{tradeID}/dispute", accessReady, a.disputeC2CTrade)
	r.implement("C", "GET /api/admin/disputes", accessAdmin, a.listAdminDisputes)
	r.implement("C", "POST /api/admin/c2c/trades/{tradeID}/resolve", accessAdmin, a.resolveAdminC2CTrade)
}
