package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity/identitytest"
)

const fixedTestPassword = "Synthetic-fixed-password-2026"

func fixedTestService(t *testing.T, store *fakeStore) *identity.Service {
	t.Helper()
	service, err := identity.NewService(store, time.Hour, identity.WithPasswordParams(identitytest.Params), identity.WithFixedAdministrator("testadmin"))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestFixedAdministratorLifecycleAndAPIProtection(t *testing.T) {
	store := newFakeStore()
	service := fixedTestService(t, store)
	handler := NewHandler(Dependencies{Identity: service, CookieSecure: true})
	spec := loadOpenAPI(t)
	first := &client{spec: spec, handler: handler}
	first.expect(t, http.StatusForbidden, "POST", "/api/instance/initialize", map[string]string{"username": "intruder", "display_name": "抢先初始化", "password": fixedTestPassword})
	if err := service.EnsureFixedAdministrator(context.Background(), fixedTestPassword); err != nil {
		t.Fatal(err)
	}
	state := first.expect(t, http.StatusOK, "GET", "/api/instance", nil)
	if state["initialized"] != true || state["test_site"] != true || state["fixed_admin_username"] != "testadmin" {
		t.Fatalf("instance = %v", state)
	}
	if strings.Contains(first.call(t, "GET", "/api/instance", nil).Body.String(), fixedTestPassword) {
		t.Fatal("instance leaked credentials")
	}
	login := map[string]string{"username": "testadmin", "password": fixedTestPassword}
	fixed := first.expect(t, http.StatusOK, "POST", "/api/auth/login", login)["account"].(map[string]any)
	fixedID := fixed["id"].(string)
	second := &client{spec: spec, handler: handler}
	second.expect(t, http.StatusOK, "POST", "/api/auth/login", login)
	first.expect(t, http.StatusOK, "GET", "/api/me", nil)
	first.expect(t, http.StatusForbidden, "POST", "/api/me/password", map[string]string{"current_password": fixedTestPassword, "new_password": "Changed-password-2026"})

	created := first.expect(t, http.StatusCreated, "POST", "/api/admin/accounts", map[string]any{"username": "reviewer", "display_name": "另一管理员", "is_admin": true})
	other := &client{spec: spec, handler: handler}
	other.expect(t, http.StatusOK, "POST", "/api/auth/login", map[string]string{"username": "reviewer", "password": created["initial_password"].(string)})
	other.expect(t, http.StatusOK, "POST", "/api/me/password", map[string]string{"current_password": created["initial_password"].(string), "new_password": "Reviewer-password-2026"})
	for _, actor := range []*client{first, other} {
		for _, body := range []map[string]any{{"status": "disabled"}, {"is_admin": false}} {
			actor.expect(t, http.StatusForbidden, "PATCH", "/api/admin/accounts/"+fixedID, body)
		}
		actor.expect(t, http.StatusForbidden, "POST", "/api/admin/accounts/"+fixedID+"/reset-password", nil)
	}
	// Display names and credit remain ordinary testable business functionality.
	other.expect(t, http.StatusOK, "PATCH", "/api/admin/accounts/"+fixedID, map[string]any{"display_name": "共同测试", "credit_limit": "20"})
	// A normal member can still be reset and disabled by an administrator.
	member := first.expect(t, http.StatusCreated, "POST", "/api/admin/accounts", map[string]any{"username": "member", "display_name": "普通成员"})["account"].(map[string]any)
	memberID := member["id"].(string)
	other.expect(t, http.StatusOK, "POST", "/api/admin/accounts/"+memberID+"/reset-password", nil)
	other.expect(t, http.StatusOK, "PATCH", "/api/admin/accounts/"+memberID, map[string]any{"status": "disabled"})
	if err := fixedTestService(t, store).EnsureFixedAdministrator(context.Background(), fixedTestPassword); err != nil {
		t.Fatalf("restart: %v", err)
	}
	first.expect(t, http.StatusOK, "GET", "/api/me", nil)
	second.expect(t, http.StatusOK, "GET", "/api/me", nil)
	if err := service.EnsureFixedAdministrator(context.Background(), "Mismatched-password-2026"); !errors.Is(err, identity.ErrProtectedAccount) {
		t.Fatalf("mismatched configured password accepted: %v", err)
	}
	if len(store.accounts) != 3 {
		t.Fatalf("restart created extra accounts: %d", len(store.accounts))
	}
	// A fresh store represents the reset path and receives the same credentials.
	fresh := newFakeStore()
	freshService := fixedTestService(t, fresh)
	if err := freshService.EnsureFixedAdministrator(context.Background(), fixedTestPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := freshService.Login(context.Background(), "testadmin", fixedTestPassword); err != nil || len(fresh.accounts) != 1 {
		t.Fatalf("fresh initialization: %v", err)
	}
}

func TestFixedAdministratorDoesNotTakeOverExistingInstance(t *testing.T) {
	store := newFakeStore()
	regular, err := identitytest.NewService(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := regular.CreateBootstrapAdmin(context.Background(), "founder", "已有实例", "Founder-password-2026"); err != nil {
		t.Fatal(err)
	}
	if err := fixedTestService(t, store).EnsureFixedAdministrator(context.Background(), fixedTestPassword); !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("existing instance takeover = %v", err)
	}
}
