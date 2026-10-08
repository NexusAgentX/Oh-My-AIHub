package postgres_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestAdminDeleteModelPreservesHistoryAndRejectsChannelReferences(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	keyID, _ := e.defaultKey(consumer)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(sql string, want int) {
		t.Helper()
		var n int
		if err := e.pool.QueryRow(ctx, sql).Scan(&n); err != nil || n != want {
			t.Fatalf("%s: got %d want %d, %v", sql, n, want, err)
		}
	}
	exec(`INSERT INTO route_prefs(account_id, model_id) VALUES ($1, 'gpt-test')`, consumer.id)
	exec(`INSERT INTO route_prefs(account_id, api_key_id, model_id) VALUES ($1, $2, 'gpt-test')`, consumer.id, keyID)
	exec(`UPDATE api_keys SET allowed_models = ARRAY['gpt-test'], model_aliases = '{"alias":"gpt-test"}' WHERE id = $1`, keyID)
	exec(`INSERT INTO model_price_tiers(model_id, seq, name, min_prompt_tokens, timezone, input_price_nano_per_million, output_price_nano_per_million, cache_write_price_nano_per_million, cache_read_price_nano_per_million) VALUES ('gpt-test', 1, 'long', 100, 'UTC', 1, 1, 1, 1)`)
	callID, err := e.store.Gateway.InsertCall(ctx, gateway.CallInsert{AccountID: consumer.id, RequestedModel: "gpt-test", Format: channel.FormatOpenAIChat, Outcome: gateway.OutcomeInProgress})
	if err != nil {
		t.Fatal(err)
	}
	finish, err := e.store.Gateway.FinishCall(ctx, gateway.CallFinish{ID: callID, AccountID: consumer.id, Outcome: gateway.OutcomeSucceeded, ModelID: "gpt-test", Cost: money.FromNano(40_000_000), Bill: &gateway.Billing{ConsumerID: consumer.id, ProviderID: sharer.id, Cost: money.FromNano(40_000_000)}})
	if err != nil || finish.TxID == "" {
		t.Fatalf("finish: %+v %v", finish, err)
	}
	exec(`INSERT INTO channels(id, owner_id, name, base_url, api_key_ciphertext, api_key_nonce, api_key_key_id) VALUES ('00000000-0000-4000-8000-000000000188', $1, 'test', 'https://example.com', '\x01', '\x01', 'test')`, sharer.id)
	exec(`INSERT INTO channel_models(channel_id, model_id, upstream_model, formats) VALUES ('00000000-0000-4000-8000-000000000188', 'gpt-test', 'gpt-test', ARRAY['openai_chat'])`)
	e.apiJSON(consumer, http.StatusForbidden, http.MethodDelete, "/api/admin/models/gpt-test", nil)
	e.apiJSON(e.admin, http.StatusConflict, http.MethodDelete, "/api/admin/models/gpt-test", nil)
	count(`SELECT count(*) FROM route_prefs WHERE model_id = 'gpt-test'`, 2)
	count(`SELECT count(*) FROM model_price_tiers WHERE model_id = 'gpt-test'`, 1)
	count(`SELECT count(*) FROM audit_log WHERE action = 'model.deleted'`, 0)
	exec(`UPDATE channels SET deleted_at = now() WHERE id = '00000000-0000-4000-8000-000000000188'`)
	if r := e.api(e.admin, http.MethodDelete, "/api/admin/models/gpt-test", nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Code, r.Body.String())
	}
	for _, table := range []string{"models", "model_price_tiers", "route_prefs", "channel_models"} {
		column := "model_id"
		if table == "models" {
			column = "id"
		}
		count("SELECT count(*) FROM "+table+" WHERE "+column+" = 'gpt-test'", 0)
	}
	count(`SELECT count(*) FROM audit_log WHERE action = 'model.deleted' AND detail->'before'->>'display_name' = 'GPT-TEST'`, 1)
	count(`SELECT count(*) FROM calls WHERE model_id = 'gpt-test'`, 1)
	count(`SELECT count(*) FROM ledger_transactions WHERE related_type = 'call'`, 1)
	count(`SELECT count(*) FROM api_keys WHERE allowed_models = ARRAY['gpt-test'] AND model_aliases = '{"alias":"gpt-test"}'`, 1)
	e.apiJSON(e.admin, http.StatusNotFound, http.MethodDelete, "/api/admin/models/gpt-test", nil)
}
