package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
)

func TestKeysAndRoutingPreferences(t *testing.T) {
	t.Parallel()
	p := newPlatform(t)
	admin := p.admin(t)
	seedModel(t, admin, "deepseek-chat")
	alice, _ := p.member(t, "alice")
	bob, _ := p.member(t, "bob")

	if listed := alice.expect(t, http.StatusOK, http.MethodGet, "/api/keys", nil); len(listed["items"].([]any)) != 0 {
		t.Fatalf("keys before any = %v", listed)
	}
	created := alice.expect(t, http.StatusCreated, http.MethodPost, "/api/keys", map[string]any{
		"name": "CI", "allowed_models": []string{"deepseek-chat"}, "budget_daily": "5", "model_aliases": map[string]string{"fast": "deepseek-chat"},
	})
	key := created["key"].(map[string]any)
	keyID, secret := key["id"].(string), created["secret"].(string)
	if key["status"] != "enabled" || key["budget_daily"] != "5" || key["budget_monthly"] != nil || !strings.HasPrefix(secret, apikey.Prefix) {
		t.Fatalf("created key = %v", created)
	}
	for name, body := range map[string]map[string]any{
		"missing name":    {"allowed_models": []string{}},
		"unknown model":   {"name": "x", "allowed_models": []string{"nope"}},
		"bad budget":      {"name": "x", "budget_daily": "ten"},
		"zero budget":     {"name": "x", "budget_total": "0"},
		"unknown alias":   {"name": "x", "model_aliases": map[string]string{"fast": "nope"}},
		"unknown field":   {"name": "x", "bogus": true},
		"overlong name":   {"name": strings.Repeat("长", 65)},
		"invalid status":  {"name": "x", "status": "paused"},
		"negative budget": {"name": "x", "budget_monthly": "-1"},
	} {
		if recorder := alice.call(t, http.MethodPost, "/api/keys", body); recorder.Code < 400 {
			t.Fatalf("%s = %d", name, recorder.Code)
		}
	}

	keyPath := "/api/keys/" + keyID
	detail := alice.expect(t, http.StatusOK, http.MethodGet, keyPath, nil)
	if len(detail["routing"].([]any)) != 0 || detail["key"].(map[string]any)["name"] != "CI" {
		t.Fatalf("key detail = %v", detail)
	}
	updated := alice.expect(t, http.StatusOK, http.MethodPatch, keyPath, map[string]any{
		"name": "CI 2", "status": "disabled", "budget_monthly": "50", "budget_daily": nil, "expires_at": "2027-01-01T00:00:00Z",
		"allowed_models": []string{}, "model_aliases": map[string]string{},
	})["key"].(map[string]any)
	if updated["name"] != "CI 2" || updated["status"] != "disabled" || updated["budget_daily"] != nil || updated["budget_monthly"] != "50" || updated["expires_at"] == nil {
		t.Fatalf("updated key = %v", updated)
	}
	if recorder := alice.call(t, http.MethodPatch, keyPath, map[string]any{}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty key update = %d", recorder.Code)
	}
	if revealed := alice.expect(t, http.StatusOK, http.MethodGet, keyPath+"/secret", nil); revealed["secret"] != secret {
		t.Fatalf("secret = %v", revealed)
	}
	for _, request := range []struct {
		client       *client
		method, path string
	}{
		{bob, http.MethodGet, keyPath}, {bob, http.MethodGet, keyPath + "/secret"}, {bob, http.MethodDelete, keyPath},
		{alice, http.MethodGet, "/api/keys/not-a-uuid"},
	} {
		if recorder := request.client.call(t, request.method, request.path, nil); recorder.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d", request.method, request.path, recorder.Code)
		}
	}

	// 账户级偏好与 Key 级覆盖。
	other := "30000000-0000-4000-8000-0000000000c1"
	account := alice.expect(t, http.StatusOK, http.MethodPut, "/api/routing/deepseek-chat", map[string]any{
		"mode": "manual", "order": []string{other}, "excluded": []string{}, "max_attempts": 2, "ttft_timeout_ms": 5000,
	})
	if account["source"] != "account" || account["mode"] != "manual" || account["order"].([]any)[0] != other {
		t.Fatalf("account routing = %v", account)
	}
	if recorder := alice.call(t, http.MethodPut, "/api/routing/missing-model", map[string]any{"mode": "cheapest"}); recorder.Code != http.StatusNotFound {
		t.Fatalf("routing for unknown model = %d", recorder.Code)
	}
	if recorder := alice.call(t, http.MethodPut, "/api/routing/deepseek-chat", map[string]any{"mode": "random"}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid routing mode = %d", recorder.Code)
	}
	override := alice.expect(t, http.StatusOK, http.MethodPut, keyPath+"/routing/deepseek-chat", map[string]any{"mode": "fastest"})
	if override["source"] != "key" || override["order"].([]any) == nil {
		t.Fatalf("key routing = %v", override)
	}
	if recorder := bob.call(t, http.MethodPut, keyPath+"/routing/deepseek-chat", map[string]any{"mode": "fastest"}); recorder.Code != http.StatusNotFound {
		t.Fatalf("routing on someone else's key = %d", recorder.Code)
	}
	if detail := alice.expect(t, http.StatusOK, http.MethodGet, keyPath, nil); len(detail["routing"].([]any)) != 1 {
		t.Fatalf("key routing list = %v", detail)
	}
	alice.expect(t, http.StatusNoContent, http.MethodDelete, keyPath+"/routing/deepseek-chat", nil)
	alice.expect(t, http.StatusNoContent, http.MethodDelete, keyPath+"/routing/deepseek-chat", nil) // 已跟随账户设置

	alice.expect(t, http.StatusNoContent, http.MethodDelete, keyPath, nil)
	if recorder := alice.call(t, http.MethodGet, keyPath, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("deleted key = %d", recorder.Code)
	}
}
