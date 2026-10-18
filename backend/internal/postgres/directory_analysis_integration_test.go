package postgres_test

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestDirectoryQualityAggregationAndVisibility(t *testing.T) {
	o := newObserved(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := o.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	supplierID := "00000000-0000-4000-8000-000000000420"
	exec("INSERT INTO suppliers(id,name,kind,website_url) VALUES($1,'quality fixture','relay','https://example.test')", supplierID)
	exec("INSERT INTO supplier_endpoints(supplier_id,base_url) SELECT $1,base_url FROM channels WHERE id IN ($2,$3)", supplierID, o.brokenID, o.goodID)
	now := time.Now().UTC().Truncate(time.Hour)
	from, to := now.Add(-24*time.Hour), now
	window := "?from=" + url.QueryEscape(from.Format(time.RFC3339)) + "&to=" + url.QueryEscape(to.Format(time.RFC3339))
	path := "/api/suppliers/" + supplierID + "/analysis"
	channelPath := "/api/channel-hall/" + o.brokenID + "/analysis"
	// Unequal per-channel sample sizes: A 0/1, B 3/3. Global 3/4,
	// rather than averaging percentages to 50%. Failures remain on A.
	attempts := []map[string]any{}
	add := func(channel, result string, ttft, duration int) {
		attempts = append(attempts, map[string]any{"channel": map[string]string{"id": channel, "name": "snapshot secret name"}, "end_reason": result, "ttft_ms": ttft, "duration_ms": duration, "status_code": 200, "error_message": "PRIVATE", "error_code": "PRIVATE"})
	}
	add(o.brokenID, "upstream_error", 10, 50)
	add(o.goodID, "completed", 100, 1000)
	add(o.goodID, "completed", 200, 2000)
	add(o.goodID, "client_error", 5, 10)
	add(o.goodID, "client_disconnected", 5, 20)
	add(o.goodID, "completed", 300, 3000)
	attempts[len(attempts)-1]["metrics_version"] = 2
	raw, _ := json.Marshal(attempts)
	exec("UPDATE calls SET attempts=$2,stream=true,created_at=$3,output_tokens_per_second=25,diagnostics='{\"max_frame_gap_ms\":80}' WHERE id=$1", o.fellBack, raw, now.Add(-time.Hour))
	// A previous-period non-streaming success must not enter TTFT/speed, even if old data has fields.
	raw, _ = json.Marshal([]map[string]any{{"channel": map[string]string{"id": o.goodID}, "end_reason": "completed", "ttft_ms": 999, "duration_ms": 400, "status_code": 200}})
	exec("UPDATE calls SET attempts=$2,stream=false,created_at=$3 WHERE id=$1", o.succeeded, raw, from.Add(-time.Hour))
	read := func(path string) map[string]any { return o.get(o.outsider, path) }
	report := read(path + window + "&group_by=channel")
	current := asMap(t, report["current"])
	if current["attempts"] != float64(6) || current["counted"] != float64(4) || current["succeeded"] != float64(3) || current["failed"] != float64(1) || current["excluded"] != float64(2) {
		t.Fatalf("denominators: %v", current)
	}
	if current["ttft_samples"] != float64(3) || current["ttft_p50"] != float64(200) || math.Abs(current["ttft_p95"].(float64)-290) > 0.01 || current["speed_samples"] != float64(1) || current["speed_p50"] != float64(25) || current["gap_samples"] != float64(1) {
		t.Fatalf("measurement attribution: %v", current)
	}
	previous := asMap(t, report["previous"])
	if previous["attempts"] != float64(1) || previous["ttft_p50"] != nil || previous["speed_p50"] != nil {
		t.Fatalf("previous/non-stream: %v", previous)
	}
	if len(asList(t, report["trend"])) != 24 || len(asList(t, report["groups"])) != 2 {
		t.Fatalf("buckets/groups: %v", report)
	}
	broken := asMap(t, read(channelPath + window)["current"])
	if broken["failed"] != float64(1) || broken["succeeded"] != float64(0) || broken["ttft_p50"] != nil || broken["speed_p50"] != nil {
		t.Fatalf("fallback attribution: %v", broken)
	}
	filtered := asMap(t, read(path + window + "&channel=" + o.brokenID + "&result=completed")["current"])
	if filtered["attempts"] != float64(0) || filtered["duration_p50"] != nil {
		t.Fatal("cross-attempt filter/empty percentile")
	}
	for _, suffix := range []string{"&model=missing", "&format=gemini", "&stream=false", "&status=500"} {
		if asMap(t, read(path + window + suffix)["current"])["attempts"] != float64(0) {
			t.Fatalf("ignored filter: %s", suffix)
		}
	}
	for _, group := range []string{"model", "format", "stream", "result", "status"} {
		read(path + window + "&group_by=" + group)
	}
	// Current directory names and public schema, never stored errors or owner data.
	for _, row := range asList(t, report["groups"]) {
		if asMap(t, row)["label"] == "snapshot secret name" {
			t.Fatal("historical snapshot name leaked")
		}
	}
	// Unlisting, disabling owners, deleting channels and hiding suppliers all restrict aggregates.
	exec("UPDATE channels SET status='unlisted' WHERE id=$1", o.brokenID)
	o.apiJSON(o.outsider, 404, http.MethodGet, channelPath+window, nil)
	if asMap(t, read(path + window)["current"])["attempts"] != float64(5) {
		t.Fatal("unlisted channel still counted")
	}
	exec("UPDATE accounts SET status='disabled' WHERE id=$1", o.sharer.id)
	if asMap(t, read(path + window)["current"])["attempts"] != float64(0) {
		t.Fatal("disabled owner still counted")
	}
	exec("UPDATE accounts SET status='active' WHERE id=$1", o.sharer.id)
	exec("UPDATE channels SET deleted_at=now() WHERE id=$1", o.goodID)
	if asMap(t, read(path + window)["current"])["attempts"] != float64(0) {
		t.Fatal("deleted channel still counted")
	}
	exec("UPDATE suppliers SET visible=false WHERE id=$1", supplierID)
	o.apiJSON(o.outsider, 404, http.MethodGet, path+window, nil)
}

func TestDirectoryCacheQualityUsesMeasuredFinalAttempts(t *testing.T) {
	o := newObserved(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := o.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// The two final attempts have unequal input sizes. 900 / (1000 + 2000)
	// is 30%, not the average of channel/request percentages (45%).
	exec("UPDATE calls SET input_tokens=100,cache_read_tokens=900,cache_write_tokens=0,diagnostics=jsonb_set(coalesce(diagnostics,'{}'),'{usage_status}','\"reported\"') WHERE id=$1", o.fellBack)
	exec("UPDATE calls SET input_tokens=1900,cache_read_tokens=0,cache_write_tokens=100,diagnostics=jsonb_set(coalesce(diagnostics,'{}'),'{usage_status}','\"reported\"') WHERE id=$1", o.succeeded)
	window := "?from=" + url.QueryEscape(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)) + "&to=" + url.QueryEscape(time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	read := func(id string) map[string]any {
		return asMap(t, o.get(o.outsider, "/api/channel-hall/"+id+"/analysis"+window)["current"])
	}
	v := read(o.goodID)
	if v["cache_samples"] != float64(2) || v["cache_read_ratio"] != float64(30) || v["cache_hit_rate"] != float64(50) {
		t.Fatalf("weighted cache=%v", v)
	}
	broken := read(o.brokenID)
	if broken["cache_samples"] != float64(0) || broken["cache_read_ratio"] != nil || broken["cache_hit_rate"] != nil {
		t.Fatalf("cache credited to failed fallback=%v", broken)
	}
	exec("UPDATE calls SET diagnostics=jsonb_set(diagnostics,'{usage_status}','\"missing\"') WHERE id=$1", o.succeeded)
	v = read(o.goodID)
	if v["cache_samples"] != float64(1) || v["cache_read_ratio"] != float64(90) || v["cache_hit_rate"] != float64(100) {
		t.Fatalf("missing usage counted as miss=%v", v)
	}
	exec("UPDATE calls SET input_tokens=0,cache_read_tokens=0,cache_write_tokens=0 WHERE id=$1", o.fellBack)
	v = read(o.goodID)
	if v["cache_samples"] != float64(0) || v["cache_hit_rate"] != nil {
		t.Fatalf("zero input counted as miss=%v", v)
	}
}
