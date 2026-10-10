package api

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestChannelsModelPagesAndHome(t *testing.T) {
	t.Parallel()
	p := newPlatform(t)
	admin := p.admin(t)
	seedModel(t, admin, "deepseek-chat")
	alice, _ := p.member(t, "alice")
	bob, _ := p.member(t, "bob")

	dailyCap := "50"
	body := map[string]any{
		"name": "阿里中转", "base_url": "https://relay.example/v1", "api_key": "sk-upstream-test-key-0001",
		"models": []map[string]any{{"model_id": "deepseek-chat", "upstream_model": "deepseek-chat-v2", "multiplier": "1.2", "formats": []string{"openai_chat", "anthropic"}}},
		"advanced": map[string]any{
			"user_agent": "relay-test/1.0", "header_rules": map[string]any{"set": []map[string]string{{"name": "X-Relay", "value": "1"}}, "remove": []string{"X-Old"}},
			"concurrency_limit": 4, "rpm_limit": 60, "daily_revenue_cap": dailyCap, "ttft_timeout_ms": 20000, "total_timeout_ms": 300000,
			"cooldown_failures": 3, "cooldown_seconds": 60,
		},
	}
	created := alice.expect(t, http.StatusCreated, http.MethodPost, "/api/channels", body)["channel"].(map[string]any)
	channelID := created["id"].(string)
	if created["status"] != "listed" || created["advanced"].(map[string]any)["daily_revenue_cap"] != "50" ||
		created["models"].([]any)[0].(map[string]any)["multiplier"] != "1.2" || created["today"].(map[string]any)["success_rate"] != "0.500000" {
		t.Fatalf("created channel = %v", created)
	}
	for name, mutate := range map[string]func(map[string]any){
		"unknown model": func(b map[string]any) {
			b["models"] = []map[string]any{{"model_id": "nope", "formats": []string{"openai_chat"}}}
		},
		"plain http url": func(b map[string]any) { b["base_url"] = "http://relay.example" },
		"no models":      func(b map[string]any) { b["models"] = []map[string]any{} },
	} {
		invalid := map[string]any{}
		for key, value := range body {
			invalid[key] = value
		}
		mutate(invalid)
		if recorder := alice.call(t, http.MethodPost, "/api/channels", invalid); recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d %s", name, recorder.Code, recorder.Body.String())
		}
	}

	// 发现上游模型、读取与修改。
	discovered := alice.expect(t, http.StatusOK, http.MethodPost, "/api/channels/discover", map[string]string{"base_url": "https://relay.example/v1", "api_key": "sk-upstream-test-key-0001"})
	models := discovered["upstream_models"].([]any)
	if discovered["base_url"] != "https://relay.example" || len(models) != 2 || models[0].(map[string]any)["matched_model_id"] != "deepseek-chat" {
		t.Fatalf("discovered = %v", discovered)
	}
	if listed := alice.expect(t, http.StatusOK, http.MethodGet, "/api/channels", nil); len(listed["items"].([]any)) != 1 {
		t.Fatalf("channels = %v", listed)
	}
	if empty := bob.expect(t, http.StatusOK, http.MethodGet, "/api/channels", nil); len(empty["items"].([]any)) != 0 {
		t.Fatalf("other user's channels = %v", empty)
	}
	channelPath := "/api/channels/" + channelID
	detail := alice.expect(t, http.StatusOK, http.MethodGet, channelPath, nil)
	if len(detail["events"].([]any)) != 1 || detail["events"].([]any)[0].(map[string]any)["kind"] != "listed" {
		t.Fatalf("channel detail = %v", detail)
	}
	if recorder := bob.call(t, http.MethodGet, channelPath, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("stranger channel = %d", recorder.Code)
	}
	renamed := alice.expect(t, http.StatusOK, http.MethodPatch, channelPath, map[string]any{"name": "改名", "status": "unlisted", "api_key": "sk-upstream-test-key-0002"})["channel"].(map[string]any)
	if renamed["name"] != "改名" || renamed["status"] != "unlisted" {
		t.Fatalf("renamed = %v", renamed)
	}
	if recorder := alice.call(t, http.MethodPatch, channelPath, map[string]any{}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty channel update = %d", recorder.Code)
	}
	alice.expect(t, http.StatusOK, http.MethodPatch, channelPath, map[string]any{"status": "listed", "advanced": map[string]any{"rpm_limit": 30, "daily_revenue_cap": dailyCap}})

	// 格式测试写回结果。
	tested := alice.expect(t, http.StatusOK, http.MethodPost, channelPath+"/test", map[string]any{"model_ids": []string{"deepseek-chat"}, "formats": []string{"openai_chat", "gemini"}, "apply": true})
	results := tested["results"].([]any)
	if len(results) != 2 || results[0].(map[string]any)["ok"] != true || results[1].(map[string]any)["ok"] != false {
		t.Fatalf("test results = %v", tested)
	}
	testedModel := tested["channel"].(map[string]any)["models"].([]any)[0].(map[string]any)
	if len(testedModel["format_tests"].(map[string]any)) != 2 {
		t.Fatalf("format tests = %v", testedModel)
	}
	if recorder := alice.call(t, http.MethodPost, channelPath+"/test", map[string]any{"model_ids": []string{"missing"}}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("test of unknown model = %d", recorder.Code)
	}

	// 模型浏览与首页。
	list := bob.expect(t, http.StatusOK, http.MethodGet, "/api/models", nil)["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["online_channels"] != float64(1) || list[0].(map[string]any)["lowest_prices"] == nil {
		t.Fatalf("models = %v", list)
	}
	for viewer, wantMine := range map[*client]bool{alice: true, bob: false} {
		page := viewer.expect(t, http.StatusOK, http.MethodGet, "/api/models/deepseek-chat", nil)
		offered := page["channels"].([]any)
		if len(offered) != 1 || offered[0].(map[string]any)["is_mine"] != wantMine || offered[0].(map[string]any)["success_rate_24h"] != "0.900000" ||
			offered[0].(map[string]any)["daily_cap_remaining"] != "0.960000" {
			t.Fatalf("model page = %v", page)
		}
	}
	if recorder := bob.call(t, http.MethodGet, "/api/models/missing", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown model page = %d", recorder.Code)
	}
	keyName, keyID := "默认 Key", "30000000-0000-4000-8000-0000000000b1"
	finished, duration := time.Date(2026, 10, 8, 9, 0, 1, 0, time.UTC), int32(1200)
	p.browse.home = gateway.HomeStats{TodaySpend: money.FromNano(3_000_000_000), TodayCalls: 4, TodaySucceeded: 3, RevenueToday: money.FromNano(1_000_000_000), ChannelsOnline: 1, ChannelsTotal: 2, PendingTrades: 1}
	p.browse.recent = []gateway.CallSummary{{
		ID: "30000000-0000-4000-8000-000000000001", CreatedAt: finished.Add(-time.Second), CompletedAt: &finished, RequestedModel: "deepseek-chat",
		Format: channel.FormatOpenAIChat, Outcome: "succeeded", KeyID: &keyID, KeyName: &keyName, ChannelID: &channelID, ChannelName: &keyName, Cost: money.FromNano(1_000_000_000),
		Fee: money.FromNano(10_000_000), DurationMS: &duration, AttemptCount: 1, Booked: true,
	}}
	home := alice.expect(t, http.StatusOK, http.MethodGet, "/api/home", nil)
	if home["default_key"] == nil || len(home["recent_calls"].([]any)) != 1 || home["pending_c2c_trades"] != float64(1) {
		t.Fatalf("home = %v", home)
	}

	// 管理员视角：筛选、翻页、下架与恢复。
	if page := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/channels?limit=1&status=listed&q="+url.QueryEscape("改"), nil); len(page["items"].([]any)) != 1 || page["next_cursor"] != nil {
		t.Fatalf("admin channels = %v", page)
	}
	second := alice.expect(t, http.StatusCreated, http.MethodPost, "/api/channels", map[string]any{
		"name": "第二个", "base_url": "https://relay.example", "api_key": "sk-upstream-test-key-0003", "status": "unlisted",
		"models": []map[string]any{{"model_id": "deepseek-chat", "formats": []string{"openai_chat"}, "enabled": false}},
	})["channel"].(map[string]any)
	page := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/channels?limit=1", nil)
	if len(page["items"].([]any)) != 1 || page["next_cursor"] == nil {
		t.Fatalf("admin channels page = %v", page)
	}
	rest := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/channels?limit=1&cursor="+page["next_cursor"].(string), nil)
	if len(rest["items"].([]any)) != 1 || rest["items"].([]any)[0].(map[string]any)["owner"].(map[string]any)["username"] != "alice" {
		t.Fatalf("admin channels second page = %v", rest)
	}
	if recorder := admin.call(t, http.MethodGet, "/api/admin/channels?status=bogus", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad admin channel filter = %d", recorder.Code)
	}
	if got := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/channels/"+channelID, nil); got["channel"].(map[string]any)["id"] != channelID {
		t.Fatalf("admin channel = %v", got)
	}
	suspended := admin.expect(t, http.StatusOK, http.MethodPost, "/api/admin/channels/"+channelID+"/suspend", map[string]string{"reason": "涉嫌违规"})["channel"].(map[string]any)
	if suspended["status"] != "suspended" || suspended["suspended_reason"] != "涉嫌违规" {
		t.Fatalf("suspended = %v", suspended)
	}
	if recorder := alice.call(t, http.MethodPatch, channelPath, map[string]any{"status": "listed"}); recorder.Code != http.StatusConflict || errorCode(t, recorder) != "channel_suspended" {
		t.Fatalf("owner relist while suspended = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := admin.call(t, http.MethodPost, "/api/admin/channels/"+channelID+"/suspend", map[string]string{"reason": " "}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("suspend without reason = %d", recorder.Code)
	}
	restored := admin.expect(t, http.StatusOK, http.MethodPost, "/api/admin/channels/"+channelID+"/unsuspend", map[string]string{"reason": "已整改"})["channel"].(map[string]any)
	if restored["status"] != "listed" || restored["suspended_reason"] != nil {
		t.Fatalf("restored = %v", restored)
	}

	alice.expect(t, http.StatusNoContent, http.MethodDelete, channelPath, nil)
	alice.expect(t, http.StatusNoContent, http.MethodDelete, "/api/channels/"+second["id"].(string), nil)
	if recorder := alice.call(t, http.MethodGet, channelPath, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("deleted channel = %d", recorder.Code)
	}
}
