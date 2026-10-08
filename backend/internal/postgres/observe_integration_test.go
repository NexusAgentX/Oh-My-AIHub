package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

// Feature G: call lists and details, usage, channel statistics, points
// reports, the live reconciliation, live streams, metrics and error cleanup.

func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %#v", value)
	}
	return result
}

func asList(t *testing.T, value any) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("not an array: %#v", value)
	}
	return result
}

func (e *env) get(p person, path string) map[string]any {
	e.t.Helper()
	return e.apiJSON(p, http.StatusOK, http.MethodGet, path, nil)
}

func (e *env) chat(secret string, headers map[string]string) string {
	e.t.Helper()
	recorder := e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), headers)
	if recorder.Code != http.StatusOK {
		e.t.Fatalf("chat = %d %s", recorder.Code, recorder.Body.String())
	}
	return recorder.Header().Get("X-AIHub-Request-Id")
}

func (e *env) adjust(p person, amount string) {
	e.t.Helper()
	e.apiJSON(e.admin, http.StatusOK, http.MethodPost, "/api/admin/accounts/"+p.id+"/adjust", map[string]string{"amount": amount, "reason": "测试充值"})
}

// observed is a small world: a consumer, two sharers (one broken), tagged calls.
type observed struct {
	*env
	consumer, sharer, flaky, outsider person
	brokenID, goodID, flakyOnly       string
	secret, keyID                     string
	succeeded, fellBack               string
}

