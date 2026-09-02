package postgres_test

import (
	"context"
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
	if _, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "transfer-initial", AccountID: b.ID, Amount: mustAmount(t, "1"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "cross operation collision", BusinessType: "c2c_order", BusinessID: "collision",
	}); !errors.Is(err, ledger.ErrConflict) {
		t.Fatalf("cross-operation idempotency error = %v", err)
	}

	assetHold, err := service.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "hold-asset", AccountID: b.ID, Amount: mustAmount(t, "4"),
		Purpose: ledger.HoldPurposeAssetReservation, FundingPolicy: ledger.HoldFundingSettledBalanceOnly,
		Reason: "C2C sell order", BusinessType: "c2c_order", BusinessID: "sell-1",
	})
	if err != nil {
		t.Fatalf("asset hold: %v", err)
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

	creditCapture, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "capture-credit", HoldID: creditHold.ID, Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "2")}, Reason: "settle API"},
		Destination:       ledger.UserAccount(c.ID), ReferenceType: "api_usage", ReferenceID: "request-1",
	})
	if err != nil || creditCapture.Hold.Remaining != mustAmount(t, "1") {
		t.Fatalf("partial credit capture = %+v, err %v", creditCapture, err)
	}
	creditCaptureReplay, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "capture-credit", HoldID: creditHold.ID, Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "2")}, Reason: "settle API"},
		Destination:       ledger.UserAccount(c.ID), ReferenceType: "api_usage", ReferenceID: "request-1",
	})
	if err != nil || creditCaptureReplay.Transaction.ID != creditCapture.Transaction.ID {
		t.Fatalf("credit capture replay = %+v, err %v", creditCaptureReplay, err)
	}
	if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{IdempotencyKey: "release-credit", HoldID: creditHold.ID, Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "release unused"}); err != nil {
		t.Fatalf("release credit remainder: %v", err)
	}
	if _, err := service.CaptureHold(ctx, ledger.CaptureHoldRequest{
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "capture-asset", HoldID: assetHold.ID, Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "1")}, Reason: "C2C release"},
		Destination:       ledger.UserAccount(c.ID), ReferenceType: "c2c_order", ReferenceID: "sell-1",
	}); err != nil {
		t.Fatalf("capture asset hold: %v", err)
	}
	partialRelease, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{IdempotencyKey: "release-asset-partial", HoldID: assetHold.ID, Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "1")}, Reason: "reduce sell order"})
	if err != nil || partialRelease.Remaining != mustAmount(t, "2") {
		t.Fatalf("partial asset release = %+v, err %v", partialRelease, err)
	}
	if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{IdempotencyKey: "release-asset", HoldID: assetHold.ID, Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "cancel remainder"}); err != nil {
		t.Fatalf("release all asset remainder: %v", err)
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
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "capture-all", HoldID: allCaptureHold.ID, Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "capture all"},
		Destination:       ledger.UserAccount(c.ID), ReferenceType: "c2c_order", ReferenceID: "sell-all",
	})
	if err != nil || allCaptured.Hold.Status != "closed" || allCaptured.Hold.Remaining != 0 {
		t.Fatalf("all capture = %+v, err %v", allCaptured, err)
	}

	selfUsage, err := service.RecordSelfChannelUsage(ctx, "self-usage", a.ID, mustAmount(t, "1"), "usage", "self-1")
	if err != nil || len(selfUsage.Entries) != 2 || selfUsage.Entries[0].IdentityAccountID != a.ID || selfUsage.Entries[1].IdentityAccountID != a.ID {
		t.Fatalf("self usage = %+v, err %v", selfUsage, err)
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
		MutateHoldRequest: ledger.MutateHoldRequest{IdempotencyKey: "capture-after-freeze", HoldID: existingHold.ID, Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "1")}, Reason: "capture existing"},
		Destination:       ledger.UserAccount(c.ID), ReferenceType: "api_usage", ReferenceID: "request-freeze",
	}); err != nil {
		t.Fatalf("capture after freeze: %v", err)
	}
	if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{IdempotencyKey: "release-after-freeze", HoldID: existingHold.ID, Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "release existing"}); err != nil {
		t.Fatalf("release after freeze: %v", err)
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

	testConcurrentDebit(t, ctx, identityService, service, admin, create)
	testConcurrentCreditChange(t, ctx, identityService, service, admin, create)

	metrics, err := service.Metrics(ctx, admin)
	if err != nil || metrics.TotalPostedBalance != "0" || metrics.LossPostedBalance != "-3" || metrics.AssetReserved != "0" || metrics.SpendAuthorized != "0" {
		t.Fatalf("metrics = %+v, err %v", metrics, err)
	}
	if _, err := service.Metrics(ctx, a); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("ordinary metrics error = %v", err)
	}
	entries, err := service.Entries(ctx, a.ID, 0, 2)
	if err != nil || len(entries) != 2 || entries[0].ID <= entries[1].ID {
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
	var bLedgerID, rawTxID string
	if err := raw.QueryRow(ctx, `SELECT id::text FROM ledger_accounts WHERE identity_account_id = $1`, b.ID).Scan(&bLedgerID); err != nil {
		t.Fatalf("lookup ledger account: %v", err)
	}
	if _, err := raw.Exec(ctx, `INSERT INTO ledger_commands (idempotency_key, operation, payload_hash) VALUES ('raw-unbalanced', 'test', decode(repeat('00', 32), 'hex'))`); err != nil {
		t.Fatalf("insert raw command: %v", err)
	}
	if err := raw.QueryRow(ctx, `INSERT INTO ledger_transactions (idempotency_key, kind, reason, reference_type, reference_id) VALUES ('raw-unbalanced', 'transfer', 'invalid', 'test', 'raw') RETURNING id::text`).Scan(&rawTxID); err != nil {
		t.Fatalf("insert raw transaction: %v", err)
	}
	if _, err := raw.Exec(ctx, `INSERT INTO ledger_entries (transaction_id, ledger_account_id, entry_ordinal, amount_nano, posted_balance_before_nano, posted_balance_after_nano) VALUES ($1, $2, 1, 1, 0, 1)`, rawTxID, bLedgerID); err != nil {
		t.Fatalf("insert raw entry: %v", err)
	}
	if _, err := raw.Exec(ctx, `UPDATE ledger_transactions SET sealed = true WHERE id = $1`, rawTxID); err != nil {
		t.Fatalf("seal raw transaction: %v", err)
	}
	if err := raw.Commit(ctx); err == nil {
		t.Fatal("unbalanced raw transaction unexpectedly committed")
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
	if metricsResponse.Code != http.StatusOK || !strings.Contains(metricsResponse.Body.String(), `"zero_sum":true`) {
		t.Fatalf("administrator metrics API = %d %s", metricsResponse.Code, metricsResponse.Body.String())
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
		if hold.ID == "" || wallet.SpendAuthorized != mustAmount(t, "5") || !wallet.OverLimit {
			t.Fatalf("serialized hold-wins wallet = %+v, hold %+v", wallet, hold)
		}
		if _, err := service.ReleaseHold(ctx, ledger.MutateHoldRequest{IdempotencyKey: "concurrent-credit-release", HoldID: hold.ID, Amount: ledger.HoldAmount{Mode: ledger.HoldAmountAll}, Reason: "cleanup test hold"}); err != nil {
			t.Fatalf("release concurrent hold: %v", err)
		}
	} else if !errors.Is(holdErr, ledger.ErrInsufficientFunds) {
		t.Fatalf("concurrent hold error = %v", holdErr)
	} else if wallet.SpendAuthorized != 0 || wallet.OverLimit {
		t.Fatalf("serialized limit-wins wallet = %+v", wallet)
	}
}
