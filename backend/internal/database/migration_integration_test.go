package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func TestForumMigrationUpgradesExistingDatabaseWithoutChangingData(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	base, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := fmt.Sprintf("migration_%d", time.Now().UnixNano())
	if _, err := base.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := base.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	u, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var absent bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('forum_topics') IS NULL`).Scan(&absent); err != nil || !absent {
		t.Fatalf("forum must not exist at version 1: absent=%v err=%v", absent, err)
	}
	// Seed nonzero, balanced ledger data and an existing account before the upgrade.
	_, err = db.ExecContext(ctx, `BEGIN;
INSERT INTO accounts (id, username, display_name, password_hash) VALUES
 ('11111111-1111-4111-8111-111111111111', 'existing', 'Existing member', 'test-only-hash');
INSERT INTO ledger_accounts (account_id, kind, balance_nano) VALUES
 ('11111111-1111-4111-8111-111111111111', 'user', 7);
UPDATE ledger_accounts SET balance_nano = -7 WHERE system_code = 'platform_revenue';
INSERT INTO ledger_transactions (id, type, idempotency_key) VALUES
 ('22222222-2222-4222-8222-222222222222', 'admin_adjust', 'migration-fixture');
INSERT INTO ledger_entries (transaction_id, ledger_account_id, amount_nano, balance_after_nano)
 SELECT '22222222-2222-4222-8222-222222222222', id, balance_nano, balance_nano
 FROM ledger_accounts WHERE balance_nano <> 0;
COMMIT;`)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var value string
		err := db.QueryRowContext(ctx, `SELECT jsonb_build_object(
 'accounts', (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a),
 'ledger_accounts', (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM ledger_accounts a),
 'ledger_transactions', (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM ledger_transactions a),
 'ledger_entries', (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM ledger_entries a),
 'settings', (SELECT jsonb_agg(to_jsonb(a)) FROM settings a))::text`).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	for run := 0; run < 2; run++ {
		if err := Migrate(ctx, u.String()); err != nil {
			t.Fatalf("migration run %d: %v", run, err)
		}
		if after := snapshot(); after != before {
			t.Fatal("upgrade changed existing account, ledger or settings data")
		}
	}
	var tables, versions int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables
 WHERE table_schema = current_schema() AND table_name IN ('forum_boards','forum_topics','forum_replies','forum_attachments')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM goose_db_version WHERE version_id=2 AND is_applied`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if tables != 4 || versions != 1 {
		t.Fatalf("forum tables=%d, applied version 2 records=%d", tables, versions)
	}
}
