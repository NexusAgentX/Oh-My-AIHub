package postgres_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	storepg "github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type legacyBuyBook struct {
	pool  *pgxpool.Pool
	url   string
	buyer identity.Account
}

// seedLegacyBuyBook builds a schema at migration 0008 holding real ledger
// movements. Buy orders cannot be produced by the current services, so their
// rows are inserted directly with triggers suspended for the seeding statement.
func seedLegacyBuyBook(t *testing.T, scenario string) (context.Context, *legacyBuyBook) {
	t.Helper()
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
	schema := "c2c_sell_only_" + randomHex(t, 8)
	if _, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = basePool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)) })
	schemaURL := withSearchPath(t, databaseURL, schema)
	if err := database.MigrateTo(ctx, schemaURL, 8); err != nil {
		t.Fatalf("migrate to 0008: %v", err)
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
	createReady := func(username, credit string) identity.Account {
		created, err := identityService.CreateInvitedAccount(ctx, admin, username, username, mustAmount(t, credit), false, identity.StatusActive)
		if err != nil {
			t.Fatalf("create %s: %v", username, err)
		}
		changed, err := identityService.ChangePassword(ctx, created.Account.ID, created.InitialPassword, "Legacy-member-password-2026-"+username)
		if err != nil {
			t.Fatalf("ready %s: %v", username, err)
		}
		return changed.Account
	}
	funder := createReady("legacy.funder", "100")
	seller := createReady("legacy.seller", "0")
	buyer := createReady("legacy.buyer", "0")
	ledgerService := ledger.NewService(store)
	if _, err := ledgerService.Transfer(ctx, "legacy-fund", funder.ID, seller.ID, mustAmount(t, "10"), "fund legacy seller", "test_funding", "legacy"); err != nil {
		t.Fatalf("fund seller: %v", err)
	}

	var orderID, tradeID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text, gen_random_uuid()::text`).Scan(&orderID, &tradeID); err != nil {
		t.Fatalf("ids: %v", err)
	}
	status, available, allocated, closed := "cancelled", "0", "0", "5"
	var tradeStatus string
	switch scenario {
	case "open-order":
		status, available, closed = "open", "5", "0"
	case "unfinished-trade":
		allocated, closed, tradeStatus = "2", "3", "awaiting_payment"
	case "terminal":
		tradeStatus = "expired"
	}
	hold, err := ledgerService.CreateHold(ctx, ledger.CreateHoldRequest{
		IdempotencyKey: "legacy-trade-hold", AccountID: seller.ID, Amount: mustAmount(t, "2"),
		FundingPolicy: ledger.HoldFundingSettledBalanceOnly, Purpose: ledger.HoldPurposeAssetReservation,
		Reason: "legacy buy-order trade hold", BusinessType: "c2c_trade", BusinessID: tradeID,
	})
	if err != nil {
		t.Fatalf("legacy hold: %v", err)
	}
	if scenario == "terminal" {
		if _, err := ledgerService.ReleaseHold(ctx, ledger.MutateHoldRequest{
			IdempotencyKey: "legacy-trade-release", HoldID: hold.ID, BusinessID: tradeID,
			Amount: ledger.HoldAmount{Mode: ledger.HoldAmountExact, Amount: mustAmount(t, "2")}, Reason: "legacy expiry",
		}); err != nil {
			t.Fatalf("legacy release: %v", err)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin seed: %v", err)
	}
	defer tx.Rollback(ctx)
	statements := []string{
		`SET LOCAL session_replication_role = replica`,
		fmt.Sprintf(`INSERT INTO c2c_orders (id, owner_account_id, side, unit_price_fen, total_nano, available_nano, allocated_nano, settled_nano, closed_nano,
			minimum_nano, maximum_nano, status, parent_hold_id, cancelled_at)
			VALUES ('%s', '%s', 'buy', 100, 5000000000, %s, %s, 0, %s, 1000000000, 5000000000, '%s', NULL, %s)`,
			orderID, buyer.ID, nano(available), nano(allocated), nano(closed), status, map[bool]string{true: "now()", false: "NULL"}[status == "cancelled"]),
		fmt.Sprintf(`INSERT INTO c2c_payment_methods (id, order_id, method_type, position, key_id, nonce, ciphertext)
			VALUES ('%s', '%s', 'other', 1, 'test', decode('000000000000000000000000', 'hex'), decode('00000000000000000000000000000000000000', 'hex'))`, tradeID, orderID),
	}
	if tradeStatus != "" {
		resolved := "NULL"
		if tradeStatus == "expired" {
			resolved = "now()"
		}
		statements = append(statements, fmt.Sprintf(`INSERT INTO c2c_trades (id, order_id, buyer_account_id, seller_account_id, quantity_nano, unit_price_fen, fiat_amount_fen,
			status, hold_id, selected_payment_method_id, payment_deadline, created_at, resolved_at)
			VALUES ('%s', '%s', '%s', '%s', 2000000000, 100, 200, '%s', '%s', '%s', now() + interval '1 hour', now(), %s)`,
			tradeID, orderID, buyer.ID, seller.ID, tradeStatus, hold.ID, tradeID, resolved))
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement); err != nil {
			t.Fatalf("seed legacy buy order (%s): %v\n%s", scenario, err, statement)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	return ctx, &legacyBuyBook{pool: pool, url: schemaURL, buyer: buyer}
}

func nano(points string) string { return points + "000000000" }

type ledgerFingerprint struct {
	entries, transactions int64
	entrySum, balanceSum  int64
	reserved              int64
	holds                 string
}

func fingerprint(t *testing.T, ctx context.Context, pool *pgxpool.Pool) ledgerFingerprint {
	t.Helper()
	var f ledgerFingerprint
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM ledger_entries), (SELECT count(*) FROM ledger_transactions),
			(SELECT COALESCE(sum(amount_nano), 0) FROM ledger_entries),
			(SELECT COALESCE(sum(posted_balance_nano), 0) FROM ledger_accounts),
			(SELECT COALESCE(sum(asset_reserved_nano), 0) FROM ledger_accounts),
			(SELECT COALESCE(string_agg(id::text || ':' || remaining_nano || ':' || captured_nano || ':' || released_nano, ',' ORDER BY id), '') FROM ledger_holds)`,
	).Scan(&f.entries, &f.transactions, &f.entrySum, &f.balanceSum, &f.reserved, &f.holds); err != nil {
		t.Fatalf("ledger fingerprint: %v", err)
	}
	return f
}

