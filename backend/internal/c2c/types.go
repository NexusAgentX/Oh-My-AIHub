// Package c2c is the C2C sell-order market (ADR-0025). A seller lists points:
// they move from the seller into the c2c_escrow system account, buyers lock
// part of an order, pay off-platform, and the seller releases the escrowed
// points to the buyer. Every amount movement is one ledger transaction booked
// in the same database transaction as the order and trade rows.
//
// The package holds the vocabulary, the pure state machine (machine.go), the
// private-data keyring and the Service that validates input, seals payment
// methods and decides who may see them. Persistence lives in
// internal/postgres/c2cpg.
package c2c

import (
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

var (
	ErrInvalidInput = errors.New("invalid C2C input")
	ErrNotFound     = errors.New("C2C resource not found")
	ErrForbidden    = errors.New("C2C action not permitted for this account")
	// ErrInvalidState reports a transition the current status does not allow.
	ErrInvalidState        = errors.New("C2C status does not allow this action")
	ErrInsufficientBalance = errors.New("balance is below the listed amount")
	ErrAccountInactive     = errors.New("account is not active")
	ErrOrderNotOpen        = errors.New("C2C order is not open")
	ErrOwnOrder            = errors.New("cannot trade with own order")
	ErrAmountOutOfRange    = errors.New("amount is outside the per-trade range")
	ErrAmountUnavailable   = errors.New("amount exceeds what is still available")
	ErrDuplicateTrade      = errors.New("buyer already has an unfinished trade on this order")
	ErrPaymentExpired      = errors.New("payment deadline has passed")
	ErrInvalidCursor       = errors.New("invalid C2C cursor")
)

// EncryptedValue is one AEAD-sealed private value bound to its record,
// purpose and key ID.
type EncryptedValue struct {
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
}

type OrderStatus string

const (
	OrderOpen   OrderStatus = "open"
	OrderClosed OrderStatus = "closed"
	OrderFilled OrderStatus = "filled"
)

func (s OrderStatus) Valid() bool { return s == OrderOpen || s == OrderClosed || s == OrderFilled }

type TradeStatus string

const (
	TradeAwaitingPayment TradeStatus = "awaiting_payment"
	TradePaid            TradeStatus = "paid"
	TradeReleased        TradeStatus = "released"
	TradeCancelled       TradeStatus = "cancelled"
	TradeDisputed        TradeStatus = "disputed"
	TradeResolvedBuyer   TradeStatus = "resolved_to_buyer"
	TradeResolvedSeller  TradeStatus = "resolved_to_seller"
)

func (s TradeStatus) Valid() bool {
	switch s {
	case TradeAwaitingPayment, TradePaid, TradeReleased, TradeCancelled, TradeDisputed, TradeResolvedBuyer, TradeResolvedSeller:
		return true
	}
	return false
}

// Finished reports a terminal status.
func (s TradeStatus) Finished() bool {
	switch s {
	case TradeReleased, TradeCancelled, TradeResolvedBuyer, TradeResolvedSeller:
		return true
	}
	return false
}

// Unfinished trades still hold escrowed points in an order's in_trade part.
func (s TradeStatus) Unfinished() bool { return s.Valid() && !s.Finished() }

// PaymentMethod is plain text only: a method name such as "支付宝" and the
// account or instructions the buyer pays to.
type PaymentMethod struct {
	Channel string `json:"channel"`
	Account string `json:"account"`
}

// Party is the minimal account information shown to other users.
type Party struct {
	ID          string
	DisplayName string
}

// Order is a sell order. Total = Available + InTrade + Sold + Closed; the
// database enforces the same identity with a CHECK.
type Order struct {
	ID           string
	Seller       Party
	Total        money.Amount
	Available    money.Amount
	InTrade      money.Amount
	Sold         money.Amount
	Closed       money.Amount
	UnitPriceFen int64
	MinPerTrade  money.Amount
	MaxPerTrade  *money.Amount
	Methods      EncryptedValue
	Status       OrderStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ClosedAt     *time.Time
}

// Trade is one buyer's locked part of an order.
type Trade struct {
	ID               string
	OrderID          string
	Buyer            Party
	Seller           Party
	Amount           money.Amount
	UnitPriceFen     int64
	TotalFen         int64
	Status           TradeStatus
	PaymentDeadline  time.Time
	Methods          EncryptedValue // sealed with the order ID
	BuyerNote        *string
	DisputeOpenedBy  *string
	BuyerStatement   *string
	SellerStatement  *string
	ResolutionReason *string
	CreatedAt        time.Time
	PaidAt           *time.Time
	ReleasedAt       *time.Time
	CancelledAt      *time.Time
	DisputedAt       *time.Time
	ResolvedAt       *time.Time
}

// NewOrder is a validated listing ready to persist.
type NewOrder struct {
	ID           string
	SellerID     string
	Total        money.Amount
	UnitPriceFen int64
	MinPerTrade  money.Amount
	MaxPerTrade  *money.Amount
	Methods      EncryptedValue
}

type NewTrade struct {
	ID      string
	OrderID string
	BuyerID string
	Amount  money.Amount
}

// Resolution is an administrator's ruling on a disputed trade.
type Resolution struct {
	TradeID string
	AdminID string
	ToBuyer bool
	Reason  string
}

// Cursor is a keyset position: Price is used by the market list only, Time is
// the sort timestamp of the list.
type Cursor struct {
	Price int64
	Time  time.Time
	ID    string
}

type TradeFilter struct {
	UserID string
	Role   string // "", "buyer" or "seller"
	Status TradeStatus
	// Pending keeps trades waiting for this user: awaiting payment as the
	// buyer, or paid as the seller.
	Pending bool
	After   *Cursor
	Limit   int
}
