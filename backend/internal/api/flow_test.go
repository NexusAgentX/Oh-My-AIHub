package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// client 以浏览器方式调用处理器：同源 Origin、会话 Cookie，并按契约校验每个响应。
type client struct {
	spec    *openAPISpec
	handler http.Handler
	cookie  *http.Cookie
}

type callOption func(*http.Request)

func withoutCookie(request *http.Request) { request.Header.Del("Cookie") }

func withHeader(name, value string) callOption {
	return func(request *http.Request) { request.Header.Set(name, value) }
}

func (c *client) call(t *testing.T, method, path string, body any, options ...callOption) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = jsonBody(body)
	}
	request := httptest.NewRequest(method, "https://hub.example"+path, reader)
	request.Header.Set("Origin", "https://hub.example")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		request.AddCookie(c.cookie)
	}
	for _, option := range options {
		option(request)
	}
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	if !strings.HasPrefix(path, "/v1") {
		c.spec.assertResponse(t, method, path, recorder)
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == defaultSessionCookie && cookie.MaxAge >= 0 {
			c.cookie = cookie
		}
	}
	return recorder
}

func (c *client) expect(t *testing.T, status int, method, path string, body any, options ...callOption) map[string]any {
	t.Helper()
	recorder := c.call(t, method, path, body, options...)
	if recorder.Code != status {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, recorder.Code, status, recorder.Body.String())
	}
	if recorder.Code == http.StatusNoContent {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s %s: %v", method, path, err)
	}
	return decoded
}

func bootstrap(t *testing.T, spec *openAPISpec, handler http.Handler) *client {
	t.Helper()
	admin := &client{spec: spec, handler: handler}
	admin.expect(t, http.StatusOK, http.MethodGet, "/api/instance", nil)
	created := admin.expect(t, http.StatusCreated, http.MethodPost, "/api/instance/initialize", map[string]string{
		"username": "founder", "display_name": "创始人", "password": "Founder-password-2026",
	})
	if created["account"].(map[string]any)["is_admin"] != true || admin.cookie == nil || !admin.cookie.HttpOnly || !admin.cookie.Secure {
		t.Fatalf("initialize = %v, cookie %+v", created, admin.cookie)
	}
	return admin
}

func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	return payload.Error
}

func TestInstanceInitializationHappensOnce(t *testing.T) {
	spec := loadOpenAPI(t)
	handler := newFakeHandler(newFakeStore())
	admin := bootstrap(t, spec, handler)
	if state := admin.expect(t, http.StatusOK, http.MethodGet, "/api/instance", nil); state["initialized"] != true {
		t.Fatalf("instance = %v", state)
	}
	again := &client{spec: spec, handler: handler}
	recorder := again.call(t, http.MethodPost, "/api/instance/initialize", map[string]string{
		"username": "intruder", "display_name": "x", "password": "Intruder-password-2026",
	})
	if recorder.Code != http.StatusConflict || errorCode(t, recorder) != "already_initialized" {
		t.Fatalf("second initialize = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := again.call(t, http.MethodPost, "/api/instance/initialize", "not an object"); recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed initialize = %d", recorder.Code)
	}
}

