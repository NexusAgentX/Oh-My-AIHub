package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func TestForumMigrationUpgradesExistingDatabaseWithoutChangingData(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("AIHUB_REQUIRE_TEST_DATABASE") != "" {
			t.Fatal("TEST_DATABASE_URL is not set but AIHUB_REQUIRE_TEST_DATABASE demands a database")
		}
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
UPDATE settings SET default_total_timeout_ms = 123000;
COMMIT;`)
	if err != nil {
		t.Fatal(err)
	}
	// Upgrade the fixture to the last released schema, then exercise the real
	// populated 0016 -> 0017 transition rather than only an empty installation.
	if _, err := provider.UpTo(ctx, 16); err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO models (id,display_name,input_price_nano_per_million,output_price_nano_per_million,cache_write_price_nano_per_million,cache_read_price_nano_per_million,input_modalities,output_modalities,geo_multipliers)
 SELECT name,name,1,2,0,0,ARRAY['text','file'],ARRAY['file'],'{"us":1100000000}'::jsonb FROM unnest(ARRAY['migration-model','migration-manual']) name;
 INSERT INTO model_price_tiers (model_id,seq,service_tier,thinking_mode,input_price_nano_per_million,output_price_nano_per_million,cache_write_price_nano_per_million,cache_read_price_nano_per_million,geo_multipliers)
 VALUES ('migration-model',1,'openai:priority','',1,2,0,0,'{"us":1100000000}'),('migration-model',2,'anthropic:priority','',3,4,0,0,'{}'),('migration-model',3,'','qwen_thinking',5,6,0,0,'{}');
 INSERT INTO channels (id,owner_id,name,base_url,api_key_ciphertext,api_key_nonce,api_key_key_id,capabilities)
 VALUES ('33333333-3333-4333-8333-333333333333','11111111-1111-4111-8111-111111111111','migration','https://migration.example','\x00','\x00','v1',ARRAY['codex_backend','codex_search']);
 INSERT INTO channel_models (channel_id,model_id,upstream_model,formats,capabilities)
 VALUES ('33333333-3333-4333-8333-333333333333','migration-model','upstream',ARRAY['openai_responses','anthropic'],ARRAY['web_search','priority']);
 INSERT INTO route_prefs (account_id,model_id,mode) VALUES ('11111111-1111-4111-8111-111111111111','migration-model','cheapest'),('11111111-1111-4111-8111-111111111111','migration-manual','manual');
 INSERT INTO route_pref_channels (route_pref_id,channel_id,position) SELECT id,'33333333-3333-4333-8333-333333333333',0 FROM route_prefs;
 INSERT INTO calls (account_id,requested_model,format,outcome,price_snapshot) VALUES ('11111111-1111-4111-8111-111111111111','migration-model','openai_chat','succeeded_unbilled','{"selected_prices":{"geo_multipliers":{"us":"1.1"}}}');`)
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
 'settings', (SELECT jsonb_agg(to_jsonb(a) - ARRAY['usd_cny_rate_nano', 'usd_cny_rate_source', 'usd_cny_rate_updated_at',
   'fx_auto_sync', 'fx_sync_minute', 'fx_sync_attempted_at', 'fx_sync_error', 'account_max_inflight_calls', 'default_total_timeout_ms', 'default_request_timeout_ms', 'default_idle_timeout_ms', 'codex']) FROM settings a))::text`).Scan(&value)
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
	var routesReset, pricesClean, matricesMoved, historyIntact bool
	if err := db.QueryRowContext(ctx, `SELECT
 (SELECT count(*)=2 AND bool_and(mode='manual') FROM route_prefs) AND NOT EXISTS(SELECT 1 FROM route_pref_channels),
 (SELECT bool_and(geo_multipliers='{}' AND NOT ('file'=ANY(input_modalities)) AND NOT ('file'=ANY(output_modalities))) FROM models) AND (SELECT count(*)=1 AND bool_and(service_tier='openai:fast' AND thinking_mode='' AND geo_multipliers='{}') FROM model_price_tiers),
 (SELECT capabilities @> ARRAY['responses/inference','codex/inference','codex/search'] FROM channels LIMIT 1) AND (SELECT capabilities @> ARRAY['responses/web_search','codex/fast'] FROM channel_models LIMIT 1) AND NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='channel_models' AND column_name='formats'),
 (SELECT format='openai_chat' AND price_snapshot->'selected_prices'->'geo_multipliers'->>'us'='1.1' FROM calls WHERE requested_model='migration-model' LIMIT 1)`).Scan(&routesReset, &pricesClean, &matricesMoved, &historyIntact); err != nil || !routesReset || !pricesClean || !matricesMoved || !historyIntact {
		t.Fatalf("0017 transition routes=%v prices=%v matrix=%v history=%v err=%v", routesReset, pricesClean, matricesMoved, historyIntact, err)
	}
	// 0016 adds disabled Codex documents without changing existing settings.
	var codexEmpty bool
	if err := db.QueryRowContext(ctx, `SELECT codex = '{}'::jsonb FROM settings`).Scan(&codexEmpty); err != nil || !codexEmpty {
		t.Fatalf("Codex compatibility must start unconfigured: empty=%v err=%v", codexEmpty, err)
	}
	// 0003 adds the exchange rate with a manual 6.8 default and keeps every
	// existing model a CNY (points) model.
	var rate int64
	var source string
	if err := db.QueryRowContext(ctx, `SELECT usd_cny_rate_nano, usd_cny_rate_source FROM settings`).Scan(&rate, &source); err != nil || rate != 6_800_000_000 || source != "manual" {
		t.Fatalf("exchange rate after upgrade = %d %q %v", rate, source, err)
	}
	// 0006 adds the per-account in-flight cap with a default of 20 and lets a
	// call be recorded as refused for exceeding it.
	var inflightCap int
	if err := db.QueryRowContext(ctx, `SELECT account_max_inflight_calls FROM settings`).Scan(&inflightCap); err != nil || inflightCap != 20 {
		t.Fatalf("in-flight cap after upgrade = %d %v", inflightCap, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE settings SET account_max_inflight_calls = 1001`); err == nil {
		t.Fatal("a cap above 1000 must be rejected by the database")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO calls (account_id, format, outcome) VALUES
 ('11111111-1111-4111-8111-111111111111', 'openai_chat', 'rejected_inflight')`); err != nil {
		t.Fatalf("recording a rejected_inflight call: %v", err)
	}
	// 0007 lets a channel event record a 4xx anomaly.
	var eventKinds string
	if err := db.QueryRowContext(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'channel_events_kind_check'`).Scan(&eventKinds); err != nil ||
		!strings.Contains(eventKinds, "anomalous_client_errors") || !strings.Contains(eventKinds, "model_pending_multiplier") {
		t.Fatalf("channel event kinds after upgrade = %q %v", eventKinds, err)
	}
	// 0008 keeps the old combined timeout in both new stages and removes
	// the cross-stage comparison: a short idle wait can follow a long TTFT.
	var requestTimeout, idleTimeout int
	if err := db.QueryRowContext(ctx, `SELECT default_request_timeout_ms, default_idle_timeout_ms FROM settings`).Scan(&requestTimeout, &idleTimeout); err != nil || requestTimeout != 123000 || idleTimeout != 123000 {
		t.Fatalf("split timeouts = %d/%d, %v", requestTimeout, idleTimeout, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE settings SET default_request_timeout_ms=1000, default_idle_timeout_ms=1000`); err != nil {
		t.Fatalf("independent timeout stages rejected: %v", err)
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
