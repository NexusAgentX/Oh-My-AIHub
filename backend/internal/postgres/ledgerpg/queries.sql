-- 账户读取 -----------------------------------------------------------------

-- 钱包：用户账户按 identity_account_id，系统账户按 system_code；调用方恰好传入其一。
-- name: GetWallet :one
SELECT sqlc.embed(la),
	COALESCE(a.credit_limit_nano, 0)::bigint AS credit_limit_nano,
	COALESCE(a.credit_frozen, false)::boolean AS credit_frozen,
	COALESCE(a.status, 'active')::text AS account_status
FROM ledger_accounts la
LEFT JOIN accounts a ON a.id = la.identity_account_id
WHERE la.identity_account_id = sqlc.narg('identity_account_id')::uuid
	OR la.system_code = sqlc.narg('system_code')::text;

-- name: ListEntries :many
SELECT sqlc.embed(e),
	la.kind AS account_kind,
	COALESCE(la.identity_account_id::text, '')::text AS identity_account_id,
	t.kind AS transaction_kind,
	t.reason AS transaction_reason,
	t.reference_type,
	t.reference_id,
	COALESCE(t.actor_account_id::text, '')::text AS actor_account_id,
	COALESCE(t.reversal_of_transaction_id::text, '')::text AS reversal_of_transaction_id,
	COALESCE(t.hold_id::text, '')::text AS hold_id,
	COALESCE((
		SELECT jsonb_agg(jsonb_build_object(
			'account_kind', other_account.kind,
			'identity_account_id', COALESCE(other_account.identity_account_id::text, ''),
			'business_role', other_entry.business_role,
			'amount_nano', other_entry.amount_nano
		) ORDER BY other_entry.entry_ordinal)
		FROM ledger_entries other_entry
		JOIN ledger_accounts other_account ON other_account.id = other_entry.ledger_account_id
		WHERE other_entry.transaction_id = e.transaction_id AND other_entry.id <> e.id
	), '[]'::jsonb)::jsonb AS counterparties
FROM ledger_entries e
JOIN ledger_accounts la ON la.id = e.ledger_account_id
JOIN ledger_transactions t ON t.id = e.transaction_id
WHERE (la.identity_account_id = sqlc.narg('identity_account_id')::uuid
		OR la.system_code = sqlc.narg('system_code')::text)
	AND (@before_id::bigint = 0 OR e.id < @before_id::bigint)
ORDER BY e.id DESC
LIMIT @row_limit::bigint;

-- 对账指标：所有聚合用 numeric 求和并以文本返回，避免 BIGINT 溢出。
-- name: GetMetrics :one
WITH entry_totals AS (
	SELECT ledger_account_id, sum(amount_nano::numeric) AS posted_source
	FROM ledger_entries GROUP BY ledger_account_id
), hold_totals AS (
	SELECT ledger_account_id,
	       COALESCE(sum(remaining_nano::numeric) FILTER (WHERE purpose = 'asset_reservation'), 0) AS asset_source,
	       COALESCE(sum(remaining_nano::numeric) FILTER (WHERE purpose = 'spend_authorization'), 0) AS authorization_source
	FROM ledger_holds GROUP BY ledger_account_id
), reconciled AS (
	SELECT la.*, a.credit_limit_nano, a.credit_frozen,
	       COALESCE(et.posted_source, 0) AS posted_source,
	       COALESCE(ht.asset_source, 0) AS asset_source,
	       COALESCE(ht.authorization_source, 0) AS authorization_source
	FROM ledger_accounts la
	LEFT JOIN accounts a ON a.id = la.identity_account_id
	LEFT JOIN entry_totals et ON et.ledger_account_id = la.id
	LEFT JOIN hold_totals ht ON ht.ledger_account_id = la.id
)
SELECT
	COALESCE(sum(posted_balance_nano::numeric), 0)::text AS total_posted,
	COALESCE(sum(GREATEST(posted_balance_nano, 0)::numeric), 0)::text AS positive_posted,
	COALESCE(sum(LEAST(posted_balance_nano, 0)::numeric), 0)::text AS negative_posted,
	COALESCE(sum(CASE WHEN kind = 'user' THEN credit_limit_nano::numeric ELSE 0 END), 0)::text AS total_credit_limit,
	COALESCE(sum(CASE WHEN kind = 'user' THEN GREATEST(0::numeric, -posted_balance_nano::numeric) ELSE 0 END), 0)::text AS used_credit,
	COALESCE(sum(asset_reserved_nano::numeric), 0)::text AS asset_reserved,
	COALESCE(sum(spend_authorized_nano::numeric), 0)::text AS spend_authorized,
	COALESCE(max(posted_balance_nano) FILTER (WHERE kind = 'platform_incentive'), 0)::text AS incentive_posted,
	COALESCE(max(posted_balance_nano) FILTER (WHERE kind = 'platform_loss'), 0)::text AS loss_posted,
	(count(*) FILTER (WHERE kind = 'user' AND GREATEST(0::numeric, -posted_balance_nano::numeric) > CASE WHEN credit_frozen THEN 0 ELSE credit_limit_nano END::numeric))::bigint AS over_limit_accounts,
	(count(*) FILTER (WHERE kind = 'user' AND credit_frozen))::bigint AS credit_frozen_accounts,
	count(*)::bigint AS account_count,
	COALESCE(sum(abs(posted_balance_nano::numeric - posted_source)), 0)::text AS posted_difference,
	(count(*) FILTER (WHERE posted_balance_nano::numeric <> posted_source))::bigint AS posted_mismatch_accounts,
	COALESCE(sum(abs(asset_reserved_nano::numeric - asset_source)), 0)::text AS asset_difference,
	COALESCE(sum(abs(spend_authorized_nano::numeric - authorization_source)), 0)::text AS authorization_difference,
	(count(*) FILTER (WHERE asset_reserved_nano::numeric <> asset_source OR spend_authorized_nano::numeric <> authorization_source))::bigint AS hold_mismatch_accounts