func TestAccountLifecycleAndPoints(t *testing.T) {
	spec := loadOpenAPI(t)
	store := newFakeStore()
	handler := newFakeHandler(store)
	admin := bootstrap(t, spec, handler)
	me := admin.expect(t, http.StatusOK, http.MethodGet, "/api/me", nil)["account"].(map[string]any)
	adminID := me["id"].(string)

	// 创建账户：返回一次性初始密码，信用额度可指定。
	created := admin.expect(t, http.StatusCreated, http.MethodPost, "/api/admin/accounts", map[string]any{
		"username": "wang", "display_name": "小王", "credit_limit": "100",
	})
	member := created["account"].(map[string]any)
	memberID := member["id"].(string)
	initialPassword := created["initial_password"].(string)
	if member["credit_limit"] != "100" || member["balance"] != "0" || member["available"] != "100" || member["must_change_password"] != true {
		t.Fatalf("created account = %v", member)
	}
	if recorder := admin.call(t, http.MethodPost, "/api/admin/accounts", map[string]any{"username": "wang", "display_name": "重复"}); recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate username = %d", recorder.Code)
	}
	list := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/accounts?limit=1", nil)
	if len(list["items"].([]any)) != 1 || list["next_cursor"] == nil {
		t.Fatalf("first page = %v", list)
	}
	second := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/accounts?limit=1&cursor="+list["next_cursor"].(string), nil)
	if len(second["items"].([]any)) != 1 || second["next_cursor"] != nil || strings.Contains(second["items"].([]any)[0].(map[string]any)["username"].(string), "founder") == strings.Contains(list["items"].([]any)[0].(map[string]any)["username"].(string), "founder") {
		t.Fatalf("second page = %v", second)
	}
	if strings.Contains(admin.call(t, http.MethodGet, "/api/admin/accounts", nil).Body.String(), initialPassword) {
		t.Fatal("account list leaked the one-time password")
	}

	// 首次登录只能改密，其他接口被拒绝。
	wang := &client{spec: spec, handler: handler}
	wang.expect(t, http.StatusOK, http.MethodPost, "/api/auth/login", map[string]string{"username": "wang", "password": initialPassword})
	if recorder := wang.call(t, http.MethodGet, "/api/points", nil); recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "password_change_required" {
		t.Fatalf("points before password change = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := wang.call(t, http.MethodGet, "/api/admin/accounts", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("admin route for member = %d", recorder.Code)
	}
	wang.expect(t, http.StatusOK, http.MethodGet, "/api/me", nil)
	wang.expect(t, http.StatusOK, http.MethodPost, "/api/me/password", map[string]string{"current_password": initialPassword, "new_password": "Wang-password-2026"})
	if recorder := wang.call(t, http.MethodGet, "/api/admin/accounts", nil); recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "administrator_required" {
		t.Fatalf("admin route for ready member = %d %s", recorder.Code, recorder.Body.String())
	}
	points := wang.expect(t, http.StatusOK, http.MethodGet, "/api/points", nil)
	if points["balance"] != "0" || points["credit_limit"] != "100" || points["available"] != "100" {
		t.Fatalf("points = %v", points)
	}

	// 调账：幂等键重复不重复记账。
	adjust := map[string]string{"amount": "-30.03", "reason": "线下扣款"}
	first := admin.expect(t, http.StatusOK, http.MethodPost, "/api/admin/accounts/"+memberID+"/adjust", adjust, withHeader("Idempotency-Key", "adj-1"))
	replay := admin.expect(t, http.StatusOK, http.MethodPost, "/api/admin/accounts/"+memberID+"/adjust", adjust, withHeader("Idempotency-Key", "adj-1"))
	if first["replayed"] != false || replay["replayed"] != true || first["transaction_id"] != replay["transaction_id"] || replay["account"].(map[string]any)["balance"] != "-30.03" {
		t.Fatalf("adjust = %v / %v", first, replay)
	}
	if recorder := admin.call(t, http.MethodPost, "/api/admin/accounts/"+memberID+"/adjust", map[string]string{"amount": "0", "reason": "x"}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("zero adjust = %d", recorder.Code)
	}
	if recorder := admin.call(t, http.MethodPost, "/api/admin/accounts/not-a-uuid/adjust", adjust); recorder.Code != http.StatusNotFound {
		t.Fatalf("bad account id = %d", recorder.Code)
	}
	points = wang.expect(t, http.StatusOK, http.MethodGet, "/api/points", nil)
	if points["balance"] != "-30.03" || points["available"] != "69.97" {
		t.Fatalf("points after adjust = %v", points)
	}
	entries := wang.expect(t, http.StatusOK, http.MethodGet, "/api/points/entries", nil)
	items := entries["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["amount"] != "-30.03" || items[0].(map[string]any)["type"] != "admin_adjust" {
		t.Fatalf("entries = %v", entries)
	}
	csvResponse := wang.call(t, http.MethodGet, "/api/points/entries?format=csv", nil)
	if csvResponse.Code != http.StatusOK || !strings.HasPrefix(csvResponse.Header().Get("Content-Type"), "text/csv") ||
		!strings.HasPrefix(csvResponse.Body.String(), "\ufeff时间,类型,说明,关联,变动,余额,Key\n") {
		t.Fatalf("csv export = %d %q", csvResponse.Code, csvResponse.Body.String())
	}
	if body := csvResponse.Body.String(); !strings.Contains(body, "2026-10-08 12:30:00,管理员调账,'=SUM(A1),account:") || !strings.Contains(body, "调用支出,,,-1.5,-31.53,默认 Key") {
		t.Fatalf("csv rows = %q", body)
	}
	if recorder := wang.call(t, http.MethodGet, "/api/points/entries?group=week", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid group = %d", recorder.Code)
	}
	summary := wang.expect(t, http.StatusOK, http.MethodGet, "/api/points/entries?group=day", nil)
	if _, ok := summary["summary"].(map[string]any)["by_day"]; !ok {
		t.Fatalf("summary = %v", summary)
	}
	if recorder := wang.call(t, http.MethodGet, "/api/points/entries?limit=1000", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized limit = %d", recorder.Code)
	}

	// 坏账核销：负余额归零，再次核销无事可做。
	writeOff := admin.expect(t, http.StatusOK, http.MethodPost, "/api/admin/accounts/"+memberID+"/write-off", map[string]string{"reason": "无法收回"})
	if writeOff["account"].(map[string]any)["balance"] != "0" {
		t.Fatalf("write-off = %v", writeOff)
	}
	if recorder := admin.call(t, http.MethodPost, "/api/admin/accounts/"+memberID+"/write-off", map[string]string{"reason": "again"}); recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != "nothing_to_write_off" {
		t.Fatalf("second write-off = %d %s", recorder.Code, recorder.Body.String())
	}

	// 修改信用额度与显示名；不能停用或降级自己，不能移除最后一个管理员。
	updated := admin.expect(t, http.StatusOK, http.MethodPatch, "/api/admin/accounts/"+memberID, map[string]any{"credit_limit": "50", "display_name": "王"})
	if updated["account"].(map[string]any)["credit_limit"] != "50" {
		t.Fatalf("update = %v", updated)
	}
	if recorder := admin.call(t, http.MethodPatch, "/api/admin/accounts/"+adminID, map[string]any{"status": "disabled"}); recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != "cannot_modify_self" {
		t.Fatalf("self disable = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := admin.call(t, http.MethodPatch, "/api/admin/accounts/"+memberID, map[string]any{}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty update = %d", recorder.Code)
	}

	// 停用账户使其全部会话失效。
	admin.expect(t, http.StatusOK, http.MethodPatch, "/api/admin/accounts/"+memberID, map[string]any{"status": "disabled"})
	if recorder := wang.call(t, http.MethodGet, "/api/me", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("disabled session = %d", recorder.Code)
	}
	login := &client{spec: spec, handler: handler}
	if recorder := login.call(t, http.MethodPost, "/api/auth/login", map[string]string{"username": "wang", "password": "Wang-password-2026"}); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("disabled login = %d", recorder.Code)
	}
	admin.expect(t, http.StatusOK, http.MethodPatch, "/api/admin/accounts/"+memberID, map[string]any{"status": "active"})

	// 重置密码：返回新初始密码，旧会话失效，不能重置自己。
	login.expect(t, http.StatusOK, http.MethodPost, "/api/auth/login", map[string]string{"username": "wang", "password": "Wang-password-2026"})
	reset := admin.expect(t, http.StatusOK, http.MethodPost, "/api/admin/accounts/"+memberID+"/reset-password", nil)
	if reset["initial_password"] == "" || reset["account"].(map[string]any)["must_change_password"] != true {
		t.Fatalf("reset = %v", reset)
	}
	if recorder := login.call(t, http.MethodGet, "/api/me", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("session after reset = %d", recorder.Code)
	}
	if recorder := admin.call(t, http.MethodPost, "/api/admin/accounts/"+adminID+"/reset-password", nil); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("self reset = %d", recorder.Code)
	}

	// 退出登录清除 Cookie。
	admin.expect(t, http.StatusNoContent, http.MethodPost, "/api/auth/logout", nil)
}

