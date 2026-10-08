-- name: GetAccountByUsername :one
SELECT * FROM accounts WHERE username = $1;

-- name: GetAccountByID :one
SELECT * FROM accounts WHERE id = $1;

-- name: GetAccountBySession :one
SELECT a.*
FROM sessions s
JOIN accounts a ON a.id = s.account_id
WHERE s.token_hash = sqlc.arg(token_hash)
  AND s.expires_at > sqlc.arg(now)::timestamptz
  AND s.password_version = a.password_version
  AND a.status = 'active';

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= now();

-- name: InsertSession :exec
INSERT INTO sessions (token_hash, account_id, password_version, expires_at)
VALUES ($1, $2, $3, $4);

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: DeleteSessionsByAccount :exec
DELETE FROM sessions WHERE account_id = $1;

-- name: ReplacePassword :execrows
UPDATE accounts
SET password_hash = sqlc.arg(password_hash),
    password_version = password_version + 1,
    must_change_password = sqlc.arg(must_change_password),
    password_changed_at = sqlc.arg(changed_at),
    updated_at = sqlc.arg(changed_at)
WHERE id = sqlc.arg(id)
  AND password_version = sqlc.arg(expected_password_version);

-- name: LockBootstrap :exec
SELECT pg_advisory_xact_lock(hashtext('oh-my-aihub:bootstrap-admin'));

-- name: HasAdministrator :one
SELECT EXISTS (SELECT 1 FROM accounts WHERE is_admin) AS has_administrator;

-- name: DefaultCreditLimit :one
SELECT default_credit_limit_nano FROM settings;

-- name: InsertAccount :one
INSERT INTO accounts (username, display_name, password_hash, is_admin, must_change_password, credit_limit_nano)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: LockAccount :one
SELECT * FROM accounts WHERE id = $1 FOR UPDATE;

-- name: CountActiveAdministrators :one
SELECT count(*) FROM accounts WHERE is_admin AND status = 'active';

-- name: LockActiveAdministrators :exec
SELECT pg_advisory_xact_lock(hashtext('oh-my-aihub:active-administrators'));

-- name: UpdateAccount :one
UPDATE accounts
SET display_name = $2, status = $3, credit_limit_nano = $4, is_admin = $5, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetAdminAccount :one
SELECT sqlc.embed(a), coalesce(l.balance_nano, 0)::bigint AS balance_nano
FROM accounts a
LEFT JOIN ledger_accounts l ON l.account_id = a.id
WHERE a.id = $1;

-- name: ListAdminAccounts :many
SELECT sqlc.embed(a), coalesce(l.balance_nano, 0)::bigint AS balance_nano
FROM accounts a
LEFT JOIN ledger_accounts l ON l.account_id = a.id
WHERE (sqlc.arg(query)::text = '' OR a.username ILIKE '%' || sqlc.arg(query) || '%' OR a.display_name ILIKE '%' || sqlc.arg(query) || '%' OR a.id::text = sqlc.arg(query))
  AND (sqlc.arg(status)::text = '' OR a.status = sqlc.arg(status))
  AND a.username > sqlc.arg(after_username)::text
ORDER BY a.username
LIMIT sqlc.arg(row_limit);
