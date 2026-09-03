package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

type scanner interface {
	Scan(...any) error
}

type rowQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const accountColumnsAliased = `
		a.id::text, a.username, a.display_name, a.is_admin, a.status, a.must_change_password,
		a.password_version, a.version, a.credit_limit_nano, a.credit_frozen,
		la.posted_balance_nano, la.asset_reserved_nano, la.spend_authorized_nano,
		a.created_at, a.updated_at, a.password_changed_at`

const accountWithPasswordColumnsAliased = accountColumnsAliased + `, a.password_hash`

func scanAccount(row scanner) (identity.Account, error) {
	var account identity.Account
	var status string
	var creditLimit, posted, assetReserved, spendAuthorized int64
	err := row.Scan(
		&account.ID,
		&account.Username,
		&account.DisplayName,
		&account.IsAdmin,
		&status,
		&account.MustChangePassword,
		&account.PasswordVersion,
		&account.Version,
		&creditLimit,
		&account.CreditFrozen,
		&posted,
		&assetReserved,
		&spendAuthorized,
		&account.CreatedAt,
		&account.UpdatedAt,
		&account.PasswordChangedAt,
	)
	account.Status = identity.Status(status)
	account.CreditLimit = money.FromNano(creditLimit)
	account.PostedBalance = money.FromNano(posted)
	account.AssetReserved = money.FromNano(assetReserved)
	account.SpendAuthorized = money.FromNano(spendAuthorized)
	return account, mapIdentityError(err)
}

func scanAccountWithPassword(row scanner) (identity.AccountWithPassword, error) {
	var account identity.AccountWithPassword
	var status string
	var creditLimit, posted, assetReserved, spendAuthorized int64
	err := row.Scan(
		&account.ID,
		&account.Username,
		&account.DisplayName,
		&account.IsAdmin,
		&status,
		&account.MustChangePassword,
		&account.PasswordVersion,
		&account.Version,
		&creditLimit,
		&account.CreditFrozen,
		&posted,
		&assetReserved,
		&spendAuthorized,
		&account.CreatedAt,
		&account.UpdatedAt,
		&account.PasswordChangedAt,
		&account.PasswordHash,
	)
	account.Status = identity.Status(status)
	account.CreditLimit = money.FromNano(creditLimit)
	account.PostedBalance = money.FromNano(posted)
	account.AssetReserved = money.FromNano(assetReserved)
	account.SpendAuthorized = money.FromNano(spendAuthorized)
	return account, mapIdentityError(err)
}

func accountByID(ctx context.Context, queryer rowQueryer, id string) (identity.Account, error) {
	return scanAccount(queryer.QueryRow(ctx, `
		SELECT `+accountColumnsAliased+`
		FROM accounts a JOIN ledger_accounts la ON la.identity_account_id = a.id
		WHERE a.id = $1`, id))
}

func (s *Store) FindAccountByUsername(ctx context.Context, username string) (identity.AccountWithPassword, error) {
	return scanAccountWithPassword(s.pool.QueryRow(ctx, `
		SELECT `+accountWithPasswordColumnsAliased+`
		FROM accounts a JOIN ledger_accounts la ON la.identity_account_id = a.id
		WHERE a.username = $1`, username))
}

func (s *Store) FindAccountByID(ctx context.Context, id string) (identity.AccountWithPassword, error) {
	return scanAccountWithPassword(s.pool.QueryRow(ctx, `
		SELECT `+accountWithPasswordColumnsAliased+`
		FROM accounts a JOIN ledger_accounts la ON la.identity_account_id = a.id
		WHERE a.id = $1`, id))
}

func (s *Store) FindAccountBySession(ctx context.Context, tokenHash []byte, now time.Time) (identity.Account, error) {
	return scanAccount(s.pool.QueryRow(ctx, `
			SELECT `+accountColumnsAliased+`
			FROM accounts a
			JOIN sessions s ON s.account_id = a.id
			JOIN ledger_accounts la ON la.identity_account_id = a.id
		WHERE s.token_hash = $1
			AND s.expires_at > $2
			AND s.password_version = a.password_version
			AND a.status = 'active'`, tokenHash, now))
}

