package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
)

func TestIdentityAccountsSessionsAndAudit(t *testing.T) {
	pool, store := isolatedDatabase(t)
	ctx := context.Background()
	service, admin, members := accounts(t, store, "100")
	member := members[0]
	if admin.MustChangePassword || !admin.IsAdmin {
		t.Fatalf("bootstrap admin = %+v", admin)
	}
	if _, err := service.CreateBootstrapAdmin(ctx, "second", "第二", "Second-password-2026"); !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("second bootstrap error = %v", err)
	}
	var ledgerAccounts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_accounts WHERE kind = 'user'`).Scan(&ledgerAccounts); err != nil || ledgerAccounts != 2 {
		t.Fatalf("user ledger accounts = %d, %v", ledgerAccounts, err)
	}

	listed, err := service.ListAccounts(ctx, admin, identity.AccountFilter{Limit: 10})
	if err != nil || len(listed) != 2 || listed[0].Username != "founder" || listed[1].CreditLimit != mustAmount(t, "100") {
		t.Fatalf("accounts = %+v, %v", listed, err)
	}

	// 默认信用额度来自平台设置。
	if _, err := pool.Exec(ctx, `UPDATE settings SET default_credit_limit_nano = 7000000000`); err != nil {
		t.Fatal(err)
	}
	defaulted, err := service.CreateInvitedAccount(ctx, admin, "defaulted", "默认额度", nil, false)
	if err != nil || defaulted.Account.CreditLimit != mustAmount(t, "7") {
		t.Fatalf("default credit = %+v, %v", defaulted.Account, err)
	}

	// 停用账户使其全部会话失效。
	login, err := service.Login(ctx, member.Username, mustInitialPassword(t, service, admin, member.ID))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := service.Authenticate(ctx, login.SessionToken); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	disabled := identity.StatusDisabled
	if _, err := service.UpdateAccount(ctx, admin, member.ID, identity.AccountUpdate{Status: &disabled}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := service.Authenticate(ctx, login.SessionToken); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("session after disable error = %v", err)
	}
	var sessions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE account_id = $1`, member.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("sessions after disable = %d, %v", sessions, err)
	}

	// 不能移除最后一个启用的管理员。
	demote := false
	if _, err := service.UpdateAccount(ctx, admin, admin.ID, identity.AccountUpdate{IsAdmin: &demote}); !errors.Is(err, identity.ErrSelfModification) {
		t.Fatalf("self demote error = %v", err)
	}
	promote := true
	if _, err := service.UpdateAccount(ctx, admin, defaulted.Account.ID, identity.AccountUpdate{IsAdmin: &promote}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	other := defaulted.Account.Account
	other.IsAdmin = true
	if _, err := service.UpdateAccount(ctx, other, admin.ID, identity.AccountUpdate{IsAdmin: &demote}); err != nil {
		t.Fatalf("demote founder while another admin is active: %v", err)
	}
	if _, err := service.UpdateAccount(ctx, admin, other.ID, identity.AccountUpdate{Status: &disabled}); !errors.Is(err, identity.ErrLastAdministrator) {
		t.Fatalf("disable last admin error = %v", err)
	}

	var actions []string
	rows, err := pool.Query(ctx, `SELECT action FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	want := map[string]bool{"instance.initialized": false, "account.created": false, "account.updated": false, "account.password_reset": false}
	for _, action := range actions {
		if _, ok := want[action]; ok {
			want[action] = true
		}
	}
	for action, seen := range want {
		if !seen {
			t.Fatalf("audit log %v misses %s", actions, action)
		}
	}
}

// mustInitialPassword resets the member's password and returns the new initial password.
func mustInitialPassword(t *testing.T, service *identity.Service, admin identity.Account, accountID string) string {
	t.Helper()
	reset, err := service.AdminResetPassword(context.Background(), admin, accountID)
	if err != nil || !reset.Account.MustChangePassword {
		t.Fatalf("reset: %+v, %v", reset.Account, err)
	}
	return reset.InitialPassword
}
