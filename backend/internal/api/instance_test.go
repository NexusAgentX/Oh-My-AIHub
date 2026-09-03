package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
)

type instanceStubStore struct {
	identity.Store
	initialized bool
	created     []identity.NewAccount
}

func (s *instanceStubStore) HasAdministrator(context.Context) (bool, error) {
	return s.initialized, nil
}

func (s *instanceStubStore) CreateBootstrapAdmin(_ context.Context, account identity.NewAccount) (identity.Account, error) {
	if s.initialized {
		return identity.Account{}, identity.ErrConflict
	}
	s.created = append(s.created, account)
	return identity.Account{
		ID: "admin-id", Username: account.Username, DisplayName: account.DisplayName,
		IsAdmin: true, MustChangePassword: true, Status: identity.StatusActive,
	}, nil
}

func newInstanceApp(t *testing.T, store identity.Store) *app {
	t.Helper()
	service, err := identity.NewService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &app{identity: service, cookieName: defaultSessionCookie}
}

func TestInstanceStateEndpoint(t *testing.T) {
	application := newInstanceApp(t, &instanceStubStore{initialized: false})
	recorder := httptest.NewRecorder()
	application.instanceState(recorder, httptest.NewRequest(http.MethodGet, "/api/instance", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"initialized":false`) {
		t.Fatalf("uninitialized state = %d %s", recorder.Code, recorder.Body.String())
	}

	application = newInstanceApp(t, &instanceStubStore{initialized: true})
	recorder = httptest.NewRecorder()
	application.instanceState(recorder, httptest.NewRequest(http.MethodGet, "/api/instance", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"initialized":true`) {
		t.Fatalf("initialized state = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestInstanceInitializeCreatesFirstAdministrator(t *testing.T) {
	store := &instanceStubStore{}
	application := newInstanceApp(t, store)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/instance/initialize", strings.NewReader(
		`{"username":"founder","display_name":"创始人","password":"Instance-Init-2026!"}`,
	))
	application.instanceInitialize(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("initialize = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(store.created) != 1 || store.created[0].Username != "founder" || !store.created[0].MustChangePassword {
		t.Fatalf("created account = %+v", store.created)
	}
	if !strings.Contains(recorder.Body.String(), `"must_change_password":true`) {
		t.Fatalf("response = %s", recorder.Body.String())
	}
}

func TestInstanceInitializeRejectsInitializedInstance(t *testing.T) {
	application := newInstanceApp(t, &instanceStubStore{initialized: true})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/instance/initialize", strings.NewReader(
		`{"username":"hijacker","display_name":"抢注者","password":"Instance-Init-2026!"}`,
	))
	application.instanceInitialize(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "already_initialized") {
		t.Fatalf("initialized rejection = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestInstanceInitializeRejectsInvalidPayload(t *testing.T) {
	application := newInstanceApp(t, &instanceStubStore{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/instance/initialize", strings.NewReader(
		`{"username":"","display_name":"","password":"short"}`,
	))
	application.instanceInitialize(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid payload = %d %s", recorder.Code, recorder.Body.String())
	}
}
