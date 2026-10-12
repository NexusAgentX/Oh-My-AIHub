package api

import (
	"net/http"
	"testing"
)

func TestCallAnalysisContractAndScope(t *testing.T) {
	p := newPlatform(t)
	admin := p.admin(t)
	user, id := p.member(t, "analysis-user")
	query := "?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z"
	for _, path := range []string{"/api/calls/analysis", "/api/admin/calls/analysis"} {
		actor := user
		if path == "/api/admin/calls/analysis" {
			actor = admin
		}
		result := actor.expect(t, 200, http.MethodGet, path+query+"&stream=false&min_attempts=2&billed=false&group_by=channel&split_by=model", nil)
		if result["current"].(map[string]any)["ttft_p95"] != nil {
			t.Fatal("empty percentile is not null")
		}
		f := p.observe.analysisFilter
		if path == "/api/calls/analysis" && f.AccountID != id {
			t.Fatal("lost user scope")
		}
		if f.Stream == nil || *f.Stream || f.Billed == nil || *f.Billed || f.MinAttempts == nil || *f.MinAttempts != 2 {
			t.Fatalf("lost filters: %+v", f)
		}
	}
	user.expect(t, 200, http.MethodGet, "/api/calls/analysis"+query+"&account_id=00000000-0000-4000-8000-000000000001", nil)
	if p.observe.analysisFilter.AccountID != id {
		t.Fatal("account scope override")
	}
	for _, suffix := range []string{"&stream=1", "&min_duration_ms=4294967296", "&min_attempts=3&max_attempts=1", "&status_code=999", "&min_cost=-1", "&group_by=account", "&split_by=account", "&group_by=bogus", "&bucket_seconds=1", "&min_duration_ms=5&max_duration_ms=2"} {
		got := user.call(t, http.MethodGet, "/api/calls/analysis"+query+suffix, nil)
		if got.Code != 400 {
			t.Fatalf("%s: %d %s", suffix, got.Code, got.Body.String())
		}
	}
	if got := user.call(t, http.MethodGet, "/api/admin/calls/analysis"+query, nil); got.Code != 403 {
		t.Fatalf("admin scope: %d", got.Code)
	}
}