func newObserved(t *testing.T) *observed {
	e := newEnv(t)
	o := &observed{env: e}
	o.consumer = e.member("consumer", "100")
	o.sharer = e.member("sharer", "0")
	o.flaky = e.member("flaky", "0")
	o.outsider = e.member("outsider", "0")
	broken := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"upstream exploded"}`)
	})
	good := okUpstream(t)
	// The flaky channel is the cheapest, fails first and the call falls back to the sharer's.
	o.brokenID = e.createChannel(o.flaky, channelSpec{name: "flaky-relay", upstream: broken, multiplier: "1"})
	o.goodID = e.createChannel(o.sharer, channelSpec{name: "good-relay", upstream: good, multiplier: "2"})
	o.keyID, o.secret = e.defaultKey(o.consumer)
	o.fellBack = e.chat(o.secret, map[string]string{"X-AIHub-Tag": "suite"})
	// With the flaky channel in cooldown or still cheapest, further calls also pass the sharer.
	o.succeeded = e.chat(o.secret, map[string]string{"X-AIHub-Tag": "second"})
	return o
}

func TestObserveCallListFiltersSummaryAndPaging(t *testing.T) {
	o := newObserved(t)
	list := o.get(o.consumer, "/api/calls")
	items := asList(t, list["items"])
	summary := asMap(t, list["summary"])
	if len(items) != 2 || summary["calls"] != float64(2) || summary["succeeded"] != float64(2) || summary["failed"] != float64(0) || summary["success_rate"] != "1.000000" {
		t.Fatalf("list = %v", list)
	}
	if summary["charged"] != "0.08008" || summary["input_tokens"] != float64(2000) || summary["output_tokens"] != float64(1000) || summary["total_tokens"] != float64(3000) {
		t.Fatalf("summary = %v", summary)
	}
	if summary["ttft_p50_ms"] == nil || summary["ttft_p95_ms"] == nil {
		t.Fatalf("ttft percentiles missing: %v", summary)
	}
	newest := asMap(t, items[0])
	if newest["id"] != o.succeeded || newest["charged"] != expectedCharge || newest["tag"] != "second" || newest["attempt_count"] == nil {
		t.Fatalf("newest = %v", newest)
	}
	first := asMap(t, items[1])
	if first["id"] != o.fellBack || first["attempt_count"] != float64(2) {
		t.Fatalf("fell back call = %v", first)
	}

	for query, want := range map[string]int{
		"?tag=suite": 1, "?model=gpt-test": 2, "?model=other": 0, "?outcome=succeeded": 2, "?outcome=upstream_failed": 0, "?format=openai_chat": 2, "?format=gemini": 0,
		"?request_id=" + o.succeeded: 1, "?min_tokens=1500": 2, "?min_tokens=1501": 0, "?max_tokens=1499": 0, "?min_cost=0.04": 2, "?min_cost=0.05": 0,
		"?channel_id=" + o.goodID: 2, "?channel_id=" + o.brokenID: 0, "?api_key_id=" + o.keyID: 2, "?min_duration_ms=0": 2, "?max_duration_ms=0": 0,
	} {
		got := o.get(o.consumer, "/api/calls"+query)
		if len(asList(t, got["items"])) != want {
			t.Errorf("%s: %d items, want %d", query, len(asList(t, got["items"])), want)
		}
	}
	// Filters also narrow the summary.
	if filtered := asMap(t, o.get(o.consumer, "/api/calls?tag=suite")["summary"]); filtered["calls"] != float64(1) {
		t.Fatalf("filtered summary = %v", filtered)
	}

	// Keyset paging walks the list without gaps or repeats.
	page := o.get(o.consumer, "/api/calls?limit=1")
	if len(asList(t, page["items"])) != 1 || page["next_cursor"] == nil {
		t.Fatalf("page 1 = %v", page)
	}
	next := o.get(o.consumer, "/api/calls?limit=1&cursor="+url.QueryEscape(page["next_cursor"].(string)))
	if asMap(t, asList(t, next["items"])[0])["id"] != o.fellBack || next["next_cursor"] != nil {
		t.Fatalf("page 2 = %v", next)
	}

	for _, bad := range []string{"?outcome=nope", "?format=nope", "?request_id=x", "?min_tokens=-1", "?min_cost=abc", "?from=yesterday", "?limit=0", "?api_key_id=1"} {
		if recorder := o.api(o.consumer, http.MethodGet, "/api/calls"+bad, nil); recorder.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, recorder.Code)
		}
	}
	if recorder := o.api(o.consumer, http.MethodGet, "/api/calls?cursor=garbage", nil); recorder.Code != http.StatusBadRequest {
		t.Errorf("bad cursor = %d", recorder.Code)
	}
	// Nobody sees the calls of somebody else.
	if mine := o.get(o.outsider, "/api/calls"); len(asList(t, mine["items"])) != 0 || asMap(t, mine["summary"])["calls"] != float64(0) {
		t.Fatalf("outsider list = %v", mine)
	}

	// Administrators list everything and may filter by account.
	all := o.get(o.admin, "/api/admin/calls")
	if len(asList(t, all["items"])) != 2 || asMap(t, asList(t, all["items"])[0])["account"] == nil {
		t.Fatalf("admin list = %v", all)
	}
	if byAccount := o.get(o.admin, "/api/admin/calls?account_id="+o.outsider.id); len(asList(t, byAccount["items"])) != 0 {
		t.Fatalf("admin account filter = %v", byAccount)
	}
	if recorder := o.api(o.consumer, http.MethodGet, "/api/admin/calls", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("member admin list = %d", recorder.Code)
	}
}

func TestObserveCallDetailPermissions(t *testing.T) {
	o := newObserved(t)
	own := asMap(t, o.get(o.consumer, "/api/calls/"+o.fellBack)["call"])
	attempts := asList(t, own["attempts"])
	if len(attempts) != 2 || own["api_key"] == nil || own["tag"] != "suite" || own["price_snapshot"] == nil || own["ledger_transaction_id"] == nil || own["routing_mode"] != "cheapest" {
		t.Fatalf("consumer detail = %v", own)
	}
	failed := asMap(t, attempts[0])
	if failed["status_code"] != float64(500) || failed["end_reason"] != "upstream_error" || !strings.Contains(failed["error_message"].(string), "upstream exploded") {
		t.Fatalf("failed attempt = %v", failed)
	}

	// The owner of the channel that failed sees only their own attempt, no consumer data.
	flakyView := asMap(t, o.get(o.flaky, "/api/calls/"+o.fellBack)["call"])
	flakyAttempts := asList(t, flakyView["attempts"])
	if len(flakyAttempts) != 1 || asMap(t, asMap(t, flakyAttempts[0])["channel"])["id"] != o.brokenID ||
		flakyView["api_key"] != nil || flakyView["tag"] != nil || flakyView["client_user_agent"] != nil || flakyView["routing_mode"] != nil ||
		flakyView["ledger_transaction_id"] != nil || flakyView["channel"] != nil {
		t.Fatalf("failed channel owner detail = %v", flakyView)
	}
	// The owner of the serving channel sees the call and their own attempt only.
	servedView := asMap(t, o.get(o.sharer, "/api/calls/"+o.fellBack)["call"])
	if len(asList(t, servedView["attempts"])) != 1 || asMap(t, servedView["channel"])["id"] != o.goodID || servedView["api_key"] != nil || servedView["tag"] != nil {
		t.Fatalf("serving channel owner detail = %v", servedView)
	}
	// A stranger learns nothing, not even that the call exists.
	for _, path := range []string{"/api/calls/" + o.fellBack, "/api/calls/00000000-0000-4000-8000-000000000000", "/api/calls/not-a-uuid"} {
		if recorder := o.api(o.outsider, http.MethodGet, path, nil); recorder.Code != http.StatusNotFound {
			t.Errorf("outsider %s = %d", path, recorder.Code)
		}
	}
	if admin := asMap(t, o.get(o.admin, "/api/calls/"+o.fellBack)["call"]); len(asList(t, admin["attempts"])) != 2 || admin["tag"] != "suite" {
		t.Fatalf("admin detail = %v", admin)
	}
}

func TestObserveChannelCallsAndStats(t *testing.T) {
	o := newObserved(t)
	// The channel owner lists calls that touched the channel, without consumer identity.
	flaky := o.get(o.flaky, "/api/channels/"+o.brokenID+"/calls")
	flakyItems := asList(t, flaky["items"])
	if len(flakyItems) == 0 || flaky["summary"] != nil {
		t.Fatalf("flaky channel calls = %v", flaky)
	}
	failure := asMap(t, flakyItems[len(flakyItems)-1])
	errorInfo := asMap(t, failure["error"])
	if failure["served"] != false || failure["revenue"] != "0" || errorInfo["status_code"] != float64(500) || !strings.Contains(errorInfo["error_message"].(string), "upstream exploded") {
		t.Fatalf("flaky call = %v", failure)
	}
	for _, forbidden := range []string{"account", "api_key", "tag", "cost", "fee", "charged"} {
		if _, present := failure[forbidden]; present {
			t.Errorf("channel call leaks %q: %v", forbidden, failure)
		}
	}
	good := asList(t, o.get(o.sharer, "/api/channels/"+o.goodID+"/calls")["items"])
	if len(good) != 2 || asMap(t, good[0])["served"] != true || asMap(t, good[0])["revenue"] != expectedCost || asMap(t, good[0])["error"] != nil {
		t.Fatalf("good channel calls = %v", good)
	}
	if got := asList(t, o.get(o.sharer, "/api/channels/"+o.goodID+"/calls?outcome=upstream_failed")["items"]); len(got) != 0 {
		t.Fatalf("outcome filter = %v", got)
	}
	// Only the owner or an administrator may look.
	for _, p := range []person{o.outsider, o.consumer} {
		for _, path := range []string{"/calls", "/stats", "/calls/stream"} {
			if recorder := o.api(p, http.MethodGet, "/api/channels/"+o.goodID+path, nil); recorder.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d", p.username, path, recorder.Code)
			}
		}
	}
	if recorder := o.api(o.admin, http.MethodGet, "/api/channels/"+o.goodID+"/calls", nil); recorder.Code != http.StatusOK {
		t.Fatalf("admin channel calls = %d", recorder.Code)
	}

	stats := o.get(o.sharer, "/api/channels/"+o.goodID+"/stats")
	if stats["calls"] != float64(2) || stats["succeeded"] != float64(2) || stats["success_rate"] != "1.000000" || stats["revenue"] != "0.08" {
		t.Fatalf("good stats = %v", stats)
	}
	if asMap(t, stats["last_24h"])["calls"] != float64(2) || asMap(t, stats["last_7d"])["succeeded"] != float64(2) || stats["ttft_p95_ms"] == nil {
		t.Fatalf("windows = %v", stats)
	}
	if hourly := asList(t, stats["hourly"]); len(hourly) != 24 {
		t.Fatalf("hourly = %d buckets", len(hourly))
	}
	if today := asMap(t, stats["today"]); today["revenue"] != "0.08" || today["daily_cap"] != nil || today["progress"] != nil {
		t.Fatalf("today = %v", today)
	}
	if models := asList(t, stats["by_model"]); len(models) != 1 || asMap(t, models[0])["model_id"] != "gpt-test" || asMap(t, models[0])["revenue"] != "0.08" {
		t.Fatalf("by model = %v", models)
	}
	if stats["recent_failures"] == nil || len(asList(t, stats["recent_failures"])) != 0 {
		t.Fatalf("good channel failures = %v", stats["recent_failures"])
	}

	broken := o.get(o.flaky, "/api/channels/"+o.brokenID+"/stats")
	if broken["calls"] != float64(2) || broken["succeeded"] != float64(0) || broken["success_rate"] != "0.000000" || broken["revenue"] != "0" {
		t.Fatalf("broken stats = %v", broken)
	}
	codes := asList(t, broken["status_codes"])
	failures := asList(t, broken["recent_failures"])
	if len(codes) != 1 || asMap(t, codes[0])["status_code"] != float64(500) || asMap(t, codes[0])["count"] != float64(2) {
		t.Fatalf("status codes = %v", codes)
	}
	if len(failures) != 2 || asMap(t, failures[1])["call_id"] != o.fellBack || !strings.Contains(asMap(t, failures[0])["error_message"].(string), "upstream exploded") {
		t.Fatalf("failures = %v", failures)
	}
	for _, bad := range []string{"?from=2026-01-01T00:00:00Z&to=2025-01-01T00:00:00Z", "?from=nope"} {
		if recorder := o.api(o.sharer, http.MethodGet, "/api/channels/"+o.goodID+"/stats"+bad, nil); recorder.Code != http.StatusBadRequest {
			t.Errorf("stats %s = %d", bad, recorder.Code)
		}
	}
}

func TestObserveUsageSpendAndRevenue(t *testing.T) {
	o := newObserved(t)
	byDay := o.get(o.consumer, "/api/usage")
	if byDay["view"] != "spend" || byDay["group_by"] != "day" {
		t.Fatalf("usage = %v", byDay)
	}
	days := asList(t, byDay["items"])
	today := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
	if len(days) != 30 || asMap(t, days[29])["key"] != today || asMap(t, days[29])["calls"] != float64(2) || asMap(t, days[0])["calls"] != float64(0) {
		t.Fatalf("daily rows = %v", days)
	}
	total := asMap(t, byDay["total"])
	if total["calls"] != float64(2) || total["charged"] != "0.08008" || total["input_tokens"] != float64(2000) {
		t.Fatalf("total = %v", total)
	}
	byModel := asList(t, o.get(o.consumer, "/api/usage?group_by=model")["items"])
	if len(byModel) != 1 || asMap(t, byModel[0])["key"] != "gpt-test" || asMap(t, byModel[0])["charged"] != "0.08008" {
		t.Fatalf("by model = %v", byModel)
	}
	byKey := asList(t, o.get(o.consumer, "/api/usage?group_by=key")["items"])
	if len(byKey) != 1 || asMap(t, byKey[0])["key"] != o.keyID || asMap(t, byKey[0])["label"] != "默认 Key" {
		t.Fatalf("by key = %v", byKey)
	}
	byTag := asList(t, o.get(o.consumer, "/api/usage?group_by=tag")["items"])
	if len(byTag) != 2 {
		t.Fatalf("by tag = %v", byTag)
	}
	if filtered := asMap(t, o.get(o.consumer, "/api/usage?group_by=tag&tag=suite")["total"]); filtered["calls"] != float64(1) {
		t.Fatalf("tag filter = %v", filtered)
	}

	// The sharer sees the income side: per channel, model and day.
	revenueByChannel := o.get(o.sharer, "/api/usage?view=revenue&group_by=channel")
	rows := asList(t, revenueByChannel["items"])
	if revenueByChannel["view"] != "revenue" || len(rows) != 1 || asMap(t, rows[0])["key"] != o.goodID || asMap(t, rows[0])["label"] != "good-relay" || asMap(t, rows[0])["charged"] != "0.08" {
		t.Fatalf("revenue by channel = %v", revenueByChannel)
	}
	if byModelRevenue := asList(t, o.get(o.sharer, "/api/usage?view=revenue&group_by=model")["items"]); len(byModelRevenue) != 1 || asMap(t, byModelRevenue[0])["charged"] != "0.08" {
		t.Fatalf("revenue by model = %v", byModelRevenue)
	}
	if none := asMap(t, o.get(o.consumer, "/api/usage?view=revenue")["total"]); none["calls"] != float64(0) || none["charged"] != "0" {
		t.Fatalf("consumer revenue = %v", none)
	}
	for _, bad := range []string{"?group_by=channel", "?view=revenue&group_by=key", "?group_by=week", "?view=profit", "?from=2020-01-01T00:00:00Z&to=2026-01-01T00:00:00Z"} {
		if recorder := o.api(o.consumer, http.MethodGet, "/api/usage"+bad, nil); recorder.Code != http.StatusBadRequest {
			t.Errorf("usage %s = %d", bad, recorder.Code)
		}
	}
}

func TestObservePointsReconcileTrendAndEntries(t *testing.T) {
	o := newObserved(t)
	o.adjust(o.consumer, "5")
	points := o.get(o.consumer, "/api/points")
	period := asMap(t, points["period"])
	if period["difference"] != "0" || period["call_spend"] != "-0.08008" || period["adjustments"] != "5" || period["opening_balance"] != "0" || period["closing_balance"] != "4.91992" {
		t.Fatalf("consumer period = %v", period)
	}
	if period["income"] != "5" || period["spend"] != "0.08008" || period["channel_income"] != "0" {
		t.Fatalf("income/spend = %v", period)
	}
	trend := asList(t, points["trend"])
	if len(trend) != 30 || asMap(t, trend[29])["balance"] != "4.91992" || asMap(t, trend[0])["balance"] != "0" {
		t.Fatalf("trend = %v", trend)
	}
	sharer := o.get(o.sharer, "/api/points")
	if period := asMap(t, sharer["period"]); period["channel_income"] != "0.08" || period["difference"] != "0" {
		t.Fatalf("sharer period = %v", sharer["period"])
	}
	// An explicit past window reconciles too, and an inverted window is refused.
	past := o.get(o.consumer, "/api/points?from=2020-01-01T00:00:00Z&to=2020-02-01T00:00:00Z")
	if asMap(t, past["period"])["closing_balance"] != "0" || asMap(t, past["period"])["difference"] != "0" {
		t.Fatalf("past period = %v", past["period"])
	}
	if recorder := o.api(o.consumer, http.MethodGet, "/api/points?from=2026-02-01T00:00:00Z&to=2026-01-01T00:00:00Z", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("inverted period = %d", recorder.Code)
	}

	byDay := asMap(t, o.get(o.consumer, "/api/points/entries?group=day")["summary"])
	days := asList(t, byDay["by_day"])
	if len(days) != 1 || asMap(t, days[0])["income"] != "5" || asMap(t, days[0])["spend"] != "0.08008" || asMap(t, days[0])["net"] != "4.91992" {
		t.Fatalf("by day = %v", byDay)
	}
	byKey := asList(t, asMap(t, o.get(o.consumer, "/api/points/entries?group=key")["summary"])["by_key"])
	if len(byKey) != 1 || asMap(t, asMap(t, byKey[0])["api_key"])["id"] != o.keyID || asMap(t, byKey[0])["spend"] != "0.08008" || asMap(t, byKey[0])["entries"] != float64(2) {
		t.Fatalf("by key = %v", byKey)
	}
	if filtered := asList(t, o.get(o.consumer, "/api/points/entries?type=admin_adjust")["items"]); len(filtered) != 1 {
		t.Fatalf("type filter = %v", filtered)
	}
	if byKeyID := asList(t, o.get(o.consumer, "/api/points/entries?api_key_id="+o.keyID)["items"]); len(byKeyID) != 2 {
		t.Fatalf("key filter = %v", byKeyID)
	}
	if recorder := o.api(o.consumer, http.MethodGet, "/api/points/entries?type=bogus", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad type = %d", recorder.Code)
	}

	export := o.api(o.consumer, http.MethodGet, "/api/points/entries?format=csv", nil)
	body := export.Body.String()
	if export.Code != http.StatusOK || !strings.HasPrefix(export.Header().Get("Content-Type"), "text/csv") || !strings.HasPrefix(body, "\ufeff时间,类型,说明,关联,变动,余额,Key\n") {
		t.Fatalf("export = %d %q", export.Code, body)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 4 || !strings.Contains(body, "调用支出") || !strings.Contains(body, "管理员调账") || !strings.Contains(body, ",-0.04004,") || !strings.Contains(body, "默认 Key") {
		t.Fatalf("export rows = %q", body)
	}
	if !strings.Contains(sharerCSV(o), "渠道收入") {
		t.Fatal("sharer export lacks 渠道收入")
	}
	o.assertZeroSum()
}

func sharerCSV(o *observed) string {
	return o.api(o.sharer, http.MethodGet, "/api/points/entries?format=csv", nil).Body.String()
}

func TestObserveAdminPointsChecksPassOnHealthyData(t *testing.T) {
	o := newObserved(t)
	// A C2C round, so the escrow and the trades checks have something to count.
	o.adjust(o.sharer, "50")
	order := asMap(t, o.apiJSON(o.sharer, http.StatusCreated, http.MethodPost, "/api/c2c/orders", map[string]any{
		"amount": "30", "unit_price_fen": 92, "payment_methods": []any{map[string]string{"channel": "支付宝", "account": "a@example.com"}},
	})["order"])
	trade := asMap(t, o.apiJSON(o.outsider, http.StatusCreated, http.MethodPost, "/api/c2c/orders/"+order["id"].(string)+"/trades", map[string]string{"amount": "10"})["trade"])
	report := o.get(o.admin, "/api/admin/points")
	checks := asMap(t, report["checks"])
	balances := asMap(t, report["balances"])
	if checks["all_passed"] != true || asMap(t, report["check"])["balanced"] != true || asMap(t, report["check"])["total"] != "0" {
		t.Fatalf("checks = %v", checks)
	}
	if balances["c2c_escrow"] != "30" || balances["escrow_orders"] != float64(1) || balances["escrow_trades_in_progress"] != float64(1) || balances["total"] != "0" {
		t.Fatalf("balances = %v", balances)
	}
	if asMap(t, checks["escrow"])["difference"] != "0" || asMap(t, checks["billing_calls"])["missing_count"] != float64(0) {
		t.Fatalf("checks = %v", checks)
	}
	if balances["credit_issued"] != "0.08008" || balances["user_negative"] != "-0.08008" || balances["total_credit_limit"] != "100" {
		t.Fatalf("credit = %v", balances)
	}
	trend := asList(t, report["trend"])
	if len(trend) != 30 {
		t.Fatalf("trend days = %d", len(trend))
	}
	last := asMap(t, trend[len(trend)-1])
	if last["c2c_escrow"] != "30" || last["credit_issued"] != "0.08008" || last["api_volume"] != "0.08008" || last["api_fee"] != "0.00008" || last["c2c_volume"] != "0" || last["c2c_avg_price_fen"] != nil {
		t.Fatalf("last day = %v", last)
	}
	if asMap(t, trend[0])["circulation"] != "0" {
		t.Fatalf("first day = %v", trend[0])
	}

	// Release the trade: volume and the weighted average price appear on the trend.
	o.apiJSON(o.outsider, http.StatusOK, http.MethodPost, "/api/c2c/trades/"+trade["id"].(string)+"/paid", map[string]string{})
	o.apiJSON(o.sharer, http.StatusOK, http.MethodPost, "/api/c2c/trades/"+trade["id"].(string)+"/release", map[string]string{})
	for _, days := range []string{"7", "30", "90"} {
		report = o.get(o.admin, "/api/admin/points?days="+days)
		if want := map[string]int{"7": 7, "30": 30, "90": 90}[days]; len(asList(t, report["trend"])) != want {
			t.Errorf("days=%s -> %d points", days, len(asList(t, report["trend"])))
		}
	}
	last = asMap(t, asList(t, report["trend"])[89])
	if last["c2c_volume"] != "10" || last["c2c_avg_price_fen"] != float64(92) || last["c2c_escrow"] != "20" {
		t.Fatalf("c2c day = %v", last)
	}
	if recorder := o.api(o.admin, http.MethodGet, "/api/admin/points?days=14", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("days=14 = %d", recorder.Code)
	}
	if asMap(t, o.get(o.admin, "/api/admin/points")["checks"])["all_passed"] != true {
		t.Fatal("checks fail after a C2C round")
	}
	concentration := asMap(t, report["concentration"])
	if concentration["top5_share"] != "1.000000" || len(asList(t, concentration["top"])) != 2 {
		t.Fatalf("concentration = %v", concentration)
	}
}

// breakLedger runs an SQL statement that makes the books inconsistent on
// purpose. The immutability triggers guard entries and transactions, but the
// derived columns can be tampered with, which is what the checks must catch.
func (e *env) breakLedger(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func TestObserveChecksReportEachInjectedInconsistency(t *testing.T) {
	o := newObserved(t)
	checks := func() map[string]any { return asMap(t, o.get(o.admin, "/api/admin/points")["checks"]) }
	if checks()["all_passed"] != true {
		t.Fatal("baseline checks fail")
	}

	// ② a balance that no longer equals its entries; ① follows because the total moves too.
	o.breakLedger(`UPDATE ledger_accounts SET balance_nano = balance_nano + 5 WHERE account_id = $1`, o.sharer.id)
	report := o.get(o.admin, "/api/admin/points")
	mismatches := asList(t, asMap(t, asMap(t, report["checks"])["account_balances"])["mismatches"])
	if asMap(t, report["check"])["balanced"] != false || asMap(t, report["check"])["total"] != "0.000000005" || len(mismatches) != 1 ||
		asMap(t, asMap(t, asMap(t, mismatches[0])["ledger_account"])["account"])["id"] != o.sharer.id || asMap(t, mismatches[0])["balance"] != "0.080000005" || asMap(t, mismatches[0])["entries_total"] != "0.08" {
		t.Fatalf("balance tamper = %v", report)
	}
	o.breakLedger(`UPDATE ledger_accounts SET balance_nano = balance_nano - 5 WHERE account_id = $1`, o.sharer.id)
	if checks()["all_passed"] != true {
		t.Fatal("checks fail after restoring the balance")
	}

	// ③ escrow differing from the open orders (moved together with a user so ① stays 0).
	o.breakLedger(`UPDATE ledger_accounts SET balance_nano = balance_nano + 7 WHERE system_code = 'c2c_escrow'`)
	o.breakLedger(`UPDATE ledger_accounts SET balance_nano = balance_nano - 7 WHERE account_id = $1`, o.sharer.id)
	report = o.get(o.admin, "/api/admin/points")
	escrow := asMap(t, asMap(t, report["checks"])["escrow"])
	if asMap(t, report["check"])["balanced"] != true || escrow["passed"] != false || escrow["difference"] != "0.000000007" ||
		len(asList(t, asMap(t, asMap(t, report["checks"])["account_balances"])["mismatches"])) != 2 {
		t.Fatalf("escrow tamper = %v", report["checks"])
	}
	o.breakLedger(`UPDATE ledger_accounts SET balance_nano = balance_nano - 7 WHERE system_code = 'c2c_escrow'`)
	o.breakLedger(`UPDATE ledger_accounts SET balance_nano = balance_nano + 7 WHERE account_id = $1`, o.sharer.id)

	// ④ a billed call that lost its ledger link.
	o.breakLedger(`UPDATE calls SET ledger_tx_id = NULL WHERE id = $1`, o.succeeded)
	report = o.get(o.admin, "/api/admin/points")
	billing := asMap(t, asMap(t, report["checks"])["billing_calls"])
	missing := asList(t, billing["missing"])
	if billing["passed"] != false || billing["missing_count"] != float64(1) || len(missing) != 1 || asMap(t, missing[0])["call_id"] != o.succeeded ||
		asMap(t, missing[0])["charged"] != expectedCharge || asMap(t, asMap(t, missing[0])["account"])["username"] != "consumer" || asMap(t, asMap(t, missing[0])["channel"])["id"] != o.goodID {
		t.Fatalf("missing call = %v", billing)
	}
	// ⑤ a released trade without a ledger transaction.
	o.breakLedger(`INSERT INTO c2c_orders (id, seller_id, total_nano, available_nano, closed_nano, unit_price_fen, min_per_trade_nano,
		payment_methods_ciphertext, payment_methods_nonce, payment_methods_key_id, status)
		VALUES ('11111111-1111-4111-8111-111111111111', $1, 1000000000, 0, 1000000000, 100, 1, '\x00', '\x00', 'k1', 'closed')`, o.sharer.id)
	o.breakLedger(`INSERT INTO c2c_trades (id, order_id, buyer_id, seller_id, amount_nano, unit_price_fen, total_fen, status, payment_deadline, released_at)
		VALUES ('22222222-2222-4222-8222-222222222222', '11111111-1111-4111-8111-111111111111', $1, $2, 1000000000, 100, 100, 'released', now(), now())`, o.outsider.id, o.sharer.id)
	report = o.get(o.admin, "/api/admin/points")
	trades := asMap(t, asMap(t, report["checks"])["released_trades"])
	if trades["passed"] != false || trades["missing_count"] != float64(1) || asMap(t, asList(t, trades["missing"])[0])["trade_id"] != "22222222-2222-4222-8222-222222222222" ||
		asMap(t, report["checks"])["all_passed"] != false {
		t.Fatalf("missing trade = %v", trades)
	}

	// The overview names the failures.
	overview := o.get(o.admin, "/api/admin/overview")
	kinds := map[string]bool{}
	reconcileTitle := ""
	for _, item := range asList(t, overview["attention"]) {
		kind := asMap(t, item)["kind"].(string)
		kinds[kind] = true
		if kind == "reconciliation_failed" {
			reconcileTitle = asMap(t, item)["title"].(string)
		}
	}
	if !kinds["reconciliation_failed"] || kinds["ledger_unbalanced"] || asMap(t, overview["ledger"])["balanced"] != true {
		t.Fatalf("overview attention = %v", overview["attention"])
	}
	// Administrators read check names, not metric label values.
	if strings.Contains(reconcileTitle, "_") || !strings.Contains(reconcileTitle, "已放行交易漏记账") {
		t.Fatalf("reconciliation title = %q", reconcileTitle)
	}
}

func TestObserveRepairBooksAMissedCallExactlyOnce(t *testing.T) {
	o := newObserved(t)
	// Forget a booking entirely: delete the ledger link and book a fresh orphan call.
	orphan := o.insertUnbookedCall(o.consumer, o.goodID, "succeeded", 40_000_000, 40_000)
	before := o.balance(o.consumer)
	if recorder := o.api(o.consumer, http.MethodPost, "/api/admin/ledger/repair-call/"+orphan, map[string]string{"action": "charge", "reason": "x"}); recorder.Code != http.StatusForbidden {
		t.Fatalf("member repair = %d", recorder.Code)
	}
	repaired := o.apiJSON(o.admin, http.StatusOK, http.MethodPost, "/api/admin/ledger/repair-call/"+orphan, map[string]string{"action": "charge", "reason": "记账失败补记"})
	transactionID := repaired["transaction_id"].(string)
	if transactionID == "" || asMap(t, repaired["call"])["ledger_transaction_id"] != transactionID || asMap(t, repaired["call"])["charged"] != "0.04004" {
		t.Fatalf("repair = %v", repaired)
	}
	if o.balance(o.consumer) == before || o.balance(o.consumer) != "-0.12012" || o.balance(o.sharer) != "0.12" {
		t.Fatalf("balances after repair: consumer %s sharer %s", o.balance(o.consumer), o.balance(o.sharer))
	}
	o.assertZeroSum()
	// A second repair changes nothing.
	if recorder := o.api(o.admin, http.MethodPost, "/api/admin/ledger/repair-call/"+orphan, map[string]string{"action": "charge", "reason": "again"}); recorder.Code != http.StatusConflict {
		t.Fatalf("second repair = %d %s", recorder.Code, recorder.Body.String())
	}
	var transactions int
	_ = o.pool.QueryRow(context.Background(), `SELECT count(*) FROM ledger_transactions WHERE idempotency_key = $1`, "call:"+orphan).Scan(&transactions)
	if transactions != 1 || o.balance(o.consumer) != "-0.12012" {
		t.Fatalf("second repair moved points: %d transactions, balance %s", transactions, o.balance(o.consumer))
	}
	var audited int
	_ = o.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'ledger.repair_call' AND target_id = $1 AND reason = '记账失败补记'`, orphan).Scan(&audited)
	if audited != 1 {
		t.Fatalf("audit rows = %d", audited)
	}

	// Voiding marks the call as an unbilled interruption.
	voided := o.insertUnbookedCall(o.consumer, o.goodID, "succeeded", 40_000_000, 40_000)
	result := o.apiJSON(o.admin, http.StatusOK, http.MethodPost, "/api/admin/ledger/repair-call/"+voided, map[string]string{"action": "void", "reason": "无法核实"})
	if result["transaction_id"] != nil || asMap(t, result["call"])["outcome"] != "interrupted" || asMap(t, result["call"])["cost"] != "0" {
		t.Fatalf("void = %v", result)
	}
	// Calls that are already booked, unknown or unbillable cannot be repaired.
	booked := o.apiJSON(o.admin, http.StatusConflict, http.MethodPost, "/api/admin/ledger/repair-call/"+o.succeeded, map[string]string{"action": "charge", "reason": "x"})
	if booked["error"] != "not_repairable" {
		t.Fatalf("booked repair = %v", booked)
	}
	for path, status := range map[string]int{"00000000-0000-4000-8000-000000000000": http.StatusNotFound, "nope": http.StatusNotFound} {
		if recorder := o.api(o.admin, http.MethodPost, "/api/admin/ledger/repair-call/"+path, map[string]string{"action": "charge", "reason": "x"}); recorder.Code != status {
			t.Errorf("repair %s = %d", path, recorder.Code)
		}
	}
	for _, body := range []map[string]string{{"action": "charge", "reason": ""}, {"action": "refund", "reason": "x"}} {
		if recorder := o.api(o.admin, http.MethodPost, "/api/admin/ledger/repair-call/"+orphan, body); recorder.Code != http.StatusBadRequest {
			t.Errorf("invalid repair %v = %d", body, recorder.Code)
		}
	}
}