func (s *Store) CreateSession(ctx context.Context, session identity.Session) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, account_id, password_version, expires_at)
		VALUES ($1, $2, $3, $4)`, session.TokenHash, session.AccountID, session.PasswordVersion, session.ExpiresAt)
	return mapIdentityError(err)
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (s *Store) ReplacePasswordAndSessions(ctx context.Context, accountID string, expectedPasswordVersion int64, passwordHash string, session identity.Session, changedAt time.Time) error {
	transaction, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx) //nolint:errcheck

	result, err := transaction.Exec(ctx, `
		UPDATE accounts
		SET password_hash = $2,
			must_change_password = false,
			password_version = password_version + 1,
			password_changed_at = $3,
			updated_at = $3
		WHERE id = $1 AND status = 'active' AND password_version = $4`, accountID, passwordHash, changedAt, expectedPasswordVersion)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrConflict
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM sessions WHERE account_id = $1`, accountID); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO sessions (token_hash, account_id, password_version, expires_at)
		VALUES ($1, $2, $3, $4)`, session.TokenHash, session.AccountID, session.PasswordVersion, session.ExpiresAt); err != nil {
		return err
	}
	if err := insertAudit(ctx, transaction, accountID, "account.password_changed", "account", accountID, "account holder changed password", map[string]any{
		"password_version": session.PasswordVersion,
	}); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (s *Store) CreateAccount(ctx context.Context, account identity.NewAccount) (identity.Account, error) {
	transaction, err := s.pool.Begin(ctx)
	if err != nil {
		return identity.Account{}, err
	}
	defer transaction.Rollback(ctx) //nolint:errcheck

	var createdID string
	err = transaction.QueryRow(ctx, `
		INSERT INTO accounts (
			username, display_name, password_hash, must_change_password,
			is_admin, status, disabled_at, credit_limit_nano, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, CASE WHEN $6 = 'disabled' THEN now() END, $7, $8)
		RETURNING id::text`,
		account.Username,
		account.DisplayName,
		account.PasswordHash,
		account.MustChangePassword,
		account.IsAdmin,
		account.Status,
		account.CreditLimit.Nano(),
		account.ActorID,
	).Scan(&createdID)
	if err != nil {
		return identity.Account{}, mapIdentityError(err)
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO ledger_accounts (identity_account_id, kind) VALUES ($1, 'user')`, createdID); err != nil {
		return identity.Account{}, err
	}
	created, err := accountByID(ctx, transaction, createdID)
	if err != nil {
		return identity.Account{}, err
	}
	if err := insertAudit(ctx, transaction, account.ActorID, "account.created", "account", created.ID, "administrator created invited account", map[string]any{
		"username":          created.Username,
		"credit_limit_nano": created.CreditLimit.Nano(),
		"is_admin":          created.IsAdmin,
		"status":            created.Status,
	}); err != nil {
		return identity.Account{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return identity.Account{}, err
	}
	return created, nil
}

