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

func TestPricingCleanupUpgradePreservesHistoryAndRequiresSettledJobs(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("AIHUB_REQUIRE_TEST_DATABASE") != "" {
			t.Fatal("TEST_DATABASE_URL is required")
		}
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	base, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := fmt.Sprintf("pricing_cleanup_%d", time.Now().UnixNano())
	if _, err = base.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := base.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	target, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := target.Query()
	query.Set("search_path", schema)
	target.RawQuery = query.Encode()
	db, err := sql.Open("pgx", target.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 14); err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
 INSERT INTO accounts(id,username,display_name,password_hash) VALUES
 ('11111111-1111-4111-8111-111111111111','pricing-upgrade','Pricing upgrade','test-only-hash');
 INSERT INTO ledger_accounts(account_id,kind,balance_nano) VALUES
 ('11111111-1111-4111-8111-111111111111','user',7);
 UPDATE ledger_accounts SET balance_nano=-7 WHERE system_code='platform_revenue';
 INSERT INTO ledger_transactions(id,type,idempotency_key) VALUES
 ('22222222-2222-4222-8222-222222222222','admin_adjust','pricing-upgrade');
 INSERT INTO ledger_entries(transaction_id,ledger_account_id,amount_nano,balance_after_nano)
 SELECT '22222222-2222-4222-8222-222222222222',id,balance_nano,balance_nano FROM ledger_accounts WHERE balance_nano<>0;
 INSERT INTO channels(id,owner_id,name,base_url,api_key_ciphertext,api_key_nonce,api_key_key_id) VALUES
 ('33333333-3333-4333-8333-333333333333','11111111-1111-4111-8111-111111111111','Migration fixture','https://upstream.example','test-only-cipher','test-only-nonce','v1');
 INSERT INTO calls(id,account_id,format,outcome,price_snapshot) VALUES
 ('44444444-4444-4444-8444-444444444444','11111111-1111-4111-8111-111111111111','openai_responses','succeeded',
 '{"selected_prices":{"input":"1","output":"2","cache_write":"0","cache_read":"0","geo_multipliers":{"us":"1.1"}},"detail":{"thinking_mode":"qwen_thinking","thinking_mode_basis":"bailian_condition"}}'),
 ('55555555-5555-4555-8555-555555555555','11111111-1111-4111-8111-111111111111','openai_responses','in_progress','{}');
 INSERT INTO gateway_background_jobs(call_id,account_id,key_id,channel_id,response_id,metadata,deadline_at) VALUES
 ('55555555-5555-4555-8555-555555555555','11111111-1111-4111-8111-111111111111','66666666-6666-4666-8666-666666666666','33333333-3333-4333-8333-333333333333','resp_pending','{}',now()+interval '1 hour');
 INSERT INTO models(id,display_name,input_price_nano_per_million,output_price_nano_per_million,cache_write_price_nano_per_million,cache_read_price_nano_per_million,input_modalities,output_modalities,geo_multipliers) VALUES
 ('migration-model','Migration model',0,0,0,0,ARRAY['text','file','image'],ARRAY['file'],'{"us":1100000000}');
 INSERT INTO model_price_tiers(model_id,seq,name,service_tier,thinking_mode,min_prompt_tokens,input_price_nano_per_million,output_price_nano_per_million,cache_write_price_nano_per_million,cache_read_price_nano_per_million,geo_multipliers) VALUES
 ('migration-model',1,'legacy-priority','openai:priority','',NULL,3,4,0,0,'{"us":1200000000}'),
 ('migration-model',2,'thinking-only','','qwen_thinking',NULL,5,6,0,0,'{}'),
 ('migration-model',3,'thinking-and-tokens','','qwen_non_thinking',100,7,8,0,0,'{}'),
 ('migration-model',4,'anthropic','anthropic:priority','',NULL,9,10,0,0,'{}'),
 ('migration-model',5,'ultrafast','openai:ultrafast','',NULL,11,12,0,0,'{}');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err == nil || !strings.Contains(err.Error(), "finish pending background responses") {
		t.Fatalf("pending jobs did not block upgrade: %v", err)
	}
	var version int
	if err = db.QueryRowContext(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 14 {
		t.Fatalf("failed migration advanced version: %d %v", version, err)
	}
	var retiredColumns int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND ((table_name='models' AND column_name='geo_multipliers') OR (table_name='model_price_tiers' AND column_name IN ('thinking_mode','geo_multipliers')))`).Scan(&retiredColumns); err != nil || retiredColumns != 3 {
		t.Fatalf("failed migration changed schema: %d %v", retiredColumns, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE gateway_background_jobs SET settled_at=now(); UPDATE calls SET outcome='succeeded_unbilled',completed_at=now() WHERE id='55555555-5555-4555-8555-555555555555'`); err != nil {
		t.Fatal(err)
	}
	history := func() string {
		t.Helper()
		var value string
		err := db.QueryRowContext(ctx, `SELECT jsonb_build_object(
   'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a),
   'calls',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM calls a),
   'ledger_accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM ledger_accounts a),
   'ledger_transactions',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM ledger_transactions a),
   'ledger_entries',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM ledger_entries a),
   'jobs',(SELECT jsonb_agg(to_jsonb(a) ORDER BY call_id) FROM gateway_background_jobs a),
   'settings',(SELECT to_jsonb(a) FROM settings a))::text`).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := history()
	for range 2 {
		if _, err = provider.Up(ctx); err != nil {
			t.Fatal(err)
		}
		if after := history(); after != before {
			t.Fatal("upgrade or repeat changed historical calls, snapshots or ledger")
		}
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND ((table_name='models' AND column_name='geo_multipliers') OR (table_name='model_price_tiers' AND column_name IN ('thinking_mode','geo_multipliers')))`).Scan(&retiredColumns); err != nil || retiredColumns != 0 {
		t.Fatalf("retired columns remain: %d %v", retiredColumns, err)
	}
	var inputs, outputs, names, tiers string
	if err = db.QueryRowContext(ctx, `SELECT array_to_string(input_modalities,','),array_to_string(output_modalities,',') FROM models WHERE id='migration-model'`).Scan(&inputs, &outputs); err != nil || inputs != "text,image" || outputs != "text" {
		t.Fatalf("modalities=%s/%s %v", inputs, outputs, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT string_agg(name,',' ORDER BY seq),string_agg(service_tier,',' ORDER BY seq) FROM model_price_tiers WHERE model_id='migration-model'`).Scan(&names, &tiers); err != nil || names != "legacy-priority,anthropic,ultrafast" || tiers != "openai:fast,anthropic:priority,openai:ultrafast" {
		t.Fatalf("tiers=%s/%s %v", names, tiers, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE models SET input_modalities=ARRAY['file'] WHERE id='migration-model'`); err == nil {
		t.Fatal("database accepted removed file modality")
	}
	if _, err = db.ExecContext(ctx, `UPDATE model_price_tiers SET service_tier='openai:priority' WHERE model_id='migration-model'`); err == nil {
		t.Fatal("database accepted removed priority condition")
	}
}
