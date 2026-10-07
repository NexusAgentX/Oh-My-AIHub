// Package identitypg is the PostgreSQL implementation of identity.Store.
// SQL lives in queries.sql; the generated code is committed beside it.
package identitypg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: New(pool)}
}

func toAccount(a Account, la LedgerAccount) identity.Account {
	return identity.Account{
		ID:                 a.ID,
		Username:           a.Username,
		DisplayName:        a.DisplayName,
		IsAdmin:            a.IsAdmin,
		Status:             identity.Status(a.Status),
		MustChangePassword: a.MustChangePassword,
		PasswordVersion:    a.PasswordVersion,
		Version:            a.Version,
		CreditLimit:        a.CreditLimitNano,
		CreditFrozen:       a.CreditFrozen,
		PostedBalance:      la.PostedBalanceNano,
		AssetReserved:      la.AssetReservedNano,
		SpendAuthorized:    la.SpendAuthorizedNano,
		CreatedAt:          a.CreatedAt,
		UpdatedAt:          a.UpdatedAt,
		PasswordChangedAt:  a.PasswordChangedAt,
	}
}

func toAccountWithPassword(a Account, la LedgerAccount) identity.AccountWithPassword {
	return identity.AccountWithPassword{Account: toAccount(a, la), PasswordHash: a.PasswordHash}
}

func (s *Store) FindAccountByUsername(ctx context.Context, username string) (identity.AccountWithPassword, error) {
	row, err := s.q.GetAccountByUsername(ctx, username)
	if err != nil {
		return identity.AccountWithPassword{}, mapError(err)
	}
	return toAccountWithPassword(row.Account, row.LedgerAccount), nil
}

func (s *Store) FindAccountByID(ctx context.Context, id string) (identity.AccountWithPassword, error) {
	row, err := s.q.GetAccountByID(ctx, id)
	if err != nil {
		return identity.AccountWithPassword{}, mapError(err)
	}
	return toAccountWithPassword(row.Account, row.LedgerAccount), nil
}

func (s *Store) FindAccountBySession(ctx context.Context, tokenHash []byte, now time.Time) (identity.Account, error) {
	row, err := s.q.GetAccountBySession(ctx, GetAccountBySessionParams{TokenHash: tokenHash, Now: now})
	if err != nil {
		return identity.Account{}, mapError(err)
	}
	return toAccount(row.Account, row.LedgerAccount), nil
}

func (s *Store) CreateSession(ctx context.Context, session identity.Session) error {
	if err := s.q.DeleteExpiredSessions(ctx); err != nil {
		return err
	}
	return mapError(s.q.InsertSession(ctx, insertSessionParams(session)))
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	return s.q.DeleteSession(ctx, tokenHash)
}

func insertSessionParams(session identity.Session) InsertSessionParams {
	return InsertSessionParams{
		TokenHash:       session.TokenHash,
		AccountID:       session.AccountID,
		PasswordVersion: session.PasswordVersion,
		ExpiresAt:       session.ExpiresAt,
	}
}

func (s *Store) ReplacePasswordAndSessions(ctx context.Context, accountID string, expectedPasswordVersion int64, passwordHash string, session identity.Session, changedAt time.Time) error {
	return pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		rows, err := q.ReplaceActivePassword(ctx, ReplaceActivePasswordParams{
			PasswordHash:            passwordHash,
			ChangedAt:               changedAt,
			ID:                      accountID,
			ExpectedPasswordVersion: expectedPasswordVersion,
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return identity.ErrConflict
		}
		if err := q.DeleteSessionsByAccount(ctx, accountID); err != nil {
			return err
		}
		if err := q.InsertSession(ctx, insertSessionParams(session)); err != nil {
			return err
		}
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: accountID, Action: "account.password_changed", TargetType: "account", TargetID: accountID,
			Reason:  "account holder changed password",
			Details: map[string]any{"password_version": session.PasswordVersion},
		})
	})
}

// ResetPassword 由管理员发起：允许重置停用账户（与建号交付同链路），但绝不
// 创建新会话；密码版本 CAS 保证与并发改密只有一方成功。
func (s *Store) ResetPassword(ctx context.Context, actorID, accountID string, expectedPasswordVersion int64, passwordHash string, changedAt time.Time) error {
	return pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		rows, err := q.ResetPassword(ctx, ResetPasswordParams{
			PasswordHash:            passwordHash,
			ChangedAt:               changedAt,
			ID:                      accountID,
			ExpectedPasswordVersion: expectedPasswordVersion,
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return identity.ErrConflict
		}
		if err := q.DeleteSessionsByAccount(ctx, accountID); err != nil {
			return err
		}
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: actorID, Action: "account.password_reset", TargetType: "account", TargetID: accountID,
			Reason:  "administrator reset account password",
			Details: map[string]any{"password_version": expectedPasswordVersion + 1},
		})
	})
}

func (s *Store) CreateAccount(ctx context.Context, account identity.NewAccount) (identity.Account, error) {
	var created identity.Account
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var actor *string
		if account.ActorID != "" {
			actor = &account.ActorID
		}
		id, err := q.InsertAccount(ctx, InsertAccountParams{
			Username:           account.Username,
			DisplayName:        account.DisplayName,
			PasswordHash:       account.PasswordHash,
			MustChangePassword: account.MustChangePassword,
			IsAdmin:            account.IsAdmin,
			Status:             string(account.Status),
			CreditLimitNano:    account.CreditLimit,
			CreatedBy:          actor,
		})
		if err != nil {
			return mapError(err)
		}
		if err := q.InsertUserLedgerAccount(ctx, id); err != nil {
			return err
		}
		created, err = accountByID(ctx, q, id)
		if err != nil {
			return err
		}
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: account.ActorID, Action: "account.created", TargetType: "account", TargetID: created.ID,
			Reason: "administrator created invited account",
			Details: map[string]any{
				"username":          created.Username,
				"credit_limit_nano": created.CreditLimit.Nano(),
				"is_admin":          created.IsAdmin,
				"status":            created.Status,
			},
		})
	})
	if err != nil {
		return identity.Account{}, err
	}
	return created, nil
}

