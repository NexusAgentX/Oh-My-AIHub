package postgres_test

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"net/http"
	"strings"
	"testing"
)

func TestCodexFixedSettingsAndCapabilityIsolation(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("codex-consumer", "100")
	provider := e.member("codex-provider", "0")
	up := okUpstream(t)
	e.createChannel(provider, channelSpec{name: "ordinary", upstream: up})
	_, secret := e.defaultKey(consumer)
	request := []byte(`{"model":"gpt-test","store":false,"input":"hello"}`)
	rejected := e.call(secret, http.MethodPost, "/backend-api/codex/responses", request, nil)
	if rejected.Code != 503 || !strings.Contains(rejected.Body.String(), "capability_unavailable") {
		t.Fatalf("%d %s", rejected.Code, rejected.Body.String())
	}
	e.createChannel(provider, channelSpec{name: "codex", upstream: up, advanced: map[string]any{"capabilities": []string{"codex_backend", "codex_search"}}, capabilities: []string{"responses_lite", "compact_v2"}})
	accepted := e.call(secret, http.MethodPost, "/backend-api/codex/responses", request, nil)
	if accepted.Code != 200 {
		t.Fatalf("%d %s", accepted.Code, accepted.Body.String())
	}
	current := e.apiJSON(e.admin, 200, http.MethodGet, "/api/admin/settings", nil)["settings"].(map[string]any)
	whoami := "{\n \"chatgpt_user_id\":\"compat-user\",\"chatgpt_account_id\":\"compat\",\"chatgpt_plan_type\":\"pro\"\n}"
	current["codex"] = map[string]any{"whoami": whoami, "accounts": `{"accounts":{"compat":{}}}`, "models": `{"models":[{"slug":"inaccessible-model","prefer_websockets":false}],"extension":true}`}
	// Remove read-only fields as the frontend request serializer does.
	for _, field := range []string{"updated_at", "usd_cny_rate_source", "usd_cny_rate_updated_at", "fx_sync_attempted_at", "fx_sync_error"} {
		delete(current, field)
	}
	e.apiJSON(e.admin, 200, http.MethodPut, "/api/admin/settings", current)
	// A fresh engine observes persisted configuration without relying on the old
	// settings snapshot's refresh interval.
	e.handler = e.newHandler(gateway.NewEngine(e.engineDeps))
	// Config endpoints tested below with a fresh snapshot after the admin save.
	result := e.call(secret, http.MethodGet, "/v1/user-auth-credential/whoami", nil, nil)
	if result.Code != 200 || result.Body.String() != whoami {
		t.Fatalf("fixed JSON changed: %d %s", result.Code, result.Body.String())
	}
	result = e.call(secret, http.MethodGet, "/backend-api/codex/models", nil, nil)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "inaccessible-model") {
		t.Fatalf("catalog unexpectedly pruned: %d %s", result.Code, result.Body.String())
	}
	result = e.call("invalid", http.MethodGet, "/v1/user-auth-credential/whoami", nil, nil)
	if result.Code != 401 {
		t.Fatalf("invalid key: %d", result.Code)
	}
}
