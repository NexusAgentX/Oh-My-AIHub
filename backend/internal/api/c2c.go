package api

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// partyJSON is the OpenAPI Party schema.
type partyJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// paymentMethodJSON is the OpenAPI PaymentMethod schema.
type paymentMethodJSON struct {
	Channel string `json:"channel"`
	Account string `json:"account"`
}

// c2cOrderJSON is the OpenAPI C2COrder schema.
type c2cOrderJSON struct {
	ID              string    `json:"id"`
	Seller          partyJSON `json:"seller"`
	Available       string    `json:"available"`
	MinPerTrade     string    `json:"min_per_trade"`
	MaxPerTrade     *string   `json:"max_per_trade"`
	UnitPriceFen    int64     `json:"unit_price_fen"`
	PaymentChannels []string  `json:"payment_channels"`
	CreatedAt       time.Time `json:"created_at"`
}

// c2cMyOrderJSON is the OpenAPI C2CMyOrder schema.
type c2cMyOrderJSON struct {
	ID             string              `json:"id"`
	Total          string              `json:"total"`
	Available      string              `json:"available"`
	InTrade        string              `json:"in_trade"`
	Sold           string              `json:"sold"`
	Closed         string              `json:"closed"`
	UnitPriceFen   int64               `json:"unit_price_fen"`
	MinPerTrade    string              `json:"min_per_trade"`
	MaxPerTrade    *string             `json:"max_per_trade"`
	PaymentMethods []paymentMethodJSON `json:"payment_methods"`
	Status         c2c.OrderStatus     `json:"status"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	ClosedAt       *time.Time          `json:"closed_at"`
}

// c2cMyOrderEnvelopeJSON is the OpenAPI C2CMyOrderEnvelope schema.
type c2cMyOrderEnvelopeJSON struct {
	Order c2cMyOrderJSON `json:"order"`
}

// c2cTradeJSON is the OpenAPI C2CTrade schema.
type c2cTradeJSON struct {
	ID               string              `json:"id"`
	OrderID          string              `json:"order_id"`
	ViewerRole       string              `json:"viewer_role"`
	Buyer            partyJSON           `json:"buyer"`
	Seller           partyJSON           `json:"seller"`
	Amount           string              `json:"amount"`
	UnitPriceFen     int64               `json:"unit_price_fen"`
	TotalFen         int64               `json:"total_fen"`
	Status           c2c.TradeStatus     `json:"status"`
	PaymentDeadline  time.Time           `json:"payment_deadline"`
	PaymentMethods   []paymentMethodJSON `json:"payment_methods"`
	BuyerNote        *string             `json:"buyer_note"`
	DisputeOpenedBy  *string             `json:"dispute_opened_by"`
	BuyerStatement   *string             `json:"buyer_statement"`
	SellerStatement  *string             `json:"seller_statement"`
	ResolutionReason *string             `json:"resolution_reason"`
	CreatedAt        time.Time           `json:"created_at"`
	PaidAt           *time.Time          `json:"paid_at"`
	ReleasedAt       *time.Time          `json:"released_at"`
	CancelledAt      *time.Time          `json:"cancelled_at"`
	DisputedAt       *time.Time          `json:"disputed_at"`
	ResolvedAt       *time.Time          `json:"resolved_at"`
}

// c2cTradeEnvelopeJSON is the OpenAPI C2CTradeEnvelope schema.
type c2cTradeEnvelopeJSON struct {
	Trade c2cTradeJSON `json:"trade"`
}

func newPartyJSON(p c2c.Party) partyJSON {
	return partyJSON{ID: p.ID, DisplayName: p.DisplayName}
}

func newPaymentMethodsJSON(methods []c2c.PaymentMethod) []paymentMethodJSON {
	items := make([]paymentMethodJSON, 0, len(methods))
	for _, method := range methods {
		items = append(items, paymentMethodJSON{Channel: method.Channel, Account: method.Account})
	}
	return items
}

func newC2cOrderJSON(order c2c.MarketOrder) c2cOrderJSON {
	channels := order.Channels
	if channels == nil {
		channels = []string{}
	}
	return c2cOrderJSON{
		ID:              order.ID,
		Seller:          newPartyJSON(order.Seller),
		Available:       order.Available.String(),
		MinPerTrade:     order.MinPerTrade.String(),
		MaxPerTrade:     nullableAmount(order.MaxPerTrade),
		UnitPriceFen:    order.UnitPriceFen,
		PaymentChannels: channels,
		CreatedAt:       order.CreatedAt,
	}
}

func newC2cMyOrderJSON(order c2c.MyOrder) c2cMyOrderJSON {
	return c2cMyOrderJSON{
		ID:             order.ID,
		Total:          order.Total.String(),
		Available:      order.Available.String(),
		InTrade:        order.InTrade.String(),
		Sold:           order.Sold.String(),
		Closed:         order.Closed.String(),
		UnitPriceFen:   order.UnitPriceFen,
		MinPerTrade:    order.MinPerTrade.String(),
		MaxPerTrade:    nullableAmount(order.MaxPerTrade),
		PaymentMethods: newPaymentMethodsJSON(order.PaymentMethods),
		Status:         order.Status,
		CreatedAt:      order.CreatedAt,
		UpdatedAt:      order.UpdatedAt,
		ClosedAt:       order.ClosedAt,
	}
}

func newC2cTradeJSON(view c2c.TradeView) c2cTradeJSON {
	return c2cTradeJSON{
		ID:               view.ID,
		OrderID:          view.OrderID,
		ViewerRole:       view.ViewerRole,
		Buyer:            newPartyJSON(view.Buyer),
		Seller:           newPartyJSON(view.Seller),
		Amount:           view.Amount.String(),
		UnitPriceFen:     view.UnitPriceFen,
		TotalFen:         view.TotalFen,
		Status:           view.Status,
		PaymentDeadline:  view.PaymentDeadline,
		PaymentMethods:   newPaymentMethodsJSON(view.PaymentMethods),
		BuyerNote:        view.BuyerNote,
		DisputeOpenedBy:  view.DisputeOpenedBy,
		BuyerStatement:   view.BuyerStatement,
		SellerStatement:  view.SellerStatement,
		ResolutionReason: view.ResolutionReason,
		CreatedAt:        view.CreatedAt,
		PaidAt:           view.PaidAt,
		ReleasedAt:       view.ReleasedAt,
		CancelledAt:      view.CancelledAt,
		DisputedAt:       view.DisputedAt,
		ResolvedAt:       view.ResolvedAt,
	}
}

func writeTrade(w http.ResponseWriter, status int, view c2c.TradeView) {
	writeJSON(w, status, c2cTradeEnvelopeJSON{Trade: newC2cTradeJSON(view)})
}

// writePage converts one cursor page of domain values into its response page.
func writePage[T, J any](w http.ResponseWriter, page c2c.Page[T], convert func(T) J) {
	items := make([]J, 0, len(page.Items))
	for _, entry := range page.Items {
		items = append(items, convert(entry))
	}
	var next *string
	if page.Next != "" {
		next = &page.Next
	}
	writeJSON(w, http.StatusOK, pageJSON[J]{Items: items, NextCursor: next})
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
	writePage(w, page, newC2cOrderJSON)
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
	writeJSON(w, http.StatusCreated, c2cMyOrderEnvelopeJSON{Order: newC2cMyOrderJSON(order)})
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
	writePage(w, page, newC2cMyOrderJSON)
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
	writeJSON(w, http.StatusOK, c2cMyOrderEnvelopeJSON{Order: newC2cMyOrderJSON(order)})
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
	writePage(w, page, newC2cTradeJSON)
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
	writePage(w, page, newC2cTradeJSON)
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
