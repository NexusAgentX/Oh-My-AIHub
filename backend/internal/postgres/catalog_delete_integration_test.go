package postgres_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestRetiredGlobalModelManagementCannotRemoveHistory(t *testing.T) {
	e := newGroupEnv(t)
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
	callID, err := e.store.Gateway.InsertCall(ctx, gateway.CallInsert{AccountID: consumer.id, RequestedModel: "gpt-test", Format: channel.FormatOpenAIChat, Outcome: gateway.OutcomeInProgress})
	if err != nil {
		t.Fatal(err)
	}
	finish, err := e.store.Gateway.FinishCall(ctx, gateway.CallFinish{ID: callID, AccountID: consumer.id, Outcome: gateway.OutcomeSucceeded, ModelID: "gpt-test", Cost: money.FromNano(40_000_000), Bill: &gateway.Billing{ConsumerID: consumer.id, ProviderID: sharer.id, Cost: money.FromNano(40_000_000)}})
	if err != nil || finish.TxID == "" {
		t.Fatalf("finish: %+v %v", finish, err)
	}
	for _, actor := range []person{consumer, e.admin} {
		request := httptest.NewRequest(http.MethodDelete, "http://example.com/api/admin/models/gpt-test", nil)
		request.Header.Set("Origin", "http://example.com")
		request.AddCookie(&http.Cookie{Name: "oma_session", Value: actor.token})
		recorder := httptest.NewRecorder()
		e.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("retired management endpoint returned %d", recorder.Code)
		}
	}
	count(`SELECT count(*) FROM route_prefs WHERE model_id='gpt-test'`, 2)
	count(`SELECT count(*) FROM audit_log WHERE action='model.deleted'`, 0)
	count(`SELECT count(*) FROM calls WHERE model_id='gpt-test'`, 1)
	count(`SELECT count(*) FROM ledger_transactions WHERE related_type='call'`, 1)
	count(`SELECT count(*) FROM api_keys WHERE allowed_models=ARRAY['gpt-test'] AND model_aliases='{"alias":"gpt-test"}'`, 1)
	e.assertZeroSum()

}
