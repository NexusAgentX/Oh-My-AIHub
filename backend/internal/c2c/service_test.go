package c2c

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func testKeyring(t *testing.T) *Keyring {
	t.Helper()
	keyring, err := ParseKeyring("k1=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", "k1")
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}

// recordingStore captures what the service hands to persistence.
type recordingStore struct {
	Store
	created NewOrder
	trade   Trade
}

func (s *recordingStore) CreateOrder(_ context.Context, order NewOrder) (Order, error) {
	s.created = order
	return Order{ID: order.ID, Methods: order.Methods, Status: OrderOpen}, nil
}

func (s *recordingStore) GetTrade(context.Context, string) (Trade, error) { return s.trade, nil }

func TestCreateOrderSealsPaymentMethodsAndValidates(t *testing.T) {
	keyring := testKeyring(t)
	store := &recordingStore{}
	service := NewService(store, keyring)
	input := CreateOrderInput{
		SellerID: "seller", Amount: points(t, "10"), UnitPriceFen: 90,
		PaymentMethods: []PaymentMethod{{Channel: " 支付宝 ", Account: "seller@example.com"}},
	}
	order, err := service.CreateOrder(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(store.created.Methods.Ciphertext), "seller@example.com") {
		t.Fatal("payment methods reached the store in clear text")
	}
	if store.created.MinPerTrade != 1 || len(order.PaymentMethods) != 1 || order.PaymentMethods[0].Channel != "支付宝" {
		t.Fatalf("created = %+v order = %+v", store.created, order)
	}
	var decoded []PaymentMethod
	plaintext, err := keyring.Decrypt(store.created.ID, paymentMethodsPurpose, store.created.Methods)
	if err != nil || json.Unmarshal(plaintext, &decoded) != nil || decoded[0].Account != "seller@example.com" {
		t.Fatalf("sealed value = %s, %v", plaintext, err)
	}
	// Sealed values are bound to their order.
	if _, err := keyring.Decrypt("another-order", paymentMethodsPurpose, store.created.Methods); err == nil {
		t.Fatal("ciphertext decrypted under a different record ID")
	}

	// A client key maps to one stable order ID; without one every request is new.
	input.IdempotencyKey = "k"
	if _, err := service.CreateOrder(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	first := store.created.ID
	if _, err := service.CreateOrder(context.Background(), input); err != nil || store.created.ID != first {
		t.Fatalf("same key gave %s then %s", first, store.created.ID)
	}
	other := input
	other.SellerID = "someone-else"
	if _, err := service.CreateOrder(context.Background(), other); err != nil || store.created.ID == first {
		t.Fatalf("different seller shares the key's order ID: %v", err)
	}

	max := points(t, "2")
	min := points(t, "3")
	tooMany := make([]PaymentMethod, 6)
	for i := range tooMany {
		tooMany[i] = PaymentMethod{Channel: "银行卡", Account: "x"}
	}
	for name, mutate := range map[string]func(*CreateOrderInput){
		"zero amount":     func(i *CreateOrderInput) { i.Amount = 0 },
		"zero price":      func(i *CreateOrderInput) { i.UnitPriceFen = 0 },
		"min above total": func(i *CreateOrderInput) { v := points(t, "11"); i.MinPerTrade = &v },
		"max below min":   func(i *CreateOrderInput) { i.MinPerTrade, i.MaxPerTrade = &min, &max },
		"no methods":      func(i *CreateOrderInput) { i.PaymentMethods = nil },
		"six methods":     func(i *CreateOrderInput) { i.PaymentMethods = tooMany },
		"blank account":   func(i *CreateOrderInput) { i.PaymentMethods = []PaymentMethod{{Channel: "微信", Account: "  "}} },
		"long channel": func(i *CreateOrderInput) {
			i.PaymentMethods = []PaymentMethod{{Channel: strings.Repeat("渠", 33), Account: "a"}}
		},
		"long account": func(i *CreateOrderInput) {
			i.PaymentMethods = []PaymentMethod{{Channel: "微信", Account: strings.Repeat("号", 201)}}
		},
		"price overflow": func(i *CreateOrderInput) { i.Amount, i.UnitPriceFen = points(t, "9000000000"), 1<<62 },
	} {
		bad := input
		bad.IdempotencyKey = ""
		mutate(&bad)
		if _, err := service.CreateOrder(context.Background(), bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: error = %v, want invalid input", name, err)
		}
	}
}

func TestTradeViewVisibility(t *testing.T) {
	keyring := testKeyring(t)
	sealed, err := keyring.Encrypt("order-1", paymentMethodsPurpose, []byte(`[{"channel":"支付宝","account":"seller@example.com"}]`))
	if err != nil {
		t.Fatal(err)
	}
	trade := Trade{
		ID: "trade-1", OrderID: "order-1", Buyer: Party{ID: "buyer"}, Seller: Party{ID: "seller"}, Methods: sealed,
		Status: TradeAwaitingPayment, PaymentDeadline: time.Now().Add(time.Hour),
	}
	store := &recordingStore{trade: trade}
	service := NewService(store, keyring)
	view := func(viewer Viewer) (TradeView, error) {
		return service.GetTrade(context.Background(), viewer, "trade-1")
	}

	for _, status := range []TradeStatus{TradeAwaitingPayment, TradePaid, TradeDisputed} {
		store.trade.Status = status
		got, err := view(Viewer{ID: "buyer"})
		if err != nil || got.ViewerRole != "buyer" || len(got.PaymentMethods) != 1 {
			t.Errorf("buyer in %s: %+v, %v", status, got, err)
		}
	}
	for _, status := range []TradeStatus{TradeReleased, TradeCancelled, TradeResolvedBuyer, TradeResolvedSeller} {
		store.trade.Status = status
		got, err := view(Viewer{ID: "buyer"})
		if err != nil || got.PaymentMethods == nil || len(got.PaymentMethods) != 0 {
			t.Errorf("buyer in finished %s must not see accounts: %+v, %v", status, got, err)
		}
		if got, err := view(Viewer{ID: "seller"}); err != nil || len(got.PaymentMethods) != 1 || got.ViewerRole != "seller" {
			t.Errorf("seller in %s: %+v, %v", status, got, err)
		}
		if got, err := view(Viewer{ID: "admin", IsAdmin: true}); err != nil || len(got.PaymentMethods) != 1 || got.ViewerRole != "admin" {
			t.Errorf("admin in %s: %+v, %v", status, got, err)
		}
	}
	if _, err := view(Viewer{ID: "stranger"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("stranger error = %v", err)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 8, 12, 30, 15, 123456000, time.UTC)
	cursor := Cursor{Price: 92, Time: at, ID: "00000000-0000-4000-8000-000000000001"}
	decoded, err := decodeCursor(encodeCursor(cursor), true)
	if err != nil || decoded == nil || decoded.Price != 92 || !decoded.Time.Equal(at) || decoded.ID != cursor.ID {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
	if none, err := decodeCursor("", true); none != nil || err != nil {
		t.Fatalf("empty cursor = %v, %v", none, err)
	}
	for _, bad := range []string{"%%%", encodeCursorRaw("a|b|c"), encodeCursorRaw("1|2"), encodeCursorRaw("0|1|00000000-0000-4000-8000-000000000001")} {
		if _, err := decodeCursor(bad, true); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("cursor %q error = %v", bad, err)
		}
	}
}

func encodeCursorRaw(raw string) string {
	return strings.TrimRight(base64URL(raw), "=")
}

func TestPaginateKeepsLimitAndReturnsCursor(t *testing.T) {
	items := []int{1, 2, 3}
	kept, next := paginate(items, 2, func(i int) Cursor {
		return Cursor{Price: int64(i), Time: time.Unix(int64(i), 0), ID: "00000000-0000-4000-8000-000000000001"}
	})
	if len(kept) != 2 || next == "" {
		t.Fatalf("kept %v next %q", kept, next)
	}
	decoded, err := decodeCursor(next, true)
	if err != nil || decoded.Price != 2 {
		t.Fatalf("cursor points at %+v, %v", decoded, err)
	}
	if kept, next := paginate(items, 3, func(int) Cursor { return Cursor{} }); len(kept) != 3 || next != "" {
		t.Fatalf("exact page = %v %q", kept, next)
	}
}

func TestDerivedIDIsStableAndScoped(t *testing.T) {
	a := derivedID("ns", "owner", "key")
	if a != derivedID("ns", "owner", "key") || a == derivedID("ns", "other", "key") || a == derivedID("ns", "owner", "key2") || a == derivedID("ns2", "owner", "key") {
		t.Fatal("derived IDs are not stable and scoped")
	}
	random, errRandom := newRowID("ns", "owner", "")
	other, errOther := newRowID("ns", "owner", "")
	derived, errDerived := newRowID("ns", "owner", "key")
	if errRandom != nil || errOther != nil || errDerived != nil {
		t.Fatalf("newRowID errors: %v %v %v", errRandom, errOther, errDerived)
	}
	if len(a) != 36 || len(random) != 36 || random == other || derived != a {
		t.Fatalf("bad UUIDs: derived %s random %s %s", a, random, other)
	}
}

func base64URL(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
