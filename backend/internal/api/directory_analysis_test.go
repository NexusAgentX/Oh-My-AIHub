package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDirectoryAnalysisPublicContract(t *testing.T) {
	p := newPlatform(t)
	viewer, _ := p.member(t, "directory-viewer")
	id := "00000000-0000-4000-8000-000000000001"
	window := "?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z"
	for _, kind := range []string{"channel-hall", "suppliers"} {
		path := "/api/" + kind + "/" + id + "/analysis"
		viewer.expect(t, 401, http.MethodGet, path+window, nil, withoutCookie)
		report := viewer.expect(t, 200, http.MethodGet, path+window+"&stream=false&group_by=status&status=500", nil)
		if report["current"].(map[string]any)["ttft_p50"] != nil {
			t.Fatal("missing measurement became zero")
		}
		f := p.observe.directoryFilter
		if f.TargetID != id || f.Supplier != (kind == "suppliers") || f.Stream != "false" || f.Status != "500" || f.Group != "status" {
			t.Fatalf("scope/filter lost: %+v", f)
		}
		raw, _ := json.Marshal(report)
		for _, private := range []string{"charged", "revenue", "account_id", "api_key", "error_message", "diagnostics", "input_tokens"} {
			if strings.Contains(string(raw), private) {
				t.Fatalf("public projection includes %s", private)
			}
		}
		for _, suffix := range []string{"&account_id=" + id, "&api_key_id=" + id, "&group_by=account", "&group_by=key", "&group_by=error_code", "&tag=secret", "&model=a&model=b", "&stream=1", "&status=999", "&status=0500", "&channel=bad", "&result=private-text", "&bucket_seconds=1", "&bucket_seconds=999999999999", "&format=bogus"} {
			viewer.expect(t, 400, http.MethodGet, path+window+suffix, nil)
		}
		for _, query := range []string{"", "?from=2026-01-02T00:00:00Z&to=2026-01-01T00:00:00Z", "?from=2026-01-01T00:00:00Z&to=2026-06-01T00:00:00Z", "?from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z&bucket_seconds=300"} {
			viewer.expect(t, 400, http.MethodGet, path+query, nil)
		}
		viewer.expect(t, 404, http.MethodGet, "/api/"+kind+"/invalid/analysis"+window, nil)
	}
}
