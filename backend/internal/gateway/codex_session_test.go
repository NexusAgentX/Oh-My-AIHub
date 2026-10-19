package gateway

import (
	"net/http/httptest"
	"testing"
)

func TestNotesSessionIdentityUsesActualSessionInsteadOfCacheOverride(t *testing.T) {
	r := httptest.NewRequest("POST", "/backend-api/codex/responses", nil)
	r.Header.Set("Session-Id", "20000000-0000-8000-8000-000000000002")
	body := []byte(`{"prompt_cache_key":"20000000-0000-8000-8000-000000000002","client_metadata":{"session_id":"10000000-0000-8000-8000-000000000001","thread_id":"30000000-0000-8000-8000-000000000003"}}`)
	if got, err := codexNotesSessionID(r, body); err != nil || got != "10000000-0000-8000-8000-000000000001" {
		t.Fatal("cache override merged distinct actual sessions", got, err)
	}
	if _, err := codexNotesSessionID(r, []byte(`{"context":{"session_id":"20000000-0000-8000-8000-000000000002"},"client_metadata":{"session_id":"10000000-0000-8000-8000-000000000001"}}`)); err == nil {
		t.Fatal("conflicting actual sessions accepted")
	}
}