func (s *Store) CreateBootstrapAdmin(ctx context.Context, account identity.NewAccount) (identity.Account, error) {
	transaction, err := s.pool.Begin(ctx)
	if err != nil {
		return identity.Account{}, err
	}
	defer transaction.Rollback(ctx) //nolint:errcheck

	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('oh-my-aihub-bootstrap-admin'))`); err != nil {
		return identity.Account{}, err
	}
	var exists bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE is_admin)`).Scan(&exists); err != nil {
		return identity.Account{}, err
	}
	if exists {
		return identity.Account{}, identity.ErrConflict
	}
	var createdID string
	err = transaction.QueryRow(ctx, `
		INSERT INTO accounts (
			username, display_name, password_hash, must_change_password,
			is_admin, status, credit_limit_nano
		) VALUES ($1, $2, $3, $4, true, $5, 0)
		RETURNING id::text`,
		account.Username,
		account.DisplayName,
		account.PasswordHash,
		account.MustChangePassword,
		account.Status,
	).Scan(&createdID)
	if err != nil {
		return identity.Account{}, mapIdentityError(err)
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO ledger_accounts (identity_account_id, kind) VALUES ($1, 'user')`, createdID); err != nil {
		return identity.Account{}, err
	}
	created, err := accountByID(ctx, transaction, createdID)
	if err != nil {
		return identity.Account{}, err
	}
	if err := insertAudit(ctx, transaction, "", "account.bootstrap_admin_created", "account", created.ID, "first administrator bootstrap", map[string]any{
		"username": created.Username,
	}); err != nil {
		return identity.Account{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return identity.Account{}, err
	}
	return created, nil
}

func (s *Store) ListAccounts(ctx context.Context, query string) ([]identity.Account, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+accountColumnsAliased+`
		FROM accounts a JOIN ledger_accounts la ON la.identity_account_id = a.id
		WHERE $1 = '' OR a.id::text = $1 OR a.username ILIKE '%' || $1 || '%' OR a.display_name ILIKE '%' || $1 || '%'
		ORDER BY a.created_at DESC`, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	accounts := make([]identity.Account, 0)
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (s *Store) UpdateAccount(ctx context.Context, actorID, accountID string, update identity.AccountUpdate) (identity.Account, error) {
	transaction, err := s.pool.Begin(ctx)
	if err != nil {
		return identity.Account{}, err
	}
	defer transaction.Rollback(ctx) //nolint:errcheck
	// Account policy changes and C2C commands share this stable per-account
	// serialization key before taking ledger, identity, order, or hold rows.
	if _, err := transaction.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended('oh-my-aihub-account:' || $1, 0))`, accountID); err != nil {
		return identity.Account{}, err
	}
	// Ledger mutations lock this same row before consulting credit policy. Taking
	// it first serializes a limit/freeze change with every new debit or hold.
	var lockedLedgerAccount int
	if err := transaction.QueryRow(ctx, `
		SELECT 1 FROM ledger_accounts WHERE identity_account_id = $1 FOR UPDATE`, accountID).Scan(&lockedLedgerAccount); err != nil {
		return identity.Account{}, mapIdentityError(err)
	}
	if update.Status != nil || update.IsAdmin != nil {
		if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('oh-my-aihub-administrator-membership'))`); err != nil {
			return identity.Account{}, err
		}
	}

	var targetIsAdmin bool
	var currentStatus string
	var currentVersion int64
	if err := transaction.QueryRow(ctx, `SELECT is_admin, status, version FROM accounts WHERE id = $1 FOR UPDATE`, accountID).Scan(&targetIsAdmin, &currentStatus, &currentVersion); err != nil {
		return identity.Account{}, mapIdentityError(err)
	}
	if currentVersion != update.ExpectedVersion {
		return identity.Account{}, identity.ErrConflict
	}
	desiredAdmin := targetIsAdmin
	if update.IsAdmin != nil {
		desiredAdmin = *update.IsAdmin
	}
	desiredStatus := identity.Status(currentStatus)
	if update.Status != nil {
		desiredStatus = *update.Status
	}
	removesActiveAdministrator := targetIsAdmin && currentStatus == string(identity.StatusActive) && (!desiredAdmin || desiredStatus == identity.StatusDisabled)
	if removesActiveAdministrator {
		if actorID == accountID {
			return identity.Account{}, identity.ErrForbidden
		}
		var activeAdmins int
		if err := transaction.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE is_admin AND status = 'active'`).Scan(&activeAdmins); err != nil {
			return identity.Account{}, err
		}
		if activeAdmins <= 1 {
			return identity.Account{}, identity.ErrConflict
		}
	}

	var status any
	if update.Status != nil {
		status = string(*update.Status)
	}
	var creditLimit any
	if update.CreditLimit != nil {
		creditLimit = update.CreditLimit.Nano()
	}
	var isAdmin any
	if update.IsAdmin != nil {
		isAdmin = *update.IsAdmin
	}
	var creditFrozen any
	if update.CreditFrozen != nil {
		creditFrozen = *update.CreditFrozen
	}
	result, err := transaction.Exec(ctx, `
		UPDATE accounts
		SET status = COALESCE($2, status),
			credit_limit_nano = COALESCE($3, credit_limit_nano),
			is_admin = COALESCE($4, is_admin),
			credit_frozen = COALESCE($6, credit_frozen),
			version = version + 1,
			disabled_at = CASE
				WHEN $2 = 'disabled' AND status <> 'disabled' THEN now()
				WHEN $2 = 'active' THEN NULL
				ELSE disabled_at
			END,
			updated_at = now()
		WHERE id = $1 AND version = $5`, accountID, status, creditLimit, isAdmin, update.ExpectedVersion, creditFrozen)
	if err != nil {
		return identity.Account{}, err
	}
	if result.RowsAffected() != 1 {
		return identity.Account{}, identity.ErrConflict
	}
	account, err := accountByID(ctx, transaction, accountID)
	if err != nil {
		return identity.Account{}, err
	}
	if update.Status != nil && *update.Status == identity.StatusDisabled {
		if _, err := transaction.Exec(ctx, `DELETE FROM sessions WHERE account_id = $1`, accountID); err != nil {
			return identity.Account{}, err
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
	if err := insertAudit(ctx, transaction, actorID, "account.updated", "account", accountID, "administrator updated account access or credit", details); err != nil {
		return identity.Account{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return identity.Account{}, err
	}
	return account, nil
}

const modelColumns = `
	id, name, provider, context_window, parameter_info, input_modalities, output_modalities,
	supports_tools, supports_structured_output, supports_vision,
	input_price_nano_per_million, output_price_nano_per_million,
	cache_write_price_nano_per_million, cache_read_price_nano_per_million,
	status, version, created_at, updated_at, price_updated_at`

func scanModel(row scanner) (catalog.Model, error) {
	var model catalog.Model
	var status string
	var inputPrice, outputPrice, cacheWritePrice, cacheReadPrice int64
	err := row.Scan(
		&model.ID,
		&model.Name,
		&model.Provider,
		&model.ContextWindow,
		&model.ParameterInfo,
		&model.InputModalities,
		&model.OutputModalities,
		&model.SupportsTools,
		&model.SupportsStructuredOutput,
		&model.SupportsVision,
		&inputPrice,
		&outputPrice,
		&cacheWritePrice,
		&cacheReadPrice,
		&status,
		&model.Version,
		&model.CreatedAt,
		&model.UpdatedAt,
		&model.PriceUpdatedAt,
	)
	model.InputPrice = money.FromNano(inputPrice)
	model.OutputPrice = money.FromNano(outputPrice)
	model.CacheWritePrice = money.FromNano(cacheWritePrice)
	model.CacheReadPrice = money.FromNano(cacheReadPrice)
	model.Status = catalog.Status(status)
	return model, mapCatalogError(err)
}

func (s *Store) ListModels(ctx context.Context, includeDisabled bool, query string) ([]catalog.Model, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+modelColumns+`
		FROM models
		WHERE ($1 OR status = 'active')
			AND ($2 = '' OR id ILIKE '%' || $2 || '%' OR name ILIKE '%' || $2 || '%' OR provider ILIKE '%' || $2 || '%')
		ORDER BY provider, name, id`, includeDisabled, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	models := make([]catalog.Model, 0)
	for rows.Next() {
		model, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return models, rows.Err()
}

func (s *Store) GetModel(ctx context.Context, id string, includeDisabled bool) (catalog.Model, error) {
	return scanModel(s.pool.QueryRow(ctx, `
		SELECT `+modelColumns+`
		FROM models
		WHERE id = $1 AND ($2 OR status = 'active')`, id, includeDisabled))
}

func (s *Store) CreateModel(ctx context.Context, actorID string, model catalog.Model) (catalog.Model, error) {
	transaction, err := s.pool.Begin(ctx)
	if err != nil {
		return catalog.Model{}, err
	}
	defer transaction.Rollback(ctx) //nolint:errcheck

	created, err := scanModel(transaction.QueryRow(ctx, `
		INSERT INTO models (
			id, name, provider, context_window, parameter_info,
			input_modalities, output_modalities, supports_tools,
			supports_structured_output, supports_vision,
			input_price_nano_per_million, output_price_nano_per_million,
			cache_write_price_nano_per_million, cache_read_price_nano_per_million,
			status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING `+modelColumns,
		model.ID, model.Name, model.Provider, model.ContextWindow, model.ParameterInfo,
		model.InputModalities, model.OutputModalities, model.SupportsTools,
		model.SupportsStructuredOutput, model.SupportsVision,
		model.InputPrice.Nano(), model.OutputPrice.Nano(), model.CacheWritePrice.Nano(), model.CacheReadPrice.Nano(),
		model.Status,
	))
	if err != nil {
		return catalog.Model{}, err
	}
	if err := insertAudit(ctx, transaction, actorID, "model.created", "model", created.ID, "administrator created catalog model", modelAuditDetails(created)); err != nil {
		return catalog.Model{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return catalog.Model{}, err
	}
	return created, nil
}

func (s *Store) UpdateModel(ctx context.Context, actorID, id string, expectedVersion int64, model catalog.Model) (catalog.Model, error) {
	transaction, err := s.pool.Begin(ctx)
	if err != nil {
		return catalog.Model{}, err
	}
	defer transaction.Rollback(ctx) //nolint:errcheck

	updated, err := scanModel(transaction.QueryRow(ctx, `
		UPDATE models SET
			name = $2,
			provider = $3,
			context_window = $4,
			parameter_info = $5,
			input_modalities = $6,
			output_modalities = $7,
			supports_tools = $8,
			supports_structured_output = $9,
			supports_vision = $10,
			input_price_nano_per_million = $11,
			output_price_nano_per_million = $12,
			cache_write_price_nano_per_million = $13,
			cache_read_price_nano_per_million = $14,
			status = $15,
			version = version + 1,
			updated_at = now(),
			price_updated_at = CASE
				WHEN input_price_nano_per_million <> $11
					OR output_price_nano_per_million <> $12
					OR cache_write_price_nano_per_million <> $13
					OR cache_read_price_nano_per_million <> $14
				THEN now()
				ELSE price_updated_at
			END
		WHERE id = $1 AND version = $16
		RETURNING `+modelColumns,
		id, model.Name, model.Provider, model.ContextWindow, model.ParameterInfo,
		model.InputModalities, model.OutputModalities, model.SupportsTools,
		model.SupportsStructuredOutput, model.SupportsVision,
		model.InputPrice.Nano(), model.OutputPrice.Nano(), model.CacheWritePrice.Nano(), model.CacheReadPrice.Nano(),
		model.Status, expectedVersion,
	))
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			var exists bool
			if queryErr := transaction.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM models WHERE id = $1)`, id).Scan(&exists); queryErr != nil {
				return catalog.Model{}, queryErr
			}
			if exists {
				return catalog.Model{}, catalog.ErrConflict
			}
		}
		return catalog.Model{}, err
	}
	if err := insertAudit(ctx, transaction, actorID, "model.updated", "model", id, "administrator updated catalog model", modelAuditDetails(updated)); err != nil {
		return catalog.Model{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return catalog.Model{}, err
	}
	return updated, nil
}

func modelAuditDetails(model catalog.Model) map[string]any {
	return map[string]any{
		"status":                             model.Status,
		"input_price_nano_per_million":       model.InputPrice.Nano(),
		"output_price_nano_per_million":      model.OutputPrice.Nano(),
		"cache_write_price_nano_per_million": model.CacheWritePrice.Nano(),
		"cache_read_price_nano_per_million":  model.CacheReadPrice.Nano(),
		"context_window":                     model.ContextWindow,
		"version":                            model.Version,
	}
}

type queryExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func insertAudit(ctx context.Context, executor queryExecutor, actorID, action, targetType, targetID, reason string, details map[string]any) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var actor any
	if actorID != "" {
		actor = actorID
	}
	_, err = executor.Exec(ctx, `
		INSERT INTO audit_events (actor_account_id, action, target_type, target_id, reason, details)
		VALUES ($1, $2, $3, $4, $5, $6)`, actor, action, targetType, targetID, reason, encoded)
	return err
}

func mapIdentityError(err error) error {
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

func mapCatalogError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return catalog.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return catalog.ErrConflict
	}
	return fmt.Errorf("catalog store: %w", err)
}
