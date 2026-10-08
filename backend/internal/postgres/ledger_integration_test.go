package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
)

func TestBaselineMigrationCreatesTheRewrittenSchema(t *testing.T) {
	pool, _ := isolatedDatabase(t)
	ctx := context.Background()
	var tables int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' AND table_name <> 'goose_db_version'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 18 {
		t.Fatalf("baseline tables = %d, want 18", tables)
	}
	var systemAccounts, settingsRows, feeRate int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_accounts WHERE kind = 'system'`).Scan(&systemAccounts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*), max(fee_rate_nano) FROM settings`).Scan(&settingsRows, &feeRate); err != nil {
		t.Fatal(err)
	}
	if systemAccounts != 3 || settingsRows != 1 || feeRate != 1_000_000 {
		t.Fatalf("seed = %d system accounts, %d settings rows, fee %d", systemAccounts, settingsRows, feeRate)
	}
	var constraintTriggers int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
		WHERE c.relnamespace = current_schema()::regnamespace AND t.tgconstraint <> 0 AND t.tgname NOT LIKE 'RI_%'`).Scan(&constraintTriggers); err != nil {
		t.Fatal(err)
	}
	if constraintTriggers != 1 {
		t.Fatalf("constraint triggers = %d, want exactly the zero-sum trigger", constraintTriggers)
	}
}

func postInTx(t *testing.T, pool *pgxpool.Pool, transaction ledger.Transaction) (ledger.Posted, error) {
	t.Helper()
	var posted ledger.Posted
	err := pgkit.InTx(context.Background(), pool, func(tx pgx.Tx) error {
		var err error
		posted, err = ledgerpg.Post(context.Background(), tx, transaction)
		return err
	})
	return posted, err
}

func balanceOf(t *testing.T, pool *pgxpool.Pool, ref ledger.AccountRef) money.Amount {
	t.Helper()
	var balance int64
	var err error
	if ref.UserID != "" {
		err = pool.QueryRow(context.Background(), `SELECT balance_nano FROM ledger_accounts WHERE account_id = $1`, ref.UserID).Scan(&balance)
	} else {
		err = pool.QueryRow(context.Background(), `SELECT balance_nano FROM ledger_accounts WHERE system_code = $1`, string(ref.System)).Scan(&balance)
	}
	if err != nil {
		t.Fatal(err)
	}
	return money.FromNano(balance)
}

func isSQLState(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

func TestLedgerDatabaseInvariants(t *testing.T) {
	pool, store := isolatedDatabase(t)
	ctx := context.Background()
	_, _, members := accounts(t, store, "100", "0")
	consumer, provider := members[0].ID, members[1].ID

	t.Run("unbalanced transaction is rejected at commit", func(t *testing.T) {
		err := pgkit.InTx(ctx, pool, func(tx pgx.Tx) error {
			var txID, userLedger, revenueLedger string
			if err := tx.QueryRow(ctx, `INSERT INTO ledger_transactions (type, idempotency_key) VALUES ('admin_adjust', 'raw-unbalanced') RETURNING id`).Scan(&txID); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT id FROM ledger_accounts WHERE account_id = $1`, consumer).Scan(&userLedger); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT id FROM ledger_accounts WHERE system_code = 'platform_revenue'`).Scan(&revenueLedger); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO ledger_entries (transaction_id, ledger_account_id, amount_nano, balance_after_nano)
				VALUES ($1, $2, 5, 5), ($1, $3, -4, -4)`, txID, userLedger, revenueLedger)
			return err
		})
		if !isSQLState(err, "23514") {
			t.Fatalf("unbalanced commit error = %v, want check violation", err)
		}
		err = pgkit.InTx(ctx, pool, func(tx pgx.Tx) error {
			var txID string
			if err := tx.QueryRow(ctx, `INSERT INTO ledger_transactions (type, idempotency_key) VALUES ('admin_adjust', 'raw-single') RETURNING id`).Scan(&txID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO ledger_entries (transaction_id, ledger_account_id, amount_nano, balance_after_nano)
				SELECT $1, id, 7, 7 FROM ledger_accounts WHERE system_code = 'bad_debt'`, txID)
			return err
		})
		if !isSQLState(err, "23514") {
			t.Fatalf("single-entry commit error = %v, want check violation", err)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_transactions WHERE idempotency_key LIKE 'raw-%'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rejected transactions persisted: %d, %v", count, err)
		}
		if _, err := postInTx(t, pool, ledger.Transaction{Type: ledger.TypeAdminAdjust, IdempotencyKey: "go-unbalanced", Entries: []ledger.Line{
			{Account: ledger.User(consumer), Amount: 1}, {Account: ledger.System(ledger.SystemPlatformRevenue), Amount: -2},
		}}); !errors.Is(err, ledger.ErrUnbalanced) {
			t.Fatalf("Post unbalanced error = %v", err)
		}
	})

	var callTx ledger.Posted
	t.Run("epic example posts and is idempotent", func(t *testing.T) {
		related := &ledger.Related{Type: "account", ID: consumer}
		transaction := ledger.Transaction{
			Type: ledger.TypeAdminAdjust, IdempotencyKey: "epic-example", Related: related, Reason: "示例",
			Entries: []ledger.Line{
				{Account: ledger.User(consumer), Amount: mustAmount(t, "-30.03")},
				{Account: ledger.User(provider), Amount: mustAmount(t, "30")},
				{Account: ledger.System(ledger.SystemPlatformRevenue), Amount: mustAmount(t, "0.03")},
			},
		}
		first, err := postInTx(t, pool, transaction)
		if err != nil || first.Replayed || len(first.Entries) != 3 || first.Entries[0].BalanceAfter != mustAmount(t, "-30.03") {
			t.Fatalf("first post = %+v, %v", first, err)
		}
		second, err := postInTx(t, pool, transaction)
		if err != nil || !second.Replayed || second.ID != first.ID || len(second.Entries) != 3 {
			t.Fatalf("replay = %+v, %v", second, err)
		}
		if balanceOf(t, pool, ledger.User(consumer)) != mustAmount(t, "-30.03") || balanceOf(t, pool, ledger.User(provider)) != mustAmount(t, "30") {
			t.Fatal("replay booked twice")
		}
		changed := transaction
		changed.Type = ledger.TypeAPICall
		if _, err := postInTx(t, pool, changed); !errors.Is(err, ledger.ErrConflict) {
			t.Fatalf("key reuse error = %v", err)
		}
		balance, err := ledgerpg.Balance(ctx, pool, consumer)
		creditLimit, limitErr := ledgerpg.CreditLimit(ctx, pool, consumer)
		if err != nil || limitErr != nil || balance != mustAmount(t, "-30.03") || creditLimit != mustAmount(t, "100") {
			t.Fatalf("helpers = %s %s %v %v", balance, creditLimit, err, limitErr)
		}
		callTx = first
	})

	t.Run("entries and transactions are immutable", func(t *testing.T) {
		for _, statement := range []string{
			`UPDATE ledger_entries SET amount_nano = amount_nano WHERE transaction_id = $1`,
			`DELETE FROM ledger_entries WHERE transaction_id = $1`,
			`UPDATE ledger_transactions SET reason = 'changed' WHERE id = $1`,
			`DELETE FROM ledger_transactions WHERE id = $1`,
		} {
			if _, err := pool.Exec(ctx, statement, callTx.ID); !isSQLState(err, "23001") {
				t.Fatalf("%s error = %v, want restrict violation", statement, err)
			}
		}
	})

	t.Run("bad debt write-off moves the negative balance", func(t *testing.T) {
		posted, err := store.Ledger.WriteOff(ctx, ledger.WriteOff{ActorID: provider, AccountID: consumer, Reason: "无法收回", IdempotencyKey: "writeoff-1"})
		if err != nil || posted.Replayed {
			t.Fatalf("write-off = %+v, %v", posted, err)
		}
		if balanceOf(t, pool, ledger.User(consumer)) != 0 || balanceOf(t, pool, ledger.System(ledger.SystemBadDebt)) != mustAmount(t, "-30.03") {
			t.Fatal("write-off balances wrong")
		}
		replay, err := store.Ledger.WriteOff(ctx, ledger.WriteOff{ActorID: provider, AccountID: consumer, Reason: "无法收回", IdempotencyKey: "writeoff-1"})
		if err != nil || !replay.Replayed || replay.ID != posted.ID {
			t.Fatalf("write-off replay = %+v, %v", replay, err)
		}
		if _, err := store.Ledger.WriteOff(ctx, ledger.WriteOff{ActorID: provider, AccountID: consumer, Reason: "再次", IdempotencyKey: "writeoff-2"}); !errors.Is(err, ledger.ErrNothingToWriteOff) {
			t.Fatalf("second write-off error = %v", err)
		}
		var audits int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'ledger.write_off' AND target_id = $1`, consumer).Scan(&audits); err != nil || audits != 1 {
			t.Fatalf("write-off audit rows = %d, %v", audits, err)
		}
		entries, err := store.Ledger.ListEntries(ctx, ledger.EntryFilter{AccountID: consumer, Limit: 10})
		if err != nil || len(entries) != 2 || entries[0].Type != ledger.TypeBadDebtWriteOff || entries[0].BalanceAfter != 0 {
			t.Fatalf("entries = %+v, %v", entries, err)
		}
	})

	t.Run("admin adjustment books against platform revenue", func(t *testing.T) {
		posted, err := store.Ledger.Adjust(ctx, ledger.Adjustment{ActorID: provider, AccountID: consumer, Amount: mustAmount(t, "12.5"), Reason: "补偿", IdempotencyKey: "adjust-1"})
		if err != nil || posted.Replayed {
			t.Fatalf("adjust = %+v, %v", posted, err)
		}
		if balanceOf(t, pool, ledger.User(consumer)) != mustAmount(t, "12.5") {
			t.Fatal("adjust balance wrong")
		}
	})
	assertLedgerConsistent(t, pool)
}

