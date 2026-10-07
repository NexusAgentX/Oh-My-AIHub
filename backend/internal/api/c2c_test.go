package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestC2CDTOsNeverSerializeEncryptionEnvelopesOrHoldIdentifiers(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	encrypted := c2c.EncryptedValue{KeyID: "private-key-id", Nonce: []byte("private-nonce"), Ciphertext: []byte("private-ciphertext")}
	order := c2c.Order{
		ID: "order-id", OwnerAccountID: "owner-id", OwnerDisplayName: "共享者", Side: c2c.SideSell,
		UnitPriceFen: 100, Total: money.FromNano(100 * money.Scale), Available: money.FromNano(50 * money.Scale),
		Allocated: money.FromNano(25 * money.Scale), Settled: money.FromNano(25 * money.Scale), Minimum: money.FromNano(money.Scale),
		Maximum: money.FromNano(100 * money.Scale), Status: c2c.OrderOpen, ParentHoldID: "private-parent-hold",
		PaymentMethods: []c2c.PaymentMethod{{
			ID: "method-id", OrderID: "order-id", Type: c2c.PaymentWeChat, Position: 1,
			Private: encrypted, Contact: "owner-contact", QRAvailable: true, CreatedAt: now,
		}}, CreatedAt: now, UpdatedAt: now,
	}
	trade := c2c.Trade{
		ID: "trade-id", OrderID: order.ID, OrderSide: order.Side,
		BuyerAccountID: "buyer-id", BuyerDisplayName: "买家", SellerAccountID: "seller-id", SellerDisplayName: "卖家",
		Quantity: money.FromNano(25 * money.Scale), UnitPriceFen: 100, FiatAmountFen: 2500,
		Status: c2c.TradePaid, HoldID: "private-child-hold", SelectedPaymentMethod: &order.PaymentMethods[0],
		PaymentReference: "visible-to-participants", PaymentReferenceData: encrypted,
		PaymentDeadline: now.Add(c2c.PaymentWindow), CreatedAt: now, UpdatedAt: now,
		Statements: []c2c.Statement{{
			ID: "statement-id", TradeID: "trade-id", ActorAccountID: "buyer-id", Text: "statement",
			CharacterCount: 9, Encrypted: encrypted, CreatedAt: now,
		}},
	}

	for name, value := range map[string]any{"order": c2cOrderResponse(order), "trade": c2cTradeResponse(trade)} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			"private-key-id", "private-nonce", "private-ciphertext", "private-parent-hold", "private-child-hold",
			"key_id", "nonce", "ciphertext", "parent_hold_id", "hold_id",
		} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("%s DTO leaked %q: %s", name, forbidden, encoded)
			}
		}
	}
}

func TestC2CPartyCreditStateIsAdminOnly(t *testing.T) {
	trade := c2c.Trade{ID: "trade-id", BuyerCreditFrozen: true, SellerCreditFrozen: false, Events: []c2c.Event{
		{ID: 1, Action: "trade.disputed", Reason: "participant opened C2C dispute"},
		{ID: 2, Action: "dispute.buyer_restricted", Reason: "private administrator restriction reason"},
	}}
	participant, err := json.Marshal(c2cTradeResponse(trade))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"credit_frozen", "buyer_restricted", "private administrator restriction reason"} {
		if strings.Contains(string(participant), forbidden) {
			t.Fatalf("participant DTO leaked %q: %s", forbidden, participant)
		}
	}
	if !strings.Contains(string(participant), "trade.disputed") {
		t.Fatalf("participant DTO dropped ordinary events: %s", participant)
	}
	admin := c2cAdminTradeResponse(trade)
	if admin["buyer_credit_frozen"] != true || admin["seller_credit_frozen"] != false {
		t.Fatalf("admin DTO credit state = %v / %v", admin["buyer_credit_frozen"], admin["seller_credit_frozen"])
	}
	if events, _ := admin["events"].([]map[string]any); len(events) != 2 {
		t.Fatalf("admin DTO events = %v", admin["events"])
	}
}

func TestDecodeC2CMultipartSanitizesImageAndRejectsTrailingPayload(t *testing.T) {
	imageBytes := testPNG(t)
	request, _ := c2cMultipartRequest(t, `{"value":"ok"}`, "qr", imageBytes)
	recorder := httptest.NewRecorder()
	var payload struct {
		Value string `json:"value"`
	}
	form, err := decodeC2CRequest(recorder, request, &payload)
	if err != nil {
		t.Fatal(err)
	}
	defer form.RemoveAll() //nolint:errcheck
	clean, err := c2cMultipartImage(form, "qr")
	if err != nil {
		t.Fatal(err)
	}
	if payload.Value != "ok" || clean.MIME != "image/png" || clean.Width != 2 || clean.Height != 2 || len(clean.Bytes) == 0 {
		t.Fatalf("decoded payload/image = %#v %#v", payload, clean)
	}

	request, _ = c2cMultipartRequest(t, `{"value":"ok"} {}`, "qr", imageBytes)
	if form, err := decodeC2CRequest(httptest.NewRecorder(), request, &payload); err == nil {
		if form != nil {
			_ = form.RemoveAll()
		}
		t.Fatal("multipart payload with trailing JSON unexpectedly succeeded")
	}
}

func c2cMultipartRequest(t *testing.T, payload, field string, content []byte) (*http.Request, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("payload", payload); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile(field, "image.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request, writer.FormDataContentType()
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	pixels := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	pixels.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, pixels); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