func TestAdminModelsSettingsAndAudit(t *testing.T) {
	spec := loadOpenAPI(t)
	store := newFakeStore()
	admin := bootstrap(t, spec, newFakeHandler(store))

	model := map[string]any{
		"id": "deepseek-chat", "display_name": "DeepSeek Chat",
		"base_prices": map[string]string{"input": "1.5", "output": "4.5", "cache_write": "0", "cache_read": "0.15"},
		"price_tiers": []map[string]any{{
			"name": "高峰", "timezone": "Asia/Shanghai", "weekdays": []int{1, 2, 3, 4, 5},
			"start_minute_of_day": 540, "end_minute_of_day": 720,
			"prices": map[string]string{"input": "3", "output": "9", "cache_write": "0", "cache_read": "0.3"},
		}},
		"provider": "DeepSeek", "context_window": 128000,
	}
	created := admin.expect(t, http.StatusCreated, http.MethodPost, "/api/admin/models", model)["model"].(map[string]any)
	if created["enabled"] != true || len(created["price_tiers"].([]any)) != 1 || created["price_tiers"].([]any)[0].(map[string]any)["seq"] != float64(1) {
		t.Fatalf("created model = %v", created)
	}
	if recorder := admin.call(t, http.MethodPost, "/api/admin/models", model); recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate model = %d", recorder.Code)
	}
	invalid := map[string]any{"id": "bad/model", "display_name": "x", "base_prices": model["base_prices"]}
	if recorder := admin.call(t, http.MethodPost, "/api/admin/models", invalid); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("slash model = %d", recorder.Code)
	}
	tooExpensive := map[string]any{"id": "pricey", "display_name": "x", "base_prices": map[string]string{"input": "100000.000000001", "output": "0", "cache_write": "0", "cache_read": "0"}}
	if recorder := admin.call(t, http.MethodPost, "/api/admin/models", tooExpensive); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("price ceiling = %d", recorder.Code)
	}

	patched := admin.expect(t, http.StatusOK, http.MethodPatch, "/api/admin/models/deepseek-chat", map[string]any{
		"enabled": false, "price_tiers": []any{}, "context_window": nil,
	})["model"].(map[string]any)
	if patched["enabled"] != false || len(patched["price_tiers"].([]any)) != 0 || patched["context_window"] != nil || patched["display_name"] != "DeepSeek Chat" {
		t.Fatalf("patched model = %v", patched)
	}
	if recorder := admin.call(t, http.MethodPatch, "/api/admin/models/missing", map[string]any{"enabled": true}); recorder.Code != http.StatusNotFound {
		t.Fatalf("missing model = %d", recorder.Code)
	}
	if recorder := admin.call(t, http.MethodPatch, "/api/admin/models/deepseek-chat", map[string]any{"id": "renamed"}); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rename model = %d", recorder.Code)
	}
	listed := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/models", nil)
	if len(listed["items"].([]any)) != 1 {
		t.Fatalf("admin models = %v", listed)
	}

	current := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/settings", nil)["settings"].(map[string]any)
	if current["fee_rate_nano"] != float64(1_000_000) || current["c2c_payment_timeout_minutes"] != float64(30) {
		t.Fatalf("settings = %v", current)
	}
	update := map[string]any{
		"fee_rate_nano": 2_000_000, "c2c_payment_timeout_minutes": 15, "default_credit_limit": "20",
		"default_max_attempts": 2, "default_ttft_timeout_ms": 20000, "default_total_timeout_ms": 300000,
		"default_cooldown_failures": 5, "default_cooldown_seconds": 60, "extra_blocked_hosts": []string{"Evil.Example"},
	}
	saved := admin.expect(t, http.StatusOK, http.MethodPut, "/api/admin/settings", update)["settings"].(map[string]any)
	if saved["default_credit_limit"] != "20" || saved["extra_blocked_hosts"].([]any)[0] != "evil.example" {
		t.Fatalf("saved settings = %v", saved)
	}
	update["default_total_timeout_ms"] = 1000
	if recorder := admin.call(t, http.MethodPut, "/api/admin/settings", update); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid settings = %d", recorder.Code)
	}
	delete(update, "fee_rate_nano")
	if recorder := admin.call(t, http.MethodPut, "/api/admin/settings", update); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("partial settings = %d", recorder.Code)
	}

	// 新账户默认信用额度来自平台设置。
	created = admin.expect(t, http.StatusCreated, http.MethodPost, "/api/admin/accounts", map[string]any{"username": "chen", "display_name": "老陈"})["account"].(map[string]any)
	if created["credit_limit"] != "20" {
		t.Fatalf("default credit limit = %v", created)
	}

	page := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/audit?limit=2", nil)
	if len(page["items"].([]any)) != 2 || page["next_cursor"] == nil {
		t.Fatalf("audit page = %v", page)
	}
	rest := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/audit?cursor="+page["next_cursor"].(string), nil)
	actions := map[string]bool{}
	for _, item := range append(page["items"].([]any), rest["items"].([]any)...) {
		actions[item.(map[string]any)["action"].(string)] = true
	}
	for _, want := range []string{"account.created", "model.created", "model.updated", "settings.updated"} {
		if !actions[want] {
			t.Fatalf("audit actions = %v, missing %s", actions, want)
		}
	}
	if recorder := admin.call(t, http.MethodGet, "/api/admin/audit?cursor=abc", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad audit cursor = %d", recorder.Code)
	}
}
