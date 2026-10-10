package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
)

// priceSnapshotJSON 是调用类交易记录的价格快照，字段与规范的 PriceSnapshot 一致。
const priceSnapshotJSON = `{"base_prices":{"input":"1.5","output":"4.5","cache_write":"0","cache_read":"0.15"},"tier":{"seq":1,"name":"高峰"},` +
	`"multiplier":"1.2","fee_rate_nano":1000000,"prices":{"input":"1.8","output":"5.4","cache_write":"0","cache_read":"0.18"}}`

// observation 把调用、渠道与账本交易的固定数据装入观测存储，返回涉及的 ID。
type observation struct {
	billed, unbilledCharge, unbilledVoid string
	transaction                          string
}

func (p *platform) observeFixtures(consumer, channelID, keyID string) observation {
	ids := observation{
		billed: "30000000-0000-4000-8000-000000000001", unbilledCharge: "30000000-0000-4000-8000-000000000002",
		unbilledVoid: "30000000-0000-4000-8000-000000000003", transaction: "30000000-0000-4000-8000-0000000000a1",
	}
	now := time.Now().UTC().Truncate(time.Second)
	account := observe.AccountRef{ID: consumer, Username: "alice", DisplayName: "用户alice"}
	p.observe.people = account
	p.observe.repaired = "30000000-0000-4000-8000-0000000000a2"
	model, tag, agent, mode, source := "deepseek-chat", "ci", "curl/8.0", "cheapest", "account"
	status, errCode, errMessage := 502, "bad_gateway", "上游失败"
	connect, ttft, total, intervalP50, intervalP95 := 20, 300, 1200, 25, 80
	responseBytes := int64(2048)
	speed := 42.5
	tx := ids.transaction
	channelRef := &observe.ChannelRef{ID: channelID, Name: "阿里中转"}
	ok := 200
	base := func(id string, created time.Time, outcome string) observe.CallRecord {
		return observe.CallRecord{CallRow: observe.CallRow{
			ID: id, CreatedAt: created, CompletedAt: &now, Account: account, RequestedModel: "deepseek-chat", ModelID: &model, Format: "openai_chat",
			Stream: true, Outcome: outcome, Usage: observe.Usage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 10},
			Cost: money.FromNano(1_000_000_000), Fee: money.FromNano(10_000_000), TTFTMS: &ttft, DurationMS: &total, AttemptCount: 2,
		}}
	}
	billed := base(ids.billed, now.Add(-time.Minute), "succeeded")
	billed.Key, billed.Tag, billed.Channel, billed.LedgerTxID = &observe.KeyRef{ID: keyID, Name: "默认 Key"}, &tag, channelRef, &tx
	billed.RoutingMode, billed.RoutingSource, billed.ClientUserAgent = &mode, &source, &agent
	billed.TokensPerSecond, billed.IntervalP50MS, billed.IntervalP95MS, billed.ResponseBytes = &speed, &intervalP50, &intervalP95, &responseBytes
	billed.PriceSnapshot = json.RawMessage(priceSnapshotJSON)
	billed.Attempts = []observe.Attempt{
		{Channel: channelRef, StatusCode: &status, ErrorCode: &errCode, ErrorMessage: &errMessage, ConnectMS: &connect, EndReason: "upstream_error"},
		{Channel: channelRef, StatusCode: &ok, ConnectMS: &connect, TTFTMS: &ttft, DurationMS: &total, ResponseByte: &responseBytes, EndReason: "completed"},
	}
	charge := base(ids.unbilledCharge, now.Add(-2*time.Minute), "succeeded")
	charge.Channel, charge.Attempts = channelRef, []observe.Attempt{{Channel: channelRef, StatusCode: &ok, EndReason: "completed"}}
	charge.AttemptCount = 1
	void := base(ids.unbilledVoid, now.Add(-3*time.Minute), "succeeded")
	void.Channel, void.Attempts = channelRef, []observe.Attempt{{Channel: channelRef, StatusCode: &ok, EndReason: "completed"}}
	void.AttemptCount = 1
	p.observe.calls = []observe.CallRecord{billed, charge, void}

	system := "platform_revenue"
	first := observe.Transaction{
		ID: ids.transaction, Type: "api_call", IdempotencyKey: "call:" + ids.billed, RelatedType: "call", RelatedID: ids.billed, Reason: "", CreatedAt: now,
		RelatedSummary: "deepseek-chat", PriceSnapshot: json.RawMessage(priceSnapshotJSON),
		Entries: []observe.TxEntry{
			{Account: observe.LedgerAccountRef{Kind: "user", Account: &account}, Amount: money.FromNano(-1_010_000_000), BalanceBefore: money.FromNano(5_000_000_000), BalanceAfter: money.FromNano(3_990_000_000)},
			{Account: observe.LedgerAccountRef{Kind: "system", SystemCode: &system}, Amount: money.FromNano(1_010_000_000)},
		},
	}
	second := observe.Transaction{
		ID: "30000000-0000-4000-8000-0000000000a3", Type: "admin_adjust", IdempotencyKey: "adjust:1", Actor: &account, Reason: "线下补偿", CreatedAt: now.Add(-time.Hour),
		Entries: []observe.TxEntry{{Account: observe.LedgerAccountRef{Kind: "user", Account: &account}, Amount: money.FromNano(5_000_000_000), BalanceAfter: money.FromNano(5_000_000_000)}},
	}
	p.observe.transactions = []observe.Transaction{first, second}
	return ids
}

