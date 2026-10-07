package api

import (
	"net/http"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type c2cPaymentMethodRequest struct {
	Type         c2c.PaymentMethodType `json:"type"`
	Contact      string                `json:"contact"`
	Instructions string                `json:"instructions"`
}

type c2cCreateOrderRequest struct {
	Side           c2c.Side                  `json:"side"`
	UnitPriceFen   int64                     `json:"unit_price_fen"`
	Total          string                    `json:"total"`
	Minimum        string                    `json:"minimum"`
	Maximum        string                    `json:"maximum"`
	PaymentMethods []c2cPaymentMethodRequest `json:"payment_methods"`
}

func (a *app) c2cMarket(w http.ResponseWriter, r *http.Request) {
	market, err := a.c2c.Market(r.Context(), accountFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	sell := make([]map[string]any, 0, len(market.SellOrders))
	for _, order := range market.SellOrders {
		sell = append(sell, c2cOrderResponse(order))
	}
	buy := make([]map[string]any, 0, len(market.BuyOrders))
	for _, order := range market.BuyOrders {
		buy = append(buy, c2cOrderResponse(order))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"metrics": map[string]any{
			"guidance_price_fen": market.GuidancePriceFen,
			"latest_price_fen":   market.LatestPriceFen,
			"best_bid_fen":       market.BestBidFen,
			"best_ask_fen":       market.BestAskFen,
			"spread_fen":         market.SpreadFen,
		},
		"sell_orders": sell,
		"buy_orders":  buy,
	})
}

func (a *app) c2cCreateOrder(w http.ResponseWriter, r *http.Request) {
	var request c2cCreateOrderRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	total, err := money.Parse(request.Total)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	minimum, err := money.Parse(request.Minimum)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	maximum, err := money.Parse(request.Maximum)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	methods := make([]c2c.PaymentMethodInput, 0, len(request.PaymentMethods))
	for _, item := range request.PaymentMethods {
		method := c2c.PaymentMethodInput{Type: item.Type, Contact: item.Contact, Instructions: item.Instructions}
		methods = append(methods, method)
	}
	created, err := a.c2c.CreateOrder(
		r.Context(), accountFromContext(r.Context()), idempotencyKey(r),
		request.Side, request.UnitPriceFen, total, minimum, maximum, methods,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"order": c2cOrderResponse(created)})
}

func (a *app) c2cOrder(w http.ResponseWriter, r *http.Request) {
	order, err := a.c2c.Order(r.Context(), accountFromContext(r.Context()), r.PathValue("orderID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": c2cOrderResponse(order)})
}

