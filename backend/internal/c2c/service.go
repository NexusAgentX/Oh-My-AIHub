package c2c

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

const (
	maxPaymentMethods = 5
	maxChannelRunes   = 32
	maxAccountRunes   = 200
	maxNoteRunes      = 500
	maxStatementRunes = 1000
	maxReasonRunes    = 500

	paymentMethodsPurpose = "payment_methods"
	// minPerTradeUnbounded is the stored minimum when the seller sets none:
	// the smallest representable amount.
	minPerTradeUnbounded = money.Amount(1)
)

// Store is the persistence of the C2C market. Every method that moves points
// runs in one database transaction that also books the ledger entries.
type Store interface {
	// CreateOrder locks the seller's balance, books the escrow transfer and
	// inserts the order. A repeated order ID returns the stored order.
	CreateOrder(ctx context.Context, order NewOrder) (Order, error)
	ListMarket(ctx context.Context, viewerID string, after *Cursor, limit int) ([]Order, error)
	ListSellerOrders(ctx context.Context, sellerID string, status OrderStatus, after *Cursor, limit int) ([]Order, error)
	CloseOrder(ctx context.Context, orderID, sellerID string) (Order, error)

	CreateTrade(ctx context.Context, trade NewTrade) (Trade, error)
	GetTrade(ctx context.Context, tradeID string) (Trade, error)
	ListTrades(ctx context.Context, filter TradeFilter) ([]Trade, error)
	ListDisputes(ctx context.Context, status TradeStatus, after *Cursor, limit int) ([]Trade, error)
	MarkPaid(ctx context.Context, tradeID, buyerID, note string) (Trade, error)
	Release(ctx context.Context, tradeID, sellerID string) (Trade, error)
	// Cancel cancels a trade for the buyer; an empty actorID is the timeout job.
	Cancel(ctx context.Context, tradeID, actorID string) (Trade, error)
	Dispute(ctx context.Context, tradeID, actorID, statement string) (Trade, error)
	Resolve(ctx context.Context, resolution Resolution) (Trade, error)
	// DueTrades lists trades awaiting payment past their deadline.
	DueTrades(ctx context.Context, limit int) ([]string, error)
}

type Service struct {
	store   Store
	keyring *Keyring
}

func NewService(store Store, keyring *Keyring) *Service {
	return &Service{store: store, keyring: keyring}
}

// Viewer is the account asking to see a trade.
type Viewer struct {
	ID      string
	IsAdmin bool
}

// MarketOrder is a public order: payment method names without accounts.
type MarketOrder struct {
	Order
	Channels []string
}

// MyOrder is the seller's own order with its payment methods in clear.
type MyOrder struct {
	Order
	PaymentMethods []PaymentMethod
}

// TradeView is a trade as one viewer may see it.
type TradeView struct {
	Trade
	ViewerRole     string // buyer, seller or admin
	PaymentMethods []PaymentMethod
}

type Page[T any] struct {
	Items []T
	// Next is the cursor of the next page; empty means there is none.
	Next string
}

type CreateOrderInput struct {
	SellerID       string
	Amount         money.Amount
	UnitPriceFen   int64
	MinPerTrade    *money.Amount
	MaxPerTrade    *money.Amount
	PaymentMethods []PaymentMethod
	// IdempotencyKey is the client's key; with it a repeated request maps to
	// the same order.
	IdempotencyKey string
}

func (s *Service) CreateOrder(ctx context.Context, input CreateOrderInput) (MyOrder, error) {
	min := minPerTradeUnbounded
	if input.MinPerTrade != nil {
		min = *input.MinPerTrade
	}
	methods, err := normalizeMethods(input.PaymentMethods)
	if err != nil {
		return MyOrder{}, err
	}
	switch {
	case input.SellerID == "", input.Amount <= 0, input.UnitPriceFen < 1, min < 1, min > input.Amount,
		input.MaxPerTrade != nil && *input.MaxPerTrade < min:
		return MyOrder{}, ErrInvalidInput
	}
	if _, err := TotalFen(input.Amount, input.UnitPriceFen); err != nil {
		return MyOrder{}, err
	}
	id := newID()
	if input.IdempotencyKey != "" {
		id = derivedID("c2c-order", input.SellerID, input.IdempotencyKey)
	}
	plaintext, err := json.Marshal(methods)
	if err != nil {
		return MyOrder{}, err
	}
	sealed, err := s.keyring.Encrypt(id, paymentMethodsPurpose, plaintext)
	if err != nil {
		return MyOrder{}, err
	}
	order, err := s.store.CreateOrder(ctx, NewOrder{
		ID: id, SellerID: input.SellerID, Total: input.Amount, UnitPriceFen: input.UnitPriceFen,
		MinPerTrade: min, MaxPerTrade: input.MaxPerTrade, Methods: sealed,
	})
	if err != nil {
		return MyOrder{}, err
	}
	return s.myOrder(order)
}

