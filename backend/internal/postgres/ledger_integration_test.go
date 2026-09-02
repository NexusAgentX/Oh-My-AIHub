package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/api"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
)

type ledgerHTTPErrorResponse struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

type ledgerHTTPEntry struct {
	TransactionID       string `json:"transaction_id"`
	AccountID           string `json:"account_id"`
	AccountKind         string `json:"account_kind"`
	BusinessRole        string `json:"business_role"`
	Amount              string `json:"amount"`
	PostedBalanceBefore string `json:"posted_balance_before"`
	PostedBalanceAfter  string `json:"posted_balance_after"`
	TransactionKind     string `json:"transaction_kind"`
	Reason              string `json:"reason"`
	ReferenceType       string `json:"reference_type"`
	ReferenceID         string `json:"reference_id"`
	ActorAccountID      string `json:"actor_account_id"`
	Counterparties      []struct {
		AccountKind  string `json:"account_kind"`
		AccountID    string `json:"account_id"`
		BusinessRole string `json:"business_role"`
		Amount       string `json:"amount"`
	} `json:"counterparties"`
}

type ledgerHTTPTransaction struct {
	ID               string            `json:"id"`
	Kind             string            `json:"kind"`
	Reason           string            `json:"reason"`
	ReferenceType    string            `json:"reference_type"`
	ReferenceID      string            `json:"reference_id"`
	ActorAccountID   string            `json:"actor_account_id"`
	Entries          []ledgerHTTPEntry `json:"entries"`
	ReversalOf       string            `json:"reversal_of_transaction_id"`
	AssociatedHoldID string            `json:"hold_id"`
}

type ledgerHTTPTransactionResponse struct {
	Transaction ledgerHTTPTransaction `json:"transaction"`
}

type ledgerHTTPEntriesResponse struct {
	Entries []ledgerHTTPEntry `json:"entries"`
}

func TestLedgerIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	basePool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(basePool.Close)
	schema := "ledger_" + randomHex(t, 8)
	if _, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = basePool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)) })
	schemaURL := withSearchPath(t, databaseURL, schema)
	if err := database.Migrate(ctx, schemaURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := database.Open(ctx, schemaURL)
	if err != nil {
		t.Fatalf("open schema: %v", err)
	}
	t.Cleanup(pool.Close)

	store := storepg.New(pool)
	identityService, err := identity.NewService(store, time.Hour)
	if err != nil {
		t.Fatalf("identity service: %v", err)
	}
	admin := createExactlyOneBootstrapAdmin(t, ctx, store)
	service := ledger.NewService(store)
	create := func(username, credit string) identity.Account {
		t.Helper()
		created, err := identityService.CreateInvitedAccount(ctx, admin, username, username, mustAmount(t, credit), false, identity.StatusActive)
		if err != nil {
			t.Fatalf("create %s: %v", username, err)
		}
		return created.Account
	}
	a := create("ledger.a", "10")
	b := create("ledger.b", "0")
	c := create("ledger.c", "0")

	initial, err := service.Transfer(ctx, "transfer-initial", a.ID, b.ID, mustAmount(t, "6"), "initial API settlement", "usage", "usage-1")
	if err != nil {
		t.Fatalf("initial transfer: %v", err)
	}
	if len(initial.Entries) != 2 || initial.Entries[0].Ordinal != 1 || initial.Entries[1].Ordinal != 2 || initial.Entries[0].PostedBalanceBefore != 0 || initial.Entries[0].PostedBalanceAfter != mustAmount(t, "-6") {
		t.Fatalf("initial entries = %+v", initial.Entries)
	}
	replayed, err := service.Transfer(ctx, "transfer-initial", a.ID, b.ID, mustAmount(t, "6"), "initial API settlement", "usage", "usage-1")
	if err != nil || replayed.ID != initial.ID {
		t.Fatalf("idempotent replay = %s, err %v, want %s", replayed.ID, err, initial.ID)
	}
	if _, err := service.Transfer(ctx, "transfer-initial", a.ID, b.ID, mustAmount(t, "5"), "different", "usage", "usage-1"); !errors.Is(err, ledger.ErrConflict) {
		t.Fatalf("changed idempotent payload error = %v", err)
	}
	if _, err := service.Transfer(ctx, "transfer-initial-new-key", a.ID, b.ID, mustAmount(t, "1"), "duplicate business", "usage", "usage-1"); !errors.Is(err, ledger.ErrConflict) {
		t.Fatalf("duplicate transaction business reference error = %v", err)
	}
	crossNamespaceHold, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "transfer-initial", AccountID: b.ID, Amount: mustAmount(t, "1"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "cross operation collision", BusinessType: "c2c_order", BusinessID: "collision",
	})
	if err != nil {
		t.Fatalf("operation-scoped idempotency key = %v", err)
	}
	if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{
		IdempotencyKey: "release-cross-operation", HoldID: crossNamespaceHold.ID, BusinessID: "release-collision",
		Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "clean up namespace test",
	}); err != nil {
		t.Fatalf("release operation-scoped hold = %v", err)
	}

	assetHold, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "hold-asset", AccountID: b.ID, Amount: mustAmount(t, "4"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "C2C sell order", BusinessType: "c2c_order", BusinessID: "sell-1",
	})
	if err != nil {
		t.Fatalf("asset hold: %v", err)
	}
	if _, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "hold-asset-new-key", AccountID: b.ID, Amount: mustAmount(t, "1"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "duplicate order hold", BusinessType: "c2c_order", BusinessID: "sell-1",
	}); !errors.Is(err, ledger.ErrConflict) {
		t.Fatalf("duplicate hold business reference error = %v", err)
	}
	assetReplay, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "hold-asset", AccountID: b.ID, Amount: mustAmount(t, "4"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "C2C sell order", BusinessType: "c2c_order", BusinessID: "sell-1",
	})
	if err != nil || assetReplay.ID != assetHold.ID {
		t.Fatalf("asset hold replay = %+v, err %v", assetReplay, err)
	}
	creditHold, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "hold-credit", AccountID: a.ID, Amount: mustAmount(t, "3"),
		Purpose: ledger.HoldPurposeSpendAuthorization, FundingPolicy: ledger.HoldFundingCreditAllowed,
		Reason: "API authorization", BusinessType: "api_request", BusinessID: "request-1",
	})
	if err != nil {
		t.Fatalf("credit hold: %v", err)
	}
	if _, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "hold-credit-too-large", AccountID: a.ID, Amount: mustAmount(t, "2"),
		Purpose: ledger.HoldPurposeSpendAuthorization, FundingPolicy: ledger.HoldFundingCreditAllowed,
		Reason: "too much", BusinessType: "api_request", BusinessID: "request-2",
	}); !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("excess credit hold error = %v", err)
	}
	if _, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "hold-negative-assets", AccountID: a.ID, Amount: mustAmount(t, "1"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "cannot sell credit", BusinessType: "c2c_order", BusinessID: "sell-invalid",
	}); !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("credit-backed C2C hold error = %v", err)
	}
	zeroBalanceWithCredit := create("ledger.zero.with.credit", "10")
	if _, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "hold-zero-assets", AccountID: zeroBalanceWithCredit.ID, Amount: mustAmount(t, "1"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "cannot sell unspent credit", BusinessType: "c2c_order", BusinessID: "sell-zero-balance-invalid",
	}); !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("zero-balance credit-backed C2C hold error = %v", err)
	}

	creditCapture, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "capture-credit", HoldID: creditHold.ID, BusinessID: "capture-credit", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "2")}, Reason: "settle API"},
		Credits:           []ledger.Posting{{Account: ledger.UserAccount(c.ID), BusinessRole: ledger.EntryRoleProvider, Amount: mustAmount(t, "2")}}, ReferenceType: "api_usage", ReferenceID: "request-1",
	})
	if err != nil || creditCapture.Hold.Remaining != mustAmount(t, "1") {
		t.Fatalf("partial credit capture = %+v, err %v", creditCapture, err)
	}
	creditCaptureReplay, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "capture-credit", HoldID: creditHold.ID, BusinessID: "capture-credit", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "2")}, Reason: "settle API"},
		Credits:           []ledger.Posting{{Account: ledger.UserAccount(c.ID), BusinessRole: ledger.EntryRoleProvider, Amount: mustAmount(t, "2")}}, ReferenceType: "api_usage", ReferenceID: "request-1",
	})
	if err != nil || creditCaptureReplay.Transaction.ID != creditCapture.Transaction.ID {
		t.Fatalf("credit capture replay = %+v, err %v", creditCaptureReplay, err)
	}
	if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{IdempotencyKey: "release-credit", HoldID: creditHold.ID, BusinessID: "release-credit", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "release unused"}); err != nil {
		t.Fatalf("release credit remainder: %v", err)
	}
	delayedCaptureReplay, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "capture-credit", HoldID: creditHold.ID, BusinessID: "capture-credit", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "2")}, Reason: "settle API"},
		Credits:           []ledger.Posting{{Account: ledger.UserAccount(c.ID), BusinessRole: ledger.EntryRoleProvider, Amount: mustAmount(t, "2")}}, ReferenceType: "api_usage", ReferenceID: "request-1",
	})
	if err != nil || delayedCaptureReplay.Hold.Remaining != creditCapture.Hold.Remaining || delayedCaptureReplay.Hold.Status != creditCapture.Hold.Status {
		t.Fatalf("delayed capture replay = %+v, err %v, want original %+v", delayedCaptureReplay, err, creditCapture)
	}
	if _, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "capture-asset", HoldID: assetHold.ID, BusinessID: "capture-asset", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "1")}, Reason: "C2C release"},
		Credits:           []ledger.Posting{{Account: ledger.UserAccount(c.ID), BusinessRole: ledger.EntryRoleBuyer, Amount: mustAmount(t, "1")}}, ReferenceType: "c2c_order", ReferenceID: "sell-1",
	}); err != nil {
		t.Fatalf("capture asset hold: %v", err)
	}
	partialRelease, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{IdempotencyKey: "release-asset-partial", HoldID: assetHold.ID, BusinessID: "release-asset-partial", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "1")}, Reason: "reduce sell order"})
	if err != nil || partialRelease.Remaining != mustAmount(t, "2") {
		t.Fatalf("partial asset release = %+v, err %v", partialRelease, err)
	}
	if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{IdempotencyKey: "release-asset", HoldID: assetHold.ID, BusinessID: "release-asset", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "cancel remainder"}); err != nil {
		t.Fatalf("release all asset remainder: %v", err)
	}
	delayedCreateReplay, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "hold-asset", AccountID: b.ID, Amount: mustAmount(t, "4"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "C2C sell order", BusinessType: "c2c_order", BusinessID: "sell-1",
	})
	if err != nil || delayedCreateReplay.Remaining != assetHold.Remaining || delayedCreateReplay.Status != assetHold.Status {
		t.Fatalf("delayed create replay = %+v, err %v, want original %+v", delayedCreateReplay, err, assetHold)
	}
	delayedReleaseReplay, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{
		IdempotencyKey: "release-asset-partial", HoldID: assetHold.ID, BusinessID: "release-asset-partial",
		Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "1")}, Reason: "reduce sell order",
	})
	if err != nil || delayedReleaseReplay.Remaining != partialRelease.Remaining || delayedReleaseReplay.Status != partialRelease.Status {
		t.Fatalf("delayed release replay = %+v, err %v, want original %+v", delayedReleaseReplay, err, partialRelease)
	}
	allCaptureHold, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "hold-all-capture", AccountID: b.ID, Amount: mustAmount(t, "1"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "all capture", BusinessType: "c2c_order", BusinessID: "sell-all",
	})
	if err != nil {
		t.Fatalf("all-capture hold: %v", err)
	}
	allCaptured, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "capture-all", HoldID: allCaptureHold.ID, BusinessID: "capture-all", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "capture all"},
		Credits:           []ledger.Posting{{Account: ledger.UserAccount(c.ID), BusinessRole: ledger.EntryRoleBuyer, Amount: mustAmount(t, "1")}}, ReferenceType: "c2c_order", ReferenceID: "sell-all",
	})
	if err != nil || allCaptured.Hold.Status != "closed" || allCaptured.Hold.Remaining != 0 {
		t.Fatalf("all capture = %+v, err %v", allCaptured, err)
	}

	splitConsumer := create("ledger.split.consumer", "2")
	splitProvider := create("ledger.split.provider", "0")
	splitHold, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "split-hold", AccountID: splitConsumer.ID, Amount: mustAmount(t, "1"),
		Purpose: ledger.HoldPurposeSpendAuthorization, FundingPolicy: ledger.HoldFundingCreditAllowed,
		Reason: "split provider and platform fee", BusinessType: "api_request", BusinessID: "split-usage",
	})
	if err != nil {
		t.Fatalf("create split hold: %v", err)
	}
	splitCapture, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "split-capture", HoldID: splitHold.ID, BusinessID: "split-capture", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "settle provider and fee atomically"},
		Credits: []ledger.Posting{
			{Account: ledger.UserAccount(splitProvider.ID), BusinessRole: ledger.EntryRoleProvider, Amount: mustAmount(t, "0.9")},
			{Account: ledger.SystemAccount(ledger.AccountIncentive), BusinessRole: ledger.EntryRolePlatformFee, Amount: mustAmount(t, "0.1")},
		},
		ReferenceType: "api_usage", ReferenceID: "split-usage",
	})
	if err != nil || len(splitCapture.Transaction.Entries) != 3 {
		t.Fatalf("split capture = %+v, err %v", splitCapture, err)
	}
	splitReplay, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "split-capture", HoldID: splitHold.ID, BusinessID: "split-capture", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "settle provider and fee atomically"},
		Credits: []ledger.Posting{
			{Account: ledger.UserAccount(splitProvider.ID), BusinessRole: ledger.EntryRoleProvider, Amount: mustAmount(t, "0.9")},
			{Account: ledger.SystemAccount(ledger.AccountIncentive), BusinessRole: ledger.EntryRolePlatformFee, Amount: mustAmount(t, "0.1")},
		},
		ReferenceType: "api_usage", ReferenceID: "split-usage",
	})
	if err != nil || splitReplay.Transaction.ID != splitCapture.Transaction.ID {
		t.Fatalf("split capture replay = %+v, err %v", splitReplay, err)
	}
	for label, credits := range map[string][]ledger.Posting{
		"fee without provider": {
			{Account: ledger.SystemAccount(ledger.AccountIncentive), BusinessRole: ledger.EntryRolePlatformFee, Amount: mustAmount(t, "1")},
		},
		"multiple providers": {
			{Account: ledger.UserAccount(splitProvider.ID), BusinessRole: ledger.EntryRoleProvider, Amount: mustAmount(t, "0.5")},
			{Account: ledger.UserAccount(c.ID), BusinessRole: ledger.EntryRoleProvider, Amount: mustAmount(t, "0.5")},
		},
		"multiple platform fees": {
			{Account: ledger.UserAccount(splitProvider.ID), BusinessRole: ledger.EntryRoleProvider, Amount: mustAmount(t, "0.8")},
			{Account: ledger.SystemAccount(ledger.AccountIncentive), BusinessRole: ledger.EntryRolePlatformFee, Amount: mustAmount(t, "0.1")},
			{Account: ledger.SystemAccount(ledger.AccountIncentive), BusinessRole: ledger.EntryRolePlatformFee, Amount: mustAmount(t, "0.1")},
		},
	} {
		if _, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
			MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "invalid-split-" + strings.ReplaceAll(label, " ", "-"), HoldID: splitHold.ID, BusinessID: "invalid-split-" + strings.ReplaceAll(label, " ", "-"), Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "1")}, Reason: label},
			Credits:           credits, ReferenceType: "api_usage", ReferenceID: "invalid-split-" + strings.ReplaceAll(label, " ", "-"),
		}); !errors.Is(err, ledger.ErrInvalidInput) {
			t.Fatalf("%s capture error = %v", label, err)
		}
	}
	invalidAssetHold, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "invalid-asset-capture-hold", AccountID: b.ID, Amount: mustAmount(t, "1"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "test asset capture shape", BusinessType: "c2c_order", BusinessID: "invalid-asset-capture",
	})
	if err != nil {
		t.Fatalf("invalid asset capture hold: %v", err)
	}
	if _, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "invalid-asset-capture", HoldID: invalidAssetHold.ID, BusinessID: "invalid-asset-capture", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "C2C cannot charge platform fee"},
		Credits: []ledger.Posting{
			{Account: ledger.UserAccount(c.ID), BusinessRole: ledger.EntryRoleBuyer, Amount: mustAmount(t, "0.9")},
			{Account: ledger.SystemAccount(ledger.AccountIncentive), BusinessRole: ledger.EntryRolePlatformFee, Amount: mustAmount(t, "0.1")},
		},
		ReferenceType: "c2c_order", ReferenceID: "invalid-asset-capture",
	}); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("asset capture with fee error = %v", err)
	}
	if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{IdempotencyKey: "release-invalid-asset-capture", HoldID: invalidAssetHold.ID, BusinessID: "release-invalid-asset-capture", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "clean up invalid capture hold"}); err != nil {
		t.Fatalf("release invalid asset capture hold: %v", err)
	}
	if _, err := service.RecordSelfChannelUsage(ctx, "cross-kind-settlement", splitConsumer.ID, mustAmount(t, "0.1"), "api_usage", "split-usage"); !errors.Is(err, ledger.ErrConflict) {
		t.Fatalf("cross-kind duplicate settlement error = %v", err)
	}

	beforeSelfUsage, err := service.Wallet(ctx, a.ID)
	if err != nil {
		t.Fatalf("wallet before self usage: %v", err)
	}
	selfUsage, err := service.RecordSelfChannelUsage(ctx, "self-usage", a.ID, mustAmount(t, "1"), "usage", "self-1")
	if err != nil || len(selfUsage.Entries) != 2 ||
		selfUsage.Entries[0].IdentityAccountID != a.ID || selfUsage.Entries[1].IdentityAccountID != a.ID ||
		selfUsage.Entries[0].Ordinal != 1 || selfUsage.Entries[1].Ordinal != 2 ||
		selfUsage.Entries[0].BusinessRole != ledger.EntryRoleConsumer || selfUsage.Entries[1].BusinessRole != ledger.EntryRoleProvider ||
		selfUsage.Entries[0].Amount != mustAmount(t, "-1") || selfUsage.Entries[1].Amount != mustAmount(t, "1") ||
		selfUsage.Entries[0].PostedBalanceBefore != beforeSelfUsage.PostedBalance ||
		selfUsage.Entries[1].PostedBalanceAfter != beforeSelfUsage.PostedBalance {
		t.Fatalf("self usage = %+v, err %v", selfUsage, err)
	}
	afterSelfUsage, err := service.Wallet(ctx, a.ID)
	if err != nil || afterSelfUsage.PostedBalance != beforeSelfUsage.PostedBalance {
		t.Fatalf("self usage wallet = %+v, before %+v, err %v", afterSelfUsage, beforeSelfUsage, err)
	}
	beforeSelfReversal, _ := service.Wallet(ctx, a.ID)
	reversal, err := service.ReverseTransaction(ctx, admin, "reverse-self", selfUsage.ID, "correct duplicate usage", "correction-1")
	if err != nil || reversal.ReversalOfTransactionID != selfUsage.ID {
		t.Fatalf("reversal = %+v, err %v", reversal, err)
	}
	afterSelfReversal, _ := service.Wallet(ctx, a.ID)
	if afterSelfReversal.PostedBalance != beforeSelfReversal.PostedBalance {
		t.Fatalf("self reversal changed projection from %s to %s", beforeSelfReversal.PostedBalance, afterSelfReversal.PostedBalance)
	}
	if _, err := service.ReverseTransaction(ctx, admin, "reverse-self-again", selfUsage.ID, "second reversal", "correction-2"); !errors.Is(err, ledger.ErrConflict) {
		t.Fatalf("second reversal error = %v", err)
	}

	reversalSource := create("ledger.reversal.source", "5")
	reversalTarget := create("ledger.reversal.target", "0")
	reversible, err := service.Transfer(ctx, "reversible-transfer", reversalSource.ID, reversalTarget.ID, mustAmount(t, "2"), "mistaken credit", "test_credit", "mistaken-credit-1")
	if err != nil {
		t.Fatalf("create reversible transfer: %v", err)
	}
	reversalSink := create("ledger.reversal.sink", "0")
	if _, err := service.Transfer(ctx, "spend-reversible-credit", reversalTarget.ID, reversalSink.ID, mustAmount(t, "2"), "spend mistaken credit", "test_debit", "mistaken-credit-spent"); err != nil {
		t.Fatalf("spend reversible transfer: %v", err)
	}
	targetFrozen := true
	targetDisabled := identity.StatusDisabled
	if _, err := identityService.UpdateAccount(ctx, admin, reversalTarget.ID, identity.AccountUpdate{
		ExpectedVersion: reversalTarget.Version, CreditFrozen: &targetFrozen, Status: &targetDisabled,
	}); err != nil {
		t.Fatalf("freeze reversal target: %v", err)
	}
	privilegedReversal, err := service.ReverseTransaction(ctx, admin, "reverse-frozen-credit", reversible.ID, "remove mistaken credit", "mistaken-credit-reversal")
	if err != nil || privilegedReversal.ReversalOfTransactionID != reversible.ID {
		t.Fatalf("privileged reversal = %+v, err %v", privilegedReversal, err)
	}
	reversedTarget, err := service.Wallet(ctx, reversalTarget.ID)
	if err != nil || reversedTarget.PostedBalance != mustAmount(t, "-2") || !reversedTarget.OverLimit {
		t.Fatalf("reversed frozen target wallet = %+v, err %v", reversedTarget, err)
	}

	existingHold, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "hold-before-freeze", AccountID: a.ID, Amount: mustAmount(t, "2"),
		Purpose: ledger.HoldPurposeSpendAuthorization, FundingPolicy: ledger.HoldFundingCreditAllowed,
		Reason: "already authorized", BusinessType: "api_request", BusinessID: "request-freeze",
	})
	if err != nil {
		t.Fatalf("hold before freeze: %v", err)
	}
	lowered := mustAmount(t, "5")
	updatedA, err := identityService.UpdateAccount(ctx, admin, a.ID, identity.AccountUpdate{ExpectedVersion: a.Version, CreditLimit: &lowered})
	if err != nil {
		t.Fatalf("lower credit: %v", err)
	}
	frozen := true
	updatedA, err = identityService.UpdateAccount(ctx, admin, a.ID, identity.AccountUpdate{ExpectedVersion: updatedA.Version, CreditFrozen: &frozen})
	if err != nil {
		t.Fatalf("freeze credit: %v", err)
	}
	walletA, err := service.Wallet(ctx, a.ID)
	if err != nil || !walletA.OverLimit || walletA.EffectiveCredit != 0 || walletA.SpendAuthorized != mustAmount(t, "2") {
		t.Fatalf("frozen wallet = %+v, err %v", walletA, err)
	}
	if _, err := service.Transfer(ctx, "frozen-debit", a.ID, c.ID, mustAmount(t, "1"), "blocked", "test", "frozen"); !errors.Is(err, ledger.ErrCreditFrozen) {
		t.Fatalf("frozen debit error = %v", err)
	}
	if _, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "frozen-hold", AccountID: a.ID, Amount: mustAmount(t, "1"),
		Purpose: ledger.HoldPurposeSpendAuthorization, FundingPolicy: ledger.HoldFundingCreditAllowed,
		Reason: "blocked", BusinessType: "api_request", BusinessID: "frozen",
	}); !errors.Is(err, ledger.ErrCreditFrozen) {
		t.Fatalf("frozen new hold error = %v", err)
	}
	if _, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "capture-after-freeze", HoldID: existingHold.ID, BusinessID: "capture-after-freeze", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "1")}, Reason: "capture existing"},
		Credits:           []ledger.Posting{{Account: ledger.UserAccount(c.ID), BusinessRole: ledger.EntryRoleProvider, Amount: mustAmount(t, "1")}}, ReferenceType: "api_usage", ReferenceID: "request-freeze",
	}); err != nil {
		t.Fatalf("capture after freeze: %v", err)
	}
	if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{IdempotencyKey: "release-after-freeze", HoldID: existingHold.ID, BusinessID: "release-after-freeze", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "release existing"}); err != nil {
		t.Fatalf("release after freeze: %v", err)
	}
	positiveFrozenDonor := create("ledger.frozen.donor", "2")
	positiveFrozen := create("ledger.frozen.positive", "0")
	if _, err := service.Transfer(ctx, "fund-positive-frozen", positiveFrozenDonor.ID, positiveFrozen.ID, mustAmount(t, "2"), "fund positive frozen account", "test", "fund-positive-frozen"); err != nil {
		t.Fatalf("fund positive frozen account: %v", err)
	}
	if _, err := identityService.UpdateAccount(ctx, admin, positiveFrozen.ID, identity.AccountUpdate{ExpectedVersion: positiveFrozen.Version, CreditFrozen: &frozen}); err != nil {
		t.Fatalf("freeze positive account: %v", err)
	}
	positiveFrozenWallet, err := service.Wallet(ctx, positiveFrozen.ID)
	if err != nil || positiveFrozenWallet.PostedBalance != mustAmount(t, "2") || positiveFrozenWallet.SpendableCapacity != 0 {
		t.Fatalf("positive frozen wallet = %+v, err %v", positiveFrozenWallet, err)
	}

	if _, err := service.AdminAdjustment(ctx, admin, "incentive-negative", ledger.SystemAccount(ledger.AccountIncentive), ledger.UserAccount(b.ID), mustAmount(t, "1"), "invalid incentive debit", "admin", "incentive-negative"); !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("negative incentive error = %v", err)
	}
	adjustment, err := service.AdminAdjustment(ctx, admin, "explicit-adjustment", ledger.UserAccount(b.ID), ledger.UserAccount(c.ID), mustAmount(t, "1"), "explicit counterparty correction", "admin_adjustment", "adjustment-1")
	if err != nil || adjustment.Kind != ledger.TransactionAdjustment || len(adjustment.Entries) != 2 {
		t.Fatalf("explicit adjustment = %+v, err %v", adjustment, err)
	}
	badDebt, err := service.TransferBadDebt(ctx, admin, "bad-debt", a.ID, mustAmount(t, "3"), "move unrecoverable balance", "bad-debt-1")
	if err != nil || badDebt.Kind != ledger.TransactionBadDebt {
		t.Fatalf("bad debt = %+v, err %v", badDebt, err)
	}
	staleOperatorCreated, err := identityService.CreateInvitedAccount(ctx, admin, "ledger.stale.operator", "Stale ledger operator", 0, true, identity.StatusActive)
	if err != nil {
		t.Fatalf("create stale ledger operator: %v", err)
	}
	staleOperator := staleOperatorCreated.Account
	staleReplayAdjustment, err := service.AdminAdjustment(
		ctx, staleOperator, "stale-replay-adjustment", ledger.UserAccount(c.ID), ledger.UserAccount(b.ID),
		mustAmount(t, "0.1"), "stale actor replay adjustment", "admin_adjustment", "stale-replay-adjustment",
	)
	if err != nil {
		t.Fatalf("create stale actor replay adjustment: %v", err)
	}
	staleReplayBadDebt, err := service.TransferBadDebt(
		ctx, staleOperator, "stale-replay-bad-debt", a.ID, mustAmount(t, "0.1"),
		"stale actor replay bad debt", "stale-replay-bad-debt",
	)
	if err != nil {
		t.Fatalf("create stale actor replay bad debt: %v", err)
	}
	staleReversalSource := create("ledger.stale.reversal.source", "1")
	staleReversalTarget := create("ledger.stale.reversal.target", "0")
	staleReversible, err := service.Transfer(
		ctx, "stale-replay-reversible", staleReversalSource.ID, staleReversalTarget.ID,
		mustAmount(t, "0.1"), "transaction for stale replay reversal", "test", "stale-replay-reversible",
	)
	if err != nil {
		t.Fatalf("create stale actor replay reversal source: %v", err)
	}
	staleReplayReversal, err := service.ReverseTransaction(
		ctx, staleOperator, "stale-replay-reversal", staleReversible.ID,
		"stale actor replay reversal", "stale-replay-reversal",
	)
	if err != nil {
		t.Fatalf("create stale actor replay reversal: %v", err)
	}
	if _, err := service.ReverseTransaction(ctx, admin, "clean-stale-replay-adjustment", staleReplayAdjustment.ID, "restore replay adjustment fixture", "clean-stale-replay-adjustment"); err != nil {
		t.Fatalf("restore stale replay adjustment fixture: %v", err)
	}
	if _, err := service.ReverseTransaction(ctx, admin, "clean-stale-replay-bad-debt", staleReplayBadDebt.ID, "restore replay bad debt fixture", "clean-stale-replay-bad-debt"); err != nil {
		t.Fatalf("restore stale replay bad debt fixture: %v", err)
	}
	demoted := false
	if _, err := identityService.UpdateAccount(ctx, admin, staleOperatorCreated.Account.ID, identity.AccountUpdate{ExpectedVersion: staleOperatorCreated.Account.Version, IsAdmin: &demoted}); err != nil {
		t.Fatalf("demote stale ledger operator: %v", err)
	}
	for label, replay := range map[string]func() error{
		"adjustment replay": func() error {
			transaction, err := service.AdminAdjustment(
				ctx, staleOperator, "stale-replay-adjustment", ledger.UserAccount(c.ID), ledger.UserAccount(b.ID),
				mustAmount(t, "0.1"), "stale actor replay adjustment", "admin_adjustment", "stale-replay-adjustment",
			)
			if err == nil && transaction.ID != staleReplayAdjustment.ID {
				t.Fatalf("adjustment replay ID = %s, want %s", transaction.ID, staleReplayAdjustment.ID)
			}
			return err
		},
		"bad debt replay": func() error {
			transaction, err := service.TransferBadDebt(
				ctx, staleOperator, "stale-replay-bad-debt", a.ID, mustAmount(t, "0.1"),
				"stale actor replay bad debt", "stale-replay-bad-debt",
			)
			if err == nil && transaction.ID != staleReplayBadDebt.ID {
				t.Fatalf("bad debt replay ID = %s, want %s", transaction.ID, staleReplayBadDebt.ID)
			}
			return err
		},
		"reversal replay": func() error {
			transaction, err := service.ReverseTransaction(
				ctx, staleOperator, "stale-replay-reversal", staleReversible.ID,
				"stale actor replay reversal", "stale-replay-reversal",
			)
			if err == nil && transaction.ID != staleReplayReversal.ID {
				t.Fatalf("reversal replay ID = %s, want %s", transaction.ID, staleReplayReversal.ID)
			}
			return err
		},
	} {
		if err := replay(); !errors.Is(err, identity.ErrForbidden) {
			t.Fatalf("demoted %s error = %v", label, err)
		}
	}
	for label, operation := range map[string]func() error{
		"adjustment": func() error {
			_, err := service.AdminAdjustment(ctx, staleOperator, "stale-adjustment", ledger.UserAccount(b.ID), ledger.UserAccount(c.ID), mustAmount(t, "0.1"), "stale actor", "admin_adjustment", "stale-adjustment")
			return err
		},
		"bad debt": func() error {
			_, err := service.TransferBadDebt(ctx, staleOperator, "stale-bad-debt", a.ID, mustAmount(t, "0.1"), "stale actor", "stale-bad-debt")
			return err
		},
		"reversal": func() error {
			_, err := service.ReverseTransaction(ctx, staleOperator, "stale-reversal", initial.ID, "stale actor", "stale-reversal")
			return err
		},
	} {
		if err := operation(); !errors.Is(err, identity.ErrForbidden) {
			t.Fatalf("stale %s actor error = %v", label, err)
		}
	}

	testConcurrentDebit(t, ctx, identityService, service, admin, create)
	testConcurrentCreditChange(t, ctx, identityService, service, admin, create)
	testConcurrentHoldCapacity(t, ctx, service, create)
	testConcurrentSameKeyReplay(t, ctx, service, create)
	testConcurrentCaptureRelease(t, ctx, service, create)
	testCaptureFailureRollback(t, ctx, service, create)
	testTransactionBoundLedgerPrimitive(t, ctx, store, service, create)
	testMinimumBalanceGuard(t, ctx, service, admin, create)

	metrics, err := service.Metrics(ctx, admin)
	if err != nil || metrics.TotalPostedBalance != "0" || metrics.LossPostedBalance != "-3" || metrics.AssetReserved != "0" || metrics.SpendAuthorized != "0" {
		t.Fatalf("metrics = %+v, err %v", metrics, err)
	}
	if _, err := service.Metrics(ctx, a); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("ordinary metrics error = %v", err)
	}
	entries, err := service.Entries(ctx, a.ID, 0, 2)
	if err != nil || len(entries) != 2 || entries[0].ID <= entries[1].ID || entries[0].Reason == "" || entries[0].ReferenceID == "" || len(entries[0].Counterparties) == 0 || entries[0].BusinessRole == "" {
		t.Fatalf("recent entries = %+v, err %v", entries, err)
	}
	older, err := service.Entries(ctx, a.ID, entries[1].ID, 10)
	if err != nil || len(older) == 0 || older[0].ID >= entries[1].ID {
		t.Fatalf("older entries = %+v, err %v", older, err)
	}

	var accountCount, ledgerUserCount, systemCount int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts), (SELECT count(*) FROM ledger_accounts WHERE kind = 'user'), (SELECT count(*) FROM ledger_accounts WHERE kind <> 'user')`).Scan(&accountCount, &ledgerUserCount, &systemCount); err != nil || accountCount != ledgerUserCount || systemCount != 2 {
		t.Fatalf("ledger initialization counts = %d/%d/%d, err %v", accountCount, ledgerUserCount, systemCount, err)
	}

	raw, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin raw transaction: %v", err)
	}
	defer raw.Rollback(ctx) //nolint:errcheck
	var bLedgerID, rawTxID string
	if err := raw.QueryRow(ctx, `SELECT id::text FROM ledger_accounts WHERE identity_account_id = $1`, b.ID).Scan(&bLedgerID); err != nil {
		t.Fatalf("lookup ledger account: %v", err)
	}
	if _, err := raw.Exec(ctx, `INSERT INTO ledger_commands (operation, idempotency_key, payload_hash) VALUES ('test', 'raw-unbalanced', decode(repeat('00', 32), 'hex'))`); err != nil {
		t.Fatalf("insert raw command: %v", err)
	}
	if err := raw.QueryRow(ctx, `INSERT INTO ledger_transactions (command_operation, idempotency_key, kind, reason, reference_type, reference_id) VALUES ('test', 'raw-unbalanced', 'transfer', 'invalid', 'test', 'raw') RETURNING id::text`).Scan(&rawTxID); err != nil {
		t.Fatalf("insert raw transaction: %v", err)
	}
	if _, err := raw.Exec(ctx, `INSERT INTO ledger_entries (transaction_id, ledger_account_id, entry_ordinal, business_role, amount_nano, posted_balance_before_nano, posted_balance_after_nano) VALUES ($1, $2, 1, 'test', 1, 0, 1)`, rawTxID, bLedgerID); err != nil {
		t.Fatalf("insert raw entry: %v", err)
	}
	if _, err := raw.Exec(ctx, `UPDATE ledger_transactions SET sealed = true WHERE id = $1`, rawTxID); err != nil {
		t.Fatalf("seal raw transaction: %v", err)
	}
	if _, err := raw.Exec(ctx, `UPDATE ledger_commands SET result_id = $1, result_payload = '{}'::jsonb, completed_at = now() WHERE operation = 'test' AND idempotency_key = 'raw-unbalanced'`, rawTxID); err != nil {
		t.Fatalf("complete raw unbalanced command: %v", err)
	}
	if err := raw.Commit(ctx); err == nil {
		t.Fatal("unbalanced raw transaction unexpectedly committed")
	} else if !strings.Contains(err.Error(), "sealed with at least two balanced entries") {
		t.Fatalf("unbalanced raw transaction failed for the wrong constraint: %v", err)
	}

	rawOrdinal, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin out-of-order transaction: %v", err)
	}
	defer rawOrdinal.Rollback(ctx) //nolint:errcheck
	var ordinalBalance int64
	if err := rawOrdinal.QueryRow(ctx, `SELECT id::text, posted_balance_nano FROM ledger_accounts WHERE identity_account_id = $1 FOR UPDATE`, b.ID).Scan(&bLedgerID, &ordinalBalance); err != nil {
		t.Fatalf("lock out-of-order ledger account: %v", err)
	}
	if _, err := rawOrdinal.Exec(ctx, `INSERT INTO ledger_commands (operation, idempotency_key, payload_hash) VALUES ('transaction:self_channel_usage', 'raw-out-of-order', decode(repeat('11', 32), 'hex'))`); err != nil {
		t.Fatalf("insert out-of-order command: %v", err)
	}
	var rawOrdinalTxID string
	if err := rawOrdinal.QueryRow(ctx, `
		INSERT INTO ledger_transactions (command_operation, idempotency_key, kind, reason, reference_type, reference_id)
		VALUES ('transaction:self_channel_usage', 'raw-out-of-order', 'self_channel_usage', 'wrong ordinal order', 'test', 'raw-out-of-order')
		RETURNING id::text`).Scan(&rawOrdinalTxID); err != nil {
		t.Fatalf("insert out-of-order transaction: %v", err)
	}
	if _, err := rawOrdinal.Exec(ctx, `
		INSERT INTO ledger_entries (transaction_id, ledger_account_id, entry_ordinal, business_role, amount_nano, posted_balance_before_nano, posted_balance_after_nano)
		VALUES ($1, $2, 2, 'consumer', -1, $3, $3::bigint - 1)`, rawOrdinalTxID, bLedgerID, ordinalBalance); err != nil {
		t.Fatalf("insert out-of-order first entry: %v", err)
	}
	if _, err := rawOrdinal.Exec(ctx, `
		INSERT INTO ledger_entries (transaction_id, ledger_account_id, entry_ordinal, business_role, amount_nano, posted_balance_before_nano, posted_balance_after_nano)
		VALUES ($1, $2, 1, 'provider', 1, $3::bigint - 1, $3)`, rawOrdinalTxID, bLedgerID, ordinalBalance); err != nil {
		t.Fatalf("insert out-of-order second entry: %v", err)
	}
	if _, err := rawOrdinal.Exec(ctx, `UPDATE ledger_transactions SET sealed = true WHERE id = $1`, rawOrdinalTxID); err != nil {
		t.Fatalf("seal out-of-order transaction: %v", err)
	}
	if _, err := rawOrdinal.Exec(ctx, `UPDATE ledger_commands SET result_id = $1, result_payload = '{}'::jsonb, completed_at = now() WHERE operation = 'transaction:self_channel_usage' AND idempotency_key = 'raw-out-of-order'`, rawOrdinalTxID); err != nil {
		t.Fatalf("complete out-of-order command: %v", err)
	}
	if err := rawOrdinal.Commit(ctx); err == nil {
		t.Fatal("out-of-order entry ordinals unexpectedly committed")
	} else if !strings.Contains(err.Error(), "entry ordinals must be contiguous and match insertion order") {
		t.Fatalf("out-of-order transaction failed for the wrong constraint: %v", err)
	}
	rawLoss, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin raw loss transaction: %v", err)
	}
	defer rawLoss.Rollback(ctx) //nolint:errcheck
	var lossLedgerID, rawLossTxID string
	var bBalance, lossBalance int64
	if err := rawLoss.QueryRow(ctx, `SELECT id::text, posted_balance_nano FROM ledger_accounts WHERE identity_account_id = $1 FOR UPDATE`, b.ID).Scan(&bLedgerID, &bBalance); err != nil {
		t.Fatalf("lock user ledger for raw loss: %v", err)
	}
	if err := rawLoss.QueryRow(ctx, `SELECT id::text, posted_balance_nano FROM ledger_accounts WHERE kind = 'platform_loss' FOR UPDATE`).Scan(&lossLedgerID, &lossBalance); err != nil {
		t.Fatalf("lock loss ledger: %v", err)
	}
	if _, err := rawLoss.Exec(ctx, `INSERT INTO ledger_commands (operation, idempotency_key, payload_hash) VALUES ('raw.loss', 'raw-loss-transfer', decode(repeat('22', 32), 'hex'))`); err != nil {
		t.Fatalf("insert raw loss command: %v", err)
	}
	if err := rawLoss.QueryRow(ctx, `
		INSERT INTO ledger_transactions (command_operation, idempotency_key, kind, reason, reference_type, reference_id)
		VALUES ('raw.loss', 'raw-loss-transfer', 'transfer', 'invalid loss use', 'test', 'raw-loss') RETURNING id::text`).Scan(&rawLossTxID); err != nil {
		t.Fatalf("insert raw loss transaction: %v", err)
	}
	if _, err := rawLoss.Exec(ctx, `
		INSERT INTO ledger_entries (transaction_id, ledger_account_id, entry_ordinal, business_role, amount_nano, posted_balance_before_nano, posted_balance_after_nano)
		VALUES ($1, $2, 1, 'consumer', -1, $3::bigint, $3::bigint - 1), ($1, $4, 2, 'platform_loss', 1, $5::bigint, $5::bigint + 1)`, rawLossTxID, bLedgerID, bBalance, lossLedgerID, lossBalance); err != nil {
		t.Fatalf("insert raw loss entries: %v", err)
	}
	if _, err := rawLoss.Exec(ctx, `UPDATE ledger_accounts SET posted_balance_nano = posted_balance_nano - 1 WHERE id = $1`, bLedgerID); err != nil {
		t.Fatalf("update raw loss user projection: %v", err)
	}
	if _, err := rawLoss.Exec(ctx, `UPDATE ledger_accounts SET posted_balance_nano = posted_balance_nano + 1 WHERE id = $1`, lossLedgerID); err != nil {
		t.Fatalf("update raw loss system projection: %v", err)
	}
	if _, err := rawLoss.Exec(ctx, `UPDATE ledger_transactions SET sealed = true WHERE id = $1`, rawLossTxID); err != nil {
		t.Fatalf("seal raw loss transaction: %v", err)
	}
	if _, err := rawLoss.Exec(ctx, `UPDATE ledger_commands SET result_id = $1, result_payload = '{}'::jsonb, completed_at = now() WHERE operation = 'raw.loss' AND idempotency_key = 'raw-loss-transfer'`, rawLossTxID); err != nil {
		t.Fatalf("complete raw loss command: %v", err)
	}
	if err := rawLoss.Commit(ctx); err == nil {
		t.Fatal("ordinary transaction using platform loss unexpectedly committed")
	}

	rawReversal, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin forged reversal transaction: %v", err)
	}
	defer rawReversal.Rollback(ctx) //nolint:errcheck
	var aLedgerID, forgedReversalID string
	var aBalance, currentBBalance int64
	if err := rawReversal.QueryRow(ctx, `SELECT id::text, posted_balance_nano FROM ledger_accounts WHERE identity_account_id = $1 FOR UPDATE`, a.ID).Scan(&aLedgerID, &aBalance); err != nil {
		t.Fatalf("lock reversal source: %v", err)
	}
	if err := rawReversal.QueryRow(ctx, `SELECT id::text, posted_balance_nano FROM ledger_accounts WHERE identity_account_id = $1 FOR UPDATE`, b.ID).Scan(&bLedgerID, &currentBBalance); err != nil {
		t.Fatalf("lock reversal target: %v", err)
	}
	if _, err := rawReversal.Exec(ctx, `INSERT INTO ledger_commands (operation, idempotency_key, payload_hash) VALUES ('transaction:reversal', 'raw-forged-reversal', decode(repeat('33', 32), 'hex'))`); err != nil {
		t.Fatalf("insert forged reversal command: %v", err)
	}
	if err := rawReversal.QueryRow(ctx, `
		INSERT INTO ledger_transactions (command_operation, idempotency_key, kind, reason, reference_type, reference_id, actor_account_id, reversal_of_transaction_id)
		VALUES ('transaction:reversal', 'raw-forged-reversal', 'reversal', 'wrong amount reversal', 'reversal', 'raw-forged-reversal', $1, $2)
		RETURNING id::text`, admin.ID, initial.ID).Scan(&forgedReversalID); err != nil {
		t.Fatalf("insert forged reversal transaction: %v", err)
	}
	forgedAmount := mustAmount(t, "5").Nano()
	if _, err := rawReversal.Exec(ctx, `
		INSERT INTO ledger_entries (transaction_id, ledger_account_id, entry_ordinal, business_role, amount_nano, posted_balance_before_nano, posted_balance_after_nano)
		VALUES ($1, $2, 1, 'reversal', $3, $4, $4::bigint + $3::bigint),
		       ($1, $5, 2, 'reversal', -$3, $6, $6::bigint - $3::bigint)`,
		forgedReversalID, aLedgerID, forgedAmount, aBalance, bLedgerID, currentBBalance); err != nil {
		t.Fatalf("insert forged reversal entries: %v", err)
	}
	if _, err := rawReversal.Exec(ctx, `UPDATE ledger_accounts SET posted_balance_nano = posted_balance_nano + $2 WHERE id = $1`, aLedgerID, forgedAmount); err != nil {
		t.Fatalf("update forged reversal source projection: %v", err)
	}
	if _, err := rawReversal.Exec(ctx, `UPDATE ledger_accounts SET posted_balance_nano = posted_balance_nano - $2 WHERE id = $1`, bLedgerID, forgedAmount); err != nil {
		t.Fatalf("update forged reversal target projection: %v", err)
	}
	if _, err := rawReversal.Exec(ctx, `UPDATE ledger_transactions SET sealed = true WHERE id = $1`, forgedReversalID); err != nil {
		t.Fatalf("seal forged reversal: %v", err)
	}
	if _, err := rawReversal.Exec(ctx, `UPDATE ledger_commands SET result_id = $1, result_payload = '{}'::jsonb, completed_at = now() WHERE operation = 'transaction:reversal' AND idempotency_key = 'raw-forged-reversal'`, forgedReversalID); err != nil {
		t.Fatalf("complete forged reversal command: %v", err)
	}
	if err := rawReversal.Commit(ctx); err == nil {
		t.Fatal("non-exact raw reversal unexpectedly committed")
	}

	rawCapture, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin orphan capture transaction: %v", err)
	}
	defer rawCapture.Rollback(ctx) //nolint:errcheck
	var cLedgerID, orphanCaptureID string
	var currentABalance, currentCBalance int64
	if err := rawCapture.QueryRow(ctx, `SELECT id::text, posted_balance_nano FROM ledger_accounts WHERE identity_account_id = $1 FOR UPDATE`, a.ID).Scan(&aLedgerID, &currentABalance); err != nil {
		t.Fatalf("lock orphan capture source: %v", err)
	}
	if err := rawCapture.QueryRow(ctx, `SELECT id::text, posted_balance_nano FROM ledger_accounts WHERE identity_account_id = $1 FOR UPDATE`, c.ID).Scan(&cLedgerID, &currentCBalance); err != nil {
		t.Fatalf("lock orphan capture target: %v", err)
	}
	if _, err := rawCapture.Exec(ctx, `INSERT INTO ledger_commands (operation, idempotency_key, payload_hash) VALUES ('hold.capture', 'raw-orphan-capture', decode(repeat('44', 32), 'hex'))`); err != nil {
		t.Fatalf("insert orphan capture command: %v", err)
	}
	if err := rawCapture.QueryRow(ctx, `
		INSERT INTO ledger_transactions (command_operation, idempotency_key, kind, reason, reference_type, reference_id, hold_id)
		VALUES ('hold.capture', 'raw-orphan-capture', 'hold_capture', 'capture without event', 'test', 'raw-orphan-capture', $1)
		RETURNING id::text`, creditHold.ID).Scan(&orphanCaptureID); err != nil {
		t.Fatalf("insert orphan capture transaction: %v", err)
	}
	if _, err := rawCapture.Exec(ctx, `
		INSERT INTO ledger_entries (transaction_id, ledger_account_id, entry_ordinal, business_role, amount_nano, posted_balance_before_nano, posted_balance_after_nano)
		VALUES ($1, $2, 1, 'consumer', -1, $3, $3::bigint - 1),
		       ($1, $4, 2, 'provider', 1, $5, $5::bigint + 1)`,
		orphanCaptureID, aLedgerID, currentABalance, cLedgerID, currentCBalance); err != nil {
		t.Fatalf("insert orphan capture entries: %v", err)
	}
	if _, err := rawCapture.Exec(ctx, `UPDATE ledger_accounts SET posted_balance_nano = posted_balance_nano - 1 WHERE id = $1`, aLedgerID); err != nil {
		t.Fatalf("update orphan capture source projection: %v", err)
	}
	if _, err := rawCapture.Exec(ctx, `UPDATE ledger_accounts SET posted_balance_nano = posted_balance_nano + 1 WHERE id = $1`, cLedgerID); err != nil {
		t.Fatalf("update orphan capture target projection: %v", err)
	}
	if _, err := rawCapture.Exec(ctx, `UPDATE ledger_transactions SET sealed = true WHERE id = $1`, orphanCaptureID); err != nil {
		t.Fatalf("seal orphan capture: %v", err)
	}
	if _, err := rawCapture.Exec(ctx, `UPDATE ledger_commands SET result_id = $1, result_payload = '{}'::jsonb, completed_at = now() WHERE operation = 'hold.capture' AND idempotency_key = 'raw-orphan-capture'`, orphanCaptureID); err != nil {
		t.Fatalf("complete orphan capture command: %v", err)
	}
	if err := rawCapture.Commit(ctx); err == nil {
		t.Fatal("capture transaction without matching hold event unexpectedly committed")
	}
	sameAccountCaptureHold, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "raw-same-account-capture-hold", AccountID: b.ID, Amount: mustAmount(t, "1"),
		Purpose: ledger.HoldPurposeSpendAuthorization, FundingPolicy: ledger.HoldFundingCreditAllowed,
		Reason: "test same-account capture guard", BusinessType: "api_request", BusinessID: "raw-same-account-capture",
	})
	if err != nil {
		t.Fatalf("create same-account capture hold: %v", err)
	}
	rawSameAccountCapture, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin same-account capture transaction: %v", err)
	}
	defer rawSameAccountCapture.Rollback(ctx) //nolint:errcheck
	var sameAccountCaptureID, sameAccountLedgerID string
	var sameAccountBalance int64
	if err := rawSameAccountCapture.QueryRow(ctx, `SELECT id::text, posted_balance_nano FROM ledger_accounts WHERE identity_account_id = $1 FOR UPDATE`, b.ID).Scan(&sameAccountLedgerID, &sameAccountBalance); err != nil {
		t.Fatalf("lock same-account capture source: %v", err)
	}
	if _, err := rawSameAccountCapture.Exec(ctx, `INSERT INTO ledger_commands (operation, idempotency_key, payload_hash) VALUES ('hold.capture', 'raw-same-account-capture', decode(repeat('66', 32), 'hex'))`); err != nil {
		t.Fatalf("insert same-account capture command: %v", err)
	}
	if err := rawSameAccountCapture.QueryRow(ctx, `
		INSERT INTO ledger_transactions (command_operation, idempotency_key, kind, reason, reference_type, reference_id, hold_id)
		VALUES ('hold.capture', 'raw-same-account-capture', 'hold_capture', 'invalid same-account provider', 'api_usage', 'raw-same-account-capture', $1)
		RETURNING id::text`, sameAccountCaptureHold.ID).Scan(&sameAccountCaptureID); err != nil {
		t.Fatalf("insert same-account capture transaction: %v", err)
	}
	amountNano := mustAmount(t, "1").Nano()
	if _, err := rawSameAccountCapture.Exec(ctx, `
		INSERT INTO ledger_entries (transaction_id, ledger_account_id, entry_ordinal, business_role, amount_nano, posted_balance_before_nano, posted_balance_after_nano)
		VALUES ($1, $2, 1, 'consumer', -$3::bigint, $4, $4::bigint - $3::bigint),
		       ($1, $2, 2, 'provider', $3::bigint, $4::bigint - $3::bigint, $4)`,
		sameAccountCaptureID, sameAccountLedgerID, amountNano, sameAccountBalance); err != nil {
		t.Fatalf("insert same-account capture entries: %v", err)
	}
	if _, err := rawSameAccountCapture.Exec(ctx, `UPDATE ledger_accounts SET spend_authorized_nano = spend_authorized_nano - $2, version = version + 1, updated_at = now() WHERE id = $1`, sameAccountLedgerID, amountNano); err != nil {
		t.Fatalf("update same-account capture hold projection: %v", err)
	}
	if _, err := rawSameAccountCapture.Exec(ctx, `
		UPDATE ledger_holds
		SET remaining_nano = 0, captured_nano = captured_nano + $2, status = 'closed', updated_at = now()
		WHERE id = $1`, sameAccountCaptureHold.ID, amountNano); err != nil {
		t.Fatalf("update same-account capture hold totals: %v", err)
	}
	if _, err := rawSameAccountCapture.Exec(ctx, `
		INSERT INTO ledger_hold_events (hold_id, command_operation, idempotency_key, kind, business_id, amount_nano, transaction_id, reason)
		VALUES ($1, 'hold.capture', 'raw-same-account-capture', 'capture', 'raw-same-account-capture', $2, $3, 'invalid same-account provider')`, sameAccountCaptureHold.ID, amountNano, sameAccountCaptureID); err != nil {
		t.Fatalf("insert same-account capture event: %v", err)
	}
	if _, err := rawSameAccountCapture.Exec(ctx, `UPDATE ledger_transactions SET sealed = true WHERE id = $1`, sameAccountCaptureID); err != nil {
		t.Fatalf("seal same-account capture transaction: %v", err)
	}
	if _, err := rawSameAccountCapture.Exec(ctx, `UPDATE ledger_commands SET result_id = $1, result_payload = '{}'::jsonb, completed_at = now() WHERE operation = 'hold.capture' AND idempotency_key = 'raw-same-account-capture'`, sameAccountCaptureID); err != nil {
		t.Fatalf("complete same-account capture command: %v", err)
	}
	if err := rawSameAccountCapture.Commit(ctx); err == nil {
		t.Fatal("same-account provider capture unexpectedly committed")
	} else if !strings.Contains(err.Error(), "invalid destination shape") {
		t.Fatalf("same-account capture failed for the wrong constraint: %v", err)
	}
	if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{
		IdempotencyKey: "release-raw-same-account-capture", HoldID: sameAccountCaptureHold.ID,
		BusinessID: "release-raw-same-account-capture", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll},
		Reason: "clean up rejected same-account capture",
	}); err != nil {
		t.Fatalf("release same-account capture hold: %v", err)
	}
	rawHold, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin ineligible raw hold: %v", err)
	}
	defer rawHold.Rollback(ctx) //nolint:errcheck
	var positiveFrozenLedgerID, rawHoldID string
	if err := rawHold.QueryRow(ctx, `SELECT id::text FROM ledger_accounts WHERE identity_account_id = $1 FOR UPDATE`, positiveFrozen.ID).Scan(&positiveFrozenLedgerID); err != nil {
		t.Fatalf("lock frozen raw hold account: %v", err)
	}
	if _, err := rawHold.Exec(ctx, `INSERT INTO ledger_commands (operation, idempotency_key, payload_hash) VALUES ('hold.create', 'raw-frozen-hold', decode(repeat('55', 32), 'hex'))`); err != nil {
		t.Fatalf("insert frozen hold command: %v", err)
	}
	if err := rawHold.QueryRow(ctx, `
		INSERT INTO ledger_holds (ledger_account_id, create_idempotency_key, purpose, funding_policy, amount_nano, remaining_nano, reason, business_type, business_id)
		VALUES ($1, 'raw-frozen-hold', 'asset_reservation', 'settled_balance_only', 1000000000, 1000000000, 'frozen account bypass', 'test', 'raw-frozen-hold')
		RETURNING id::text`, positiveFrozenLedgerID).Scan(&rawHoldID); err != nil {
		t.Fatalf("insert frozen raw hold: %v", err)
	}
	if _, err := rawHold.Exec(ctx, `UPDATE ledger_accounts SET asset_reserved_nano = asset_reserved_nano + 1000000000 WHERE id = $1`, positiveFrozenLedgerID); err != nil {
		t.Fatalf("update frozen raw hold projection: %v", err)
	}
	if _, err := rawHold.Exec(ctx, `UPDATE ledger_commands SET result_id = $1, result_payload = '{}'::jsonb, completed_at = now() WHERE operation = 'hold.create' AND idempotency_key = 'raw-frozen-hold'`, rawHoldID); err != nil {
		t.Fatalf("complete frozen raw hold command: %v", err)
	}
	if err := rawHold.Commit(ctx); err == nil {
		t.Fatal("frozen account raw hold unexpectedly committed")
	} else if !strings.Contains(err.Error(), "active unfrozen user account") {
		t.Fatalf("frozen raw hold failed for the wrong constraint: %v", err)
	}
	projectionUpdate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin projection update: %v", err)
	}
	defer projectionUpdate.Rollback(ctx) //nolint:errcheck
	if _, err := projectionUpdate.Exec(ctx, `UPDATE ledger_accounts SET posted_balance_nano = posted_balance_nano + 1 WHERE identity_account_id = $1`, b.ID); err != nil {
		t.Fatalf("stage direct projection update: %v", err)
	}
	if err := projectionUpdate.Commit(ctx); err == nil {
		t.Fatal("direct posted balance projection update unexpectedly committed")
	}
	holdTotalsUpdate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin hold totals update: %v", err)
	}
	defer holdTotalsUpdate.Rollback(ctx) //nolint:errcheck
	if _, err := holdTotalsUpdate.Exec(ctx, `UPDATE ledger_holds SET captured_nano = captured_nano + 1, released_nano = released_nano - 1 WHERE id = $1`, creditHold.ID); err != nil {
		t.Fatalf("stage direct hold totals update: %v", err)
	}
	if err := holdTotalsUpdate.Commit(ctx); err == nil {
		t.Fatal("direct hold totals update unexpectedly committed")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM ledger_commands WHERE operation = 'transaction:transfer' AND idempotency_key = 'transfer-initial'`); err == nil {
		t.Fatal("completed ledger command unexpectedly deleted")
	}
	if _, err := pool.Exec(ctx, `UPDATE ledger_entries SET amount_nano = amount_nano WHERE transaction_id = $1`, initial.ID); err == nil {
		t.Fatal("immutable entry unexpectedly updated")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM ledger_hold_events WHERE hold_id = $1`, creditHold.ID); err == nil {
		t.Fatal("immutable hold event unexpectedly deleted")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM ledger_accounts WHERE kind = 'platform_incentive'`); err == nil {
		t.Fatal("fixed system account unexpectedly deleted")
	}

	readyAdmin, err := identityService.ChangePassword(ctx, admin.ID, "Bootstrap-password-2026", "Ledger-admin-password-2026")
	if err != nil {
		t.Fatalf("ready administrator for HTTP boundary: %v", err)
	}
	handler := api.NewHandler(api.Dependencies{Identity: identityService, Catalog: catalog.NewService(store), Ledger: service, CookieSecure: true})
	adminCookie := loginCookie(t, handler, readyAdmin.Account.Username, "Ledger-admin-password-2026")
	ordinaryCreated, err := identityService.CreateInvitedAccount(ctx, readyAdmin.Account, "ledger.http.member", "Ledger HTTP member", mustAmount(t, "1"), false, identity.StatusActive)
	if err != nil {
		t.Fatalf("create HTTP member: %v", err)
	}
	if _, err := identityService.ChangePassword(ctx, ordinaryCreated.Account.ID, ordinaryCreated.InitialPassword, "Ledger-member-password-2026"); err != nil {
		t.Fatalf("ready HTTP member: %v", err)
	}
	ordinaryCookie := loginCookie(t, handler, ordinaryCreated.Account.Username, "Ledger-member-password-2026")
	doLedgerHTTP := func(method, path string, payload any, cookie *http.Cookie, key string) *httptest.ResponseRecorder {
		t.Helper()
		var request *http.Request
		if payload == nil {
			request = httptest.NewRequest(method, "https://hub.example"+path, nil)
		} else {
			request = jsonRequest(t, method, "https://hub.example"+path, payload)
		}
		if method != http.MethodGet && method != http.MethodHead {
			request.Header.Set("Origin", "https://hub.example")
		}
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	for _, denial := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/admin/ledger/metrics", nil},
		{http.MethodGet, "/api/admin/ledger/accounts/" + a.ID + "/wallet", nil},
		{http.MethodGet, "/api/admin/ledger/accounts/" + a.ID + "/entries", nil},
		{http.MethodGet, "/api/admin/ledger/system-accounts/platform_loss/wallet", nil},
		{http.MethodGet, "/api/admin/ledger/system-accounts/platform_loss/entries", nil},
		{http.MethodPost, "/api/admin/ledger/adjustments", map[string]any{}},
		{http.MethodPost, "/api/admin/ledger/bad-debts", map[string]any{}},
	} {
		response := doLedgerHTTP(denial.method, denial.path, denial.body, ordinaryCookie, "ordinary-user-denied")
		if response.Code != http.StatusForbidden {
			t.Fatalf("ordinary user %s %s = %d %s", denial.method, denial.path, response.Code, response.Body.String())
		}
		failure := decodeLedgerHTTP[ledgerHTTPErrorResponse](t, response)
		if failure.Error.Code != "administrator_required" {
			t.Fatalf("ordinary user %s %s error = %+v", denial.method, denial.path, failure)
		}
	}
	ordinaryWallet := doLedgerHTTP(http.MethodGet, "/api/wallet", nil, ordinaryCookie, "")
	ordinaryEntries := doLedgerHTTP(http.MethodGet, "/api/wallet/entries", nil, ordinaryCookie, "")
	if ordinaryWallet.Code != http.StatusOK || ordinaryEntries.Code != http.StatusOK {
		t.Fatalf("ordinary wallet routes = %d/%d", ordinaryWallet.Code, ordinaryEntries.Code)
	}
	invalidAccountWallet := doLedgerHTTP(http.MethodGet, "/api/admin/ledger/accounts/not-a-uuid/wallet", nil, adminCookie, "")
	if invalidAccountWallet.Code != http.StatusNotFound || decodeLedgerHTTP[ledgerHTTPErrorResponse](t, invalidAccountWallet).Error.Code != "not_found" {
		t.Fatalf("invalid administrator ledger account = %d %s", invalidAccountWallet.Code, invalidAccountWallet.Body.String())
	}
	invalidAdjustment := doLedgerHTTP(http.MethodPost, "/api/admin/ledger/adjustments", map[string]any{
		"from":   map[string]string{"account_id": "not-a-uuid"},
		"to":     map[string]string{"system_kind": string(ledger.AccountIncentive)},
		"amount": "1", "reason": "invalid account reference",
		"reference_type": "invalid_test", "reference_id": "invalid-adjustment-account",
	}, adminCookie, "invalid-adjustment-account")
	invalidBadDebt := doLedgerHTTP(http.MethodPost, "/api/admin/ledger/bad-debts", map[string]any{
		"account_id": "not-a-uuid", "amount": "1", "reason": "invalid debtor reference", "reference_id": "invalid-bad-debt-account",
	}, adminCookie, "invalid-bad-debt-account")
	for label, response := range map[string]*httptest.ResponseRecorder{
		"adjustment": invalidAdjustment,
		"bad debt":   invalidBadDebt,
	} {
		if response.Code != http.StatusUnprocessableEntity || decodeLedgerHTTP[ledgerHTTPErrorResponse](t, response).Error.Code != "invalid_input" {
			t.Fatalf("invalid %s account reference = %d %s", label, response.Code, response.Body.String())
		}
	}

	httpDebtor := create("ledger.http.debtor", "5")
	httpCounterparty := create("ledger.http.counterparty", "0")
	adjustmentBody := map[string]any{
		"from":   map[string]string{"account_id": httpDebtor.ID},
		"to":     map[string]string{"account_id": httpCounterparty.ID},
		"amount": "2", "reason": "HTTP administrator correction",
		"reference_type": "http_admin_adjustment", "reference_id": "http-adjustment-1",
	}
	adjustmentResponse := doLedgerHTTP(http.MethodPost, "/api/admin/ledger/adjustments", adjustmentBody, adminCookie, "http-adjustment-key")
	if adjustmentResponse.Code != http.StatusOK {
		t.Fatalf("HTTP adjustment = %d %s", adjustmentResponse.Code, adjustmentResponse.Body.String())
	}
	adjustmentHTTP := decodeLedgerHTTP[ledgerHTTPTransactionResponse](t, adjustmentResponse).Transaction
	if adjustmentHTTP.Kind != string(ledger.TransactionAdjustment) || adjustmentHTTP.ActorAccountID != admin.ID || adjustmentHTTP.Reason != "HTTP administrator correction" || adjustmentHTTP.ReferenceType != "http_admin_adjustment" || adjustmentHTTP.ReferenceID != "http-adjustment-1" || len(adjustmentHTTP.Entries) != 2 {
		t.Fatalf("HTTP adjustment transaction = %+v", adjustmentHTTP)
	}
	if adjustmentHTTP.Entries[0].BusinessRole != string(ledger.EntryRoleAdjustmentSource) || adjustmentHTTP.Entries[0].Amount != "-2" || adjustmentHTTP.Entries[0].PostedBalanceBefore != "0" || adjustmentHTTP.Entries[0].PostedBalanceAfter != "-2" || adjustmentHTTP.Entries[0].TransactionKind != string(ledger.TransactionAdjustment) || adjustmentHTTP.Entries[0].ActorAccountID != admin.ID || len(adjustmentHTTP.Entries[0].Counterparties) != 1 || adjustmentHTTP.Entries[1].BusinessRole != string(ledger.EntryRoleAdjustmentTarget) || adjustmentHTTP.Entries[1].Amount != "2" {
		t.Fatalf("HTTP adjustment entries = %+v", adjustmentHTTP.Entries)
	}

	lossBefore, err := service.AdminWallet(ctx, readyAdmin.Account, ledger.SystemAccount(ledger.AccountLoss))
	if err != nil {
		t.Fatalf("loss wallet before HTTP bad debt: %v", err)
	}
	badDebtBody := map[string]any{
		"account_id": httpDebtor.ID, "amount": "1", "reason": "HTTP unrecoverable debt", "reference_id": "http-bad-debt-1",
	}
	badDebtResponse := doLedgerHTTP(http.MethodPost, "/api/admin/ledger/bad-debts", badDebtBody, adminCookie, "http-bad-debt-key")
	if badDebtResponse.Code != http.StatusOK {
		t.Fatalf("HTTP bad debt = %d %s", badDebtResponse.Code, badDebtResponse.Body.String())
	}
	badDebtHTTP := decodeLedgerHTTP[ledgerHTTPTransactionResponse](t, badDebtResponse).Transaction
	if badDebtHTTP.Kind != string(ledger.TransactionBadDebt) || badDebtHTTP.ActorAccountID != admin.ID || badDebtHTTP.ReferenceType != "bad_debt" || badDebtHTTP.ReferenceID != "http-bad-debt-1" || len(badDebtHTTP.Entries) != 2 {
		t.Fatalf("HTTP bad debt transaction = %+v", badDebtHTTP)
	}
	lossAfter, err := service.AdminWallet(ctx, readyAdmin.Account, ledger.SystemAccount(ledger.AccountLoss))
	if err != nil || lossAfter.PostedBalance != lossBefore.PostedBalance-mustAmount(t, "1") {
		t.Fatalf("loss wallet after HTTP bad debt = %+v, before %+v, err %v", lossAfter, lossBefore, err)
	}
	debtorAfter, err := service.Wallet(ctx, httpDebtor.ID)
	if err != nil || debtorAfter.PostedBalance != mustAmount(t, "-1") {
		t.Fatalf("debtor wallet after HTTP bad debt = %+v, err %v", debtorAfter, err)
	}
	badDebtReplay := doLedgerHTTP(http.MethodPost, "/api/admin/ledger/bad-debts", badDebtBody, adminCookie, "http-bad-debt-key")
	if badDebtReplay.Code != http.StatusOK {
		t.Fatalf("HTTP bad debt replay = %d %s", badDebtReplay.Code, badDebtReplay.Body.String())
	}
	badDebtReplayHTTP := decodeLedgerHTTP[ledgerHTTPTransactionResponse](t, badDebtReplay).Transaction
	lossAfterReplay, replayWalletErr := service.AdminWallet(ctx, readyAdmin.Account, ledger.SystemAccount(ledger.AccountLoss))
	if badDebtReplayHTTP.ID != badDebtHTTP.ID || badDebtReplayHTTP.Entries[0].PostedBalanceBefore != "-2" || badDebtReplayHTTP.Entries[0].PostedBalanceAfter != "-1" || replayWalletErr != nil || lossAfterReplay.PostedBalance != lossAfter.PostedBalance {
		t.Fatalf("HTTP bad debt replay snapshot = %+v, wallet %+v, err %v", badDebtReplayHTTP, lossAfterReplay, replayWalletErr)
	}

	adjustmentReplay := doLedgerHTTP(http.MethodPost, "/api/admin/ledger/adjustments", adjustmentBody, adminCookie, "http-adjustment-key")
	if adjustmentReplay.Code != http.StatusOK {
		t.Fatalf("HTTP adjustment replay = %d %s", adjustmentReplay.Code, adjustmentReplay.Body.String())
	}
	adjustmentReplayHTTP := decodeLedgerHTTP[ledgerHTTPTransactionResponse](t, adjustmentReplay).Transaction
	if adjustmentReplayHTTP.ID != adjustmentHTTP.ID || adjustmentReplayHTTP.Entries[0].PostedBalanceBefore != "0" || adjustmentReplayHTTP.Entries[0].PostedBalanceAfter != "-2" {
		t.Fatalf("HTTP adjustment replay snapshot = %+v, original %+v", adjustmentReplayHTTP, adjustmentHTTP)
	}
	changedAdjustment := map[string]any{
		"from":   map[string]string{"account_id": httpDebtor.ID},
		"to":     map[string]string{"account_id": httpCounterparty.ID},
		"amount": "3", "reason": "HTTP administrator correction",
		"reference_type": "http_admin_adjustment", "reference_id": "http-adjustment-1",
	}
	adjustmentConflict := doLedgerHTTP(http.MethodPost, "/api/admin/ledger/adjustments", changedAdjustment, adminCookie, "http-adjustment-key")
	badDebtConflictBody := map[string]any{
		"account_id": httpDebtor.ID, "amount": "1", "reason": "changed debt reason", "reference_id": "http-bad-debt-1",
	}
	badDebtConflict := doLedgerHTTP(http.MethodPost, "/api/admin/ledger/bad-debts", badDebtConflictBody, adminCookie, "http-bad-debt-key")
	for label, response := range map[string]*httptest.ResponseRecorder{"adjustment": adjustmentConflict, "bad debt": badDebtConflict} {
		if response.Code != http.StatusConflict || decodeLedgerHTTP[ledgerHTTPErrorResponse](t, response).Error.Code != "ledger_conflict" {
			t.Fatalf("HTTP %s idempotency conflict = %d %s", label, response.Code, response.Body.String())
		}
	}

	debtorEntriesResponse := doLedgerHTTP(http.MethodGet, "/api/admin/ledger/accounts/"+httpDebtor.ID+"/entries?limit=100", nil, adminCookie, "")
	lossEntriesResponse := doLedgerHTTP(http.MethodGet, "/api/admin/ledger/system-accounts/platform_loss/entries?limit=100", nil, adminCookie, "")
	if debtorEntriesResponse.Code != http.StatusOK || lossEntriesResponse.Code != http.StatusOK {
		t.Fatalf("HTTP audit entry reads = %d/%d", debtorEntriesResponse.Code, lossEntriesResponse.Code)
	}
	debtorEntriesHTTP := decodeLedgerHTTP[ledgerHTTPEntriesResponse](t, debtorEntriesResponse)
	adjustmentEntryHTTP := findLedgerHTTPEntry(t, debtorEntriesHTTP.Entries, adjustmentHTTP.ID)
	badDebtEntryHTTP := findLedgerHTTPEntry(t, debtorEntriesHTTP.Entries, badDebtHTTP.ID)
	if adjustmentEntryHTTP.ActorAccountID != admin.ID || adjustmentEntryHTTP.TransactionKind != string(ledger.TransactionAdjustment) || adjustmentEntryHTTP.ReferenceType != "http_admin_adjustment" || adjustmentEntryHTTP.BusinessRole != string(ledger.EntryRoleAdjustmentSource) || len(adjustmentEntryHTTP.Counterparties) != 1 {
		t.Fatalf("HTTP adjustment audit entry = %+v", adjustmentEntryHTTP)
	}
	if badDebtEntryHTTP.ActorAccountID != admin.ID || badDebtEntryHTTP.TransactionKind != string(ledger.TransactionBadDebt) || badDebtEntryHTTP.BusinessRole != string(ledger.EntryRoleDebtor) || badDebtEntryHTTP.Amount != "1" || badDebtEntryHTTP.PostedBalanceBefore != "-2" || badDebtEntryHTTP.PostedBalanceAfter != "-1" {
		t.Fatalf("HTTP bad debt audit entry = %+v", badDebtEntryHTTP)
	}
	lossEntriesHTTP := decodeLedgerHTTP[ledgerHTTPEntriesResponse](t, lossEntriesResponse)
	lossEntryHTTP := findLedgerHTTPEntry(t, lossEntriesHTTP.Entries, badDebtHTTP.ID)
	if lossEntryHTTP.BusinessRole != string(ledger.EntryRolePlatformLoss) || lossEntryHTTP.Amount != "-1" || lossEntryHTTP.ActorAccountID != admin.ID {
		t.Fatalf("HTTP loss audit entry = %+v", lossEntryHTTP)
	}
	walletRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/wallet", nil)
	walletRequest.AddCookie(adminCookie)
	walletResponse := httptest.NewRecorder()
	handler.ServeHTTP(walletResponse, walletRequest)
	if walletResponse.Code != http.StatusOK || !strings.Contains(walletResponse.Body.String(), `"posted_balance"`) || !strings.Contains(walletResponse.Body.String(), `"asset_reserved"`) || strings.Contains(walletResponse.Body.String(), "available_credit") {
		t.Fatalf("canonical wallet API = %d %s", walletResponse.Code, walletResponse.Body.String())
	}
	metricsRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/admin/ledger/metrics", nil)
	metricsRequest.AddCookie(adminCookie)
	metricsResponse := httptest.NewRecorder()
	handler.ServeHTTP(metricsResponse, metricsRequest)
	if metricsResponse.Code != http.StatusOK || !strings.Contains(metricsResponse.Body.String(), `"zero_sum":true`) || !strings.Contains(metricsResponse.Body.String(), `"ledger_consistent":true`) {
		t.Fatalf("administrator metrics API = %d %s", metricsResponse.Code, metricsResponse.Body.String())
	}
	accountEntriesRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/admin/ledger/accounts/"+a.ID+"/entries", nil)
	accountEntriesRequest.AddCookie(adminCookie)
	accountEntriesResponse := httptest.NewRecorder()
	handler.ServeHTTP(accountEntriesResponse, accountEntriesRequest)
	if accountEntriesResponse.Code != http.StatusOK || !strings.Contains(accountEntriesResponse.Body.String(), `"business_role"`) || !strings.Contains(accountEntriesResponse.Body.String(), `"counterparties"`) || !strings.Contains(accountEntriesResponse.Body.String(), `"reason"`) {
		t.Fatalf("administrator account entries API = %d %s", accountEntriesResponse.Code, accountEntriesResponse.Body.String())
	}
	systemWalletRequest := httptest.NewRequest(http.MethodGet, "https://hub.example/api/admin/ledger/system-accounts/platform_loss/wallet", nil)
	systemWalletRequest.AddCookie(adminCookie)
	systemWalletResponse := httptest.NewRecorder()
	handler.ServeHTTP(systemWalletResponse, systemWalletRequest)
	if systemWalletResponse.Code != http.StatusOK || !strings.Contains(systemWalletResponse.Body.String(), `"posted_balance":"`+lossAfter.PostedBalance.String()+`"`) {
		t.Fatalf("administrator system wallet API = %d %s", systemWalletResponse.Code, systemWalletResponse.Body.String())
	}
}

