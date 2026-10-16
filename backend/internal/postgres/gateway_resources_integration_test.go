package postgres_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestStoredResourceRegistrySurvivesRestartAndCredentialRotation(t *testing.T) {
	e := newEnv(t)
	e.engine.Resources = e.store.Gateway
	e.engineDeps.Resources = e.store.Gateway
	consumer, other, sharer := e.member("consumer", "100"), e.member("other", "100"), e.member("sharer", "0")
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if r.Header.Get("Authorization") != "Bearer original-secret" {
			t.Error("resource credential was not pinned")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_resource","status":"completed","output":[{"id":"msg_resource","type":"message","content":[]}],"usage":{"input_tokens":100,"output_tokens":20}}`)
	})
	channelID := e.createChannel(sharer, channelSpec{name: "stored-resource", upstream: upstream, key: "original-secret"})
	_, secret := e.defaultKey(consumer)
	_, foreign := e.defaultKey(other)
	created := e.call(secret, http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-test","store":true,"input":"private input"}`), nil)
	if created.Code != 200 {
		t.Fatalf("%d %s", created.Code, created.Body.String())
	}
	var raw []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT metadata FROM gateway_resources WHERE kind='response' AND resource_id='resp_resource'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "original-secret") || strings.Contains(string(raw), "private input") {
		t.Fatal("plaintext persisted")
	}
	e.apiJSON(sharer, 200, http.MethodPatch, "/api/channels/"+channelID, map[string]any{"api_key": "rotated-secret"})
	e.restartGateway()
	if err := e.store.Gateway.VerifyResourceCredentials(context.Background(), e.keyring); err != nil {
		t.Fatal(err)
	}
	before := e.callRow(created.Header().Get("X-AIHub-Request-Id"))
	if out := e.call(foreign, http.MethodGet, "/v1/responses/resp_resource", nil, nil); out.Code != 404 {
		t.Fatal(out.Code)
	}
	if out := e.call(secret, http.MethodGet, "/v1/responses/resp_resource", nil, nil); out.Code != 200 {
		t.Fatalf("%d %s", out.Code, out.Body.String())
	}
	after := e.callRow(created.Header().Get("X-AIHub-Request-Id"))
	if before.CostNano != after.CostNano || before.Outcome != after.Outcome {
		t.Fatal("retrieval changed settlement")
	}
	var count int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM gateway_resources WHERE kind='item' AND resource_id='msg_resource'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
