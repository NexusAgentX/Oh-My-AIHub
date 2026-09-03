package c2c

import (
	"context"
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type Side string

const (
	SideSell Side = "sell"
	SideBuy  Side = "buy"
)

type OrderStatus string

const (
	OrderOpen      OrderStatus = "open"
	OrderAllocated OrderStatus = "allocated"
	OrderFilled    OrderStatus = "filled"
	OrderCancelled OrderStatus = "cancelled"
)

type TradeStatus string

const (
	TradeAwaitingPayment  TradeStatus = "awaiting_payment"
	TradePaid             TradeStatus = "paid"
	TradeDisputed         TradeStatus = "disputed"
	TradeReleasedToBuyer  TradeStatus = "released_to_buyer"
	TradeReturnedToSeller TradeStatus = "returned_to_seller"
	TradeCancelled        TradeStatus = "cancelled"
	TradeExpired          TradeStatus = "expired"
)

type PaymentMethodType string

const (
	PaymentWeChat       PaymentMethodType = "wechat"
	PaymentAlipay       PaymentMethodType = "alipay"
	PaymentBankTransfer PaymentMethodType = "bank_transfer"
	PaymentOther        PaymentMethodType = "other"
)

type EvidenceKind string

const (
	EvidencePayment EvidenceKind = "payment"
	EvidenceDispute EvidenceKind = "dispute"
)

type ResolutionAction string

const (
	ResolutionRelease ResolutionAction = "release_to_buyer"
	ResolutionReturn  ResolutionAction = "return_to_seller"
	ResolutionExtend  ResolutionAction = "extend_review"
)

var (
	ErrInvalidInput = errors.New("invalid C2C input")
	ErrNotFound     = errors.New("C2C resource not found")
	ErrConflict     = errors.New("C2C state conflict")
	ErrForbidden    = errors.New("C2C operation forbidden")
	ErrExpired      = errors.New("C2C payment deadline expired")
)

const (
	PaymentWindow       = 15 * time.Minute
	ReviewExtension     = 24 * time.Hour
	EvidenceRetention   = 180 * 24 * time.Hour
	MaximumImageBytes   = 5 << 20
	MaximumImagePixels  = 20_000_000
	MaximumMethods      = 5
	MaximumDisputeFiles = 5
	MaximumStatement    = 2_000
)

type EncryptedValue struct {
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
}

type SanitizedImage struct {
	MIME   string
	Bytes  []byte
	SHA256 [32]byte
	Width  int
	Height int
}

type PaymentPrivate struct {
	Contact      string `json:"contact,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	QRMIME       string `json:"qr_mime,omitempty"`
	QRBytes      []byte `json:"qr_bytes,omitempty"`
}

type PaymentMethodInput struct {
	Type         PaymentMethodType
	Contact      string
	Instructions string
	QR           *SanitizedImage
}

type PaymentMethod struct {
	ID           string
	OrderID      string
	Type         PaymentMethodType
	Position     int
	Private      EncryptedValue
	Contact      string
	Instructions string
	QRAvailable  bool
	CreatedAt    time.Time
}

type Order struct {
	ID               string
	OwnerAccountID   string
	OwnerDisplayName string
	Side             Side
	UnitPriceFen     int64
	Total            money.Amount
	Available        money.Amount
	Allocated        money.Amount
	Settled          money.Amount
	Closed           money.Amount
	Minimum          money.Amount
	Maximum          money.Amount
	Status           OrderStatus
	ParentHoldID     string
	PaymentMethods   []PaymentMethod
	PaymentTypes     []PaymentMethodType
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CancelledAt      *time.Time
}

type Evidence struct {
	ID                string
	TradeID           string
	UploaderAccountID string
	UploaderName      string
	Kind              EvidenceKind
	MIME              string
	SizeBytes         int64
	Width             int
	Height            int
	SHA256            [32]byte
	Encrypted         EncryptedValue
	CreatedAt         time.Time
	DeletedAt         *time.Time
}

type Statement struct {
	ID               string
	TradeID          string
	ActorAccountID   string
	ActorDisplayName string
	Text             string
	CharacterCount   int
	Encrypted        EncryptedValue
	CreatedAt        time.Time
	DeletedAt        *time.Time
}

type Event struct {
	ID                  int64
	OrderID             string
	TradeID             string
	ActorAccountID      string
	Action              string
	Reason              string
	LedgerTransactionID string
	HoldBusinessID      string
	CreatedAt           time.Time
}

type Trade struct {
	ID                    string
	OrderID               string
	OrderSide             Side
	BuyerAccountID        string
	BuyerDisplayName      string
	SellerAccountID       string
	SellerDisplayName     string
	Quantity              money.Amount
	UnitPriceFen          int64
	FiatAmountFen         int64
	Status                TradeStatus
	HoldID                string
	SelectedPaymentMethod *PaymentMethod
	PaymentReference      string
	PaymentReferenceChars int
	PaymentReferenceData  EncryptedValue
	PaymentReferenceGone  *time.Time
	PaymentDeadline       time.Time
	ReviewDueAt           *time.Time
	LedgerTransactionID   string
	Evidence              []Evidence
	Statements            []Statement
	Events                []Event
	CreatedAt             time.Time
	UpdatedAt             time.Time
	PaidAt                *time.Time
	ResolvedAt            *time.Time
}

type Market struct {
	GuidancePriceFen int64
	LatestPriceFen   *int64
	BestBidFen       *int64
	BestAskFen       *int64
	SpreadFen        *int64
	SellOrders       []Order
	BuyOrders        []Order
}

type Command struct {
	Actor          identity.Account
	Operation      string
	IdempotencyKey string
	PayloadHash    [32]byte
	Now            time.Time
}

type NewOrder struct {
	ID             string
	Side           Side
	UnitPriceFen   int64
	Total          money.Amount
	Minimum        money.Amount
	Maximum        money.Amount
	PaymentMethods []PaymentMethod
}

type NewTrade struct {
	ID              string
	Quantity        money.Amount
	PaymentMethodID string
	PaymentDeadline time.Time
}

type NewEvidence struct {
	ID        string
	Kind      EvidenceKind
	MIME      string
	SizeBytes int64
	Width     int
	Height    int
	SHA256    [32]byte
	Encrypted EncryptedValue
}

type NewStatement struct {
	ID             string
	CharacterCount int
	Encrypted      EncryptedValue
}

type EncryptionTarget struct {
	RecordID  string
	Purpose   string
	Encrypted EncryptedValue
}

type Store interface {
	Market(context.Context) (Market, error)
	Order(context.Context, string) (Order, error)
	OrderParticipant(context.Context, string, string) (bool, error)
	Trade(context.Context, string) (Trade, error)
	MyActivity(context.Context, string) ([]Order, []Trade, error)
	AdminDisputes(context.Context) ([]Trade, error)
	Evidence(context.Context, string) (Evidence, error)
	EncryptionTargets(context.Context) ([]EncryptionTarget, error)

	CreateOrder(context.Context, Command, NewOrder) (Order, error)
	TakeOrder(context.Context, Command, string, NewTrade) (Trade, error)
	CancelOrder(context.Context, Command, string) (Order, error)
	MarkPaid(context.Context, Command, string, *NewEvidence, *EncryptedValue, int) (Trade, error)
	CancelTrade(context.Context, Command, string, bool) (Trade, error)
	ConfirmReceipt(context.Context, Command, string) (Trade, error)
	OpenDispute(context.Context, Command, string, NewStatement, []NewEvidence) (Trade, error)
	AddDisputeEvidence(context.Context, Command, string, NewStatement, []NewEvidence) (Trade, error)
	AdminCancelOrder(context.Context, Command, string, string) (Order, error)
	ResolveDispute(context.Context, Command, string, ResolutionAction, string, time.Time) (Trade, error)
	ExpireDue(context.Context, time.Time, int) (int, error)
	CleanupEvidence(context.Context, time.Time, int) (int, error)
}
