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

type resetStubStore struct {
	identity.Store
	accounts map[string]identity.AccountWithPassword
	resets   []resetCall
	err      error
}

type resetCall struct {
	actorID         string
	expectedVersion int64
}

func (s *resetStubStore) FindAccountByID(_ context.Context, id string) (identity.AccountWithPassword, error) {
	account, ok := s.accounts[id]
	if !ok {
		return identity.AccountWithPassword{}, identity.ErrNotFound
	}
	return account, nil
}

func (s *resetStubStore) ResetPassword(_ context.Context, actorID, _ string, expectedPasswordVersion int64, _ string, _ time.Time) error {
	if s.err != nil {
		return s.err
	}
	s.resets = append(s.resets, resetCall{actorID: actorID, expectedVersion: expectedPasswordVersion})
	return nil
}

func newResetTestApp(t *testing.T, store identity.Store) *app {
	t.Helper()
	service, err := identity.NewService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &app{
		identity:             service,
		cookieName:           defaultSessionCookie,
		accountPasswordSlots: make(chan struct{}, 2),
		passwordChanges:      newLoginLimiter(8, 15*time.Minute, 10_000),
		passwordChangeIPs:    newLoginLimiter(32, 15*time.Minute, 10_000),
	}
}

func resetPasswordRequest(actor identity.Account, targetID, body string) (*http.Request, *httptest.ResponseRecorder) {
	request := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/"+targetID+"/password-reset", strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), accountContextKey, actor))
	request.SetPathValue("accountID", targetID)
	return request, httptest.NewRecorder()
}

func resetTestActor(admin bool) (identity.Account, identity.AccountWithPassword) {
	actor := identity.Account{
		ID: "0d7d60a3-1e6f-4b8a-9d99-2f4a28c1b001", Username: "actor",
		IsAdmin: admin, Status: identity.StatusActive,
	}
	target := identity.AccountWithPassword{
		Account: identity.Account{
			ID: "1b671a64-40d5-4917-9af8-78d1c3f8f703", Username: "member",
			Status: identity.StatusActive, PasswordVersion: 3,
		},
		PasswordHash: "not-verified-during-reset",
	}
	return actor, target
}

func TestResetAccountPasswordReturnsOneTimePassword(t *testing.T) {
	admin, target := resetTestActor(true)
	store := &resetStubStore{accounts: map[string]identity.AccountWithPassword{target.ID: target}}
	request, recorder := resetPasswordRequest(admin, target.ID, "{}")
	newResetTestApp(t, store).resetAccountPassword(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("reset = %d %s", recorder.Code, recorder.Body)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"initial_password"`) || !strings.Contains(body, `"must_change_password":true`) {
		t.Fatalf("reset response = %s, want one-time password and forced change flag", body)
	}
	if len(store.resets) != 1 || store.resets[0].actorID != admin.ID || store.resets[0].expectedVersion != 3 {
		t.Fatalf("store reset calls = %+v, want actor %s at version 3", store.resets, admin.ID)
	}
}

func TestResetAccountPasswordRejectsSelf(t *testing.T) {
	admin, _ := resetTestActor(true)
	store := &resetStubStore{}
	request, recorder := resetPasswordRequest(admin, admin.ID, "{}")
	newResetTestApp(t, store).resetAccountPassword(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "invalid_input") {
		t.Fatalf("self reset = %d %s, want invalid input", recorder.Code, recorder.Body)
	}
	if len(store.resets) != 0 {
		t.Fatalf("self reset reached the store: %+v", store.resets)
	}
}

func TestResetAccountPasswordRejectsNonAdministrator(t *testing.T) {
	member, target := resetTestActor(false)
	store := &resetStubStore{accounts: map[string]identity.AccountWithPassword{target.ID: target}}
	request, recorder := resetPasswordRequest(member, target.ID, "{}")
	newResetTestApp(t, store).resetAccountPassword(recorder, request)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "forbidden") {
		t.Fatalf("non-admin reset = %d %s, want forbidden", recorder.Code, recorder.Body)
	}
}

func TestResetAccountPasswordRejectsUnknownTargets(t *testing.T) {
	admin, _ := resetTestActor(true)
	request, recorder := resetPasswordRequest(admin, "not-a-uuid", "{}")
	newResetTestApp(t, &resetStubStore{}).resetAccountPassword(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("invalid uuid reset = %d %s, want not found", recorder.Code, recorder.Body)
	}

	request, recorder = resetPasswordRequest(admin, "2c8f4d1e-9b1e-4f0a-8d3f-6f3b7d9c6004", "{}")
	newResetTestApp(t, &resetStubStore{}).resetAccountPassword(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown account reset = %d %s, want not found", recorder.Code, recorder.Body)
	}
}

func TestResetAccountPasswordReportsConflict(t *testing.T) {
	admin, target := resetTestActor(true)
	store := &resetStubStore{accounts: map[string]identity.AccountWithPassword{target.ID: target}, err: identity.ErrConflict}
	request, recorder := resetPasswordRequest(admin, target.ID, "{}")
	newResetTestApp(t, store).resetAccountPassword(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "conflict") {
		t.Fatalf("conflicting reset = %d %s, want conflict", recorder.Code, recorder.Body)
	}
}

func TestResetAccountPasswordRejectsInvalidJSON(t *testing.T) {
	admin, target := resetTestActor(true)
	request, recorder := resetPasswordRequest(admin, target.ID, "not-json")
	newResetTestApp(t, &resetStubStore{accounts: map[string]identity.AccountWithPassword{target.ID: target}}).resetAccountPassword(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid json reset = %d %s, want bad request", recorder.Code, recorder.Body)
	}
}