// insertUnbookedCall writes a finished call whose booking never happened.
func (o *observed) insertUnbookedCall(account person, channelID, outcome string, cost, fee int64) string {
	o.t.Helper()
	var id string
	err := o.pool.QueryRow(context.Background(), `
		INSERT INTO calls (account_id, requested_model, model_id, format, outcome, final_channel_id, cost_nano, fee_nano, input_tokens, output_tokens, completed_at)
		VALUES ($1, 'gpt-test', 'gpt-test', 'openai_chat', $2, $3, $4, $5, 1000, 500, now()) RETURNING id`, account.id, outcome, channelID, cost, fee).Scan(&id)
	if err != nil {
		o.t.Fatal(err)
	}
	return id
}

func TestObserveOwnChannelCallsAreNotMissingBookings(t *testing.T) {
	e := newEnv(t)
	owner := e.member("owner", "10")
	relay := okUpstream(t)
	channelID := e.createChannel(owner, channelSpec{name: "mine", upstream: relay})
	_, secret := e.defaultKey(owner)
	id := e.chat(secret, nil)
	if row := e.callRow(id); row.HasLedgerTx || row.CostNano != 40_000_000 {
		t.Fatalf("own call = %+v", row)
	}
	checks := asMap(t, e.get(e.admin, "/api/admin/points")["checks"])
	if checks["all_passed"] != true || asMap(t, checks["billing_calls"])["missing_count"] != float64(0) {
		t.Fatalf("own-channel call flagged: %v", checks)
	}
	// Nothing was debited, so it neither counts as spend nor as revenue, and it cannot be repaired.
	list := e.get(owner, "/api/calls")
	item := asMap(t, asList(t, list["items"])[0])
	if item["charged"] != "0" || item["cost"] != expectedCost || asMap(t, list["summary"])["charged"] != "0" {
		t.Fatalf("own call listing = %v", list)
	}
	if revenue := asMap(t, e.get(owner, "/api/usage?view=revenue")["total"]); revenue["calls"] != float64(1) || revenue["charged"] != "0" {
		t.Fatalf("own revenue = %v", revenue)
	}
	if channelStats := e.get(owner, "/api/channels/"+channelID+"/stats"); channelStats["revenue"] != "0" {
		t.Fatalf("own channel stats = %v", channelStats)
	}
	if recorder := e.api(e.admin, http.MethodPost, "/api/admin/ledger/repair-call/"+id, map[string]string{"action": "charge", "reason": "x"}); recorder.Code != http.StatusConflict {
		t.Fatalf("own call repair = %d", recorder.Code)
	}
}