func (s *Service) ListMarket(ctx context.Context, viewerID, cursor string, limit int) (Page[MarketOrder], error) {
	after, err := decodeCursor(cursor, true)
	if err != nil {
		return Page[MarketOrder]{}, err
	}
	orders, err := s.store.ListMarket(ctx, viewerID, after, limit+1)
	if err != nil {
		return Page[MarketOrder]{}, err
	}
	orders, next := paginate(orders, limit, func(o Order) Cursor {
		return Cursor{Price: o.UnitPriceFen, Time: o.CreatedAt, ID: o.ID}
	})
	page := Page[MarketOrder]{Items: make([]MarketOrder, 0, len(orders)), Next: next}
	for _, order := range orders {
		methods, err := s.openMethods(order.ID, order.Methods)
		if err != nil {
			return Page[MarketOrder]{}, err
		}
		channels := make([]string, 0, len(methods))
		for _, method := range methods {
			channels = append(channels, method.Channel)
		}
		page.Items = append(page.Items, MarketOrder{Order: order, Channels: channels})
	}
	return page, nil
}

func (s *Service) ListMyOrders(ctx context.Context, sellerID string, status OrderStatus, cursor string, limit int) (Page[MyOrder], error) {
	if status != "" && !status.Valid() {
		return Page[MyOrder]{}, ErrInvalidInput
	}
	after, err := decodeCursor(cursor, false)
	if err != nil {
		return Page[MyOrder]{}, err
	}
	orders, err := s.store.ListSellerOrders(ctx, sellerID, status, after, limit+1)
	if err != nil {
		return Page[MyOrder]{}, err
	}
	orders, next := paginate(orders, limit, func(o Order) Cursor { return Cursor{Time: o.CreatedAt, ID: o.ID} })
	page := Page[MyOrder]{Items: make([]MyOrder, 0, len(orders)), Next: next}
	for _, order := range orders {
		view, err := s.myOrder(order)
		if err != nil {
			return Page[MyOrder]{}, err
		}
		page.Items = append(page.Items, view)
	}
	return page, nil
}

func (s *Service) CloseOrder(ctx context.Context, orderID, sellerID string) (MyOrder, error) {
	order, err := s.store.CloseOrder(ctx, orderID, sellerID)
	if err != nil {
		return MyOrder{}, err
	}
	return s.myOrder(order)
}

func (s *Service) CreateTrade(ctx context.Context, orderID, buyerID string, amount money.Amount, idempotencyKey string) (TradeView, error) {
	if orderID == "" || buyerID == "" || amount <= 0 {
		return TradeView{}, ErrInvalidInput
	}
	id := newID()
	if idempotencyKey != "" {
		id = derivedID("c2c-trade", buyerID+":"+orderID, idempotencyKey)
	}
	trade, err := s.store.CreateTrade(ctx, NewTrade{ID: id, OrderID: orderID, BuyerID: buyerID, Amount: amount})
	if err != nil {
		return TradeView{}, err
	}
	return s.tradeView(Viewer{ID: buyerID}, trade)
}

func (s *Service) GetTrade(ctx context.Context, viewer Viewer, tradeID string) (TradeView, error) {
	trade, err := s.store.GetTrade(ctx, tradeID)
	if err != nil {
		return TradeView{}, err
	}
	return s.tradeView(viewer, trade)
}

func (s *Service) ListMyTrades(ctx context.Context, filter TradeFilter, cursor string) (Page[TradeView], error) {
	if filter.UserID == "" || (filter.Role != "" && filter.Role != "buyer" && filter.Role != "seller") ||
		(filter.Status != "" && !filter.Status.Valid()) {
		return Page[TradeView]{}, ErrInvalidInput
	}
	after, err := decodeCursor(cursor, false)
	if err != nil {
		return Page[TradeView]{}, err
	}
	limit := filter.Limit
	filter.After, filter.Limit = after, limit+1
	trades, err := s.store.ListTrades(ctx, filter)
	if err != nil {
		return Page[TradeView]{}, err
	}
	return s.tradePage(Viewer{ID: filter.UserID}, trades, limit, func(t Trade) Cursor { return Cursor{Time: t.CreatedAt, ID: t.ID} })
}

