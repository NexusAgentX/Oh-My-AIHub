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

-- Books the lines of one posting in a single round trip: the new balance of
-- every account and its entry with that balance after. The accounts are locked
-- by LockLedgerAccounts already and the balances after were computed from what
-- it returned, so the rows are written without being read again. The three
-- arrays hold one element per line, in line order; the entries are inserted in
-- that order, so their ids follow it. Nothing about the locking changes: this
-- statement is the last thing a settlement does while it holds the locks, and
-- the shorter it is, the sooner the next posting over the same accounts (the
-- platform_revenue row of every API call) can start.
-- name: BookEntries :exec
WITH lines AS (
    SELECT l.ledger_account_id, l.amount_nano, l.balance_after_nano, l.position
    FROM ROWS FROM (unnest(sqlc.arg(ledger_account_ids)::uuid[]), unnest(sqlc.arg(amounts_nano)::bigint[]), unnest(sqlc.arg(balances_after_nano)::bigint[]))
         WITH ORDINALITY AS l(ledger_account_id, amount_nano, balance_after_nano, position)
), moved AS (
    UPDATE ledger_accounts a SET balance_nano = lines.balance_after_nano, updated_at = now()
    FROM lines
    WHERE a.id = lines.ledger_account_id
)
INSERT INTO ledger_entries (transaction_id, ledger_account_id, amount_nano, balance_after_nano)
SELECT sqlc.arg(transaction_id), ledger_account_id, amount_nano, balance_after_nano
FROM lines
ORDER BY position;

-- name: GetPoints :one
SELECT l.balance_nano, a.credit_limit_nano, l.updated_at
FROM ledger_accounts l
JOIN accounts a ON a.id = l.account_id
WHERE l.account_id = $1;

-- The balances and credit limits of several users at once: the gateway's
-- balance cache loads and refreshes its entries with it.
-- name: ListPoints :many
SELECT l.account_id, l.balance_nano, a.credit_limit_nano, l.updated_at
FROM ledger_accounts l
JOIN accounts a ON a.id = l.account_id
WHERE l.account_id = ANY(sqlc.arg(account_ids)::uuid[]);

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
  AND (sqlc.arg(transaction_id)::text = '' OR t.id::text = sqlc.arg(transaction_id))
  AND (sqlc.arg(api_key_id)::text = '' OR c.api_key_id::text = sqlc.arg(api_key_id))
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR e.created_at >= sqlc.narg(from_time))
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR e.created_at < sqlc.narg(to_time))
  AND (sqlc.arg(before_id)::bigint = 0 OR e.id < sqlc.arg(before_id))
ORDER BY e.id DESC
LIMIT sqlc.arg(row_limit);
