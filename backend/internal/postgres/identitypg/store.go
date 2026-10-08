// Package identitypg is the PostgreSQL implementation of identity.Store.
// SQL lives in queries.sql; the generated code is committed beside it.
package identitypg

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/ledgerpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: New(pool)}
}

func toAccount(a Account) identity.Account {
	return identity.Account{
		ID:                 a.ID,
		Username:           a.Username,
		DisplayName:        a.DisplayName,
		IsAdmin:            a.IsAdmin,
		Status:             identity.Status(a.Status),
		MustChangePassword: a.MustChangePassword,
		PasswordVersion:    a.PasswordVersion,
		CreditLimit:        a.CreditLimitNano,
		CreatedAt:          a.CreatedAt,
		UpdatedAt:          a.UpdatedAt,
		PasswordChangedAt:  a.PasswordChangedAt,
	}
}

func toAdminAccount(a Account, balance int64) identity.AdminAccount {
	return identity.AdminAccount{Account: toAccount(a), Balance: money.FromNano(balance)}
}

func (s *Store) FindAccountByUsername(ctx context.Context, username string) (identity.AccountWithPassword, error) {
	row, err := s.q.GetAccountByUsername(ctx, username)
	if err != nil {
		return identity.AccountWithPassword{}, mapError(err)
	}
	return identity.AccountWithPassword{Account: toAccount(row), PasswordHash: row.PasswordHash}, nil
}

func (s *Store) FindAccountByID(ctx context.Context, id string) (identity.AccountWithPassword, error) {
	row, err := s.q.GetAccountByID(ctx, id)
	if err != nil {
		return identity.AccountWithPassword{}, mapError(err)
	}
	return identity.AccountWithPassword{Account: toAccount(row), PasswordHash: row.PasswordHash}, nil
}

func (s *Store) FindAccountBySession(ctx context.Context, tokenHash []byte, now time.Time) (identity.Account, error) {
	row, err := s.q.GetAccountBySession(ctx, GetAccountBySessionParams{TokenHash: tokenHash, Now: now})
	if err != nil {
		return identity.Account{}, mapError(err)
	}
	return toAccount(row), nil
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
		rows, err := q.ReplacePassword(ctx, ReplacePasswordParams{
			PasswordHash: passwordHash, MustChangePassword: false, ChangedAt: &changedAt,
			ID: accountID, ExpectedPasswordVersion: expectedPasswordVersion,
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
			ActorID: accountID, Action: audit.ActionAccountPasswordChange, TargetType: "account", TargetID: accountID,
		})
	})
}

func (s *Store) ResetPassword(ctx context.Context, actorID, accountID, passwordHash string, changedAt time.Time) (identity.AdminAccount, error) {
	var result identity.AdminAccount
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		current, err := q.LockAccount(ctx, accountID)
		if err != nil {
			return mapError(err)
		}
		if _, err := q.ReplacePassword(ctx, ReplacePasswordParams{
			PasswordHash: passwordHash, MustChangePassword: true, ChangedAt: &changedAt,
			ID: accountID, ExpectedPasswordVersion: current.PasswordVersion,
		}); err != nil {
			return err
		}
		if err := q.DeleteSessionsByAccount(ctx, accountID); err != nil {
			return err
		}
		if err := auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: actorID, Action: audit.ActionAccountPasswordReset, TargetType: "account", TargetID: accountID,
		}); err != nil {
			return err
		}
		row, err := q.GetAdminAccount(ctx, accountID)
		if err != nil {
			return err
		}
		result = toAdminAccount(row.Account, row.BalanceNano)
		return nil
	})
	return result, err
}

// insertAccount creates the identity row and its user ledger account in tx.
func (s *Store) insertAccount(ctx context.Context, tx pgx.Tx, account identity.NewAccount) (Account, error) {
	q := s.q.WithTx(tx)
	var creditLimit money.Amount
	if account.CreditLimit != nil {
		creditLimit = *account.CreditLimit
	} else {
		defaultLimit, err := q.DefaultCreditLimit(ctx)
		if err != nil {
			return Account{}, err
		}
		creditLimit = defaultLimit
	}
	row, err := q.InsertAccount(ctx, InsertAccountParams{
		Username: account.Username, DisplayName: account.DisplayName, PasswordHash: account.PasswordHash,
		IsAdmin: account.IsAdmin, MustChangePassword: account.MustChangePassword, CreditLimitNano: creditLimit,
	})
	if err != nil {
		return Account{}, mapError(err)
	}
	if err := ledgerpg.CreateUserAccount(ctx, tx, row.ID); err != nil {
		return Account{}, err
	}
	actor := account.ActorID
	if actor == "" {
		actor = row.ID
	}
	return row, auditpg.Record(ctx, tx, auditpg.Event{
		ActorID: actor, Action: audit.ActionAccountCreated, TargetType: "account", TargetID: row.ID,
		Detail: map[string]any{"username": row.Username, "is_admin": row.IsAdmin, "credit_limit": row.CreditLimitNano.String()},
	})
}

