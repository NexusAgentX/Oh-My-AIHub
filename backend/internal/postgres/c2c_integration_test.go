package postgres_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestC2CIntegration(t *testing.T) {
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
	schema := "c2c_" + randomHex(t, 8)
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
	changedAdmin, err := identityService.ChangePassword(ctx, admin.ID, "Bootstrap-password-2026", "C2C-administrator-password-2026")
	if err != nil {
		t.Fatalf("ready administrator: %v", err)
	}
	admin = changedAdmin.Account
	createReady := func(username, credit string) identity.Account {
		t.Helper()
		created, err := identityService.CreateInvitedAccount(ctx, admin, username, username, mustAmount(t, credit), false, identity.StatusActive)
		if err != nil {
			t.Fatalf("create %s: %v", username, err)
		}
		changed, err := identityService.ChangePassword(ctx, created.Account.ID, created.InitialPassword, "C2C-member-password-2026-"+username)
		if err != nil {
			t.Fatalf("ready %s: %v", username, err)
		}
		return changed.Account
	}
	funder := createReady("c2c.funder", "200")
	seller := createReady("c2c.seller", "0")
	sellerTwo := createReady("c2c.seller.two", "0")
	sellerThree := createReady("c2c.seller.three", "0")
	restrictedSeller := createReady("c2c.seller.restricted", "0")
	buyerOne := createReady("c2c.buyer.one", "0")
	buyerTwo := createReady("c2c.buyer.two", "0")
	buyerThree := createReady("c2c.buyer.three", "0")
	restrictedBuyer := createReady("c2c.buyer.restricted", "0")
	ledgerService := ledger.NewService(store)
	for index, funded := range []struct {
		account identity.Account
		amount  string
	}{{seller, "30"}, {sellerTwo, "10"}, {sellerThree, "3"}, {restrictedSeller, "5"}} {
		if _, err := ledgerService.Transfer(ctx, fmt.Sprintf("fund-seller-%d", index), funder.ID, funded.account.ID, mustAmount(t, funded.amount), "fund C2C integration seller", "test_funding", fmt.Sprintf("seller-%d", index)); err != nil {
			t.Fatalf("fund seller %d: %v", index, err)
		}
	}
	keyring, err := c2c.ParseKeyring("test="+base64.StdEncoding.EncodeToString(make([]byte, 32)), "test")
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	service, err := c2c.NewServiceWithClock(store, keyring, func() time.Time { return now })
	if err != nil {
		t.Fatalf("C2C service: %v", err)
	}
	method := []c2c.PaymentMethodInput{{Type: c2c.PaymentWeChat, Contact: "wx-private", Instructions: "pay exact amount"}}
	var releasedTradeID, releasedEvidenceID, returnedTradeID string

	t.Run("sell parent hold, partial fills, idempotency, cancellation and privacy", func(t *testing.T) {
		forged := c2c.SanitizedImage{MIME: "image/png", Bytes: []byte("not really a PNG"), Width: 1, Height: 1}
		if _, err := service.CreateOrder(ctx, seller, "forged-image", c2c.SideSell, 100, mustAmount(t, "1"), mustAmount(t, "1"), mustAmount(t, "1"), []c2c.PaymentMethodInput{{Type: c2c.PaymentWeChat, QR: &forged}}); !errors.Is(err, c2c.ErrInvalidInput) {
			t.Fatalf("forged pre-sanitized image error = %v", err)
		}
		order, err := service.CreateOrder(ctx, seller, "sell-create", c2c.SideSell, 100, mustAmount(t, "10"), mustAmount(t, "2"), mustAmount(t, "6"), method)
		if err != nil {
			t.Fatalf("create sell: %v", err)
		}
		assertOrderAmounts(t, order, "10", "10", "0", "0", "0", c2c.OrderOpen)
		assertHold(t, pool, order.ParentHoldID, "10", "10", "0", "0")
		market, err := service.Market(ctx, buyerOne)
		if err != nil || len(market.SellOrders) != 1 || len(market.SellOrders[0].PaymentMethods) != 0 || len(market.SellOrders[0].PaymentTypes) != 1 {
			t.Fatalf("market privacy = %+v, %v", market, err)
		}
		potential, err := service.Order(ctx, buyerOne, order.ID)
		if err != nil || len(potential.PaymentMethods) != 1 || potential.PaymentMethods[0].Contact != "wx-private" {
			t.Fatalf("potential counterparty payment method = %+v, %v", potential.PaymentMethods, err)
		}
		trade, err := service.TakeOrder(ctx, buyerOne, "sell-take-first", order.ID, mustAmount(t, "4"), order.PaymentMethods[0].ID)
		if err != nil {
			t.Fatalf("take sell: %v", err)
		}
		replay, err := service.TakeOrder(ctx, buyerOne, "sell-take-first", order.ID, mustAmount(t, "4"), order.PaymentMethods[0].ID)
		if err != nil || replay.ID != trade.ID {
			t.Fatalf("take replay = %s, %v; want %s", replay.ID, err, trade.ID)
		}
		if _, err := service.TakeOrder(ctx, buyerOne, "sell-take-first", order.ID, mustAmount(t, "3"), order.PaymentMethods[0].ID); !errors.Is(err, c2c.ErrConflict) {
			t.Fatalf("changed replay error = %v", err)
		}

		type takeResult struct {
			actor identity.Account
			trade c2c.Trade
			err   error
		}
		results := make(chan takeResult, 2)
		var group sync.WaitGroup
		for index, actor := range []identity.Account{buyerTwo, buyerThree} {
			group.Add(1)
			go func(index int, actor identity.Account) {
				defer group.Done()
				created, err := service.TakeOrder(ctx, actor, fmt.Sprintf("sell-last-%d", index), order.ID, mustAmount(t, "6"), order.PaymentMethods[0].ID)
				results <- takeResult{actor: actor, trade: created, err: err}
			}(index, actor)
		}
		group.Wait()
		close(results)
		var last takeResult
		successes, conflicts := 0, 0
		for result := range results {
			if result.err == nil {
				last = result
				successes++
			} else if errors.Is(result.err, c2c.ErrConflict) {
				conflicts++
			} else {
				t.Fatalf("concurrent take error = %v", result.err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("concurrent last quantity successes/conflicts = %d/%d", successes, conflicts)
		}
		order, err = service.Order(ctx, seller, order.ID)
		if err != nil {
			t.Fatalf("reload sell: %v", err)
		}
		assertOrderAmounts(t, order, "10", "0", "10", "0", "0", c2c.OrderAllocated)
		assertHold(t, pool, order.ParentHoldID, "10", "10", "0", "0")

		screenshot := c2cIntegrationImage(t)
		if _, err := service.MarkPaid(ctx, buyerOne, "sell-paid", trade.ID, "wx-transaction-1", &screenshot); err != nil {
			t.Fatalf("mark paid: %v", err)
		}
		released, err := service.ConfirmReceipt(ctx, seller, "sell-release", trade.ID)
		if err != nil || released.Status != c2c.TradeReleasedToBuyer || released.LedgerTransactionID == "" {
			t.Fatalf("release = %+v, %v", released, err)
		}
		releasedTradeID = released.ID
		decrypted, err := service.Trade(ctx, buyerOne, released.ID)
		if err != nil || decrypted.PaymentReference != "wx-transaction-1" || len(decrypted.PaymentReferenceData.Ciphertext) != 0 || len(decrypted.Evidence) != 1 || len(decrypted.Evidence[0].Encrypted.Ciphertext) != 0 {
			t.Fatalf("decrypted payment reference = %q envelope %d, %v", decrypted.PaymentReference, len(decrypted.PaymentReferenceData.Ciphertext), err)
		}
		releasedEvidenceID = decrypted.Evidence[0].ID
		var encryptedReference []byte
		if err := pool.QueryRow(ctx, `SELECT payment_reference_ciphertext FROM c2c_trades WHERE id = $1`, released.ID).Scan(&encryptedReference); err != nil || string(encryptedReference) == "wx-transaction-1" {
			t.Fatalf("stored payment reference was not encrypted: %q, %v", encryptedReference, err)
		}
		cancelled, err := service.CancelOrder(ctx, seller, "sell-cancel-parent", order.ID)
		if err != nil || cancelled.Status != c2c.OrderCancelled {
			t.Fatalf("cancel parent = %+v, %v", cancelled, err)
		}
		closedTrade, err := service.CancelTrade(ctx, last.actor, "sell-cancel-child", last.trade.ID)
		if err != nil || closedTrade.Status != c2c.TradeCancelled {
			t.Fatalf("cancel allocated child = %+v, %v", closedTrade, err)
		}
		order, err = service.Order(ctx, seller, order.ID)
		if err != nil {
			t.Fatalf("reload final sell: %v", err)
		}
		assertOrderAmounts(t, order, "10", "0", "0", "4", "6", c2c.OrderCancelled)
		assertHold(t, pool, order.ParentHoldID, "10", "0", "4", "6")
		if _, err := service.Trade(ctx, buyerTwo, trade.ID); !errors.Is(err, c2c.ErrNotFound) {
			t.Fatalf("unrelated trade read error = %v", err)
		}
	})

	t.Run("buy child hold survives parent cancel and account restriction", func(t *testing.T) {
		buyOrder, err := service.CreateOrder(ctx, buyerOne, "buy-create", c2c.SideBuy, 99, mustAmount(t, "8"), mustAmount(t, "2"), mustAmount(t, "5"), []c2c.PaymentMethodInput{{Type: c2c.PaymentOther, Contact: "buyer-contact"}})
		if err != nil {
			t.Fatalf("create buy: %v", err)
		}
		trade, err := service.TakeOrder(ctx, seller, "buy-take", buyOrder.ID, mustAmount(t, "3"), buyOrder.PaymentMethods[0].ID)
		if err != nil {
			t.Fatalf("take buy: %v", err)
		}
		assertHold(t, pool, trade.HoldID, "3", "3", "0", "0")
		if _, err := service.CancelOrder(ctx, buyerOne, "buy-cancel-parent", buyOrder.ID); err != nil {
			t.Fatalf("cancel buy parent: %v", err)
		}
		if _, err := service.MarkPaid(ctx, buyerOne, "buy-paid", trade.ID, "", nil); err != nil {
			t.Fatalf("mark buy trade paid: %v", err)
		}
		disputeImage := c2cIntegrationImage(t)
		if _, err := service.OpenDispute(ctx, buyerOne, "buy-dispute", trade.ID, "seller payment details could not be verified", []c2c.SanitizedImage{disputeImage, disputeImage, disputeImage}); err != nil {
			t.Fatalf("open buy dispute: %v", err)
		}
		if _, err := service.AddDisputeEvidence(ctx, buyerOne, "buy-dispute-follow-up", trade.ID, "additional timeline supplied by the same participant", []c2c.SanitizedImage{disputeImage, disputeImage}); err != nil {
			t.Fatalf("append dispute statement: %v", err)
		}
		if _, err := service.AddDisputeEvidence(ctx, buyerOne, "buy-dispute-sixth-image", trade.ID, "attempted sixth image", []c2c.SanitizedImage{disputeImage}); !errors.Is(err, c2c.ErrInvalidInput) {
			t.Fatalf("sixth participant image error = %v", err)
		}
		if _, err := service.AddDisputeEvidence(ctx, buyerOne, "buy-dispute-too-long", trade.ID, strings.Repeat("x", 1_980), nil); err == nil {
			t.Fatal("cumulative 2,000-character statement limit unexpectedly accepted")
		}
		disputeView, err := service.Trade(ctx, admin, trade.ID)
		if err != nil || len(disputeView.Statements) != 2 || len(disputeView.Evidence) != 5 || disputeView.Statements[0].Text == "" || disputeView.Statements[1].Text == "" || len(disputeView.Statements[0].Encrypted.Ciphertext) != 0 || len(disputeView.Evidence[0].Encrypted.Ciphertext) != 0 {
			t.Fatalf("append-only decrypted dispute statements = %+v, %v", disputeView.Statements, err)
		}
		disabled := identity.StatusDisabled
		updatedSeller, err := identityService.UpdateAccount(ctx, admin, seller.ID, identity.AccountUpdate{ExpectedVersion: seller.Version, Status: &disabled})
		if err != nil || updatedSeller.Status != identity.StatusDisabled {
			t.Fatalf("disable seller = %+v, %v", updatedSeller, err)
		}
		returned, err := service.ResolveDispute(ctx, admin, "buy-return", trade.ID, c2c.ResolutionReturn, "seller prevailed after administrator review")
		if err != nil || returned.Status != c2c.TradeReturnedToSeller || returned.LedgerTransactionID != "" {
			t.Fatalf("admin return = %+v, %v", returned, err)
		}
		returnedTradeID = returned.ID
		assertHold(t, pool, trade.HoldID, "3", "0", "0", "3")
		buyOrder, err = service.Order(ctx, buyerOne, buyOrder.ID)
		if err != nil {
			t.Fatalf("reload cancelled buy: %v", err)
		}
		assertOrderAmounts(t, buyOrder, "8", "0", "0", "0", "8", c2c.OrderCancelled)

		otherBuy, err := service.CreateOrder(ctx, buyerTwo, "buy-create-disabled-test", c2c.SideBuy, 100, mustAmount(t, "2"), mustAmount(t, "1"), mustAmount(t, "2"), []c2c.PaymentMethodInput{{Type: c2c.PaymentOther, Contact: "buyer-two-contact"}})
		if err != nil {
			t.Fatalf("create second buy: %v", err)
		}
		if _, err := service.TakeOrder(ctx, seller, "disabled-seller-take", otherBuy.ID, mustAmount(t, "1"), otherBuy.PaymentMethods[0].ID); !errors.Is(err, c2c.ErrForbidden) {
			t.Fatalf("disabled seller new hold error = %v", err)
		}
	})

	t.Run("open and cancelled parent returns preserve exact hold quantities", func(t *testing.T) {
		openOrder, err := service.CreateOrder(ctx, sellerTwo, "open-return-create", c2c.SideSell, 100, mustAmount(t, "5"), mustAmount(t, "2"), mustAmount(t, "4"), method)
		if err != nil {
			t.Fatal(err)
		}
		first, err := service.TakeOrder(ctx, buyerOne, "open-return-first", openOrder.ID, mustAmount(t, "4"), openOrder.PaymentMethods[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		tail, err := service.TakeOrder(ctx, buyerTwo, "open-return-tail", openOrder.ID, mustAmount(t, "1"), openOrder.PaymentMethods[0].ID)
		if err != nil {
			t.Fatalf("take tail below minimum: %v", err)
		}
		if _, err := service.CancelTrade(ctx, buyerOne, "open-return-cancel-first", first.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := service.CancelTrade(ctx, buyerTwo, "open-return-cancel-tail", tail.ID); err != nil {
			t.Fatal(err)
		}
		openOrder, err = service.Order(ctx, sellerTwo, openOrder.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertOrderAmounts(t, openOrder, "5", "5", "0", "0", "0", c2c.OrderOpen)
		assertHold(t, pool, openOrder.ParentHoldID, "5", "5", "0", "0")
		if _, err := service.CancelOrder(ctx, sellerTwo, "open-return-close", openOrder.ID); err != nil {
			t.Fatal(err)
		}

		cancelledOrder, err := service.CreateOrder(ctx, sellerTwo, "cancelled-return-create", c2c.SideSell, 100, mustAmount(t, "5"), mustAmount(t, "2"), mustAmount(t, "4"), method)
		if err != nil {
			t.Fatal(err)
		}
		allocated, err := service.TakeOrder(ctx, buyerThree, "cancelled-return-take", cancelledOrder.ID, mustAmount(t, "2"), cancelledOrder.PaymentMethods[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		cancelledOrder, err = service.CancelOrder(ctx, sellerTwo, "cancelled-return-parent", cancelledOrder.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertOrderAmounts(t, cancelledOrder, "5", "0", "2", "0", "3", c2c.OrderCancelled)
		assertHold(t, pool, cancelledOrder.ParentHoldID, "5", "2", "0", "3")
		if _, err := service.CancelTrade(ctx, buyerThree, "cancelled-return-child", allocated.ID); err != nil {
			t.Fatal(err)
		}
		cancelledOrder, err = service.Order(ctx, sellerTwo, cancelledOrder.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertOrderAmounts(t, cancelledOrder, "5", "0", "0", "0", "5", c2c.OrderCancelled)
		assertHold(t, pool, cancelledOrder.ParentHoldID, "5", "0", "0", "5")

		buyOrder, err := service.CreateOrder(ctx, buyerThree, "open-buy-return-create", c2c.SideBuy, 100, mustAmount(t, "2"), mustAmount(t, "1"), mustAmount(t, "2"), []c2c.PaymentMethodInput{{Type: c2c.PaymentOther, Contact: "buyer-three-contact"}})
		if err != nil {
			t.Fatal(err)
		}
		buyTrade, err := service.TakeOrder(ctx, sellerTwo, "open-buy-return-take", buyOrder.ID, mustAmount(t, "1"), buyOrder.PaymentMethods[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.CancelTrade(ctx, buyerThree, "open-buy-return-cancel", buyTrade.ID); err != nil {
			t.Fatal(err)
		}
		buyOrder, err = service.Order(ctx, buyerThree, buyOrder.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertOrderAmounts(t, buyOrder, "2", "2", "0", "0", "0", c2c.OrderOpen)
		assertHold(t, pool, buyTrade.HoldID, "1", "0", "0", "1")
		if _, err := service.CancelOrder(ctx, buyerThree, "open-buy-return-close", buyOrder.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("administrator can close restricted accounts without reactivation", func(t *testing.T) {
		order, err := service.CreateOrder(ctx, restrictedSeller, "restricted-create", c2c.SideSell, 100, mustAmount(t, "2"), mustAmount(t, "1"), mustAmount(t, "2"), method)
		if err != nil {
			t.Fatalf("create restricted scenario: %v", err)
		}
		trade, err := service.TakeOrder(ctx, restrictedBuyer, "restricted-take", order.ID, mustAmount(t, "1"), order.PaymentMethods[0].ID)
		if err != nil {
			t.Fatalf("take restricted scenario: %v", err)
		}
		if _, err := service.MarkPaid(ctx, restrictedBuyer, "restricted-paid", trade.ID, "private-reference", nil); err != nil {
			t.Fatalf("mark restricted scenario paid: %v", err)
		}
		disabled := identity.StatusDisabled
		for _, account := range []identity.Account{restrictedSeller, restrictedBuyer} {
			if _, err := identityService.UpdateAccount(ctx, admin, account.ID, identity.AccountUpdate{ExpectedVersion: account.Version, Status: &disabled}); err != nil {
				t.Fatalf("disable %s: %v", account.Username, err)
			}
		}
		settled, err := service.ResolveDispute(ctx, admin, "restricted-direct-release", trade.ID, c2c.ResolutionRelease, "administrator verified the paid trade after both accounts were disabled")
		if err != nil || settled.Status != c2c.TradeReleasedToBuyer {
			t.Fatalf("direct paid administrator release = %+v, %v", settled, err)
		}
		cancelled, err := service.AdminCancelOrder(ctx, admin, "restricted-admin-cancel", order.ID, "administrator removed the disabled seller's remaining listing")
		if err != nil || cancelled.Status != c2c.OrderCancelled || cancelled.Closed != mustAmount(t, "1") || cancelled.Settled != mustAmount(t, "1") {
			t.Fatalf("administrator cancel restricted order = %+v, %v", cancelled, err)
		}
		assertHold(t, pool, order.ParentHoldID, "2", "0", "1", "1")
	})

	t.Run("terminal races create one hold effect", func(t *testing.T) {
		order, err := service.CreateOrder(ctx, sellerTwo, "race-sell-create", c2c.SideSell, 100, mustAmount(t, "4"), mustAmount(t, "1"), mustAmount(t, "2"), method)
		if err != nil {
			t.Fatalf("create race sell: %v", err)
		}
		trade, err := service.TakeOrder(ctx, buyerOne, "race-take", order.ID, mustAmount(t, "2"), order.PaymentMethods[0].ID)
		if err != nil {
			t.Fatalf("take race trade: %v", err)
		}
		if _, err := service.MarkPaid(ctx, buyerOne, "race-paid", trade.ID, "", nil); err != nil {
			t.Fatalf("mark race paid: %v", err)
		}
		errorsOut := make(chan error, 2)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			_, err := service.ConfirmReceipt(ctx, sellerTwo, "race-confirm", trade.ID)
			errorsOut <- err
		}()
		go func() {
			defer group.Done()
			_, err := service.OpenDispute(ctx, buyerOne, "race-dispute", trade.ID, "payment requires administrator review", nil)
			errorsOut <- err
		}()
		group.Wait()
		close(errorsOut)
		successes := 0
		for err := range errorsOut {
			if err == nil {
				successes++
			} else if !errors.Is(err, c2c.ErrConflict) {
				t.Fatalf("confirm/dispute race error = %v", err)
			}
		}
		if successes != 1 {
			t.Fatalf("confirm/dispute race successes = %d", successes)
		}
		final, err := service.Trade(ctx, admin, trade.ID)
		if err != nil {
			t.Fatalf("read race result: %v", err)
		}
		if final.Status == c2c.TradeDisputed {
			final, err = service.ResolveDispute(ctx, admin, "race-admin-release", trade.ID, c2c.ResolutionRelease, "administrator confirmed buyer payment")
			if err != nil {
				t.Fatalf("resolve raced dispute: %v", err)
			}
		}
		if final.Status != c2c.TradeReleasedToBuyer {
			t.Fatalf("race final status = %s", final.Status)
		}
		assertTradeEffects(t, pool, trade.ID, 1, 1)

		second, err := service.TakeOrder(ctx, buyerTwo, "race-second-take", order.ID, mustAmount(t, "2"), order.PaymentMethods[0].ID)
		if err != nil {
			t.Fatalf("take second race trade: %v", err)
		}
		if _, err := service.MarkPaid(ctx, buyerTwo, "race-second-paid", second.ID, "", nil); err != nil {
			t.Fatalf("mark second paid: %v", err)
		}
		if _, err := service.OpenDispute(ctx, buyerTwo, "race-second-dispute", second.ID, "administrator must choose terminal owner", nil); err != nil {
			t.Fatalf("open second dispute: %v", err)
		}
		resolveErrors := make(chan error, 2)
		group.Add(2)
		go func() {
			defer group.Done()
			_, err := service.ResolveDispute(ctx, admin, "race-resolution-release", second.ID, c2c.ResolutionRelease, "buyer evidence prevailed")
			resolveErrors <- err
		}()
		go func() {
			defer group.Done()
			_, err := service.ResolveDispute(ctx, admin, "race-resolution-return", second.ID, c2c.ResolutionReturn, "seller evidence prevailed")
			resolveErrors <- err
		}()
		group.Wait()
		close(resolveErrors)
		successes = 0
		for err := range resolveErrors {
			if err == nil {
				successes++
			} else if !errors.Is(err, c2c.ErrConflict) {
				t.Fatalf("admin resolution race error = %v", err)
			}
		}
		if successes != 1 {
			t.Fatalf("admin resolution race successes = %d", successes)
		}
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM c2c_trades WHERE id = $1`, second.ID).Scan(&status); err != nil {
			t.Fatalf("read admin race status: %v", err)
		}
		if status != string(c2c.TradeReleasedToBuyer) && status != string(c2c.TradeReturnedToSeller) {
			t.Fatalf("admin race status = %s", status)
		}
		var effects int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_hold_events WHERE hold_id = $1 AND business_id = $2`, second.HoldID, second.ID).Scan(&effects); err != nil || effects != 1 {
			if err != nil {
				t.Fatalf("admin race hold effects: %v", err)
			}
		}
		wantEffects := 1
		if status == string(c2c.TradeReturnedToSeller) {
			wantEffects = 0 // open sell parent restores allocation without releasing its hold
		}
		if effects != wantEffects {
			t.Fatalf("admin race hold effects = %d, want %d for %s", effects, wantEffects, status)
		}
	})

	t.Run("exact deadline expires and concurrent buy holds cannot oversubscribe", func(t *testing.T) {
		order, err := service.CreateOrder(ctx, sellerTwo, "deadline-create", c2c.SideSell, 100, mustAmount(t, "1"), mustAmount(t, "1"), mustAmount(t, "1"), method)
		if err != nil {
			t.Fatalf("create deadline order: %v", err)
		}
		trade, err := service.TakeOrder(ctx, buyerThree, "deadline-take", order.ID, mustAmount(t, "1"), order.PaymentMethods[0].ID)
		if err != nil {
			t.Fatalf("take deadline order: %v", err)
		}
		deadlineService, err := c2c.NewServiceWithClock(store, keyring, func() time.Time { return trade.PaymentDeadline })
		if err != nil {
			t.Fatalf("deadline service: %v", err)
		}
		if _, err := deadlineService.MarkPaid(ctx, buyerThree, "deadline-paid", trade.ID, "", nil); !errors.Is(err, c2c.ErrExpired) {
			t.Fatalf("exact deadline mark-paid error = %v", err)
		}
		final, err := deadlineService.Trade(ctx, buyerThree, trade.ID)
		if err != nil || final.Status != c2c.TradeExpired {
			t.Fatalf("deadline final = %+v, %v", final, err)
		}

		buyA, err := service.CreateOrder(ctx, buyerOne, "capacity-buy-a", c2c.SideBuy, 100, mustAmount(t, "2"), mustAmount(t, "2"), mustAmount(t, "2"), []c2c.PaymentMethodInput{{Type: c2c.PaymentOther, Contact: "capacity-a"}})
		if err != nil {
			t.Fatalf("create capacity buy A: %v", err)
		}
		buyB, err := service.CreateOrder(ctx, buyerTwo, "capacity-buy-b", c2c.SideBuy, 100, mustAmount(t, "2"), mustAmount(t, "2"), mustAmount(t, "2"), []c2c.PaymentMethodInput{{Type: c2c.PaymentOther, Contact: "capacity-b"}})
		if err != nil {
			t.Fatalf("create capacity buy B: %v", err)
		}
		capacityErrors := make(chan error, 2)
		var group sync.WaitGroup
		for index, buy := range []c2c.Order{buyA, buyB} {
			group.Add(1)
			go func(index int, buy c2c.Order) {
				defer group.Done()
				_, err := service.TakeOrder(ctx, sellerThree, fmt.Sprintf("capacity-take-%d", index), buy.ID, mustAmount(t, "2"), buy.PaymentMethods[0].ID)
				capacityErrors <- err
			}(index, buy)
		}
		group.Wait()
		close(capacityErrors)
		successes, insufficient := 0, 0
		for err := range capacityErrors {
			if err == nil {
				successes++
			} else if errors.Is(err, ledger.ErrInsufficientFunds) {
				insufficient++
			} else {
				t.Fatalf("capacity race error = %v", err)
			}
		}
		if successes != 1 || insufficient != 1 {
			t.Fatalf("capacity race successes/insufficient = %d/%d", successes, insufficient)
		}
	})

	t.Run("account administration races serialize without deadlock", func(t *testing.T) {
		createFundedSeller := func(username string) identity.Account {
			t.Helper()
			account := createReady(username, "0")
			if _, err := ledgerService.Transfer(
				ctx, "fund-"+username, funder.ID, account.ID, mustAmount(t, "2"),
				"fund account administration race", "test_funding", username,
			); err != nil {
				t.Fatalf("fund %s: %v", username, err)
			}
			return account
		}
		runRace := func(t *testing.T, left, right func(context.Context) error) (error, error) {
			t.Helper()
			raceContext, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			start := make(chan struct{})
			type raceResult struct {
				index int
				err   error
			}
			results := make(chan raceResult, 2)
			var group sync.WaitGroup
			for index, operation := range []func(context.Context) error{left, right} {
				group.Add(1)
				go func(index int, operation func(context.Context) error) {
					defer group.Done()
					<-start
					results <- raceResult{index: index, err: operation(raceContext)}
				}(index, operation)
			}
			close(start)
			group.Wait()
			close(results)
			values := make([]error, 2)
			count := 0
			for result := range results {
				values[result.index] = result.err
				count++
			}
			if count != 2 {
				t.Fatalf("race returned %d results", count)
			}
			for _, err := range values {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					t.Fatalf("account/C2C lock race timed out: %v", err)
				}
			}
			return values[0], values[1]
		}

		t.Run("disable order owner against take", func(t *testing.T) {
			seller := createFundedSeller("c2c.lock.take.seller")
			buyer := createReady("c2c.lock.take.buyer", "0")
			order, err := service.CreateOrder(ctx, seller, "lock-take-create", c2c.SideSell, 100, mustAmount(t, "2"), mustAmount(t, "1"), mustAmount(t, "1"), method)
			if err != nil {
				t.Fatal(err)
			}
			disabled := identity.StatusDisabled
			var trade c2c.Trade
			adminErr, takeErr := runRace(t,
				func(raceContext context.Context) error {
					_, err := identityService.UpdateAccount(raceContext, admin, seller.ID, identity.AccountUpdate{ExpectedVersion: seller.Version, Status: &disabled})
					return err
				},
				func(raceContext context.Context) error {
					var err error
					trade, err = service.TakeOrder(raceContext, buyer, "lock-take", order.ID, mustAmount(t, "1"), order.PaymentMethods[0].ID)
					return err
				},
			)
			if adminErr != nil {
				t.Fatalf("disable order owner: %v", adminErr)
			}
			if takeErr != nil && !errors.Is(takeErr, c2c.ErrConflict) && !errors.Is(takeErr, c2c.ErrForbidden) {
				t.Fatalf("take against disable: %v", takeErr)
			}
			if _, err := service.AdminCancelOrder(ctx, admin, "lock-take-admin-cancel", order.ID, "close disabled owner order after lock race"); err != nil {
				t.Fatalf("administrator cancel after disable/take: %v", err)
			}
			if takeErr == nil {
				future, err := c2c.NewServiceWithClock(store, keyring, func() time.Time { return trade.PaymentDeadline.Add(time.Second) })
				if err != nil {
					t.Fatal(err)
				}
				if expired, err := future.ExpireDue(ctx, 10); err != nil || expired < 1 {
					t.Fatalf("expire allocated trade after disable/take = %d, %v", expired, err)
				}
			}
		})

		t.Run("freeze buyer against cancel trade", func(t *testing.T) {
			seller := createFundedSeller("c2c.lock.cancel.seller")
			buyer := createReady("c2c.lock.cancel.buyer", "0")
			order, err := service.CreateOrder(ctx, seller, "lock-cancel-create", c2c.SideSell, 100, mustAmount(t, "1"), mustAmount(t, "1"), mustAmount(t, "1"), method)
			if err != nil {
				t.Fatal(err)
			}
			trade, err := service.TakeOrder(ctx, buyer, "lock-cancel-take", order.ID, mustAmount(t, "1"), order.PaymentMethods[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			frozen := true
			freezeErr, cancelErr := runRace(t,
				func(raceContext context.Context) error {
					_, err := identityService.UpdateAccount(raceContext, admin, buyer.ID, identity.AccountUpdate{ExpectedVersion: buyer.Version, CreditFrozen: &frozen})
					return err
				},
				func(raceContext context.Context) error {
					_, err := service.CancelTrade(raceContext, buyer, "lock-cancel", trade.ID)
					return err
				},
			)
			if freezeErr != nil || cancelErr != nil {
				t.Fatalf("freeze/cancel errors = %v / %v", freezeErr, cancelErr)
			}
			if _, err := service.CancelOrder(ctx, seller, "lock-cancel-order", order.ID); err != nil {
				t.Fatalf("close freeze/cancel order: %v", err)
			}
		})

		t.Run("disable seller against capture", func(t *testing.T) {
			seller := createFundedSeller("c2c.lock.capture.seller")
			buyer := createReady("c2c.lock.capture.buyer", "0")
			order, err := service.CreateOrder(ctx, seller, "lock-capture-create", c2c.SideSell, 100, mustAmount(t, "1"), mustAmount(t, "1"), mustAmount(t, "1"), method)
			if err != nil {
				t.Fatal(err)
			}
			trade, err := service.TakeOrder(ctx, buyer, "lock-capture-take", order.ID, mustAmount(t, "1"), order.PaymentMethods[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.MarkPaid(ctx, buyer, "lock-capture-paid", trade.ID, "", nil); err != nil {
				t.Fatal(err)
			}
			disabled := identity.StatusDisabled
			disableErr, captureErr := runRace(t,
				func(raceContext context.Context) error {
					_, err := identityService.UpdateAccount(raceContext, admin, seller.ID, identity.AccountUpdate{ExpectedVersion: seller.Version, Status: &disabled})
					return err
				},
				func(raceContext context.Context) error {
					_, err := service.ConfirmReceipt(raceContext, seller, "lock-capture", trade.ID)
					return err
				},
			)
			if disableErr != nil {
				t.Fatalf("disable seller: %v", disableErr)
			}
			if captureErr != nil && !errors.Is(captureErr, c2c.ErrForbidden) && !errors.Is(captureErr, c2c.ErrConflict) {
				t.Fatalf("capture against disable: %v", captureErr)
			}
			if captureErr != nil {
				if _, err := service.ResolveDispute(ctx, admin, "lock-capture-admin", trade.ID, c2c.ResolutionRelease, "administrator completed paid trade after account disable race"); err != nil {
					t.Fatalf("administrator capture after disable race: %v", err)
				}
			}
			assertTradeEffects(t, pool, trade.ID, 1, 1)
		})

		t.Run("freeze seller against release available hold", func(t *testing.T) {
			seller := createFundedSeller("c2c.lock.release.seller")
			order, err := service.CreateOrder(ctx, seller, "lock-release-create", c2c.SideSell, 100, mustAmount(t, "1"), mustAmount(t, "1"), mustAmount(t, "1"), method)
			if err != nil {
				t.Fatal(err)
			}
			frozen := true
			freezeErr, releaseErr := runRace(t,
				func(raceContext context.Context) error {
					_, err := identityService.UpdateAccount(raceContext, admin, seller.ID, identity.AccountUpdate{ExpectedVersion: seller.Version, CreditFrozen: &frozen})
					return err
				},
				func(raceContext context.Context) error {
					_, err := service.CancelOrder(raceContext, seller, "lock-release", order.ID)
					return err
				},
			)
			if freezeErr != nil || releaseErr != nil {
				t.Fatalf("freeze/release errors = %v / %v", freezeErr, releaseErr)
			}
			assertHold(t, pool, order.ParentHoldID, "1", "0", "0", "1")
		})
	})

	metrics, err := ledgerService.Metrics(ctx, admin)
	if err != nil {
		t.Fatalf("ledger metrics: %v", err)
	}
	if metrics.TotalPostedBalance != "0" || metrics.PostedProjectionDifference != "0" || metrics.AssetReservationDifference != "0" || metrics.SpendAuthorizationDifference != "0" || metrics.IncentivePostedBalance != "0" {
		t.Fatalf("ledger reconciliation after C2C = %+v", metrics)
	}
	if releasedTradeID == "" || releasedEvidenceID == "" || returnedTradeID == "" {
		t.Fatal("expected terminal trades for retention test")
	}
	futureService, err := c2c.NewServiceWithClock(store, keyring, func() time.Time { return now.Add(c2c.EvidenceRetention + time.Hour) })
	if err != nil {
		t.Fatalf("future C2C service: %v", err)
	}
	if cleaned, err := futureService.CleanupEvidence(ctx, 100); err != nil || cleaned < 3 {
		t.Fatalf("private evidence cleanup = %d, %v", cleaned, err)
	}
	var referenceCiphertext []byte
	var referenceDeleted *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(payment_reference_ciphertext, ''::bytea), payment_reference_deleted_at
		FROM c2c_trades WHERE id = $1`, releasedTradeID).Scan(&referenceCiphertext, &referenceDeleted); err != nil || len(referenceCiphertext) != 0 || referenceDeleted == nil {
		t.Fatalf("payment reference retention cleanup = bytes %d deleted %v, %v", len(referenceCiphertext), referenceDeleted, err)
	}
	var evidenceCiphertext []byte
	var evidenceDeleted *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(ciphertext, ''::bytea), deleted_at
		FROM c2c_evidence WHERE id = $1`, releasedEvidenceID).Scan(&evidenceCiphertext, &evidenceDeleted); err != nil || len(evidenceCiphertext) != 0 || evidenceDeleted == nil {
		t.Fatalf("evidence retention cleanup = bytes %d deleted %v, %v", len(evidenceCiphertext), evidenceDeleted, err)
	}
}

func c2cIntegrationImage(t *testing.T) c2c.SanitizedImage {
	t.Helper()
	pixels := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	pixels.Set(0, 0, color.NRGBA{R: 0x65, G: 0x6d, B: 0x76, A: 0xff})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, pixels); err != nil {
		t.Fatalf("encode C2C integration image: %v", err)
	}
	clean, err := c2c.SanitizeImage(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("sanitize C2C integration image: %v", err)
	}
	return clean
}

func assertOrderAmounts(t *testing.T, order c2c.Order, total, available, allocated, settled, closed string, status c2c.OrderStatus) {
	t.Helper()
	if order.Total != mustAmount(t, total) || order.Available != mustAmount(t, available) || order.Allocated != mustAmount(t, allocated) || order.Settled != mustAmount(t, settled) || order.Closed != mustAmount(t, closed) || order.Status != status {
		t.Fatalf("order amounts = total %s available %s allocated %s settled %s closed %s status %s", order.Total, order.Available, order.Allocated, order.Settled, order.Closed, order.Status)
	}
}

func assertHold(t *testing.T, pool *pgxpool.Pool, holdID, amount, remaining, captured, released string) {
	t.Helper()
	var gotAmount, gotRemaining, gotCaptured, gotReleased int64
	if err := pool.QueryRow(context.Background(), `
		SELECT amount_nano, remaining_nano, captured_nano, released_nano
		FROM ledger_holds WHERE id = $1`, holdID).Scan(&gotAmount, &gotRemaining, &gotCaptured, &gotReleased); err != nil {
		t.Fatalf("read hold %s: %v", holdID, err)
	}
	if gotAmount != mustAmount(t, amount).Nano() || gotRemaining != mustAmount(t, remaining).Nano() || gotCaptured != mustAmount(t, captured).Nano() || gotReleased != mustAmount(t, released).Nano() {
		t.Fatalf("hold %s = amount %d remaining %d captured %d released %d", holdID, gotAmount, gotRemaining, gotCaptured, gotReleased)
	}
}

func assertTradeEffects(t *testing.T, pool *pgxpool.Pool, tradeID string, holdEvents, ledgerTransactions int) {
	t.Helper()
	var effects, transactions int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM ledger_hold_events WHERE business_id = $1`, tradeID).Scan(&effects); err != nil {
		t.Fatalf("count hold effects: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM ledger_transactions WHERE reference_type = 'c2c_trade' AND reference_id = $1`, tradeID).Scan(&transactions); err != nil {
		t.Fatalf("count C2C ledger transactions: %v", err)
	}
	if effects != holdEvents || transactions != ledgerTransactions {
		t.Fatalf("trade effects = holds %d transactions %d, want %d/%d", effects, transactions, holdEvents, ledgerTransactions)
	}
}