func TestC2CSellOnlyMigrationRejectsUnfinishedBuyState(t *testing.T) {
	for _, scenario := range []string{"open-order", "unfinished-trade"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, book := seedLegacyBuyBook(t, scenario)
			before := fingerprint(t, ctx, book.pool)
			err := database.Migrate(ctx, book.url)
			if err == nil || !strings.Contains(err.Error(), "C2C buy orders are removed") {
				t.Fatalf("migration over unfinished buy state = %v", err)
			}
			var version int64
			if err := book.pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 8 {
				t.Fatalf("schema version after refused migration = %d, %v", version, err)
			}
			var sideCheck bool
			if err := book.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'c2c_orders_sell_only_check' AND conrelid = 'c2c_orders'::regclass)`).Scan(&sideCheck); err != nil || sideCheck {
				t.Fatalf("refused migration left partial changes: %v, %v", sideCheck, err)
			}
			if after := fingerprint(t, ctx, book.pool); after != before {
				t.Fatalf("refused migration changed ledger: %+v -> %+v", before, after)
			}
			var cancelled int
			if err := book.pool.QueryRow(ctx, `SELECT count(*) FROM c2c_orders WHERE side = 'buy' AND status IN ('open', 'allocated', 'cancelled')`).Scan(&cancelled); err != nil || cancelled != 1 {
				t.Fatalf("buy order was rewritten: %d, %v", cancelled, err)
			}
		})
	}
}

func TestC2CSellOnlyMigrationKeepsTerminalBuyHistory(t *testing.T) {
	ctx, book := seedLegacyBuyBook(t, "terminal")
	before := fingerprint(t, ctx, book.pool)
	if before.entrySum != 0 || before.balanceSum != 0 {
		t.Fatalf("seed ledger is not zero-sum: %+v", before)
	}
	if err := database.Migrate(ctx, book.url); err != nil {
		t.Fatalf("migration over terminal buy history: %v", err)
	}
	if after := fingerprint(t, ctx, book.pool); after != before {
		t.Fatalf("migration changed ledger: %+v -> %+v", before, after)
	}
	var orders, trades int
	if err := book.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM c2c_orders WHERE side = 'buy' AND status = 'cancelled'),
			(SELECT count(*) FROM c2c_trades t JOIN c2c_orders o ON o.id = t.order_id WHERE o.side = 'buy' AND t.status = 'expired')`).Scan(&orders, &trades); err != nil || orders != 1 || trades != 1 {
		t.Fatalf("terminal buy history = %d orders, %d trades, %v", orders, trades, err)
	}
	if _, err := book.pool.Exec(ctx, `
		INSERT INTO c2c_orders (id, owner_account_id, side, unit_price_fen, total_nano, available_nano, minimum_nano, maximum_nano, status)
		VALUES (gen_random_uuid(), $1, 'buy', 100, 1000000000, 1000000000, 1000000000, 1000000000, 'open')`, book.buyer.ID); err == nil || !strings.Contains(err.Error(), "c2c_orders_sell_only_check") {
		t.Fatalf("new buy order accepted after migration: %v", err)
	}
	if err := database.Migrate(ctx, book.url); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}