func TestLedgerConcurrentPostingKeepsBalancesConsistent(t *testing.T) {
	pool, store := isolatedDatabase(t)
	ctx := context.Background()
	_, _, members := accounts(t, store, "1000", "1000", "1000", "1000")
	const workers, perWorker = 8, 25
	var group sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			random := rand.New(rand.NewSource(int64(worker)))
			for index := 0; index < perWorker; index++ {
				from := members[random.Intn(len(members))].ID
				to := members[random.Intn(len(members))].ID
				for to == from {
					to = members[random.Intn(len(members))].ID
				}
				cost := money.FromNano(int64(random.Intn(1_000_000_000) + 1))
				fee := money.FromNano(int64(random.Intn(1_000_000) + 1))
				_, err := postInTx(t, pool, ledger.Transaction{
					Type: ledger.TypeAPICall, IdempotencyKey: fmt.Sprintf("concurrent-%d-%d", worker, index),
					Entries: []ledger.Line{
						{Account: ledger.User(from), Amount: -(cost + fee)},
						{Account: ledger.User(to), Amount: cost},
						{Account: ledger.System(ledger.SystemPlatformRevenue), Amount: fee},
					},
				})
				errs <- err
			}
		}(worker)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent post: %v", err)
		}
	}
	var transactions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_transactions WHERE type = 'api_call'`).Scan(&transactions); err != nil || transactions != workers*perWorker {
		t.Fatalf("transactions = %d, %v", transactions, err)
	}
	assertLedgerConsistent(t, pool)
}

// assertLedgerConsistent checks the zero-sum identity, that every stored
// balance equals the sum of its entries, and that balance_after chains.
func assertLedgerConsistent(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var total string
	if err := pool.QueryRow(ctx, `SELECT coalesce(sum(balance_nano), 0)::text FROM ledger_accounts`).Scan(&total); err != nil || total != "0" {
		t.Fatalf("sum of balances = %s, %v", total, err)
	}
	var mismatched int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM ledger_accounts a
		LEFT JOIN (SELECT ledger_account_id, sum(amount_nano) AS total FROM ledger_entries GROUP BY ledger_account_id) e ON e.ledger_account_id = a.id
		WHERE a.balance_nano <> coalesce(e.total, 0)`).Scan(&mismatched); err != nil || mismatched != 0 {
		t.Fatalf("accounts whose balance differs from their entries = %d, %v", mismatched, err)
	}
	var broken int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT balance_after_nano, sum(amount_nano) OVER (PARTITION BY ledger_account_id ORDER BY id) AS running
			FROM ledger_entries
		) chain WHERE balance_after_nano <> running`).Scan(&broken); err != nil || broken != 0 {
		t.Fatalf("broken balance_after chain rows = %d, %v", broken, err)
	}
}