FROM reconciled;

-- 记账 ---------------------------------------------------------------------

-- name: FindLedgerAccountIDByIdentity :one
SELECT id::text FROM ledger_accounts WHERE identity_account_id = @identity_account_id;

-- name: FindLedgerAccountIDBySystemCode :one
SELECT id::text FROM ledger_accounts WHERE system_code = @system_code;

-- 行锁：调用方按 id 升序逐个加锁，保持全局锁顺序。
-- name: LockLedgerAccount :one
SELECT sqlc.embed(la) FROM ledger_accounts la WHERE la.id = @id FOR UPDATE;

-- 信用状态不加锁：账户行由 identity 领域的写入方负责。
-- name: GetAccountCreditState :one
SELECT credit_limit_nano, credit_frozen, status FROM accounts WHERE id = @id;

-- name: LockAccountAdminState :one
SELECT is_admin, status FROM accounts WHERE id = @id FOR UPDATE;

-- 幂等命令：首次写入返回键，冲突时无行（pgx.ErrNoRows）表示重放或冲突。
-- name: ReserveLedgerCommand :one
INSERT INTO ledger_commands (idempotency_key, operation, payload_hash)
VALUES (@idempotency_key, @operation, @payload_hash) ON CONFLICT DO NOTHING
RETURNING idempotency_key;

-- name: GetLedgerCommand :one
SELECT operation, payload_hash, COALESCE(result_id::text, '')::text AS result_id, result_payload
FROM ledger_commands WHERE operation = @operation AND idempotency_key = @idempotency_key;

-- name: CompleteLedgerCommand :execrows
UPDATE ledger_commands
SET result_id = @result_id, result_payload = @result_payload, completed_at = now()
WHERE operation = @operation AND idempotency_key = @idempotency_key
  AND result_id IS NULL AND result_payload IS NULL AND completed_at IS NULL;

-- name: LockSealedTransaction :one
SELECT kind, reference_type FROM ledger_transactions WHERE id = @id AND sealed FOR UPDATE;

-- name: IsGatewaySettlementTransaction :one
SELECT EXISTS (
	SELECT 1 FROM api_call_settlements
	WHERE capture_transaction_id = @transaction_id OR self_transaction_id = @transaction_id
)::boolean AS settled;

-- name: IsTransactionReversed :one
SELECT EXISTS (
	SELECT 1 FROM ledger_transactions WHERE reversal_of_transaction_id = @transaction_id
)::boolean AS reversed;