// ListDisputes lists disputed trades, oldest dispute first. status defaults
// to disputed; resolved rulings can be listed too.
func (s *Service) ListDisputes(ctx context.Context, admin Viewer, status TradeStatus, cursor string, limit int) (Page[TradeView], error) {
	if status == "" {
		status = TradeDisputed
	}
	if status != TradeDisputed && status != TradeResolvedBuyer && status != TradeResolvedSeller {
		return Page[TradeView]{}, ErrInvalidInput
	}
	after, err := decodeCursor(cursor, false)
	if err != nil {
		return Page[TradeView]{}, err
	}
	trades, err := s.store.ListDisputes(ctx, status, after, limit+1)
	if err != nil {
		return Page[TradeView]{}, err
	}
	return s.tradePage(admin, trades, limit, func(t Trade) Cursor {
		at := t.CreatedAt
		if t.DisputedAt != nil {
			at = *t.DisputedAt
		}
		return Cursor{Time: at, ID: t.ID}
	})
}

func (s *Service) MarkPaid(ctx context.Context, tradeID, buyerID, note string) (TradeView, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxNoteRunes {
		return TradeView{}, ErrInvalidInput
	}
	trade, err := s.store.MarkPaid(ctx, tradeID, buyerID, note)
	return s.afterTransition(Viewer{ID: buyerID}, trade, err)
}

func (s *Service) Release(ctx context.Context, tradeID, sellerID string) (TradeView, error) {
	trade, err := s.store.Release(ctx, tradeID, sellerID)
	return s.afterTransition(Viewer{ID: sellerID}, trade, err)
}

func (s *Service) Cancel(ctx context.Context, tradeID, buyerID string) (TradeView, error) {
	if buyerID == "" {
		return TradeView{}, ErrForbidden
	}
	trade, err := s.store.Cancel(ctx, tradeID, buyerID)
	return s.afterTransition(Viewer{ID: buyerID}, trade, err)
}

func (s *Service) Dispute(ctx context.Context, tradeID, actorID, statement string) (TradeView, error) {
	statement = strings.TrimSpace(statement)
	if statement == "" || utf8.RuneCountInString(statement) > maxStatementRunes {
		return TradeView{}, ErrInvalidInput
	}
	trade, err := s.store.Dispute(ctx, tradeID, actorID, statement)
	return s.afterTransition(Viewer{ID: actorID}, trade, err)
}

func (s *Service) Resolve(ctx context.Context, resolution Resolution) (TradeView, error) {
	resolution.Reason = strings.TrimSpace(resolution.Reason)
	if resolution.AdminID == "" || resolution.Reason == "" || utf8.RuneCountInString(resolution.Reason) > maxReasonRunes {
		return TradeView{}, ErrInvalidInput
	}
	trade, err := s.store.Resolve(ctx, resolution)
	return s.afterTransition(Viewer{ID: resolution.AdminID, IsAdmin: true}, trade, err)
}

// ExpireDue cancels trades whose payment deadline has passed and reports how
// many it cancelled. A trade that changed since it was listed is skipped.
func (s *Service) ExpireDue(ctx context.Context, batch int) (int, error) {
	ids, err := s.store.DueTrades(ctx, batch)
	if err != nil {
		return 0, err
	}
	cancelled := 0
	for _, id := range ids {
		_, err := s.store.Cancel(ctx, id, "")
		switch {
		case err == nil:
			cancelled++
		case errors.Is(err, ErrInvalidState), errors.Is(err, ErrNotFound):
		default:
			return cancelled, err
		}
	}
	return cancelled, nil
}

func (s *Service) afterTransition(viewer Viewer, trade Trade, err error) (TradeView, error) {
	if err != nil {
		return TradeView{}, err
	}
	return s.tradeView(viewer, trade)
}

