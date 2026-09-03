package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

const c2cMultipartLimit = 32 << 20

type c2cPaymentMethodRequest struct {
	Type         c2c.PaymentMethodType `json:"type"`
	Contact      string                `json:"contact"`
	Instructions string                `json:"instructions"`
	QRField      string                `json:"qr_field"`
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
	form, err := decodeC2CRequest(w, r, &request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求格式无效")
		return
	}
	if form != nil {
		defer form.RemoveAll() //nolint:errcheck
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
		if item.QRField != "" {
			if form == nil {
				writeDomainError(w, c2c.ErrInvalidInput)
				return
			}
			image, err := c2cMultipartImage(form, item.QRField)
			if err != nil {
				writeDomainError(w, err)
				return
			}
			method.QR = &image
		}
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

func (a *app) c2cPaymentQR(w http.ResponseWriter, r *http.Request) {
	image, err := a.c2c.PaymentQR(
		r.Context(), accountFromContext(r.Context()), r.PathValue("orderID"), r.PathValue("methodID"),
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	w.Header().Set("Content-Type", image.MIME)
	w.Header().Set("Content-Disposition", `inline; filename="payment-qr"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image.Bytes)
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
	writeJSON(w, http.StatusOK, map[string]any{"trade": c2cTradeResponse(trade)})
}

func (a *app) c2cMarkPaid(w http.ResponseWriter, r *http.Request) {
	var request struct {
		PaymentReference string `json:"payment_reference"`
	}
	form, err := decodeC2CRequest(w, r, &request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求格式无效")
		return
	}
	if form != nil {
		defer form.RemoveAll() //nolint:errcheck
	}
	var screenshot *c2c.SanitizedImage
	if form != nil && len(form.File["screenshot"]) > 0 {
		image, err := c2cMultipartImage(form, "screenshot")
		if err != nil {
			writeDomainError(w, err)
			return
		}
		screenshot = &image
	}
	trade, err := a.c2c.MarkPaid(
		r.Context(), accountFromContext(r.Context()), idempotencyKey(r), r.PathValue("tradeID"),
		request.PaymentReference, screenshot,
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

func (a *app) c2cAddEvidence(w http.ResponseWriter, r *http.Request) {
	a.c2cSubmitDispute(w, r, false)
}

func (a *app) c2cSubmitDispute(w http.ResponseWriter, r *http.Request, open bool) {
	var request struct {
		Statement string `json:"statement"`
	}
	form, err := decodeC2CRequest(w, r, &request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求格式无效")
		return
	}
	if form != nil {
		defer form.RemoveAll() //nolint:errcheck
	}
	images := make([]c2c.SanitizedImage, 0)
	if form != nil {
		for _, header := range form.File["evidence"] {
			image, err := c2cSanitizeFile(header)
			if err != nil {
				writeDomainError(w, err)
				return
			}
			images = append(images, image)
		}
	}
	var trade c2c.Trade
	if open {
		trade, err = a.c2c.OpenDispute(r.Context(), accountFromContext(r.Context()), idempotencyKey(r), r.PathValue("tradeID"), request.Statement, images)
	} else {
		trade, err = a.c2c.AddDisputeEvidence(r.Context(), accountFromContext(r.Context()), idempotencyKey(r), r.PathValue("tradeID"), request.Statement, images)
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trade": c2cTradeResponse(trade)})
}

func (a *app) c2cEvidence(w http.ResponseWriter, r *http.Request) {
	evidence, content, err := a.c2c.EvidenceFile(r.Context(), accountFromContext(r.Context()), r.PathValue("evidenceID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	w.Header().Set("Content-Type", evidence.MIME)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="c2c-evidence-%s"`, evidence.ID))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (a *app) adminC2CDisputes(w http.ResponseWriter, r *http.Request) {
	trades, err := a.c2c.AdminDisputes(r.Context(), accountFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(trades))
	for _, trade := range trades {
		items = append(items, c2cTradeResponse(trade))
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
	writeJSON(w, http.StatusOK, map[string]any{"trade": c2cTradeResponse(trade)})
}

func decodeC2CRequest(w http.ResponseWriter, r *http.Request, target any) (*multipart.Form, error) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		return nil, decodeJSON(w, r, target)
	}
	r.Body = http.MaxBytesReader(w, r.Body, c2cMultipartLimit)
	if err := r.ParseMultipartForm(c2cMultipartLimit); err != nil {
		return nil, err
	}
	payload := r.FormValue("payload")
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	if payload == "" || decoder.Decode(target) != nil {
		return r.MultipartForm, errors.New("invalid multipart payload")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return r.MultipartForm, errors.New("multipart payload has trailing content")
	}
	return r.MultipartForm, nil
}

func c2cMultipartImage(form *multipart.Form, field string) (c2c.SanitizedImage, error) {
	files := form.File[field]
	if len(files) != 1 {
		return c2c.SanitizedImage{}, c2c.ErrInvalidInput
	}
	return c2cSanitizeFile(files[0])
}

func c2cSanitizeFile(header *multipart.FileHeader) (c2c.SanitizedImage, error) {
	if header == nil || header.Size < 1 || header.Size > c2c.MaximumImageBytes {
		return c2c.SanitizedImage{}, c2c.ErrInvalidInput
	}
	file, err := header.Open()
	if err != nil {
		return c2c.SanitizedImage{}, c2c.ErrInvalidInput
	}
	defer file.Close() //nolint:errcheck
	return c2c.SanitizeImage(file)
}

func c2cOrderResponse(order c2c.Order) map[string]any {
	methods := make([]map[string]any, 0, len(order.PaymentMethods))
	for _, method := range order.PaymentMethods {
		methods = append(methods, map[string]any{
			"id": method.ID, "type": method.Type, "position": method.Position,
			"contact": method.Contact, "instructions": method.Instructions,
			"qr_available": method.QRAvailable,
			"qr_url":       fmt.Sprintf("/api/c2c/orders/%s/payment-methods/%s/qr", order.ID, method.ID),
		})
	}
	return map[string]any{
		"id": order.ID, "owner_account_id": order.OwnerAccountID,
		"owner_display_name": order.OwnerDisplayName, "side": order.Side,
		"unit_price_fen": order.UnitPriceFen, "total": order.Total.String(),
		"available": order.Available.String(), "allocated": order.Allocated.String(),
		"settled": order.Settled.String(), "closed": order.Closed.String(),
		"minimum": order.Minimum.String(), "maximum": order.Maximum.String(),
		"status": order.Status, "payment_types": order.PaymentTypes,
		"payment_methods": methods, "created_at": order.CreatedAt,
		"updated_at": order.UpdatedAt, "cancelled_at": order.CancelledAt,
	}
}

func c2cTradeResponse(trade c2c.Trade) map[string]any {
	var method any
	if trade.SelectedPaymentMethod != nil {
		method = map[string]any{
			"id": trade.SelectedPaymentMethod.ID, "type": trade.SelectedPaymentMethod.Type,
			"contact":      trade.SelectedPaymentMethod.Contact,
			"instructions": trade.SelectedPaymentMethod.Instructions,
			"qr_available": trade.SelectedPaymentMethod.QRAvailable,
			"qr_url":       fmt.Sprintf("/api/c2c/orders/%s/payment-methods/%s/qr", trade.OrderID, trade.SelectedPaymentMethod.ID),
		}
	}
	evidence := make([]map[string]any, 0, len(trade.Evidence))
	for _, item := range trade.Evidence {
		evidence = append(evidence, map[string]any{
			"id": item.ID, "uploader_account_id": item.UploaderAccountID,
			"uploader_name": item.UploaderName, "kind": item.Kind, "mime_type": item.MIME,
			"size_bytes": item.SizeBytes, "width": item.Width, "height": item.Height,
			"created_at": item.CreatedAt, "deleted_at": item.DeletedAt,
			"download_url": "/api/c2c/evidence/" + item.ID,
		})
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
		"evidence":              evidence, "statements": statements, "events": events,
		"created_at": trade.CreatedAt, "updated_at": trade.UpdatedAt,
		"paid_at": trade.PaidAt, "resolved_at": trade.ResolvedAt,
	}
}