func (a *app) c2cTakeOrder(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Quantity        string `json:"quantity"`
		PaymentMethodID string `json:"payment_method_id"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	quantity, err := money.Parse(request.Quantity)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	trade, err := a.c2c.TakeOrder(
		r.Context(), accountFromContext(r.Context()), idempotencyKey(r), r.PathValue("orderID"),
		quantity, request.PaymentMethodID,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"trade": c2cTradeResponse(trade)})
}

func (a *app) c2cCancelOrder(w http.ResponseWriter, r *http.Request) {
	order, err := a.c2c.CancelOrder(r.Context(), accountFromContext(r.Context()), idempotencyKey(r), r.PathValue("orderID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": c2cOrderResponse(order)})
}

func (a *app) c2cMyActivity(w http.ResponseWriter, r *http.Request) {
	orders, trades, err := a.c2c.MyActivity(r.Context(), accountFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	orderItems := make([]map[string]any, 0, len(orders))
	for _, order := range orders {
		orderItems = append(orderItems, c2cOrderResponse(order))
	}
	tradeItems := make([]map[string]any, 0, len(trades))
	for _, trade := range trades {
		tradeItems = append(tradeItems, c2cTradeResponse(trade))
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orderItems, "trades": tradeItems})
}

func (a *app) c2cTrade(w http.ResponseWriter, r *http.Request) {
	trade, err := a.c2c.Trade(r.Context(), accountFromContext(r.Context()), r.PathValue("tradeID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if accountFromContext(r.Context()).IsAdmin {
		writeJSON(w, http.StatusOK, map[string]any{"trade": c2cAdminTradeResponse(trade)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trade": c2cTradeResponse(trade)})
}

func (a *app) c2cMarkPaid(w http.ResponseWriter, r *http.Request) {
	var request struct {
		PaymentReference string `json:"payment_reference"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	trade, err := a.c2c.MarkPaid(
		r.Context(), accountFromContext(r.Context()), idempotencyKey(r), r.PathValue("tradeID"),
		request.PaymentReference,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trade": c2cTradeResponse(trade)})
}

func (a *app) c2cCancelTrade(w http.ResponseWriter, r *http.Request) {
	trade, err := a.c2c.CancelTrade(r.Context(), accountFromContext(r.Context()), idempotencyKey(r), r.PathValue("tradeID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trade": c2cTradeResponse(trade)})
}

func (a *app) c2cConfirmReceipt(w http.ResponseWriter, r *http.Request) {
	trade, err := a.c2c.ConfirmReceipt(r.Context(), accountFromContext(r.Context()), idempotencyKey(r), r.PathValue("tradeID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trade": c2cTradeResponse(trade)})
}

func (a *app) c2cOpenDispute(w http.ResponseWriter, r *http.Request) {
	a.c2cSubmitDispute(w, r, true)
}

func (a *app) c2cAddStatement(w http.ResponseWriter, r *http.Request) {
	a.c2cSubmitDispute(w, r, false)
}

func (a *app) c2cSubmitDispute(w http.ResponseWriter, r *http.Request, open bool) {
	var request struct {
		Statement string `json:"statement"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	var trade c2c.Trade
	var err error
	if open {
		trade, err = a.c2c.OpenDispute(r.Context(), accountFromContext(r.Context()), idempotencyKey(r), r.PathValue("tradeID"), request.Statement)
	} else {
		trade, err = a.c2c.AddDisputeStatement(r.Context(), accountFromContext(r.Context()), idempotencyKey(r), r.PathValue("tradeID"), request.Statement)
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trade": c2cTradeResponse(trade)})
}

func (a *app) adminC2CDisputes(w http.ResponseWriter, r *http.Request) {
	trades, err := a.c2c.AdminDisputes(r.Context(), accountFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(trades))
	for _, trade := range trades {
		items = append(items, c2cAdminTradeResponse(trade))
	}
	writeJSON(w, http.StatusOK, map[string]any{"trades": items})
}

func (a *app) adminC2CCancelOrder(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	order, err := a.c2c.AdminCancelOrder(r.Context(), accountFromContext(r.Context()), idempotencyKey(r), r.PathValue("orderID"), request.Reason)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": c2cOrderResponse(order)})
}

func (a *app) adminC2CResolve(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Action c2c.ResolutionAction `json:"action"`
		Reason string               `json:"reason"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return
	}
	trade, err := a.c2c.ResolveDispute(
		r.Context(), accountFromContext(r.Context()), idempotencyKey(r), r.PathValue("tradeID"), request.Action, request.Reason,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trade": c2cAdminTradeResponse(trade)})
}

func c2cOrderResponse(order c2c.Order) map[string]any {
	methods := make([]map[string]any, 0, len(order.PaymentMethods))
	for _, method := range order.PaymentMethods {
		methods = append(methods, map[string]any{
			"id": method.ID, "type": method.Type, "position": method.Position,
			"contact": method.Contact, "instructions": method.Instructions,
		})
	}
	return map[string]any{
		"id": order.ID, "owner_account_id": order.OwnerAccountID,
		"owner_display_name": order.OwnerDisplayName, "side": order.Side,
		"unit_price_fen": order.UnitPriceFen, "total": order.Total.String(),
		"available": order.Available.String(), "allocated": order.Allocated.String(),
		"settled": order.Settled.String(), "closed": order.Closed.String(),
		"minimum": order.Minimum.String(), "maximum": order.Maximum.String(),
		"status": order.Status, "takeable": order.Takeable, "payment_types": order.PaymentTypes,
		"payment_methods": methods, "created_at": order.CreatedAt,
		"updated_at": order.UpdatedAt, "cancelled_at": order.CancelledAt,
	}
}

// c2cAdminTradeResponse adds party account policy state that only
// administrators may see while handling a dispute.
// Party restriction events stay out of participant responses for the same reason.
func c2cAdminTradeResponse(trade c2c.Trade) map[string]any {
	response := c2cTradeResponseFor(trade, true)
	response["buyer_credit_frozen"] = trade.BuyerCreditFrozen
	response["seller_credit_frozen"] = trade.SellerCreditFrozen
	return response
}

func c2cTradeResponse(trade c2c.Trade) map[string]any {
	return c2cTradeResponseFor(trade, false)
}

func c2cAdminOnlyEvent(action string) bool {
	return action == "dispute.buyer_restricted" || action == "dispute.seller_restricted"
}

func c2cTradeResponseFor(trade c2c.Trade, admin bool) map[string]any {
	var method any
	if trade.SelectedPaymentMethod != nil {
		method = map[string]any{
			"id": trade.SelectedPaymentMethod.ID, "type": trade.SelectedPaymentMethod.Type,
			"contact":      trade.SelectedPaymentMethod.Contact,
			"instructions": trade.SelectedPaymentMethod.Instructions,
		}
	}
	statements := make([]map[string]any, 0, len(trade.Statements))
	for _, item := range trade.Statements {
		statements = append(statements, map[string]any{
			"id": item.ID, "actor_account_id": item.ActorAccountID,
			"actor_display_name": item.ActorDisplayName, "text": item.Text,
			"character_count": item.CharacterCount, "created_at": item.CreatedAt,
			"deleted_at": item.DeletedAt,
		})
	}
	events := make([]map[string]any, 0, len(trade.Events))
	for _, item := range trade.Events {
		if !admin && c2cAdminOnlyEvent(item.Action) {
			continue
		}
		events = append(events, map[string]any{
			"id": item.ID, "actor_account_id": item.ActorAccountID,
			"action": item.Action, "reason": item.Reason,
			"ledger_transaction_id": item.LedgerTransactionID, "created_at": item.CreatedAt,
		})
	}
	return map[string]any{
		"id": trade.ID, "order_id": trade.OrderID, "order_side": trade.OrderSide,
		"buyer_account_id": trade.BuyerAccountID, "buyer_display_name": trade.BuyerDisplayName,
		"seller_account_id": trade.SellerAccountID, "seller_display_name": trade.SellerDisplayName,
		"quantity": trade.Quantity.String(), "unit_price_fen": trade.UnitPriceFen,
		"fiat_amount_fen": trade.FiatAmountFen, "status": trade.Status,
		"payment_method": method, "payment_reference": trade.PaymentReference,
		"payment_reference_deleted_at": trade.PaymentReferenceGone,
		"payment_deadline":             trade.PaymentDeadline, "review_due_at": trade.ReviewDueAt,
		"ledger_transaction_id": trade.LedgerTransactionID,
		"statements":            statements, "events": events,
		"created_at": trade.CreatedAt, "updated_at": trade.UpdatedAt,
		"paid_at": trade.PaidAt, "resolved_at": trade.ResolvedAt,
	}
}