func TestLedgerMigrationUpgradesExistingIdentityData(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	basePool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(basePool.Close)
	schema := "ledger_upgrade_" + randomHex(t, 8)
	if _, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = basePool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)) })
	schemaURL := withSearchPath(t, databaseURL, schema)
	if err := database.MigrateTo(ctx, schemaURL, 1); err != nil {
		t.Fatalf("migrate to identity schema: %v", err)
	}
	pool, err := database.Open(ctx, schemaURL)
	if err != nil {
		t.Fatalf("open identity schema: %v", err)
	}
	t.Cleanup(pool.Close)
	var activeID, disabledID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO accounts (username, display_name, password_hash, status, credit_limit_nano)
		VALUES ('upgrade.active', 'Upgrade Active', 'placeholder', 'active', 5000000000)
		RETURNING id::text`).Scan(&activeID); err != nil {
		t.Fatalf("insert active account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO accounts (username, display_name, password_hash, status, disabled_at)
		VALUES ('upgrade.disabled', 'Upgrade Disabled', 'placeholder', 'disabled', now())
		RETURNING id::text`).Scan(&disabledID); err != nil {
		t.Fatalf("insert disabled account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, account_id, password_version, expires_at)
		VALUES (decode(repeat('11', 32), 'hex'), $1, 1, now() + interval '1 hour')`, activeID); err != nil {
		t.Fatalf("insert existing session: %v", err)
	}
	if err := database.MigrateTo(ctx, schemaURL, 2); err != nil {
		t.Fatalf("upgrade ledger schema: %v", err)
	}
	if err := database.Migrate(ctx, schemaURL); err != nil {
		t.Fatalf("repeat migrations: %v", err)
	}
	var accounts, userLedgers, systems, sessions int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM accounts),
			(SELECT count(*) FROM ledger_accounts WHERE kind = 'user'),
			(SELECT count(*) FROM ledger_accounts WHERE kind <> 'user'),
			(SELECT count(*) FROM sessions)`).Scan(&accounts, &userLedgers, &systems, &sessions); err != nil {
		t.Fatalf("query upgraded counts: %v", err)
	}
	if accounts != 2 || userLedgers != 2 || systems != 2 || sessions != 1 {
		t.Fatalf("upgraded counts = accounts %d, ledgers %d, systems %d, sessions %d", accounts, userLedgers, systems, sessions)
	}
	var frozen bool
	var posted int64
	if err := pool.QueryRow(ctx, `
		SELECT a.credit_frozen, la.posted_balance_nano
		FROM accounts a JOIN ledger_accounts la ON la.identity_account_id = a.id
		WHERE a.id = $1`, disabledID).Scan(&frozen, &posted); err != nil || frozen || posted != 0 {
		t.Fatalf("disabled account ledger = frozen %v, posted %d, err %v", frozen, posted, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ledger_accounts (kind) VALUES ('platform_loss')`); err == nil {
		t.Fatal("system account without system_code unexpectedly inserted")
	}
}

func testConcurrentDebit(t *testing.T, ctx context.Context, identityService *identity.Service, service *ledger.Service, admin identity.Account, create func(string, string) identity.Account) {
	t.Helper()
	debtor := create("ledger.concurrent.debit", "5")
	one := create("ledger.concurrent.one", "0")
	two := create("ledger.concurrent.two", "0")
	results := make(chan error, 2)
	for index, target := range []identity.Account{one, two} {
		go func(index int, target identity.Account) {
			_, err := service.Transfer(ctx, fmt.Sprintf("concurrent-debit-%d", index), debtor.ID, target.ID, mustAmount(t, "4"), "concurrent", "test", fmt.Sprint(index))
			results <- err
		}(index, target)
	}
	var success, insufficient int
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ledger.ErrInsufficientFunds) {
			insufficient++
		} else {
			t.Fatalf("concurrent debit: %v", err)
		}
	}
	if success != 1 || insufficient != 1 {
		t.Fatalf("concurrent debit success/insufficient = %d/%d", success, insufficient)
	}
	_ = identityService
	_ = admin
}

func testConcurrentCreditChange(t *testing.T, ctx context.Context, identityService *identity.Service, service *ledger.Service, admin identity.Account, create func(string, string) identity.Account) {
	t.Helper()
	account := create("ledger.concurrent.credit", "5")
	zero := money.Amount(0)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var hold ledger.Hold
	var holdErr, updateErr error
	go func() {
		defer wait.Done()
		<-start
		hold, holdErr = service.CreateHold(ctx, ledger.CreateHoldRequest{IdempotencyKey: "concurrent-credit-hold", AccountID: account.ID, Amount: mustAmount(t, "5"), Purpose: ledger.HoldPurposeSpendAuthorization, FundingPolicy: ledger.HoldFundingCreditAllowed, Reason: "race", BusinessType: "api_request", BusinessID: "race"})
	}()
	go func() {
		defer wait.Done()
		<-start
		_, updateErr = identityService.UpdateAccount(ctx, admin, account.ID, identity.AccountUpdate{ExpectedVersion: account.Version, CreditLimit: &zero})
	}()
	close(start)
	wait.Wait()
	if updateErr != nil {
		t.Fatalf("concurrent credit update: %v", updateErr)
	}
	wallet, err := service.Wallet(ctx, account.ID)
	if err != nil {
		t.Fatalf("concurrent wallet: %v", err)
	}
	if holdErr == nil {
		if hold.ID == "" || wallet.SpendAuthorized != mustAmount(t, "5") || wallet.OverLimit {
			t.Fatalf("serialized hold-wins wallet = %+v, hold %+v", wallet, hold)
		}
		if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{IdempotencyKey: "concurrent-credit-release", HoldID: hold.ID, BusinessID: "concurrent-credit-release", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "cleanup test hold"}); err != nil {
			t.Fatalf("release concurrent hold: %v", err)
		}
	} else if !errors.Is(holdErr, ledger.ErrInsufficientFunds) {
		t.Fatalf("concurrent hold error = %v", holdErr)
	} else if wallet.SpendAuthorized != 0 || wallet.OverLimit {
		t.Fatalf("serialized limit-wins wallet = %+v", wallet)
	}
}

func testConcurrentHoldCapacity(t *testing.T, ctx context.Context, service *ledger.Service, create func(string, string) identity.Account) {
	t.Helper()
	account := create("ledger.concurrent.holds", "5")
	results := make(chan struct {
		hold ledger.Hold
		err  error
	}, 2)
	for index := range 2 {
		go func(index int) {
			hold, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
				IdempotencyKey: fmt.Sprintf("concurrent-hold-%d", index), AccountID: account.ID, Amount: mustAmount(t, "4"),
				Purpose: ledger.HoldPurposeSpendAuthorization, FundingPolicy: ledger.HoldFundingCreditAllowed,
				Reason: "competing authorization", BusinessType: "api_request", BusinessID: fmt.Sprintf("concurrent-hold-%d", index),
			})
			results <- struct {
				hold ledger.Hold
				err  error
			}{hold, err}
		}(index)
	}
	var winner ledger.Hold
	var success, insufficient int
	for range 2 {
		result := <-results
		if result.err == nil {
			success++
			winner = result.hold
		} else if errors.Is(result.err, ledger.ErrInsufficientFunds) {
			insufficient++
		} else {
			t.Fatalf("concurrent hold error: %v", result.err)
		}
	}
	if success != 1 || insufficient != 1 {
		t.Fatalf("concurrent hold success/insufficient = %d/%d", success, insufficient)
	}
	if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{
		IdempotencyKey: "concurrent-hold-cleanup", HoldID: winner.ID, BusinessID: "concurrent-hold-cleanup",
		Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "clean up competing authorization",
	}); err != nil {
		t.Fatalf("release concurrent hold winner: %v", err)
	}
}

func testConcurrentSameKeyReplay(t *testing.T, ctx context.Context, service *ledger.Service, create func(string, string) identity.Account) {
	t.Helper()
	account := create("ledger.concurrent.replay", "5")
	request := ledger.CreateHoldRequest{
		IdempotencyKey: "concurrent-same-key", AccountID: account.ID, Amount: mustAmount(t, "3"),
		Purpose: ledger.HoldPurposeSpendAuthorization, FundingPolicy: ledger.HoldFundingCreditAllowed,
		Reason: "same command", BusinessType: "api_request", BusinessID: "concurrent-same-key",
	}
	results := make(chan struct {
		hold ledger.Hold
		err  error
	}, 2)
	for range 2 {
		go func() {
			hold, err := service.CreateHold(ctx, request)
			results <- struct {
				hold ledger.Hold
				err  error
			}{hold, err}
		}()
	}
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.hold.ID == "" || first.hold.ID != second.hold.ID {
		t.Fatalf("concurrent replay = %+v / %+v", first, second)
	}
	wallet, err := service.Wallet(ctx, account.ID)
	if err != nil || wallet.SpendAuthorized != mustAmount(t, "3") {
		t.Fatalf("concurrent replay wallet = %+v, err %v", wallet, err)
	}
	if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{
		IdempotencyKey: "concurrent-same-key-release", HoldID: first.hold.ID, BusinessID: "concurrent-same-key-release",
		Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "release replay hold",
	}); err != nil {
		t.Fatalf("release replay hold: %v", err)
	}
}

func testConcurrentCaptureRelease(t *testing.T, ctx context.Context, service *ledger.Service, create func(string, string) identity.Account) {
	t.Helper()
	donor := create("ledger.capture.donor", "5")
	seller := create("ledger.capture.seller", "0")
	buyer := create("ledger.capture.buyer", "0")
	if _, err := service.Transfer(ctx, "capture-funding", donor.ID, seller.ID, mustAmount(t, "5"), "fund seller", "test", "capture-funding"); err != nil {
		t.Fatalf("fund seller: %v", err)
	}
	hold, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "capture-release-hold", AccountID: seller.ID, Amount: mustAmount(t, "5"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "capture release race", BusinessType: "c2c_order", BusinessID: "capture-release-race",
	})
	if err != nil {
		t.Fatalf("create capture/release hold: %v", err)
	}
	results := make(chan error, 2)
	go func() {
		_, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
			MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "race-capture", HoldID: hold.ID, BusinessID: "race-capture", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "capture wins"},
			Credits:           []ledger.Posting{{Account: ledger.UserAccount(buyer.ID), BusinessRole: ledger.EntryRoleBuyer, Amount: mustAmount(t, "5")}}, ReferenceType: "c2c_trade", ReferenceID: "race-capture",
		})
		results <- err
	}()
	go func() {
		_, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{
			IdempotencyKey: "race-release", HoldID: hold.ID, BusinessID: "race-release",
			Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "release wins",
		})
		results <- err
	}()
	var success, closed int
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ledger.ErrHoldClosed) || errors.Is(err, ledger.ErrConflict) {
			closed++
		} else {
			t.Fatalf("capture/release race error: %v", err)
		}
	}
	if success != 1 || closed != 1 {
		t.Fatalf("capture/release success/closed = %d/%d", success, closed)
	}
	sellerWallet, err := service.Wallet(ctx, seller.ID)
	if err != nil || sellerWallet.AssetReserved != 0 || (sellerWallet.PostedBalance != 0 && sellerWallet.PostedBalance != mustAmount(t, "5")) {
		t.Fatalf("capture/release seller wallet = %+v, err %v", sellerWallet, err)
	}
}

func testCaptureFailureRollback(t *testing.T, ctx context.Context, service *ledger.Service, create func(string, string) identity.Account) {
	t.Helper()
	donor := create("ledger.rollback.donor", "3")
	seller := create("ledger.rollback.seller", "0")
	buyer := create("ledger.rollback.buyer", "0")
	if _, err := service.Transfer(ctx, "rollback-funding", donor.ID, seller.ID, mustAmount(t, "3"), "fund rollback", "test", "rollback-funding"); err != nil {
		t.Fatalf("fund rollback seller: %v", err)
	}
	hold, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "rollback-hold", AccountID: seller.ID, Amount: mustAmount(t, "3"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "rollback hold", BusinessType: "c2c_order", BusinessID: "rollback-hold",
	})
	if err != nil {
		t.Fatalf("create rollback hold: %v", err)
	}
	request := ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "rollback-capture", HoldID: hold.ID, BusinessID: "rollback-capture", Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "rollback capture"},
		Credits:           []ledger.Posting{{Account: ledger.UserAccount("00000000-0000-0000-0000-000000000000"), BusinessRole: ledger.EntryRoleBuyer, Amount: mustAmount(t, "3")}}, ReferenceType: "c2c_trade", ReferenceID: "rollback-capture",
	}
	if _, err := service.CaptureHold(ctx, request); !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("failed capture error = %v", err)
	}
	wallet, err := service.Wallet(ctx, seller.ID)
	if err != nil || wallet.PostedBalance != mustAmount(t, "3") || wallet.AssetReserved != mustAmount(t, "3") {
		t.Fatalf("wallet after failed capture = %+v, err %v", wallet, err)
	}
	request.Credits = []ledger.Posting{{Account: ledger.UserAccount(buyer.ID), BusinessRole: ledger.EntryRoleBuyer, Amount: mustAmount(t, "3")}}
	result, err := service.CaptureHold(ctx, request)
	if err != nil || result.Hold.Status != "closed" {
		t.Fatalf("retry capture after rollback = %+v, err %v", result, err)
	}
}

func testTransactionBoundLedgerPrimitive(t *testing.T, ctx context.Context, store *storepg.Store, service *ledger.Service, create func(string, string) identity.Account) {
	t.Helper()
	account := create("ledger.transaction.bound", "2")
	rollbackMarker := errors.New("rollback business transaction")
	err := store.WithLedgerTransaction(ctx, func(tx *storepg.LedgerTransaction) error {
		transactionService := ledger.NewService(tx)
		if _, err := transactionService.CreateHold(ctx, ledger.CreateHoldRequest{
			IdempotencyKey: "transaction-bound-rollback", AccountID: account.ID, Amount: mustAmount(t, "1"),
			Purpose: ledger.HoldPurposeSpendAuthorization, FundingPolicy: ledger.HoldFundingCreditAllowed,
			Reason: "business transaction rollback", BusinessType: "test_business", BusinessID: "transaction-bound-rollback",
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_events (actor_account_id, action, target_type, target_id, reason)
			VALUES ($1::uuid, 'test.transaction_bound.rollback', 'account', $1::text, 'prove shared rollback')`, account.ID); err != nil {
			return err
		}
		return rollbackMarker
	})
	if !errors.Is(err, rollbackMarker) {
		t.Fatalf("transaction-bound rollback error = %v", err)
	}
	wallet, err := service.Wallet(ctx, account.ID)
	if err != nil || wallet.SpendAuthorized != 0 {
		t.Fatalf("wallet after shared rollback = %+v, err %v", wallet, err)
	}
	var rolledBackEvents int
	err = store.WithLedgerTransaction(ctx, func(tx *storepg.LedgerTransaction) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'test.transaction_bound.rollback' AND actor_account_id = $1`, account.ID).Scan(&rolledBackEvents)
	})
	if err != nil || rolledBackEvents != 0 {
		t.Fatalf("rolled-back business record count = %d, err %v", rolledBackEvents, err)
	}

	var committedHold ledger.Hold
	err = store.WithLedgerTransaction(ctx, func(tx *storepg.LedgerTransaction) error {
		transactionService := ledger.NewService(tx)
		var err error
		committedHold, err = transactionService.CreateHold(ctx, ledger.CreateHoldRequest{
			IdempotencyKey: "transaction-bound-commit", AccountID: account.ID, Amount: mustAmount(t, "1"),
			Purpose: ledger.HoldPurposeSpendAuthorization, FundingPolicy: ledger.HoldFundingCreditAllowed,
			Reason: "business transaction commit", BusinessType: "test_business", BusinessID: "transaction-bound-commit",
		})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO audit_events (actor_account_id, action, target_type, target_id, reason)
			VALUES ($1::uuid, 'test.transaction_bound.commit', 'account', $1::text, 'prove shared commit')`, account.ID)
		return err
	})
	if err != nil {
		t.Fatalf("transaction-bound commit: %v", err)
	}
	var committedEvents int
	err = store.WithLedgerTransaction(ctx, func(tx *storepg.LedgerTransaction) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'test.transaction_bound.commit' AND actor_account_id = $1`, account.ID).Scan(&committedEvents)
	})
	if err != nil || committedEvents != 1 {
		t.Fatalf("transaction-bound business record count = %d, err %v", committedEvents, err)
	}
	if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{
		IdempotencyKey: "transaction-bound-release", HoldID: committedHold.ID, BusinessID: "transaction-bound-release",
		Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "clean up committed transaction hold",
	}); err != nil {
		t.Fatalf("release transaction-bound hold: %v", err)
	}

	multiPayer := create("ledger.transaction.multi.payer", "3")
	multiFirst := create("ledger.transaction.multi.first", "0")
	multiSecond := create("ledger.transaction.multi.second", "0")
	err = store.WithLedgerTransaction(ctx, func(tx *storepg.LedgerTransaction) error {
		transactionService := ledger.NewService(tx)
		if _, err := transactionService.Transfer(ctx, "transaction-multi-first", multiPayer.ID, multiFirst.ID, mustAmount(t, "1"), "first transfer in one outer transaction", "test", "transaction-multi-first"); err != nil {
			return err
		}
		_, err := transactionService.Transfer(ctx, "transaction-multi-second", multiPayer.ID, multiSecond.ID, mustAmount(t, "1"), "second transfer in one outer transaction", "test", "transaction-multi-second")
		return err
	})
	if err != nil {
		t.Fatalf("multiple ledger transactions in one outer transaction: %v", err)
	}
	multiWallet, err := service.Wallet(ctx, multiPayer.ID)
	if err != nil || multiWallet.PostedBalance != mustAmount(t, "-2") {
		t.Fatalf("multiple transaction wallet = %+v, err %v", multiWallet, err)
	}

	poisoned := create("ledger.transaction.incomplete", "1")
	poisonTarget := create("ledger.tx.incomplete.target", "0")
	err = store.WithLedgerTransaction(ctx, func(tx *storepg.LedgerTransaction) error {
		transactionService := ledger.NewService(tx)
		if _, err := transactionService.Transfer(ctx, "transaction-incomplete", poisoned.ID, poisonTarget.ID, mustAmount(t, "2"), "intentionally exceed credit", "test", "transaction-incomplete"); !errors.Is(err, ledger.ErrInsufficientFunds) {
			return fmt.Errorf("unexpected incomplete command error: %w", err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO audit_events (actor_account_id, action, target_type, target_id, reason)
			VALUES ($1::uuid, 'test.transaction_bound.incomplete', 'account', $1::text, 'must roll back with incomplete ledger command')`, poisoned.ID)
		return err
	})
	if err == nil {
		t.Fatal("outer transaction with swallowed ledger failure unexpectedly committed")
	}
	var incompleteAuditEvents int
	err = store.WithLedgerTransaction(ctx, func(tx *storepg.LedgerTransaction) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'test.transaction_bound.incomplete' AND actor_account_id = $1`, poisoned.ID).Scan(&incompleteAuditEvents)
	})
	if err != nil || incompleteAuditEvents != 0 {
		t.Fatalf("incomplete command audit rows = %d, err %v", incompleteAuditEvents, err)
	}
	if _, err := service.Transfer(ctx, "transaction-incomplete", poisoned.ID, poisonTarget.ID, mustAmount(t, "1"), "valid retry after rollback", "test", "transaction-incomplete"); err != nil {
		t.Fatalf("retry after incomplete command rollback: %v", err)
	}

	err = store.WithLedgerTransaction(ctx, func(tx *storepg.LedgerTransaction) error {
		invalidHash := [32]byte{1}
		if _, err := tx.Post(ctx, ledger.PostRequest{
			IdempotencyKey: "forged-reversal", Kind: ledger.TransactionReversal, Reason: "forged reversal",
			ReferenceType: "test", ReferenceID: "forged-reversal",
			Entries: []ledger.Posting{
				{Account: ledger.UserAccount(multiPayer.ID), BusinessRole: ledger.EntryRoleReversal, Amount: mustAmount(t, "-1")},
				{Account: ledger.UserAccount(multiFirst.ID), BusinessRole: ledger.EntryRoleReversal, Amount: mustAmount(t, "1")},
			},
		}, invalidHash); !errors.Is(err, ledger.ErrInvalidInput) {
			return fmt.Errorf("forged reversal error = %w", err)
		}
		if _, err := tx.Post(ctx, ledger.PostRequest{
			IdempotencyKey: "forged-self-usage", Kind: ledger.TransactionSelfUsage, Reason: "forged self usage",
			ReferenceType: "test", ReferenceID: "forged-self-usage",
			Entries: []ledger.Posting{
				{Account: ledger.UserAccount(multiPayer.ID), BusinessRole: ledger.EntryRoleConsumer, Amount: mustAmount(t, "-1")},
				{Account: ledger.UserAccount(multiFirst.ID), BusinessRole: ledger.EntryRoleProvider, Amount: mustAmount(t, "1")},
			},
		}, invalidHash); !errors.Is(err, ledger.ErrInvalidInput) {
			return fmt.Errorf("forged self usage error = %w", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reject privileged direct posting shapes: %v", err)
	}
}

func testMinimumBalanceGuard(t *testing.T, ctx context.Context, service *ledger.Service, admin identity.Account, create func(string, string) identity.Account) {
	t.Helper()
	minimumCandidate := create("ledger.minimum.candidate", "9223372036.854775807")
	largeCounterparty := create("ledger.minimum.counterparty", "0")
	unitCounterparty := create("ledger.minimum.unit", "0")
	maximum := mustAmount(t, "9223372036.854775807")
	if _, err := service.Transfer(ctx, "minimum-max-debit", minimumCandidate.ID, largeCounterparty.ID, maximum, "reach negative maximum", "test", "minimum-max-debit"); err != nil {
		t.Fatalf("reach negative maximum: %v", err)
	}
	reversibleCredit, err := service.Transfer(ctx, "minimum-reversible-credit", largeCounterparty.ID, minimumCandidate.ID, mustAmount(t, "0.000000001"), "temporary one nano credit", "test", "minimum-reversible-credit")
	if err != nil {
		t.Fatalf("temporary one nano credit: %v", err)
	}
	if _, err := service.Transfer(ctx, "minimum-return-to-max", minimumCandidate.ID, unitCounterparty.ID, mustAmount(t, "0.000000001"), "return to negative maximum", "test", "minimum-return-to-max"); err != nil {
		t.Fatalf("return to negative maximum: %v", err)
	}
	if _, err := service.ReverseTransaction(ctx, admin, "minimum-forbidden-reversal", reversibleCredit.ID, "would create unrepresentable credit used", "minimum-forbidden-reversal"); !errors.Is(err, ledger.ErrAmountOverflow) {
		t.Fatalf("minimum balance reversal error = %v", err)
	}
	wallet, err := service.Wallet(ctx, minimumCandidate.ID)
	if err != nil || wallet.PostedBalance != -maximum || ledger.CreditUsed(wallet.PostedBalance) != maximum {
		t.Fatalf("minimum guard wallet = %+v, err %v", wallet, err)
	}
}

func decodeLedgerHTTP[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode HTTP response %q: %v", response.Body.String(), err)
	}
	return value
}

func findLedgerHTTPEntry(t *testing.T, entries []ledgerHTTPEntry, transactionID string) ledgerHTTPEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.TransactionID == transactionID {
			return entry
		}
	}
	t.Fatalf("transaction %s not found in HTTP entries: %+v", transactionID, entries)
	return ledgerHTTPEntry{}
}
