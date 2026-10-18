package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGlobalModelAdministrationRoutesAreRemoved(t *testing.T) {
	assertRetiredRoutes(t, []struct{ method, path string }{{"GET", "/api/admin/models"}, {"POST", "/api/admin/models"}, {"PATCH", "/api/admin/models/retired"}, {"DELETE", "/api/admin/models/retired"}})
}

func TestLegacySupplierAndHallRoutesAreRemoved(t *testing.T) {
	assertRetiredRoutes(t, []struct{ method, path string }{
		{"POST", "/api/admin/suppliers/preview"},
		{"GET", "/api/suppliers"},
		{"GET", "/api/suppliers/retired"},
		{"POST", "/api/suppliers/recognize"},
		{"GET", "/api/channel-hall"},
		{"GET", "/api/channel-hall/retired"},
		{"GET", "/api/admin/suppliers"},
		{"POST", "/api/admin/suppliers"},
		{"PUT", "/api/admin/suppliers/retired"},
		{"POST", "/api/admin/suppliers/icon"},
	})
}

func assertRetiredRoutes(t *testing.T, operations []struct{ method, path string }) {
	t.Helper()
	p := newPlatform(t)
	admin := p.admin(t)
	for _, operation := range operations {
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