func TestObserveLedgerTransactionBrowse(t *testing.T) {
	o := newObserved(t)
	o.adjust(o.consumer, "5")
	list := o.get(o.admin, "/api/admin/ledger/transactions")
	items := asList(t, list["items"])
	if len(items) != 3 {
		t.Fatalf("transactions = %v", list)
	}
	byType := o.get(o.admin, "/api/admin/ledger/transactions?type=admin_adjust")
	adjustment := asMap(t, asList(t, byType["items"])[0])
	if len(asList(t, byType["items"])) != 1 || asMap(t, adjustment["related"])["type"] != "account" || adjustment["related_summary"] != "账户 · consumer" || asMap(t, adjustment["actor"])["username"] != "founder" {
		t.Fatalf("adjustment = %v", byType)
	}
	for _, entry := range asList(t, adjustment["entries"]) {
		item := asMap(t, entry)
		if item["balance_before"] == nil || item["balance_after"] == nil {
			t.Fatalf("entry = %v", item)
		}
	}
	byUser := asList(t, o.get(o.admin, "/api/admin/ledger/transactions?account_id="+o.sharer.id)["items"])
	if len(byUser) != 2 {
		t.Fatalf("sharer transactions = %d", len(byUser))
	}
	byRelated := asList(t, o.get(o.admin, "/api/admin/ledger/transactions?related_type=call&related_id="+o.succeeded)["items"])
	if len(byRelated) != 1 || !strings.HasPrefix(asMap(t, byRelated[0])["related_summary"].(string), "gpt-test · good-relay") || len(asList(t, asMap(t, byRelated[0])["entries"])) != 3 {
		t.Fatalf("by related = %v", byRelated)
	}
	if len(asList(t, o.get(o.admin, "/api/admin/ledger/transactions?from=2999-01-01T00:00:00Z")["items"])) != 0 {
		t.Fatal("future window not empty")
	}
	page := o.get(o.admin, "/api/admin/ledger/transactions?limit=2")
	next := o.get(o.admin, "/api/admin/ledger/transactions?limit=2&cursor="+url.QueryEscape(page["next_cursor"].(string)))
	if len(asList(t, page["items"])) != 2 || len(asList(t, next["items"])) != 1 || next["next_cursor"] != nil {
		t.Fatalf("paging = %v / %v", page, next)
	}

	callTransaction := asMap(t, byRelated[0])
	detail := asMap(t, o.get(o.admin, "/api/admin/ledger/transactions/"+callTransaction["id"].(string))["transaction"])
	snapshot := asMap(t, detail["price_snapshot"])
	if snapshot["multiplier"] != "2" || asMap(t, snapshot["base_prices"])["input"] != "10" || detail["recent_actions"] == nil {
		t.Fatalf("detail = %v", detail)
	}
	adjustDetail := asMap(t, o.get(o.admin, "/api/admin/ledger/transactions/"+adjustment["id"].(string))["transaction"])
	actions := asList(t, adjustDetail["recent_actions"])
	if adjustDetail["price_snapshot"] != nil || len(actions) == 0 || asMap(t, actions[0])["action"] != "ledger.adjust" {
		t.Fatalf("adjust detail = %v", adjustDetail)
	}
	for _, bad := range []string{"?type=nope", "?account_id=x", "?related_type=call", "?related_id=" + o.succeeded, "?related_type=trade&related_id=" + o.succeeded} {
		if recorder := o.api(o.admin, http.MethodGet, "/api/admin/ledger/transactions"+bad, nil); recorder.Code != http.StatusBadRequest {
			t.Errorf("transactions %s = %d", bad, recorder.Code)
		}
	}
	for path, status := range map[string]int{"00000000-0000-4000-8000-000000000000": http.StatusNotFound, "x": http.StatusNotFound} {
		if recorder := o.api(o.admin, http.MethodGet, "/api/admin/ledger/transactions/"+path, nil); recorder.Code != status {
			t.Errorf("transaction %s = %d", path, recorder.Code)
		}
	}
	if recorder := o.api(o.consumer, http.MethodGet, "/api/admin/ledger/transactions", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("member browse = %d", recorder.Code)
	}
}