func TestCallObservationReports(t *testing.T) {
	t.Parallel()
	p := newPlatform(t)
	admin := p.admin(t)
	seedModel(t, admin, "deepseek-chat")
	alice, aliceID := p.member(t, "alice")
	bob, _ := p.member(t, "bob")
	carol, _ := p.member(t, "carol")
	channelID := bob.expect(t, http.StatusCreated, http.MethodPost, "/api/channels", map[string]any{
		"name": "阿里中转", "base_url": "https://relay.example", "api_key": "sk-upstream-test-key-0001",
		"models":   []map[string]any{{"model_id": "deepseek-chat", "formats": []string{"openai_chat"}}},
		"advanced": map[string]any{"daily_revenue_cap": "10"},
	})["channel"].(map[string]any)["id"].(string)
	keyID := alice.expect(t, http.StatusCreated, http.MethodPost, "/api/keys", map[string]any{"name": "默认 Key"})["key"].(map[string]any)["id"].(string)
	ids := p.observeFixtures(aliceID, channelID, keyID)

	// 我的调用：分页、汇总与详情；渠道所有者只看到自己渠道的一侧。
	first := alice.expect(t, http.StatusOK, http.MethodGet, "/api/calls?limit=2&outcome=succeeded", nil)
	if len(first["items"].([]any)) != 2 || first["next_cursor"] == nil || first["summary"].(map[string]any)["calls"] != float64(3) {
		t.Fatalf("calls = %v", first)
	}
	second := alice.expect(t, http.StatusOK, http.MethodGet, "/api/calls?limit=2&cursor="+first["next_cursor"].(string), nil)
	if len(second["items"].([]any)) != 1 || second["next_cursor"] != nil {
		t.Fatalf("second call page = %v", second)
	}
	for _, query := range []string{"?format=bogus", "?outcome=bogus", "?from=yesterday", "?min_tokens=-1", "?api_key_id=nope", "?limit=0"} {
		if recorder := alice.call(t, http.MethodGet, "/api/calls"+query, nil); recorder.Code != http.StatusBadRequest {
			t.Fatalf("calls%s = %d", query, recorder.Code)
		}
	}
	if recorder := alice.call(t, http.MethodGet, "/api/calls?cursor=bogus", nil); recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_cursor" {
		t.Fatalf("bad call cursor = %d %s", recorder.Code, recorder.Body.String())
	}
	own := alice.expect(t, http.StatusOK, http.MethodGet, "/api/calls/"+ids.billed, nil)["call"].(map[string]any)
	if len(own["attempts"].([]any)) != 2 || own["ledger_transaction_id"] != ids.transaction || own["price_snapshot"] == nil || own["api_key"] == nil {
		t.Fatalf("call detail = %v", own)
	}
	seen := bob.expect(t, http.StatusOK, http.MethodGet, "/api/calls/"+ids.billed, nil)["call"].(map[string]any)
	if seen["api_key"] != nil || seen["ledger_transaction_id"] != nil {
		t.Fatalf("channel owner sees consumer data: %v", seen)
	}
	for _, request := range []struct {
		client *client
		path   string
	}{{carol, "/api/calls/" + ids.billed}, {alice, "/api/calls/30000000-0000-4000-8000-0000000000ff"}, {alice, "/api/calls/not-a-uuid"}} {
		if recorder := request.client.call(t, http.MethodGet, request.path, nil); recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d", request.path, recorder.Code)
		}
	}

	// 用量：支出、渠道收入、分组与参数校验。
	spend := alice.expect(t, http.StatusOK, http.MethodGet, "/api/usage", nil)
	if spend["view"] != "spend" || spend["group_by"] != "day" || spend["total"].(map[string]any)["calls"] != float64(3) {
		t.Fatalf("usage = %v", spend)
	}
	alice.expect(t, http.StatusOK, http.MethodGet, "/api/usage?group_by=model&view=spend&model=deepseek-chat", nil)
	revenue := bob.expect(t, http.StatusOK, http.MethodGet, "/api/usage?view=revenue&group_by=channel&channel_id="+channelID, nil)
	if revenue["view"] != "revenue" {
		t.Fatalf("revenue usage = %v", revenue)
	}
	for _, query := range []string{"?view=profit", "?group_by=channel", "?api_key_id=nope", "?from=x"} {
		if recorder := alice.call(t, http.MethodGet, "/api/usage"+query, nil); recorder.Code != http.StatusBadRequest {
			t.Fatalf("usage%s = %d", query, recorder.Code)
		}
	}

	// 渠道统计与渠道调用只对所有者与管理员开放。
	stats := bob.expect(t, http.StatusOK, http.MethodGet, "/api/channels/"+channelID+"/stats", nil)
	if stats["calls"] != float64(10) || stats["today"].(map[string]any)["daily_cap"] != "10" || len(stats["hourly"].([]any)) != 24 {
		t.Fatalf("channel stats = %v", stats)
	}
	admin.expect(t, http.StatusOK, http.MethodGet, "/api/channels/"+channelID+"/stats", nil)
	channelCalls := bob.expect(t, http.StatusOK, http.MethodGet, "/api/channels/"+channelID+"/calls?limit=2", nil)
	if len(channelCalls["items"].([]any)) != 2 || channelCalls["next_cursor"] == nil || channelCalls["items"].([]any)[0].(map[string]any)["served"] != true {
		t.Fatalf("channel calls = %v", channelCalls)
	}
	for _, request := range []struct {
		client *client
		path   string
		status int
	}{
		{alice, "/api/channels/" + channelID + "/stats", http.StatusNotFound}, {alice, "/api/channels/" + channelID + "/calls", http.StatusNotFound},
		{bob, "/api/channels/not-a-uuid/stats", http.StatusNotFound}, {bob, "/api/channels/" + channelID + "/stats?from=x", http.StatusBadRequest},
		{bob, "/api/channels/" + channelID + "/calls?format=bogus", http.StatusBadRequest},
	} {
		if recorder := request.client.call(t, http.MethodGet, request.path, nil); recorder.Code != request.status {
			t.Fatalf("GET %s = %d, want %d", request.path, recorder.Code, request.status)
		}
	}

	// 管理员报表。
	adminCalls := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/calls?account_id="+aliceID, nil)
	if len(adminCalls["items"].([]any)) != 3 || adminCalls["items"].([]any)[0].(map[string]any)["account"] == nil {
		t.Fatalf("admin calls = %v", adminCalls)
	}
	overview := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/overview", nil)
	if len(overview["attention"].([]any)) < 5 || overview["c2c"].(map[string]any)["open_disputes"] != float64(1) {
		t.Fatalf("overview = %v", overview)
	}
	points := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/points?days=7", nil)
	if len(points["risks"].([]any)) != 2 || len(points["trend"].([]any)) != 7 || points["checks"].(map[string]any)["all_passed"] != false {
		t.Fatalf("admin points = %v", points)
	}
	if recorder := admin.call(t, http.MethodGet, "/api/admin/points?days=5", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("admin points days = %d", recorder.Code)
	}
	transactions := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/ledger/transactions?limit=1&type=api_call", nil)
	if len(transactions["items"].([]any)) != 1 || transactions["next_cursor"] == nil {
		t.Fatalf("transactions = %v", transactions)
	}
	admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/ledger/transactions?related_type=call&related_id="+ids.billed, nil)
	detail := admin.expect(t, http.StatusOK, http.MethodGet, "/api/admin/ledger/transactions/"+ids.transaction, nil)["transaction"].(map[string]any)
	if len(detail["entries"].([]any)) != 2 || detail["price_snapshot"] == nil {
		t.Fatalf("transaction = %v", detail)
	}
	for _, path := range []string{"/api/admin/ledger/transactions?related_type=call", "/api/admin/ledger/transactions?type=bogus", "/api/admin/ledger/transactions?cursor=bogus"} {
		if recorder := admin.call(t, http.MethodGet, path, nil); recorder.Code != http.StatusBadRequest {
			t.Fatalf("GET %s = %d", path, recorder.Code)
		}
	}
	if recorder := admin.call(t, http.MethodGet, "/api/admin/ledger/transactions/30000000-0000-4000-8000-0000000000ff", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("missing transaction = %d", recorder.Code)
	}

	// 补记：按决定记账或作废，已记账的调用不能再补。
	repair := "/api/admin/ledger/repair-call/"
	charged := admin.expect(t, http.StatusOK, http.MethodPost, repair+ids.unbilledCharge, map[string]string{"action": "charge", "reason": "补记账"})
	if charged["transaction_id"] != p.observe.repaired || charged["call"].(map[string]any)["ledger_transaction_id"] != p.observe.repaired {
		t.Fatalf("charged repair = %v", charged)
	}
	if voided := admin.expect(t, http.StatusOK, http.MethodPost, repair+ids.unbilledVoid, map[string]string{"action": "void", "reason": "作废"}); voided["transaction_id"] != nil {
		t.Fatalf("voided repair = %v", voided)
	}
	if recorder := admin.call(t, http.MethodPost, repair+ids.unbilledCharge, map[string]string{"action": "charge", "reason": "again"}); recorder.Code != http.StatusConflict || errorCode(t, recorder) != "not_repairable" {
		t.Fatalf("second repair = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := admin.call(t, http.MethodPost, repair+ids.unbilledVoid, map[string]string{"action": "refund", "reason": "x"}); recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad repair action = %d", recorder.Code)
	}
	if recorder := alice.call(t, http.MethodGet, "/api/admin/calls", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("member admin calls = %d", recorder.Code)
	}

	// 实时流：三个入口都先收到建立注释，再按各自的范围推送 JSON 事件。
	started, finished := gateway.EventCallStarted, gateway.EventCallFinished
	publish := func(kind gateway.EventKind) func() {
		return func() { p.bus.publish(gateway.Event{Kind: kind, CallID: ids.billed}) }
	}
	events := p.stream(t, alice, "/api/calls/stream?model=deepseek-chat", publish(started), "CallSummary")
	if len(events) != 1 || events[0].name != "call.started" {
		t.Fatalf("my stream = %+v", events)
	}
	events = p.stream(t, admin, "/api/admin/calls/stream?outcome=succeeded&account_id="+aliceID, publish(finished), "AdminCall")
	if len(events) != 1 || events[0].name != "call.finished" {
		t.Fatalf("admin stream = %+v", events)
	}
	events = p.stream(t, bob, "/api/channels/"+channelID+"/calls/stream?model=deepseek-chat", publish(finished), "ChannelCall")
	if len(events) != 1 || events[0].name != "call.finished" {
		t.Fatalf("channel stream = %+v", events)
	}
	for _, request := range []struct {
		client *client
		path   string
		status int
	}{
		{alice, "/api/calls/stream?outcome=bogus", http.StatusBadRequest}, {alice, "/api/calls/stream?api_key_id=nope", http.StatusBadRequest},
		{admin, "/api/admin/calls/stream?outcome=bogus", http.StatusBadRequest}, {alice, "/api/channels/" + channelID + "/calls/stream", http.StatusNotFound},
		{bob, "/api/channels/not-a-uuid/calls/stream", http.StatusNotFound},
	} {
		if recorder := request.client.call(t, http.MethodGet, request.path, nil); recorder.Code != request.status {
			t.Fatalf("GET %s = %d, want %d", request.path, recorder.Code, request.status)
		}
	}
}

type sseEvent struct{ name, data string }

// stream 以真实 HTTP 连接订阅 SSE：校验响应状态与媒体类型符合规范，等到建立注释后
// 触发事件，并把收到的每个 data 按 schema 校验。返回收到的事件（每个触发一次）。
func (p *platform) stream(t *testing.T, user *client, path string, trigger func(), schema string) []sseEvent {
	t.Helper()
	server := httptest.NewServer(p.handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(user.cookie)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	recorder := httptest.NewRecorder()
	for name, values := range response.Header {
		recorder.Header()[name] = values
	}
	recorder.WriteHeader(response.StatusCode)
	p.spec.assertResponse(t, http.MethodGet, path, recorder)

	reader := bufio.NewReader(response.Body)
	readLine := func() string {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		return strings.TrimRight(line, "\r\n")
	}
	for line := readLine(); line != ": connected"; line = readLine() {
	}
	trigger()
	var event sseEvent
	for {
		line := readLine()
		if name, ok := strings.CutPrefix(line, "event: "); ok {
			event.name = name
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			event.data = data
			break
		}
	}
	p.spec.assertSchema(t, schema, []byte(event.data))
	return []sseEvent{event}
}
