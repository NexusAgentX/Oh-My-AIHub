package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestCustomModelsListPriceAndRoute(t *testing.T) {
	t.Parallel()
	p := newPlatform(t)
	admin := p.admin(t)
	seedModel(t, admin, "deepseek-chat")
	alice, _ := p.member(t, "alice")
	bob, _ := p.member(t, "bob")

	customModel := func(input, output string) map[string]any {
		return map[string]any{
			"model_id": "foo-1", "multiplier": "3",
			"custom_prices": map[string]string{"input": input, "output": output, "cache_write": "0", "cache_read": "0.1"},
		}
	}
	channelBody := func(name string, models ...map[string]any) map[string]any {
		return map[string]any{"name": name, "base_url": "https://relay.example/v1", "api_key": "sk-upstream-test-key-0001", "advanced": map[string]any{"compatibility": map[string]any{"responses": []string{"inference", "nonstream", "stream"}, "codex": []string{"inference", "nonstream"}}}, "models": models}
	}
	created := alice.expect(t, http.StatusCreated, http.MethodPost, "/api/channels", channelBody("贵", customModel("4", "4"),
		map[string]any{"model_id": "deepseek-chat"}))["channel"].(map[string]any)
	var custom map[string]any
	for _, item := range created["models"].([]any) {
		if model := item.(map[string]any); model["model_id"] == "foo-1" {
			custom = model
		}
	}
	if custom == nil || custom["multiplier"] != "1" || custom["custom_prices"].(map[string]any)["input"] != "4" ||
		custom["current_prices"].(map[string]any)["output"] != "4" || custom["pending_multiplier"] != false {
		t.Fatalf("custom channel model = %v", custom)
	}
	// Blend (3 × 1 + 9) / 4 = 3 beats (3 × 4 + 4) / 4 = 4 although the output price is higher.
	bob.expect(t, http.StatusCreated, http.MethodPost, "/api/channels", channelBody("便宜", customModel("1", "9")))

	taken := customModel("1", "1")
	taken["model_id"] = "deepseek-chat"
	recorder := alice.call(t, http.MethodPost, "/api/channels", channelBody("重名", taken))
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "model_name_taken") {
		t.Fatalf("catalog name as custom = %d %s", recorder.Code, recorder.Body.String())
	}
	bad := customModel("1", "1")
	bad["model_id"] = "foo/bar"
	if recorder := alice.call(t, http.MethodPost, "/api/channels", channelBody("非法", bad)); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid custom name = %d", recorder.Code)
	}

	list := alice.expect(t, http.StatusOK, http.MethodGet, "/api/models", nil)["items"].([]any)
	last := list[len(list)-1].(map[string]any)
	if len(list) != 2 || last["id"] != "foo-1" || last["custom"] != true || last["online_channels"] != float64(2) ||
		last["lowest_prices"].(map[string]any)["output"] != "9" || list[0].(map[string]any)["custom"] != false {
		t.Fatalf("models = %v", list)
	}
	detail := alice.expect(t, http.StatusOK, http.MethodGet, "/api/models/foo-1", nil)
	if detail["model"].(map[string]any)["custom"] != true || len(detail["channels"].([]any)) != 2 || len(detail["price_tiers"].([]any)) != 0 {
		t.Fatalf("custom model detail = %v", detail)
	}
	alice.expect(t, http.StatusNotFound, http.MethodGet, "/api/models/missing", nil)

	alice.expect(t, http.StatusOK, http.MethodPut, "/api/routing/foo-1", map[string]any{"mode": "manual"})
	alice.expect(t, http.StatusNotFound, http.MethodPut, "/api/routing/missing", map[string]any{"mode": "manual"})
}