func (s *Store) HasAdministrator(ctx context.Context) (bool, error) {
	return s.q.AdministratorExists(ctx)
}

func (s *Store) CreateBootstrapAdmin(ctx context.Context, account identity.NewAccount) (identity.Account, error) {
	var created identity.Account
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockBootstrapAdmin(ctx); err != nil {
			return err
		}
		exists, err := q.AdministratorExists(ctx)
		if err != nil {
			return err
		}
		if exists {
			return identity.ErrConflict
		}
		id, err := q.InsertBootstrapAdmin(ctx, InsertBootstrapAdminParams{
			Username:           account.Username,
			DisplayName:        account.DisplayName,
			PasswordHash:       account.PasswordHash,
			MustChangePassword: account.MustChangePassword,
			Status:             string(account.Status),
		})
		if err != nil {
			return mapError(err)
		}
		if err := q.InsertUserLedgerAccount(ctx, id); err != nil {
			return err
		}
		created, err = accountByID(ctx, q, id)
		if err != nil {
			return err
		}
		return auditpg.Record(ctx, tx, auditpg.Event{
			Action: "account.bootstrap_admin_created", TargetType: "account", TargetID: created.ID,
			Reason:  "first administrator bootstrap",
			Details: map[string]any{"username": created.Username},
		})
	})
	if err != nil {
		return identity.Account{}, err
	}
	return created, nil
}

func accountByID(ctx context.Context, q *Queries, id string) (identity.Account, error) {
	row, err := q.GetAccountByID(ctx, id)
	if err != nil {
		return identity.Account{}, mapError(err)
	}
	return toAccount(row.Account, row.LedgerAccount), nil
}

func (s *Store) ListAccounts(ctx context.Context, query string) ([]identity.Account, error) {
	rows, err := s.q.ListAccounts(ctx, query)
	if err != nil {
		return nil, err
	}
	accounts := make([]identity.Account, 0, len(rows))
	for _, row := range rows {
		accounts = append(accounts, toAccount(row.Account, row.LedgerAccount))
	}
	return accounts, nil
}

func (s *Store) UpdateAccount(ctx context.Context, actorID, accountID string, update identity.AccountUpdate) (identity.Account, error) {
	var account identity.Account
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockAccount(ctx, accountID); err != nil {
			return err
		}
		// Taking the ledger row first serializes a limit/freeze change with
		// every new debit or hold, which lock it before reading credit policy.
		if _, err := q.LockLedgerAccountByIdentity(ctx, accountID); err != nil {
			return mapError(err)
		}
		if update.Status != nil || update.IsAdmin != nil {
			if err := q.LockAdministratorMembership(ctx); err != nil {
				return err
			}
		}
		target, err := q.LockAccountForUpdate(ctx, accountID)
		if err != nil {
			return mapError(err)
		}
		if target.Version != update.ExpectedVersion {
			return identity.ErrConflict
		}
		desiredAdmin := target.IsAdmin
		if update.IsAdmin != nil {
			desiredAdmin = *update.IsAdmin
		}
		desiredStatus := identity.Status(target.Status)
		if update.Status != nil {
			desiredStatus = *update.Status
		}
		removesActiveAdministrator := target.IsAdmin && target.Status == string(identity.StatusActive) &&
			(!desiredAdmin || desiredStatus == identity.StatusDisabled)
		if removesActiveAdministrator {
			if actorID == accountID {
				return identity.ErrForbidden
			}
			activeAdmins, err := q.CountActiveAdministrators(ctx)
			if err != nil {
				return err
			}
			if activeAdmins <= 1 {
				return identity.ErrConflict
			}
		}

		params := UpdateAccountPolicyParams{
			IsAdmin:         update.IsAdmin,
			CreditFrozen:    update.CreditFrozen,
			ID:              accountID,
			ExpectedVersion: update.ExpectedVersion,
		}
		if update.Status != nil {
			status := string(*update.Status)
			params.Status = &status
		}
		if update.CreditLimit != nil {
			nano := update.CreditLimit.Nano()
			params.CreditLimitNano = &nano
		}
		rows, err := q.UpdateAccountPolicy(ctx, params)
		if err != nil {
			return err
		}
		if rows != 1 {
			return identity.ErrConflict
		}
		account, err = accountByID(ctx, q, accountID)
		if err != nil {
			return err
		}
		if update.Status != nil && *update.Status == identity.StatusDisabled {
			if err := q.DeleteSessionsByAccount(ctx, accountID); err != nil {
				return err
			}
		}
		details := map[string]any{}
		if update.Status != nil {
			details["status"] = *update.Status
		}
		if update.CreditLimit != nil {
			details["credit_limit_nano"] = update.CreditLimit.Nano()
		}
		if update.CreditFrozen != nil {
			details["credit_frozen"] = *update.CreditFrozen
		}
		if update.IsAdmin != nil {
			details["is_admin"] = *update.IsAdmin
		}
		details["version"] = account.Version
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: actorID, Action: "account.updated", TargetType: "account", TargetID: accountID,
			Reason: "administrator updated account access or credit", Details: details,
		})
	})
	if err != nil {
		return identity.Account{}, err
	}
	return account, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return identity.ErrConflict
	}
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
		return identity.ErrNotFound
	}
	return fmt.Errorf("identity store: %w", err)
}

var _ identity.Store = (*Store)(nil)
