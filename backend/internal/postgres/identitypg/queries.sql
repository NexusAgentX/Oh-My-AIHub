-- 账户与其账本余额投影总是成对读取：sqlc.embed 让所有查询共享同一映射函数。

-- name: GetAccountByID :one
SELECT sqlc.embed(a), sqlc.embed(la)
FROM accounts a JOIN ledger_accounts la ON la.identity_account_id = a.id
WHERE a.id = @id;

-- name: GetAccountByUsername :one
SELECT sqlc.embed(a), sqlc.embed(la)
FROM accounts a JOIN ledger_accounts la ON la.identity_account_id = a.id
WHERE a.username = @username;

-- name: GetAccountBySession :one
SELECT sqlc.embed(a), sqlc.embed(la)
FROM accounts a
JOIN sessions s ON s.account_id = a.id
JOIN ledger_accounts la ON la.identity_account_id = a.id
WHERE s.token_hash = @token_hash
	AND s.expires_at > @now
	AND s.password_version = a.password_version
	AND a.status = 'active';

-- name: ListAccounts :many
SELECT sqlc.embed(a), sqlc.embed(la)
FROM accounts a JOIN ledger_accounts la ON la.identity_account_id = a.id
WHERE sqlc.arg(query)::text = '' OR a.id::text = sqlc.arg(query)::text
	OR a.username ILIKE '%' || sqlc.arg(query)::text || '%'
	OR a.display_name ILIKE '%' || sqlc.arg(query)::text || '%'
ORDER BY a.created_at DESC;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= now();

-- name: InsertSession :exec
INSERT INTO sessions (token_hash, account_id, password_version, expires_at)
VALUES (@token_hash, @account_id, @password_version, @expires_at);

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = @token_hash;

-- name: DeleteSessionsByAccount :exec
DELETE FROM sessions WHERE account_id = @account_id;

-- name: ReplaceActivePassword :execrows
UPDATE accounts
SET password_hash = @password_hash,
	must_change_password = false,
	password_version = password_version + 1,
	password_changed_at = @changed_at::timestamptz,
	updated_at = @changed_at::timestamptz
WHERE id = @id AND status = 'active' AND password_version = @expected_password_version;

-- name: ResetPassword :execrows
UPDATE accounts
SET password_hash = @password_hash,
	must_change_password = true,
	password_version = password_version + 1,
	password_changed_at = @changed_at::timestamptz,
	updated_at = @changed_at::timestamptz
WHERE id = @id AND password_version = @expected_password_version;

-- name: InsertAccount :one
INSERT INTO accounts (
	username, display_name, password_hash, must_change_password,
	is_admin, status, disabled_at, credit_limit_nano, created_by
) VALUES (
	@username, @display_name, @password_hash, @must_change_password,
	@is_admin, @status::text, CASE WHEN @status::text = 'disabled' THEN now() END,
	@credit_limit_nano, sqlc.narg(created_by)
)
RETURNING id;

-- name: InsertBootstrapAdmin :one
INSERT INTO accounts (
	username, display_name, password_hash, must_change_password,
	is_admin, status, credit_limit_nano
) VALUES (@username, @display_name, @password_hash, @must_change_password, true, @status, 0)
RETURNING id;

-- name: InsertUserLedgerAccount :exec
INSERT INTO ledger_accounts (identity_account_id, kind) VALUES (@identity_account_id::uuid, 'user');

-- name: AdministratorExists :one
SELECT EXISTS (SELECT 1 FROM accounts WHERE is_admin);

-- name: LockBootstrapAdmin :exec
SELECT pg_advisory_xact_lock(hashtext('oh-my-aihub-bootstrap-admin'));

-- name: LockAdministratorMembership :exec
SELECT pg_advisory_xact_lock(hashtext('oh-my-aihub-administrator-membership'));

-- Account policy changes and C2C commands share this stable per-account
-- serialization key before taking ledger, identity, order, or hold rows.
-- name: LockAccount :exec
SELECT pg_advisory_xact_lock(hashtextextended('oh-my-aihub-account:' || @account_id::text, 0));

-- Ledger mutations lock this same row before consulting credit policy.
-- name: LockLedgerAccountByIdentity :one
SELECT 1::int FROM ledger_accounts WHERE identity_account_id = @identity_account_id::uuid FOR UPDATE;

-- name: LockAccountForUpdate :one
SELECT is_admin, status, version FROM accounts WHERE id = @id FOR UPDATE;

-- name: CountActiveAdministrators :one
SELECT count(*) FROM accounts WHERE is_admin AND status = 'active';

-- name: UpdateAccountPolicy :execrows
UPDATE accounts
SET status = COALESCE(sqlc.narg(status)::text, status),
	credit_limit_nano = COALESCE(sqlc.narg(credit_limit_nano)::bigint, credit_limit_nano),
	is_admin = COALESCE(sqlc.narg(is_admin)::boolean, is_admin),
	credit_frozen = COALESCE(sqlc.narg(credit_frozen)::boolean, credit_frozen),
	version = version + 1,
	disabled_at = CASE
		WHEN sqlc.narg(status)::text = 'disabled' AND status <> 'disabled' THEN now()
		WHEN sqlc.narg(status)::text = 'active' THEN NULL
		ELSE disabled_at
	END,
	updated_at = now()
WHERE id = @id AND version = @expected_version;
