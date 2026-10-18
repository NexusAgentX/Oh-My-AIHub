package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPCallbackRequiresItsOwnTokenWithoutOriginOrCookie(t *testing.T) {
	h := NewHandler(Dependencies{})
	for _, token := range []string{"", "Bearer platform-test-key"} {
		r := httptest.NewRequest("POST", "https://hub.example/api/detection/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list","id":1}`))
		r.Header.Set("Authorization", token)
		r.AddCookie(&http.Cookie{Name: defaultSessionCookie, Value: "foreign-session"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("callback status %d: %s", w.Code, w.Body.String())
		}
	}
}
