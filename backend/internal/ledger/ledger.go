package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type AccountKind string

const (
	AccountUser      AccountKind = "user"
	AccountIncentive AccountKind = "platform_incentive"
	AccountLoss      AccountKind = "platform_loss"
)

type TransactionKind string

const (
	TransactionTransfer   TransactionKind = "transfer"
	TransactionAdjustment TransactionKind = "admin_adjustment"
	TransactionBadDebt    TransactionKind = "bad_debt_transfer"
	TransactionCapture    TransactionKind = "hold_capture"
	TransactionSelfUsage  TransactionKind = "self_channel_usage"
	TransactionReversal   TransactionKind = "reversal"
)

type HoldPurpose string

const (
	HoldPurposeAssetReservation   HoldPurpose = "asset_reservation"
	HoldPurposeSpendAuthorization HoldPurpose = "spend_authorization"
)

type HoldFundingPolicy string

const (
	HoldFundingCreditAllowed      HoldFundingPolicy = "credit_allowed"
	HoldFundingSettledBalanceOnly HoldFundingPolicy = "settled_balance_only"
)

type HoldAmountMode string

type EntryRole string

const (
	HoldAmountExact HoldAmountMode = "exact"
	HoldAmountAll   HoldAmountMode = "all_remaining"
)

const (
	EntryRoleConsumer         EntryRole = "consumer"
	EntryRoleProvider         EntryRole = "provider"
	EntryRoleSeller           EntryRole = "seller"
	EntryRoleBuyer            EntryRole = "buyer"
	EntryRoleAdjustmentSource EntryRole = "adjustment_source"
	EntryRoleAdjustmentTarget EntryRole = "adjustment_target"
	EntryRoleDebtor           EntryRole = "debtor"
	EntryRolePlatformLoss     EntryRole = "platform_loss"
	EntryRolePlatformFee      EntryRole = "platform_fee"
	EntryRoleReversal         EntryRole = "reversal"
)

var (
	ErrInvalidInput       = errors.New("invalid ledger input")
	ErrNotFound           = errors.New("ledger resource not found")
	ErrConflict           = errors.New("ledger conflict")
	ErrInsufficientFunds  = errors.New("insufficient spending power")
	ErrCreditFrozen       = errors.New("credit is frozen")
	ErrUnbalanced         = errors.New("unbalanced transaction")
	ErrAmountOverflow     = errors.New("ledger amount overflow")
	ErrHoldClosed         = errors.New("hold is closed")
	ErrHoldAmountExceeded = errors.New("hold amount exceeds remaining amount")
)

type AccountRef struct {
	IdentityAccountID string
	SystemKind        AccountKind
}

func UserAccount(accountID string) AccountRef {
	return AccountRef{IdentityAccountID: accountID}
}

func SystemAccount(kind AccountKind) AccountRef {
	return AccountRef{SystemKind: kind}
}

type Wallet struct {
	LedgerAccountID   string
	IdentityAccountID string
	Kind              AccountKind
	PostedBalance     money.Amount
	AssetReserved     money.Amount
	SpendAuthorized   money.Amount
	CreditLimit       money.Amount
	CreditFrozen      bool
	EffectiveCredit   money.Amount
	SpendableCapacity money.Amount
	OverLimit         bool
	Status            identity.Status
	UpdatedAt         time.Time
}

type Entry struct {
	ID                      int64
	TransactionID           string
	LedgerAccountID         string
	AccountKind             AccountKind
	IdentityAccountID       string
	Ordinal                 int
	BusinessRole            EntryRole
	Amount                  money.Amount
	PostedBalanceBefore     money.Amount
	PostedBalanceAfter      money.Amount
	CreatedAt               time.Time
	TransactionKind         TransactionKind
	Reason                  string
	ReferenceType           string
	ReferenceID             string
	ActorAccountID          string
	ReversalOfTransactionID string
	HoldID                  string
	Counterparties          []Counterparty
}

type Counterparty struct {
	AccountKind       AccountKind
	IdentityAccountID string
	BusinessRole      EntryRole
	Amount            money.Amount
}