func (s *Service) tradePage(viewer Viewer, trades []Trade, limit int, cursorOf func(Trade) Cursor) (Page[TradeView], error) {
	trades, next := paginate(trades, limit, cursorOf)
	page := Page[TradeView]{Items: make([]TradeView, 0, len(trades)), Next: next}
	for _, trade := range trades {
		view, err := s.tradeView(viewer, trade)
		if err != nil {
			return Page[TradeView]{}, err
		}
		page.Items = append(page.Items, view)
	}
	return page, nil
}

// tradeView applies the visibility rules. The buyer sees the payment methods
// only while the trade is unfinished; the seller and administrators always do.
// Anyone else does not learn that the trade exists.
func (s *Service) tradeView(viewer Viewer, trade Trade) (TradeView, error) {
	view := TradeView{Trade: trade, PaymentMethods: []PaymentMethod{}}
	switch {
	case viewer.ID == trade.Buyer.ID:
		view.ViewerRole = "buyer"
	case viewer.ID == trade.Seller.ID:
		view.ViewerRole = "seller"
	case viewer.IsAdmin:
		view.ViewerRole = "admin"
	default:
		return TradeView{}, ErrNotFound
	}
	if view.ViewerRole == "buyer" && trade.Status.Finished() {
		return view, nil
	}
	methods, err := s.openMethods(trade.OrderID, trade.Methods)
	if err != nil {
		return TradeView{}, err
	}
	view.PaymentMethods = methods
	return view, nil
}

func (s *Service) myOrder(order Order) (MyOrder, error) {
	methods, err := s.openMethods(order.ID, order.Methods)
	if err != nil {
		return MyOrder{}, err
	}
	return MyOrder{Order: order, PaymentMethods: methods}, nil
}

func (s *Service) openMethods(orderID string, sealed EncryptedValue) ([]PaymentMethod, error) {
	plaintext, err := s.keyring.Decrypt(orderID, paymentMethodsPurpose, sealed)
	if err != nil {
		return nil, err
	}
	var methods []PaymentMethod
	if err := json.Unmarshal(plaintext, &methods); err != nil {
		return nil, fmt.Errorf("decode C2C payment methods: %w", err)
	}
	return methods, nil
}

func normalizeMethods(methods []PaymentMethod) ([]PaymentMethod, error) {
	if len(methods) < 1 || len(methods) > maxPaymentMethods {
		return nil, ErrInvalidInput
	}
	normalized := make([]PaymentMethod, 0, len(methods))
	for _, method := range methods {
		channel, account := strings.TrimSpace(method.Channel), strings.TrimSpace(method.Account)
		if channel == "" || account == "" || utf8.RuneCountInString(channel) > maxChannelRunes || utf8.RuneCountInString(account) > maxAccountRunes {
			return nil, ErrInvalidInput
		}
		normalized = append(normalized, PaymentMethod{Channel: channel, Account: account})
	}
	return normalized, nil
}

// paginate trims the limit+1 rows fetched and returns the cursor of the last
// kept row when more exist.
func paginate[T any](items []T, limit int, cursorOf func(T) Cursor) ([]T, string) {
	if len(items) <= limit {
		return items, ""
	}
	items = items[:limit]
	return items, encodeCursor(cursorOf(items[len(items)-1]))
}

func encodeCursor(c Cursor) string {
	raw := strconv.FormatInt(c.Price, 10) + "|" + strconv.FormatInt(c.Time.UnixMicro(), 10) + "|" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(raw string, withPrice bool) (*Cursor, error) {
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 3 {
		return nil, ErrInvalidCursor
	}
	price, priceErr := strconv.ParseInt(parts[0], 10, 64)
	micros, timeErr := strconv.ParseInt(parts[1], 10, 64)
	if priceErr != nil || timeErr != nil || len(parts[2]) != 36 || (withPrice && price < 1) {
		return nil, ErrInvalidCursor
	}
	return &Cursor{Price: price, Time: time.UnixMicro(micros).UTC(), ID: parts[2]}, nil
}

// newID returns a random version-4 UUID.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return formatUUID(b, 4)
}

// derivedID maps a client idempotency key to a stable UUID, so a repeated
// request addresses the same row.
func derivedID(namespace, owner, key string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + owner + "\x00" + key))
	var b [16]byte
	copy(b[:], sum[:16])
	return formatUUID(b, 5)
}

func formatUUID(b [16]byte, version byte) string {
	b[6] = b[6]&0x0f | version<<4
	b[8] = b[8]&0x3f | 0x80
	encoded := hex.EncodeToString(b[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}