func (s *Store) CreateAccount(ctx context.Context, account identity.NewAccount) (identity.AdminAccount, error) {
	var created identity.AdminAccount
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		row, err := s.insertAccount(ctx, tx, account)
		created = toAdminAccount(row, 0)
		return err
	})
	return created, err
}

func (s *Store) CreateBootstrapAdmin(ctx context.Context, account identity.NewAccount) (identity.Account, error) {
	var created identity.Account
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockBootstrap(ctx); err != nil {
			return err
		}
		exists, err := q.HasAdministrator(ctx)
		if err != nil {
			return err
		}
		if exists {
			return identity.ErrConflict
		}
		row, err := s.insertAccount(ctx, tx, account)
		if err != nil {
			return err
		}
		created = toAccount(row)
		return auditpg.Record(ctx, tx, auditpg.Event{
			ActorID: row.ID, Action: audit.ActionInstanceInitialized, TargetType: "account", TargetID: row.ID,
		})
	})
	return created, err
}

func (s *Store) HasAdministrator(ctx context.Context) (bool, error) {
	return s.q.HasAdministrator(ctx)
}

func (s *Store) ListAccounts(ctx context.Context, filter identity.AccountFilter) ([]identity.AdminAccount, error) {
	rows, err := s.q.ListAdminAccounts(ctx, ListAdminAccountsParams{
		Query: filter.Query, Status: string(filter.Status), AfterUsername: filter.AfterCursor, RowLimit: int32(filter.Limit),
	})
	if err != nil {
		return nil, err
	}
	accounts := make([]identity.AdminAccount, 0, len(rows))
	for _, row := range rows {
		accounts = append(accounts, toAdminAccount(row.Account, row.BalanceNano))
	}
	return accounts, nil
}

func (s *Store) GetAccount(ctx context.Context, accountID string) (identity.AdminAccount, error) {
	row, err := s.q.GetAdminAccount(ctx, accountID)
	if err != nil {
		return identity.AdminAccount{}, mapError(err)
	}
	return toAdminAccount(row.Account, row.BalanceNano), nil
}

func (s *Store) UpdateAccount(ctx context.Context, actorID, accountID string, update identity.AccountUpdate) (identity.AdminAccount, error) {
	var result identity.AdminAccount
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		removesAdmin := (update.Status != nil && *update.Status == identity.StatusDisabled) || (update.IsAdmin != nil && !*update.IsAdmin)
		if removesAdmin {
			// Serialize changes that could remove the last active administrator.
			if err := q.LockActiveAdministrators(ctx); err != nil {
				return err
			}
		}
		current, err := q.LockAccount(ctx, accountID)
		if err != nil {
			return mapError(err)
		}
		next := UpdateAccountParams{
			ID: accountID, DisplayName: current.DisplayName, Status: current.Status,
			CreditLimitNano: current.CreditLimitNano, IsAdmin: current.IsAdmin,
		}
		if update.DisplayName != nil {
			next.DisplayName = *update.DisplayName
		}
		if update.Status != nil {
			next.Status = string(*update.Status)
		}
		if update.CreditLimit != nil {
			next.CreditLimitNano = *update.CreditLimit
		}
		if update.IsAdmin != nil {
			next.IsAdmin = *update.IsAdmin
		}
		wasActiveAdmin := current.IsAdmin && current.Status == string(identity.StatusActive)
		staysActiveAdmin := next.IsAdmin && next.Status == string(identity.StatusActive)
		if wasActiveAdmin && !staysActiveAdmin {
			count, err := q.CountActiveAdministrators(ctx)
			if err != nil {
				return err
			}
			if count <= 1 {
				return identity.ErrLastAdministrator
			}
		}
		row, err := q.UpdateAccount(ctx, next)
		if err != nil {
			return mapError(err)
		}
		if row.Status == string(identity.StatusDisabled) {
			if err := q.DeleteSessionsByAccount(ctx, accountID); err != nil {
				return err
			}
		}
		before, after := map[string]any{}, map[string]any{}
		record := func(field string, old, new any, changed bool) {
			if changed {
				before[field], after[field] = old, new
			}
		}
		record("display_name", current.DisplayName, row.DisplayName, current.DisplayName != row.DisplayName)
		record("status", current.Status, row.Status, current.Status != row.Status)
		record("credit_limit", current.CreditLimitNano.String(), row.CreditLimitNano.String(), current.CreditLimitNano != row.CreditLimitNano)
		record("is_admin", current.IsAdmin, row.IsAdmin, current.IsAdmin != row.IsAdmin)
		if len(after) > 0 {
			if err := auditpg.Record(ctx, tx, auditpg.Event{
				ActorID: actorID, Action: audit.ActionAccountUpdated, TargetType: "account", TargetID: accountID,
				Detail: map[string]any{"before": before, "after": after},
			}); err != nil {
				return err
			}
		}
		admin, err := q.GetAdminAccount(ctx, accountID)
		if err != nil {
			return err
		}
		result = toAdminAccount(admin.Account, admin.BalanceNano)
		return nil
	})
	return result, err
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return identity.ErrConflict
		case "23514", "22P02":
			return identity.ErrInvalidInput
		}
	}
	return err
}
