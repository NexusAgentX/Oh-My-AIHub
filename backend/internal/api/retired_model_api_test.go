package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGlobalModelAdministrationRoutesAreRemoved(t *testing.T) {
	p := newPlatform(t)
	admin := p.admin(t)
	for _, operation := range []struct{ method, path string }{{"GET", "/api/admin/models"}, {"POST", "/api/admin/models"}, {"PATCH", "/api/admin/models/retired"}, {"DELETE", "/api/admin/models/retired"}} {
		request := httptest.NewRequest(operation.method, "https://hub.example"+operation.path, strings.NewReader(`{}`))
		request.Header.Set("Origin", "https://hub.example")
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(admin.cookie)
		recorder := httptest.NewRecorder()
		p.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("retired %s %s returned %d", operation.method, operation.path, recorder.Code)
		}
	}
}