type Transaction struct {
	ID                      string
	IdempotencyKey          string
	Kind                    TransactionKind
	Reason                  string
	ReferenceType           string
	ReferenceID             string
	ActorAccountID          string
	ReversalOfTransactionID string
	HoldID                  string
	Entries                 []Entry
	CreatedAt               time.Time
}

type Posting struct {
	Account      AccountRef
	BusinessRole EntryRole
	Amount       money.Amount
}

type PostRequest struct {
	IdempotencyKey          string
	Kind                    TransactionKind
	Reason                  string
	ReferenceType           string
	ReferenceID             string
	ActorAccountID          string
	ReversalOfTransactionID string
	Entries                 []Posting
}

type Hold struct {
	ID              string
	LedgerAccountID string
	OwnerAccountID  string
	FundingPolicy   HoldFundingPolicy
	Purpose         HoldPurpose
	Amount          money.Amount
	Remaining       money.Amount
	Captured        money.Amount
	Released        money.Amount
	Status          string
	BusinessType    string
	BusinessID      string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CreateHoldRequest struct {
	IdempotencyKey string
	AccountID      string
	Amount         money.Amount
	FundingPolicy  HoldFundingPolicy
	Purpose        HoldPurpose
	Reason         string
	BusinessType   string
	BusinessID     string
}

type HoldAmount struct {
	Mode   HoldAmountMode
	Amount money.Amount
}

type MutateHoldRequest struct {
	IdempotencyKey string
	HoldID         string
	BusinessID     string
	Amount         HoldAmount
	Reason         string
}

type CaptureHoldRequest struct {
	MutateHoldRequest
	Credits       []Posting
	ReferenceType string
	ReferenceID   string
}

type CaptureResult struct {
	Hold        Hold
	Transaction Transaction
}

type Metrics struct {
	TotalPostedBalance               string
	PositivePostedBalance            string
	NegativePostedBalance            string
	TotalCreditLimit                 string
	UsedCredit                       string
	AssetReserved                    string
	SpendAuthorized                  string
	IncentivePostedBalance           string
	LossPostedBalance                string
	OverLimitAccounts                int64
	CreditFrozenAccounts             int64
	AccountCount                     int64
	PostedProjectionDifference       string
	PostedProjectionMismatchAccounts int64
	AssetReservationDifference       string
	SpendAuthorizationDifference     string
	HoldProjectionMismatchAccounts   int64
}

type Store interface {
	Wallet(context.Context, string) (Wallet, error)
	Entries(context.Context, string, int64, int) ([]Entry, error)
	WalletByRef(context.Context, AccountRef) (Wallet, error)
	EntriesByRef(context.Context, AccountRef, int64, int) ([]Entry, error)
	Metrics(context.Context) (Metrics, error)
	Post(context.Context, PostRequest, [32]byte) (Transaction, error)
	CreateHold(context.Context, CreateHoldRequest, [32]byte) (Hold, error)
	ReleaseHold(context.Context, MutateHoldRequest, [32]byte) (Hold, error)
	CaptureHold(context.Context, CaptureHoldRequest, [32]byte) (CaptureResult, error)
	TransferBadDebt(context.Context, string, money.Amount, string, string, string, string, [32]byte) (Transaction, error)
	Reverse(context.Context, string, string, string, string, string, [32]byte) (Transaction, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) Wallet(ctx context.Context, accountID string) (Wallet, error) {
	if strings.TrimSpace(accountID) == "" {
		return Wallet{}, ErrInvalidInput
	}
	return s.store.Wallet(ctx, accountID)
}

func (s *Service) RecentEntries(ctx context.Context, accountID string, limit int) ([]Entry, error) {
	return s.Entries(ctx, accountID, 0, limit)
}

func (s *Service) Entries(ctx context.Context, accountID string, beforeID int64, limit int) ([]Entry, error) {
	if strings.TrimSpace(accountID) == "" || limit < 1 || limit > 100 {
		return nil, ErrInvalidInput
	}
	if beforeID < 0 {
		return nil, ErrInvalidInput
	}
	return s.store.Entries(ctx, accountID, beforeID, limit)
}

func (s *Service) Metrics(ctx context.Context, actor identity.Account) (Metrics, error) {
	if !actor.IsAdmin {
		return Metrics{}, identity.ErrForbidden
	}
	return s.store.Metrics(ctx)
}

func (s *Service) AdminWallet(ctx context.Context, actor identity.Account, account AccountRef) (Wallet, error) {
	if !actor.IsAdmin {
		return Wallet{}, identity.ErrForbidden
	}
	if !validAccountRef(account) {
		return Wallet{}, ErrInvalidInput
	}
	return s.store.WalletByRef(ctx, account)
}

func (s *Service) AdminEntries(ctx context.Context, actor identity.Account, account AccountRef, beforeID int64, limit int) ([]Entry, error) {
	if !actor.IsAdmin {
		return nil, identity.ErrForbidden
	}
	if !validAccountRef(account) || beforeID < 0 || limit < 1 || limit > 100 {
		return nil, ErrInvalidInput
	}
	return s.store.EntriesByRef(ctx, account, beforeID, limit)
}

func (s *Service) post(ctx context.Context, request PostRequest) (Transaction, error) {
	request = normalizePost(request)
	if err := ValidatePostRequest(request); err != nil {
		return Transaction{}, err
	}
	return s.store.Post(ctx, request, payloadHash(request))
}

func (s *Service) Transfer(ctx context.Context, key, fromAccountID, toAccountID string, amount money.Amount, reason, referenceType, referenceID string) (Transaction, error) {
	if fromAccountID == toAccountID || amount <= 0 {
		return Transaction{}, ErrInvalidInput
	}
	return s.post(ctx, PostRequest{
		IdempotencyKey: key,
		Kind:           TransactionTransfer,
		Reason:         reason,
		ReferenceType:  referenceType,
		ReferenceID:    referenceID,
		Entries: []Posting{
			{Account: UserAccount(fromAccountID), BusinessRole: EntryRoleConsumer, Amount: -amount},
			{Account: UserAccount(toAccountID), BusinessRole: EntryRoleProvider, Amount: amount},
		},
	})
}

func (s *Service) AdminAdjustment(ctx context.Context, actor identity.Account, key string, from, to AccountRef, amount money.Amount, reason, referenceType, referenceID string) (Transaction, error) {
	if !actor.IsAdmin {
		return Transaction{}, identity.ErrForbidden
	}
	if from == to || amount <= 0 {
		return Transaction{}, ErrInvalidInput
	}
	return s.post(ctx, PostRequest{
		IdempotencyKey: key,
		Kind:           TransactionAdjustment,
		Reason:         reason,
		ReferenceType:  referenceType,
		ReferenceID:    referenceID,
		ActorAccountID: actor.ID,
		Entries: []Posting{
			{Account: from, BusinessRole: EntryRoleAdjustmentSource, Amount: -amount},
			{Account: to, BusinessRole: EntryRoleAdjustmentTarget, Amount: amount},
		},
	})
}

func (s *Service) TransferBadDebt(ctx context.Context, actor identity.Account, key, accountID string, amount money.Amount, reason, referenceID string) (Transaction, error) {
	if !actor.IsAdmin {
		return Transaction{}, identity.ErrForbidden
	}
	request := struct {
		Key, AccountID, Reason, ReferenceID, ActorID string
		Amount                                       money.Amount
	}{strings.TrimSpace(key), strings.TrimSpace(accountID), strings.TrimSpace(reason), strings.TrimSpace(referenceID), actor.ID, amount}
	if !validKey(request.Key) || request.AccountID == "" || request.Amount <= 0 || request.Reason == "" || len(request.Reason) > 512 || request.ReferenceID == "" || len(request.ReferenceID) > 256 {
		return Transaction{}, ErrInvalidInput
	}
	return s.store.TransferBadDebt(ctx, request.AccountID, request.Amount, request.Key, request.Reason, request.ReferenceID, actor.ID, payloadHash(request))
}

func (s *Service) RecordSelfChannelUsage(ctx context.Context, key, accountID string, amount money.Amount, referenceType, referenceID string) (Transaction, error) {
	if amount <= 0 {
		return Transaction{}, ErrInvalidInput
	}
	return s.post(ctx, PostRequest{
		IdempotencyKey: key,
		Kind:           TransactionSelfUsage,
		Reason:         "account consumed its own shared channel",
		ReferenceType:  referenceType,
		ReferenceID:    referenceID,
		Entries: []Posting{
			{Account: UserAccount(accountID), BusinessRole: EntryRoleConsumer, Amount: -amount},
			{Account: UserAccount(accountID), BusinessRole: EntryRoleProvider, Amount: amount},
		},
	})
}

func (s *Service) ReverseTransaction(ctx context.Context, actor identity.Account, key, originalTransactionID, reason, referenceID string) (Transaction, error) {
	if !actor.IsAdmin {
		return Transaction{}, identity.ErrForbidden
	}
	request := struct{ Key, OriginalID, Reason, ReferenceID, ActorID string }{
		strings.TrimSpace(key), strings.TrimSpace(originalTransactionID), strings.TrimSpace(reason), strings.TrimSpace(referenceID), actor.ID,
	}
	if !validKey(request.Key) || request.OriginalID == "" || request.Reason == "" || len(request.Reason) > 512 || request.ReferenceID == "" || len(request.ReferenceID) > 256 {
		return Transaction{}, ErrInvalidInput
	}
	return s.store.Reverse(ctx, request.Key, request.OriginalID, request.Reason, request.ReferenceID, request.ActorID, payloadHash(request))
}

func (s *Service) CreateHold(ctx context.Context, request CreateHoldRequest) (Hold, error) {
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.AccountID = strings.TrimSpace(request.AccountID)
	request.Reason = strings.TrimSpace(request.Reason)
	request.BusinessType = strings.TrimSpace(request.BusinessType)
	request.BusinessID = strings.TrimSpace(request.BusinessID)
	validPurpose := (request.Purpose == HoldPurposeAssetReservation && request.FundingPolicy == HoldFundingSettledBalanceOnly) ||
		(request.Purpose == HoldPurposeSpendAuthorization && request.FundingPolicy == HoldFundingCreditAllowed)
	if !validKey(request.IdempotencyKey) || request.AccountID == "" || request.Amount <= 0 || request.Reason == "" || len(request.Reason) > 512 || request.BusinessType == "" || len(request.BusinessType) > 64 || request.BusinessID == "" || len(request.BusinessID) > 256 || !validPurpose {
		return Hold{}, ErrInvalidInput
	}
	return s.store.CreateHold(ctx, request, payloadHash(request))
}

func (s *Service) ReleaseHold(ctx context.Context, request MutateHoldRequest) (Hold, error) {
	request = normalizeHoldMutation(request)
	if err := validateHoldMutation(request); err != nil {
		return Hold{}, err
	}
	return s.store.ReleaseHold(ctx, request, payloadHash(request))
}

func (s *Service) CaptureHold(ctx context.Context, request CaptureHoldRequest) (CaptureResult, error) {
	request.MutateHoldRequest = normalizeHoldMutation(request.MutateHoldRequest)
	request.ReferenceType = strings.TrimSpace(request.ReferenceType)
	request.ReferenceID = strings.TrimSpace(request.ReferenceID)
	for index := range request.Credits {
		request.Credits[index].Account.IdentityAccountID = strings.TrimSpace(request.Credits[index].Account.IdentityAccountID)
		request.Credits[index].BusinessRole = EntryRole(strings.TrimSpace(string(request.Credits[index].BusinessRole)))
	}
	if err := validateHoldMutation(request.MutateHoldRequest); err != nil || request.ReferenceType == "" || len(request.ReferenceType) > 64 || request.ReferenceID == "" || len(request.ReferenceID) > 256 || len(request.Credits) < 1 || len(request.Credits) > 31 {
		return CaptureResult{}, ErrInvalidInput
	}
	providerCount, buyerCount, feeCount := 0, 0, 0
	for _, credit := range request.Credits {
		if credit.Amount <= 0 || !validAccountRef(credit.Account) || (credit.BusinessRole != EntryRoleProvider && credit.BusinessRole != EntryRoleBuyer && credit.BusinessRole != EntryRolePlatformFee) {
			return CaptureResult{}, ErrInvalidInput
		}
		switch credit.BusinessRole {
		case EntryRoleProvider:
			providerCount++
		case EntryRoleBuyer:
			buyerCount++
		case EntryRolePlatformFee:
			feeCount++
		}
	}
	if providerCount+buyerCount != 1 || feeCount > 1 {
		return CaptureResult{}, ErrInvalidInput
	}
	return s.store.CaptureHold(ctx, request, payloadHash(request))
}

func normalizePost(request PostRequest) PostRequest {
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.Reason = strings.TrimSpace(request.Reason)
	request.ReferenceType = strings.TrimSpace(request.ReferenceType)
	request.ReferenceID = strings.TrimSpace(request.ReferenceID)
	request.ActorAccountID = strings.TrimSpace(request.ActorAccountID)
	for index := range request.Entries {
		request.Entries[index].Account.IdentityAccountID = strings.TrimSpace(request.Entries[index].Account.IdentityAccountID)
		request.Entries[index].BusinessRole = EntryRole(strings.TrimSpace(string(request.Entries[index].BusinessRole)))
	}
	return request
}

func ValidatePostRequest(request PostRequest) error {
	if !validKey(request.IdempotencyKey) || request.Reason == "" || len(request.Reason) > 512 || request.ReferenceType == "" || request.ReferenceID == "" || len(request.ReferenceType) > 64 || len(request.ReferenceID) > 256 || len(request.Entries) < 2 || len(request.Entries) > 32 {
		return ErrInvalidInput
	}
	if request.Kind != TransactionTransfer && request.Kind != TransactionAdjustment && request.Kind != TransactionSelfUsage {
		return ErrInvalidInput
	}
	total := money.Amount(0)
	for _, entry := range request.Entries {
		if entry.Amount == 0 || entry.Amount.Nano() == -1<<63 || !validAccountRef(entry.Account) || entry.BusinessRole == "" || len(entry.BusinessRole) > 64 {
			return ErrInvalidInput
		}
		var err error
		total, err = Add(total, entry.Amount)
		if err != nil {
			return err
		}
	}
	if total != 0 {
		return ErrUnbalanced
	}
	if err := validatePostShape(request); err != nil {
		return err
	}
	return nil
}

func validatePostShape(request PostRequest) error {
	if len(request.Entries) != 2 {
		return ErrInvalidInput
	}
	first, second := request.Entries[0], request.Entries[1]
	switch request.Kind {
	case TransactionTransfer:
		if first.Account == second.Account || first.Amount >= 0 || second.Amount <= 0 || first.BusinessRole != EntryRoleConsumer || second.BusinessRole != EntryRoleProvider {
			return ErrInvalidInput
		}
	case TransactionAdjustment:
		if first.Account == second.Account || first.Amount >= 0 || second.Amount <= 0 || first.BusinessRole != EntryRoleAdjustmentSource || second.BusinessRole != EntryRoleAdjustmentTarget || request.ActorAccountID == "" {
			return ErrInvalidInput
		}
	case TransactionSelfUsage:
		if first.Account != second.Account || first.Account.IdentityAccountID == "" || first.Amount >= 0 || second.Amount <= 0 || first.BusinessRole != EntryRoleConsumer || second.BusinessRole != EntryRoleProvider {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

func normalizeHoldMutation(request MutateHoldRequest) MutateHoldRequest {
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.HoldID = strings.TrimSpace(request.HoldID)
	request.BusinessID = strings.TrimSpace(request.BusinessID)
	request.Reason = strings.TrimSpace(request.Reason)
	return request
}

func validateHoldMutation(request MutateHoldRequest) error {
	if !validKey(request.IdempotencyKey) || request.HoldID == "" || request.BusinessID == "" || len(request.BusinessID) > 256 || request.Reason == "" || len(request.Reason) > 512 {
		return ErrInvalidInput
	}
	if request.Amount.Mode == HoldAmountExact {
		if request.Amount.Amount <= 0 {
			return ErrInvalidInput
		}
		return nil
	}
	if request.Amount.Mode != HoldAmountAll || request.Amount.Amount != 0 {
		return ErrInvalidInput
	}
	return nil
}

func validAccountRef(ref AccountRef) bool {
	if ref.IdentityAccountID != "" {
		return ref.SystemKind == ""
	}
	return ref.SystemKind == AccountIncentive || ref.SystemKind == AccountLoss
}

func validKey(key string) bool {
	return len(key) >= 1 && len(key) <= 128
}

func payloadHash(value any) [32]byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("ledger payload is not JSON encodable: %v", err))
	}
	return sha256.Sum256(encoded)
}