-- name: InsertTransaction :one
INSERT INTO ledger_transactions (command_operation, idempotency_key, kind, reason, reference_type, reference_id, actor_account_id, reversal_of_transaction_id, hold_id)
VALUES (@command_operation, @idempotency_key, @kind, @reason, @reference_type, @reference_id,
	NULLIF(@actor_account_id::text, '')::uuid, NULLIF(@reversal_of_transaction_id::text, '')::uuid, NULLIF(@hold_id::text, '')::uuid)
RETURNING id::text;

-- name: InsertEntry :exec
INSERT INTO ledger_entries (transaction_id, ledger_account_id, entry_ordinal, business_role, amount_nano, posted_balance_before_nano, posted_balance_after_nano)
VALUES (@transaction_id, @ledger_account_id, @entry_ordinal, @business_role, @amount_nano, @posted_balance_before_nano, @posted_balance_after_nano);

-- name: SetPostedBalance :exec
UPDATE ledger_accounts SET posted_balance_nano = @posted_balance_nano, version = version + 1, updated_at = now() WHERE id = @id;

-- name: SealTransaction :exec
UPDATE ledger_transactions SET sealed = true WHERE id = @id;

-- name: GetSealedTransaction :one
SELECT sqlc.embed(t) FROM ledger_transactions t WHERE t.id = @id AND t.sealed;

-- name: ListTransactionEntries :many
SELECT sqlc.embed(e), la.kind AS account_kind,
	COALESCE(la.identity_account_id::text, '')::text AS identity_account_id
FROM ledger_entries e JOIN ledger_accounts la ON la.id = e.ledger_account_id
WHERE e.transaction_id = @transaction_id ORDER BY e.entry_ordinal;

-- 冻结 ---------------------------------------------------------------------

-- name: InsertHold :one
INSERT INTO ledger_holds (
	ledger_account_id, create_idempotency_key, purpose, funding_policy, amount_nano,
	remaining_nano, reason, business_type, business_id
) VALUES (@ledger_account_id, @create_idempotency_key, @purpose, @funding_policy, @amount_nano, @amount_nano, @reason, @business_type, @business_id)
RETURNING id::text;

-- name: SetAccountReservations :exec
UPDATE ledger_accounts
SET asset_reserved_nano = @asset_reserved_nano, spend_authorized_nano = @spend_authorized_nano, version = version + 1, updated_at = now()
WHERE id = @id;

-- name: SetAssetReserved :exec
UPDATE ledger_accounts SET asset_reserved_nano = @asset_reserved_nano, version = version + 1, updated_at = now() WHERE id = @id;

-- name: SetSpendAuthorized :exec
UPDATE ledger_accounts SET spend_authorized_nano = @spend_authorized_nano, version = version + 1, updated_at = now() WHERE id = @id;

-- name: ApplyHoldAmount :execrows
UPDATE ledger_holds
SET remaining_nano = remaining_nano - @amount_nano,
    captured_nano = captured_nano + @captured_nano,
    released_nano = released_nano + @released_nano,
    status = CASE WHEN remaining_nano = @amount_nano THEN 'closed' ELSE 'active' END,
    updated_at = now()
WHERE id = @id AND status = 'active' AND remaining_nano >= @amount_nano;

-- name: InsertReleaseHoldEvent :exec
INSERT INTO ledger_hold_events (hold_id, command_operation, idempotency_key, kind, business_id, amount_nano, reason)
VALUES (@hold_id, @command_operation, @idempotency_key, 'release', @business_id, @amount_nano, @reason);

-- name: InsertCaptureHoldEvent :exec
INSERT INTO ledger_hold_events (hold_id, command_operation, idempotency_key, kind, business_id, amount_nano, transaction_id, reason)
VALUES (@hold_id, @command_operation, @idempotency_key, 'capture', @business_id, @amount_nano, @transaction_id, @reason);

-- name: GetHold :one
SELECT sqlc.embed(h), COALESCE(la.identity_account_id::text, '')::text AS owner_account_id
FROM ledger_holds h JOIN ledger_accounts la ON la.id = h.ledger_account_id
WHERE h.id = @id;

-- 只锁 ledger_holds 行（FOR UPDATE OF h），账户行由随后的 LockLedgerAccount 加锁。
-- name: GetHoldForUpdate :one
SELECT sqlc.embed(h), COALESCE(la.identity_account_id::text, '')::text AS owner_account_id
FROM ledger_holds h JOIN ledger_accounts la ON la.id = h.ledger_account_id
WHERE h.id = @id FOR UPDATE OF h;
