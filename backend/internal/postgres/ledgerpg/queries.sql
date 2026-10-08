-- name: InsertUserLedgerAccount :exec
INSERT INTO ledger_accounts (kind, account_id) VALUES ('user', $1);

-- name: InsertTransaction :one
INSERT INTO ledger_transactions (type, idempotency_key, related_type, related_id, actor_id, reason)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING id, created_at;

-- name: GetTransactionByKey :one
SELECT * FROM ledger_transactions WHERE idempotency_key = $1;

-- name: ListTransactionEntries :many
SELECT e.ledger_account_id, l.account_id, l.system_code, e.amount_nano, e.balance_after_nano
FROM ledger_entries e
JOIN ledger_accounts l ON l.id = e.ledger_account_id
WHERE e.transaction_id = $1
ORDER BY e.id;

-- Resolves and locks the ledger accounts of one posting in id order, so
-- concurrent postings over overlapping accounts cannot deadlock.
-- name: LockLedgerAccounts :many
SELECT id, account_id, system_code, balance_nano
FROM ledger_accounts
WHERE account_id = ANY(sqlc.arg(user_ids)::uuid[])
   OR system_code = ANY(sqlc.arg(system_codes)::text[])
ORDER BY id
FOR UPDATE;

-- name: UpdateLedgerBalance :exec
UPDATE ledger_accounts SET balance_nano = $2, updated_at = now() WHERE id = $1;

-- name: InsertEntry :exec
INSERT INTO ledger_entries (transaction_id, ledger_account_id, amount_nano, balance_after_nano)
VALUES ($1, $2, $3, $4);

-- name: GetPoints :one
SELECT l.balance_nano, a.credit_limit_nano, l.updated_at
FROM ledger_accounts l
JOIN accounts a ON a.id = l.account_id
WHERE l.account_id = $1;

-- name: ListUserEntries :many
SELECT e.id, e.transaction_id, t.type, t.reason, t.related_type, t.related_id,
       e.amount_nano, e.balance_after_nano, c.api_key_id, k.name AS api_key_name, e.created_at
FROM ledger_entries e
JOIN ledger_accounts l ON l.id = e.ledger_account_id
JOIN ledger_transactions t ON t.id = e.transaction_id
LEFT JOIN calls c ON t.related_type = 'call' AND c.id = t.related_id
LEFT JOIN api_keys k ON k.id = c.api_key_id
WHERE l.account_id = sqlc.arg(account_id)
  AND (sqlc.arg(type)::text = '' OR t.type = sqlc.arg(type))
  AND (sqlc.arg(api_key_id)::text = '' OR c.api_key_id::text = sqlc.arg(api_key_id))
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR e.created_at >= sqlc.narg(from_time))
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR e.created_at < sqlc.narg(to_time))
  AND (sqlc.arg(before_id)::bigint = 0 OR e.id < sqlc.arg(before_id))
ORDER BY e.id DESC
LIMIT sqlc.arg(row_limit);