func TestObserveOverviewAttentionRules(t *testing.T) {
	o := newObserved(t)
	healthy := o.get(o.admin, "/api/admin/overview")
	// With a single holder of points, that holder trivially owns more than half of them.
	if attention := asList(t, healthy["attention"]); len(attention) != 1 || asMap(t, attention[0])["kind"] != "credit_concentration" || asMap(t, healthy["ledger"])["balanced"] != true {
		t.Fatalf("healthy overview = %v", healthy)
	}
	today := asMap(t, healthy["today"])
	if today["calls"] != float64(2) || today["succeeded"] != float64(2) || today["success_rate"] != "1.000000" || today["spend"] != "0.08008" || today["fee_revenue"] != "0.00008" {
		t.Fatalf("today = %v", today)
	}
	if last := asMap(t, healthy["last_24h"]); last["calls"] != float64(2) {
		t.Fatalf("24h = %v", last)
	}
	if credit := asMap(t, healthy["credit"]); credit["issued"] != "0.08008" || credit["limit"] != "100" {
		t.Fatalf("credit = %v", credit)
	}
	if c2c := asMap(t, healthy["c2c"]); c2c["trades_24h"] != float64(0) || c2c["volume_24h"] != "0" || c2c["avg_price_fen_24h"] != nil {
		t.Fatalf("c2c = %v", c2c)
	}

	// Over the credit limit, negative for 40 days, and one holder with all the points.
	o.adjust(o.outsider, "-150")
	// Entries are immutable by trigger; age one by lifting the trigger for the statement.
	o.breakLedger(`ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_immutable`)
	o.breakLedger(`UPDATE ledger_entries SET created_at = now() - interval '40 days' WHERE id = (SELECT max(e.id) FROM ledger_entries e JOIN ledger_accounts l ON l.id = e.ledger_account_id WHERE l.account_id = $1)`, o.outsider.id)
	o.breakLedger(`ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_immutable`)
	attention := map[string]map[string]any{}
	for _, item := range asList(t, o.get(o.admin, "/api/admin/overview")["attention"]) {
		attention[asMap(t, item)["kind"].(string)] = asMap(t, item)
	}
	if attention["over_limit"] == nil || attention["negative_balance"] == nil || attention["over_limit"]["severity"] != "critical" || attention["over_limit"]["link"] != "/admin/users" {
		t.Fatalf("attention = %v", attention)
	}
	if attention["credit_concentration"] == nil {
		t.Fatalf("concentration not flagged: %v", attention)
	}
	risks := asList(t, o.get(o.admin, "/api/admin/points")["risks"])
	var sawOver, sawLong bool
	for _, risk := range risks {
		item := asMap(t, risk)
		if asMap(t, item["account"])["username"] != "outsider" {
			continue
		}
		sawOver = sawOver || item["kind"] == "over_limit"
		sawLong = sawLong || (item["kind"] == "negative_long" && item["negative_days"].(float64) >= 39)
	}
	if !sawOver || !sawLong {
		t.Fatalf("risks = %v", risks)
	}

	// An open dispute and an abnormal share of calls whose usage was not read.
	o.breakLedger(`INSERT INTO c2c_orders (id, seller_id, total_nano, available_nano, closed_nano, unit_price_fen, min_per_trade_nano,
		payment_methods_ciphertext, payment_methods_nonce, payment_methods_key_id, status)
		VALUES ('33333333-3333-4333-8333-333333333333', $1, 1000000000, 0, 1000000000, 100, 1, '\x00', '\x00', 'k1', 'closed')`, o.sharer.id)
	o.breakLedger(`INSERT INTO c2c_trades (id, order_id, buyer_id, seller_id, amount_nano, unit_price_fen, total_fen, status, payment_deadline, disputed_at)
		VALUES ('44444444-4444-4444-8444-444444444444', '33333333-3333-4333-8333-333333333333', $1, $2, 1000000000, 100, 100, 'disputed', now(), now())`, o.consumer.id, o.sharer.id)
	for index := 0; index < 21; index++ {
		o.breakLedger(`INSERT INTO calls (account_id, requested_model, model_id, format, outcome, final_channel_id, completed_at)
			VALUES ($1, 'gpt-test', 'gpt-test', 'openai_chat', 'succeeded_unbilled', $2, now())`, o.consumer.id, o.goodID)
	}
	attention = map[string]map[string]any{}
	for _, item := range asList(t, o.get(o.admin, "/api/admin/overview")["attention"]) {
		attention[asMap(t, item)["kind"].(string)] = asMap(t, item)
	}
	if attention["dispute"] == nil || attention["dispute"]["count"] != float64(1) || attention["unbilled_usage"] == nil || attention["unbilled_usage"]["count"] != float64(21) {
		t.Fatalf("attention = %v", attention)
	}
	if recorder := o.api(o.consumer, http.MethodGet, "/api/admin/overview", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("member overview = %d", recorder.Code)
	}
}

