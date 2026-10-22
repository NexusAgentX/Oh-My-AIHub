package database

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/cutover"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/cutoverpg"
	"github.com/pressly/goose/v3"
)

func legacyCutoverDatabase(t *testing.T, version int64) (*sql.DB, string, *goose.Provider) {
	t.Helper()
	source := os.Getenv("TEST_DATABASE_URL")
	if source == "" {
		if os.Getenv("AIHUB_REQUIRE_TEST_DATABASE") != "" {
			t.Fatal("TEST_DATABASE_URL is required")
		}
		t.Skip("TEST_DATABASE_URL is not set")
	}
	base, err := sql.Open("pgx", source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	schema := fmt.Sprintf("market_cutover_%d", time.Now().UnixNano())
	if _, err = base.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := base.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	target, err := url.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	q := target.Query()
	q.Set("search_path", schema)
	target.RawQuery = q.Encode()
	db, err := sql.Open("pgx", target.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(context.Background(), version); err != nil {
		t.Fatal(err)
	}
	return db, target.String(), provider
}

func TestFinalCutoverRejectsPendingWorkAndPreservesHistoryOnUpgradeAndRetry(t *testing.T) {
	// The last documented operations baseline v0.18.2 ends at 0010; it is
	// not a claim about the current target. v0.21.0 ends at 0014. Also exercise
	// an integrated group schema, a retired background response and direct SQL
	// authorization enforcement.
	for _, version := range []int64{10, 14, 21} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			db, target, provider := legacyCutoverDatabase(t, version)
			ctx := context.Background()
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			const owner = "c0000000-0000-4000-8000-000000000001"
			const cid = "c0000000-0000-4000-8000-000000000002"
			const call = "c0000000-0000-4000-8000-000000000003"
			const key = "c0000000-0000-4000-8000-000000000004"
			ring, err := channel.ParseKeyring("old=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", "old")
			if err != nil {
				t.Fatal(err)
			}
			sealed, err := ring.Encrypt(cid, 1, "synthetic-old-channel-credential")
			if err != nil {
				t.Fatal(err)
			}
			sealedKey, err := ring.Encrypt(key, 1, "synthetic-old-platform-credential")
			if err != nil {
				t.Fatal(err)
			}
			exec("INSERT INTO accounts(id,username,display_name,password_hash) VALUES($1,'cutover-owner','Synthetic owner','synthetic-only')", owner)
			exec("INSERT INTO ledger_accounts(account_id,kind,balance_nano) VALUES($1,'user',7)", owner)
			exec("UPDATE ledger_accounts SET balance_nano=-7 WHERE system_code='platform_revenue'")
			exec("INSERT INTO ledger_transactions(id,type,idempotency_key) VALUES('c0000000-0000-4000-8000-000000000005','admin_adjust','synthetic-cutover')")
			exec("INSERT INTO ledger_entries(transaction_id,ledger_account_id,amount_nano,balance_after_nano) SELECT 'c0000000-0000-4000-8000-000000000005',id,balance_nano,balance_nano FROM ledger_accounts WHERE balance_nano<>0")
			exec("INSERT INTO models(id,display_name,input_price_nano_per_million,output_price_nano_per_million,cache_write_price_nano_per_million,cache_read_price_nano_per_million) VALUES('old-model','Old model',1,2,0,0)")
			exec("INSERT INTO channels(id,owner_id,name,base_url,api_key_ciphertext,api_key_nonce,api_key_key_id) VALUES($1,$2,'Historical channel','https://synthetic.example',$3,$4,$5)", cid, owner, sealed.Ciphertext, sealed.Nonce, sealed.KeyID)
			exec("INSERT INTO channel_models(channel_id,model_id,upstream_model,formats) VALUES($1,'old-model','native-old',ARRAY['openai_chat'])", cid)
			exec("INSERT INTO route_prefs(account_id,model_id,mode) VALUES($1,'old-model','fastest')", owner)
			exec("INSERT INTO route_pref_channels(route_pref_id,channel_id,position,excluded) SELECT id,$1,0,false FROM route_prefs WHERE account_id=$2", cid, owner)
			exec("INSERT INTO api_keys(id,owner_id,name,key_hash,prefix,key_ciphertext,key_nonce,key_key_id,allowed_models,model_aliases) VALUES($1,$2,'Historical key',decode(repeat('01',32),'hex'),'sk-aih-test',$3,$4,$5,ARRAY['old-model'],'{\"alias\":\"old-model\"}')", key, owner, sealedKey.Ciphertext, sealedKey.Nonce, sealedKey.KeyID)
			exec("INSERT INTO calls(id,account_id,api_key_id,requested_model,model_id,format,final_channel_id,outcome,price_snapshot,completed_at) VALUES($1,$2,$3,'old-model','old-model','openai_responses',$4,'upstream_failed','{\"legacy_price\":7}',now())", call, owner, key, cid)
			if version >= 14 {
				// An unsettled retired job must be abandoned, not block the upgrade.
				exec("INSERT INTO gateway_background_jobs(call_id,account_id,key_id,channel_id,response_id,metadata,deadline_at) VALUES($1,$2,$3,$4,'resp_abandoned','{}',now()+interval '1 hour')", call, owner, key, cid)
			}
			snapshot := func() string {
				t.Helper()
				var value string
				err := db.QueryRowContext(ctx, `SELECT jsonb_build_object(
     'versions',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM goose_db_version v),
     'calls',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM calls v),
     'channels',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM channels v),
     'models',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM models v),
     'keys',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM api_keys v))::text`).Scan(&value)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			exec("UPDATE calls SET outcome='in_progress' WHERE id=$1", call)
			pending := snapshot()
			if err := MigrateWithOptions(ctx, target, MigrationOptions{AllowLegacyConfigCleanup: true}); !errors.Is(err, cutover.ErrNotDrained) {
				t.Fatal("unfinished call did not block preflight", err)
			}
			if snapshot() != pending {
				t.Fatal("rejected preflight changed data or migration versions")
			}
			// End only this isolated synthetic call; no real request is interrupted.
			exec("UPDATE calls SET outcome='upstream_failed' WHERE id=$1", call)
			beforeAuth := snapshot()
			if err := Migrate(ctx, target); !errors.Is(err, ErrCleanupAuthorization) {
				t.Fatal("implicit cleanup was allowed", err)
			}
			if snapshot() != beforeAuth {
				t.Fatal("authorization rejection changed data")
			}
			if version == 21 {
				if _, err := provider.Up(ctx); err == nil {
					t.Fatal("direct SQL migration bypassed authorization")
				}
				if snapshot() != beforeAuth {
					t.Fatal("SQL authorization failure was not atomic")
				}
				exec(`CREATE FUNCTION synthetic_cutover_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic cutover failure'; END $$`)
				exec("CREATE TRIGGER synthetic_cutover_failure BEFORE UPDATE ON channels FOR EACH ROW EXECUTE FUNCTION synthetic_cutover_failure()")
				if err := MigrateWithOptions(ctx, target, MigrationOptions{AllowLegacyConfigCleanup: true}); err == nil {
					t.Fatal("injected transaction failure was ignored")
				}
				if snapshot() != beforeAuth {
					t.Fatal("transaction failure partially cleared config or advanced versions")
				}
				exec("DROP TRIGGER synthetic_cutover_failure ON channels")
				exec("DROP FUNCTION synthetic_cutover_failure()")
			}
			reader := cutoverpg.Store{DB: db}
			before, err := reader.Inspect(ctx, nil)
			if err != nil {
				t.Fatal("pre-upgrade evidence", err)
			}
			// The abandoned remainder must be visible in the operator's log.
			var migrationLog bytes.Buffer
			log.SetOutput(&migrationLog)
			err = MigrateWithOptions(ctx, target, MigrationOptions{AllowLegacyConfigCleanup: true})
			log.SetOutput(os.Stderr)
			if err != nil {
				t.Fatal(err)
			}
			if version >= 14 && !strings.Contains(migrationLog.String(), "abandoning 1 unsettled background response") {
				t.Fatal("upgrade log did not report the abandoned background response", migrationLog.String())
			}
			for range 2 {
				after, err := reader.Inspect(ctx, &before)
				if err != nil {
					t.Fatal("post-upgrade evidence", err)
				}
				differences, err := cutover.Compare(before, after)
				if err != nil || len(differences) != 0 {
					t.Fatal("protected history changed", differences, err)
				}
				if after.MigrationVersion != 23 {
					t.Fatal("wrong final migration", after.MigrationVersion)
				}
				if err := Migrate(ctx, target); err != nil {
					t.Fatal("repeated migration", err)
				}
			}
			var absent, deleted, jobsRetired bool
			if err := db.QueryRowContext(ctx, "SELECT to_regclass('models') IS NULL AND to_regclass('model_price_tiers') IS NULL AND to_regclass('channel_models') IS NULL AND to_regclass('suppliers') IS NULL AND to_regclass('supplier_endpoints') IS NULL").Scan(&absent); err != nil || !absent {
				t.Fatal("legacy tables survived", err)
			}
			if err := db.QueryRowContext(ctx, "SELECT to_regclass('gateway_background_jobs') IS NULL").Scan(&jobsRetired); err != nil || !jobsRetired {
				t.Fatal("retired background responses survived", err)
			}
			if err := db.QueryRowContext(ctx, "SELECT deleted_at IS NOT NULL AND status='unlisted' FROM channels WHERE id=$1", cid).Scan(&deleted); err != nil || !deleted {
				t.Fatal("legacy identity remained routable", err)
			}
			retained := channel.EncryptedCredential{Version: 1}
			if err := db.QueryRowContext(ctx, "SELECT api_key_ciphertext,api_key_nonce,api_key_key_id FROM channels WHERE id=$1", cid).Scan(&retained.Ciphertext, &retained.Nonce, &retained.KeyID); err != nil {
				t.Fatal(err)
			}
			if raw, err := ring.OpenCredential(cid, retained); err != nil || raw != "synthetic-old-channel-credential" {
				t.Fatal("historical credential AAD changed", err)
			}
			if err := db.QueryRowContext(ctx, "SELECT key_ciphertext,key_nonce,key_key_id FROM api_keys WHERE id=$1", key).Scan(&retained.Ciphertext, &retained.Nonce, &retained.KeyID); err != nil {
				t.Fatal(err)
			}
			if raw, err := ring.Decrypt(key, retained); err != nil || raw != "synthetic-old-platform-credential" {
				t.Fatal("platform Key AAD changed", err)
			}
			var count int
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM route_pref_channels").Scan(&count); err != nil || count != 0 {
				t.Fatal("legacy selections survived", err)
			}
		})
	}
}