func TestObserveOverviewFlagsAFailingChannel(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	broken := newUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) { w.WriteHeader(http.StatusBadGateway) })
	channelID := e.createChannel(sharer, channelSpec{name: "shaky", upstream: broken, advanced: map[string]any{"cooldown_failures": 100}})
	_, secret := e.defaultKey(consumer)
	for index := 0; index < 20; index++ {
		e.call(secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"gpt-test"}`), nil)
	}
	var found map[string]any
	for _, item := range asList(t, e.get(e.admin, "/api/admin/overview")["attention"]) {
		if asMap(t, item)["kind"] == "channel_failing" {
			found = asMap(t, item)
		}
	}
	if found == nil || found["link"] != "/admin/channels/"+channelID || !strings.Contains(found["title"].(string), "shaky") || found["count"] != float64(1) {
		t.Fatalf("failing channel not flagged: %v", found)
	}
	// Failed calls count against the platform's success rate and each failed one is listed.
	overview := e.get(e.admin, "/api/admin/overview")
	if last := asMap(t, overview["last_24h"]); last["calls"] != float64(20) || last["succeeded"] != float64(0) || last["success_rate"] != "0.000000" {
		t.Fatalf("24h window = %v", last)
	}
}

func TestObserveScrubsRawErrorsAfterThirtyDays(t *testing.T) {
	o := newObserved(t)
	// Age the fell-back call; the freshly made one must keep its error text.
	o.breakLedger(`UPDATE calls SET created_at = now() - interval '31 days' WHERE id = $1`, o.fellBack)
	recent := o.chat(o.secret, nil)
	_ = recent
	cleared, err := o.observe.ScrubRawErrors(context.Background())
	if err != nil || cleared != 1 {
		t.Fatalf("scrub = %d, %v", cleared, err)
	}
	detail := asMap(t, o.get(o.consumer, "/api/calls/"+o.fellBack)["call"])
	attempt := asMap(t, asList(t, detail["attempts"])[0])
	if attempt["error_message"] != nil || attempt["status_code"] != float64(500) || attempt["end_reason"] != "upstream_error" || len(asList(t, detail["attempts"])) != 2 {
		t.Fatalf("scrubbed attempt = %v", attempt)
	}
	// Running again is a no-op, and an unaged call keeps its text (the second call had no failure).
	if again, err := o.observe.ScrubRawErrors(context.Background()); err != nil || again != 0 {
		t.Fatalf("second scrub = %d, %v", again, err)
	}
	o.breakLedger(`UPDATE calls SET attempts = jsonb_set(attempts, '{0,error_message}', '"kept"') WHERE id = $1`, o.succeeded)
	if again, err := o.observe.ScrubRawErrors(context.Background()); err != nil || again != 0 {
		t.Fatalf("scrub of fresh call = %d, %v", again, err)
	}
	if kept := asMap(t, asList(t, asMap(t, o.get(o.consumer, "/api/calls/"+o.succeeded)["call"])["attempts"])[0]); kept["error_message"] != "kept" {
		t.Fatalf("fresh call lost its error text: %v", kept)
	}
}

func TestObserveAdminAccountsShowLastActivity(t *testing.T) {
	o := newObserved(t)
	accounts := asList(t, o.get(o.admin, "/api/admin/accounts")["items"])
	active := map[string]any{}
	for _, item := range accounts {
		account := asMap(t, item)
		active[account["username"].(string)] = account["last_active_at"]
	}
	if active["consumer"] == nil || active["founder"] == nil {
		t.Fatalf("last_active_at = %v", active)
	}
}

func TestObserveModelDetailShowsDailyCapRemaining(t *testing.T) {
	e := newEnv(t)
	consumer := e.member("consumer", "100")
	sharer := e.member("sharer", "0")
	relay := okUpstream(t)
	e.createChannel(sharer, channelSpec{name: "capped", upstream: relay, multiplier: "1", advanced: map[string]any{"daily_revenue_cap": "0.1"}})
	e.createChannel(sharer, channelSpec{name: "open", upstream: relay, multiplier: "3"})
	_, secret := e.defaultKey(consumer)
	e.chat(secret, nil) // 0.02 of the 0.1 cap
	detail := e.get(consumer, "/api/models/gpt-test")
	remaining := map[string]any{}
	for _, item := range asList(t, detail["channels"]) {
		channel := asMap(t, item)
		remaining[channel["name"].(string)] = channel["daily_cap_remaining"]
	}
	if remaining["capped"] != "0.800000" || remaining["open"] != nil {
		t.Fatalf("daily_cap_remaining = %v", remaining)
	}
}

// ---- live stream and metrics ----

func TestObserveStreamsDeliverStartAndFinishAndRespectScope(t *testing.T) {
	o := newObserved(t)
	server := httptest.NewServer(o.handler)
	defer server.Close()
	open := func(p person, path string) (*http.Response, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.AddCookie(&http.Cookie{Name: "oma_session", Value: p.token})
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		return response, cancel
	}
	mine, cancelMine := open(o.consumer, "/api/calls/stream?model=gpt-test")
	defer cancelMine()
	theirs, cancelTheirs := open(o.outsider, "/api/calls/stream")
	defer cancelTheirs()
	owner, cancelOwner := open(o.sharer, "/api/channels/"+o.goodID+"/calls/stream")
	defer cancelOwner()
	admin, cancelAdmin := open(o.admin, "/api/admin/calls/stream?outcome=succeeded")
	defer cancelAdmin()
	for name, response := range map[string]*http.Response{"mine": mine, "theirs": theirs, "owner": owner, "admin": admin} {
		if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("%s stream = %d %s", name, response.StatusCode, response.Header.Get("Content-Type"))
		}
	}
	if mismatched, cancel := open(o.consumer, "/api/calls/stream?outcome=nope"); mismatched.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad filter = %d", mismatched.StatusCode)
	} else {
		cancel()
	}
	if blocked, cancel := open(o.outsider, "/api/channels/"+o.goodID+"/calls/stream"); blocked.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign channel stream = %d", blocked.StatusCode)
	} else {
		cancel()
	}

	id := o.chat(o.secret, map[string]string{"X-AIHub-Tag": "live"})
	readEvents := func(response *http.Response, want int) []map[string]any {
		type result struct{ events []map[string]any }
		done := make(chan result, 1)
		go func() {
			var events []map[string]any
			var kind string
			buffer := make([]byte, 0, 4096)
			chunk := make([]byte, 1024)
			for len(events) < want {
				n, err := response.Body.Read(chunk)
				buffer = append(buffer, chunk[:n]...)
				for {
					end := strings.Index(string(buffer), "\n\n")
					if end < 0 {
						break
					}
					block := string(buffer[:end])
					buffer = buffer[end+2:]
					for _, line := range strings.Split(block, "\n") {
						switch {
						case strings.HasPrefix(line, "event: "):
							kind = strings.TrimPrefix(line, "event: ")
						case strings.HasPrefix(line, "data: "):
							var data map[string]any
							_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data)
							data["_event"] = kind
							events = append(events, data)
						}
					}
				}
				if err != nil {
					break
				}
			}
			done <- result{events}
		}()
		select {
		case got := <-done:
			return got.events
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %d events", want)
			return nil
		}
	}
	events := readEvents(mine, 2)
	if events[0]["_event"] != "call.started" || events[0]["outcome"] != "in_progress" || events[0]["id"] != id ||
		events[1]["_event"] != "call.finished" || events[1]["outcome"] != "succeeded" || events[1]["tag"] != "live" || events[1]["charged"] != expectedCharge {
		t.Fatalf("consumer events = %v", events)
	}
	// A channel's stream only carries finished calls: before a call ends nobody knows which channels it will touch.
	ownerEvents := readEvents(owner, 1)
	if ownerEvents[0]["_event"] != "call.finished" || ownerEvents[0]["served"] != true || ownerEvents[0]["revenue"] != expectedCost || ownerEvents[0]["tag"] != nil {
		t.Fatalf("owner events = %v", ownerEvents)
	}
	if _, leaked := ownerEvents[0]["account"]; leaked {
		t.Fatalf("owner stream leaks the consumer: %v", ownerEvents[0])
	}
	adminEvents := readEvents(admin, 1) // the outcome filter drops the in-progress start
	if adminEvents[0]["_event"] != "call.finished" || adminEvents[0]["account"] == nil {
		t.Fatalf("admin events = %v", adminEvents)
	}
	// The outsider's stream never receives the consumer's calls.
	extra := make(chan struct{})
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := theirs.Body.Read(buffer)
			if strings.Contains(string(buffer[:n]), "call.") || err != nil {
				close(extra)
				return
			}
		}
	}()
	select {
	case <-extra:
		t.Fatal("outsider stream received a call event")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestObserveMetricsExposeCoreSeriesWithoutUserLabels(t *testing.T) {
	o := newObserved(t)
	time.Sleep(300 * time.Millisecond) // the feed handles events asynchronously
	if err := o.metrics.Refresh(context.Background(), o.observe); err != nil {
		t.Fatal(err)
	}
	// An unknown, user-chosen model name must not become a label value.
	o.call(o.secret, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"definitely-not-in-the-catalog-alice-secret"}`), nil)
	time.Sleep(300 * time.Millisecond)
	recorder := httptest.NewRecorder()
	o.metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics = %d", recorder.Code)
	}
	for _, want := range []string{
		`aihub_requests_total{format="openai_chat",model="gpt-test",outcome="succeeded"} 2`,
		`aihub_requests_total{format="openai_chat",model="other",outcome="rejected_model"} 1`,
		`aihub_tokens_total{kind="input",model="gpt-test"} 2000`,
		`aihub_tokens_total{kind="output",model="gpt-test"} 1000`,
		`aihub_cost_points_total{model="gpt-test"} 0.08008`,
		`aihub_upstream_attempts_total{channel="` + o.brokenID + `",result="error",status_class="5xx"} 2`,
		`aihub_upstream_attempts_total{channel="` + o.goodID + `",result="success",status_class="2xx"} 2`,
		`aihub_first_token_latency_seconds_count{format="openai_chat",model="gpt-test"} 2`,
		`aihub_upstream_latency_seconds_count{format="openai_chat",model="gpt-test"} 4`,
		`aihub_channel_up{channel="` + o.goodID + `"} 1`,
		`aihub_points_circulating 0`,
		`aihub_points_credit_issued 0.08008`,
		`aihub_points_platform_revenue 8e-05`,
		`aihub_ledger_imbalance 0`,
		`aihub_ledger_transactions_total{type="api_call"} 2`,
		`aihub_reconciliation_failed{check="zero_sum"} 0`,
		`aihub_reconciliation_failed{check="billing_calls"} 0`,
		`aihub_active_requests`,
		`aihub_settlement_failures_total 0`,
		`aihub_points_escrow 0`,
		`aihub_points_bad_debt 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q", want)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "aihub_") {
			for _, label := range []string{"user=", "account=", "api_key", "key=", "key_id=", "username"} {
				if strings.Contains(line, label) {
					t.Errorf("metric carries a user or key label: %s", line)
				}
			}
		}
	}
	if strings.Contains(body, "alice-secret") {
		t.Fatal("a user-chosen model name leaked into the metrics")
	}
}

func TestObserveMetricsReportInjectedFailures(t *testing.T) {
	o := newObserved(t)
	o.breakLedger(`UPDATE ledger_accounts SET balance_nano = balance_nano + 5 WHERE account_id = $1`, o.sharer.id)
	if err := o.metrics.Refresh(context.Background(), o.observe); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	o.metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	for _, want := range []string{`aihub_ledger_imbalance 5e-09`, `aihub_reconciliation_failed{check="zero_sum"} 1`, `aihub_reconciliation_failed{check="account_balances"} 1`, `aihub_reconciliation_failed{check="escrow"} 0`} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q\n%s", want, body)
		}
	}
	// Hold the channel in cooldown and see it go down on the next scrape.
	for index := 0; index < 3; index++ {
		o.runtime.Failure(o.goodID, gateway.Limits{CooldownAfter: 3, CooldownFor: time.Minute}, time.Now(), "test")
	}
	recorder = httptest.NewRecorder()
	o.metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(recorder.Body.String(), `aihub_channel_up{channel="`+o.goodID+`"} 0`) {
		t.Errorf("channel in cooldown still up")
	}
	if recorder.Code != http.StatusOK || strings.Contains(fmt.Sprint(recorder.Header()), "Set-Cookie") {
		t.Fatalf("metrics response = %d", recorder.Code)
	}
}
