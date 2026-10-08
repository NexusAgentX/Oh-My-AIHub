-- Feature G：积分观测的只读查询（用户视角与管理员全局）。
-- 余额一律按分录求和得到（与 balance_nano 的一致性由核对 ② 单独检验），
-- 这样期初 + 区间变动 = 期末在构造上成立。

-- ---- 用户 ----

-- name: UserBalanceBefore :one
SELECT coalesce(sum(e.amount_nano), 0)::bigint AS balance_nano
FROM ledger_entries e
JOIN ledger_accounts l ON l.id = e.ledger_account_id
WHERE l.account_id = sqlc.arg(account_id)::uuid AND e.created_at < sqlc.arg(before)::timestamptz;

-- name: UserDailyNets :many
SELECT to_char(e.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD')::text AS day,
       coalesce(sum(e.amount_nano) FILTER (WHERE e.amount_nano > 0), 0)::bigint AS income_nano,
       coalesce(-sum(e.amount_nano) FILTER (WHERE e.amount_nano < 0), 0)::bigint AS spend_nano,
       coalesce(sum(e.amount_nano), 0)::bigint AS net_nano
FROM ledger_entries e
JOIN ledger_accounts l ON l.id = e.ledger_account_id
WHERE l.account_id = sqlc.arg(account_id)::uuid
  AND e.created_at >= sqlc.arg(from_time)::timestamptz AND e.created_at < sqlc.arg(to_time)::timestamptz
GROUP BY 1
ORDER BY 1;

-- name: UserPeriodByType :many
SELECT t.type, (e.amount_nano > 0)::boolean AS inflow, coalesce(sum(e.amount_nano), 0)::bigint AS amount_nano
FROM ledger_entries e
JOIN ledger_accounts l ON l.id = e.ledger_account_id
JOIN ledger_transactions t ON t.id = e.transaction_id
WHERE l.account_id = sqlc.arg(account_id)::uuid
  AND e.created_at >= sqlc.arg(from_time)::timestamptz AND e.created_at < sqlc.arg(to_time)::timestamptz
GROUP BY 1, 2;

-- name: UserEntriesByDay :many
SELECT to_char(e.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD')::text AS day,
       coalesce(sum(e.amount_nano) FILTER (WHERE e.amount_nano > 0), 0)::bigint AS income_nano,
       coalesce(-sum(e.amount_nano) FILTER (WHERE e.amount_nano < 0), 0)::bigint AS spend_nano,
       coalesce(sum(e.amount_nano), 0)::bigint AS net_nano
FROM ledger_entries e
JOIN ledger_accounts l ON l.id = e.ledger_account_id
JOIN ledger_transactions t ON t.id = e.transaction_id
LEFT JOIN calls c ON t.related_type = 'call' AND c.id = t.related_id
WHERE l.account_id = sqlc.arg(account_id)::uuid
  AND (sqlc.arg(type)::text = '' OR t.type = sqlc.arg(type))
  AND (sqlc.arg(api_key_id)::text = '' OR c.api_key_id::text = sqlc.arg(api_key_id))
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR e.created_at >= sqlc.narg(from_time))
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR e.created_at < sqlc.narg(to_time))
GROUP BY 1
ORDER BY 1 DESC;

-- name: UserEntriesByKey :many
SELECT c.api_key_id, coalesce(max(k.name), '')::text AS api_key_name,
       coalesce(-sum(e.amount_nano), 0)::bigint AS spend_nano, count(*)::bigint AS entries
FROM ledger_entries e
JOIN ledger_accounts l ON l.id = e.ledger_account_id
JOIN ledger_transactions t ON t.id = e.transaction_id
LEFT JOIN calls c ON t.related_type = 'call' AND c.id = t.related_id
LEFT JOIN api_keys k ON k.id = c.api_key_id
WHERE l.account_id = sqlc.arg(account_id)::uuid
  AND t.type = 'api_call' AND e.amount_nano < 0
  AND (sqlc.arg(type)::text = '' OR t.type = sqlc.arg(type))
  AND (sqlc.arg(api_key_id)::text = '' OR c.api_key_id::text = sqlc.arg(api_key_id))
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR e.created_at >= sqlc.narg(from_time))
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR e.created_at < sqlc.narg(to_time))
GROUP BY c.api_key_id
ORDER BY 3 DESC;

-- name: ExportUserEntries :many
SELECT e.id, e.transaction_id, t.type, t.reason, t.related_type, t.related_id,
       e.amount_nano, e.balance_after_nano, c.api_key_id, k.name AS api_key_name, e.created_at
FROM ledger_entries e
JOIN ledger_accounts l ON l.id = e.ledger_account_id
JOIN ledger_transactions t ON t.id = e.transaction_id
LEFT JOIN calls c ON t.related_type = 'call' AND c.id = t.related_id
LEFT JOIN api_keys k ON k.id = c.api_key_id
WHERE l.account_id = sqlc.arg(account_id)::uuid
  AND (sqlc.arg(type)::text = '' OR t.type = sqlc.arg(type))
  AND (sqlc.arg(api_key_id)::text = '' OR c.api_key_id::text = sqlc.arg(api_key_id))
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR e.created_at >= sqlc.narg(from_time))
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR e.created_at < sqlc.narg(to_time))
ORDER BY e.id DESC
LIMIT sqlc.arg(row_limit);

-- ---- 管理员：余额结构 ----

-- name: LedgerBalances :one
SELECT coalesce(sum(l.balance_nano) FILTER (WHERE l.kind = 'user' AND l.balance_nano > 0), 0)::bigint AS user_positive_nano,
       coalesce(sum(l.balance_nano) FILTER (WHERE l.kind = 'user' AND l.balance_nano < 0), 0)::bigint AS user_negative_nano,
       coalesce(sum(l.balance_nano) FILTER (WHERE l.system_code = 'c2c_escrow'), 0)::bigint AS escrow_nano,
       coalesce(sum(l.balance_nano) FILTER (WHERE l.system_code = 'platform_revenue'), 0)::bigint AS platform_revenue_nano,
       coalesce(sum(l.balance_nano) FILTER (WHERE l.system_code = 'bad_debt'), 0)::bigint AS bad_debt_nano,
       coalesce(sum(l.balance_nano), 0)::bigint AS total_nano
FROM ledger_accounts l;

-- name: TotalCreditLimit :one
SELECT coalesce(sum(credit_limit_nano), 0)::bigint AS credit_limit_nano FROM accounts;

-- name: CountWriteOffs :one
SELECT count(*)::bigint FROM ledger_transactions WHERE type = 'bad_debt_writeoff';

-- name: EscrowCounts :one
SELECT (SELECT count(*) FROM c2c_orders WHERE available_nano + in_trade_nano > 0)::bigint AS orders,
       (SELECT count(*) FROM c2c_trades WHERE status IN ('awaiting_payment', 'paid', 'disputed'))::bigint AS trades_in_progress;

-- ---- 管理员：走势（只用分录与交易，不建快照表） ----

-- name: LedgerOpeningBalances :many
SELECT l.id, l.kind, l.system_code, coalesce(sum(e.amount_nano), 0)::bigint AS balance_nano
FROM ledger_accounts l
LEFT JOIN ledger_entries e ON e.ledger_account_id = l.id AND e.created_at < sqlc.arg(before)::timestamptz
GROUP BY l.id, l.kind, l.system_code;

-- name: LedgerDailyNets :many
SELECT e.ledger_account_id,
       to_char(e.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD')::text AS day,
       sum(e.amount_nano)::bigint AS net_nano
FROM ledger_entries e
WHERE e.created_at >= sqlc.arg(from_time)::timestamptz AND e.created_at < sqlc.arg(to_time)::timestamptz
GROUP BY 1, 2;

-- name: LedgerAPIDaily :many
SELECT to_char(e.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD')::text AS day,
       coalesce(sum(e.amount_nano) FILTER (WHERE e.amount_nano > 0), 0)::bigint AS volume_nano,
       coalesce(sum(e.amount_nano) FILTER (WHERE l.system_code = 'platform_revenue'), 0)::bigint AS fee_nano
FROM ledger_entries e
JOIN ledger_transactions t ON t.id = e.transaction_id
JOIN ledger_accounts l ON l.id = e.ledger_account_id
WHERE t.type = 'api_call'
  AND e.created_at >= sqlc.arg(from_time)::timestamptz AND e.created_at < sqlc.arg(to_time)::timestamptz
GROUP BY 1;

-- name: C2CDaily :many
SELECT to_char(coalesce(released_at, resolved_at) AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD')::text AS day,
       sum(amount_nano)::bigint AS volume_nano, sum(total_fen)::bigint AS total_fen
FROM c2c_trades
WHERE status IN ('released', 'resolved_to_buyer')
  AND coalesce(released_at, resolved_at) >= sqlc.arg(from_time)::timestamptz
  AND coalesce(released_at, resolved_at) < sqlc.arg(to_time)::timestamptz
GROUP BY 1;

-- ---- 管理员：风险 ----

-- name: NegativeAccounts :many
SELECT a.id, a.username, a.display_name, l.balance_nano, a.credit_limit_nano,
       (SELECT max(e.created_at) FROM ledger_entries e
         WHERE e.ledger_account_id = l.id AND e.balance_after_nano < 0 AND e.balance_after_nano - e.amount_nano >= 0) AS negative_since,
       (SELECT max(e.created_at) FROM ledger_entries e WHERE e.ledger_account_id = l.id) AS last_entry_at
FROM ledger_accounts l
JOIN accounts a ON a.id = l.account_id
WHERE l.kind = 'user' AND l.balance_nano < 0
ORDER BY l.balance_nano;

-- name: TopHolders :many
SELECT a.id, a.username, a.display_name, l.balance_nano
FROM ledger_accounts l
JOIN accounts a ON a.id = l.account_id
WHERE l.kind = 'user' AND l.balance_nano > 0
ORDER BY l.balance_nano DESC, a.username
LIMIT 5;

-- ---- 管理员：五项核对（现算） ----

-- name: AccountBalanceMismatches :many
SELECT l.id, l.kind, l.system_code, a.id AS account_id, a.username, a.display_name,
       l.balance_nano, coalesce(sum(e.amount_nano), 0)::bigint AS entries_nano
FROM ledger_accounts l
LEFT JOIN accounts a ON a.id = l.account_id
LEFT JOIN ledger_entries e ON e.ledger_account_id = l.id
GROUP BY l.id, l.kind, l.system_code, a.id, a.username, a.display_name, l.balance_nano
HAVING l.balance_nano <> coalesce(sum(e.amount_nano), 0)
ORDER BY l.id
LIMIT 100;

-- name: EscrowOrdersTotal :one
SELECT coalesce(sum(available_nano + in_trade_nano), 0)::bigint AS orders_nano FROM c2c_orders;

-- name: CountUnbilledCalls :one
SELECT count(*)::bigint
FROM calls c
JOIN channels ch ON ch.id = c.final_channel_id
WHERE c.ledger_tx_id IS NULL
  AND c.outcome IN ('succeeded', 'interrupted', 'client_disconnected')
  AND c.cost_nano + c.fee_nano > 0
  AND ch.owner_id <> c.account_id;

-- name: ListUnbilledCalls :many
SELECT c.id, c.created_at, c.outcome, c.cost_nano, c.fee_nano,
       a.id AS account_id, a.username, a.display_name, ch.id AS channel_id, ch.name AS channel_name
FROM calls c
JOIN accounts a ON a.id = c.account_id
JOIN channels ch ON ch.id = c.final_channel_id
WHERE c.ledger_tx_id IS NULL
  AND c.outcome IN ('succeeded', 'interrupted', 'client_disconnected')
  AND c.cost_nano + c.fee_nano > 0
  AND ch.owner_id <> c.account_id
ORDER BY c.created_at
LIMIT 100;

-- name: CountUnbookedTrades :one
SELECT count(*)::bigint FROM c2c_trades WHERE status IN ('released', 'resolved_to_buyer') AND ledger_tx_id IS NULL;

-- name: ListUnbookedTrades :many
SELECT id, status, amount_nano, coalesce(released_at, resolved_at) AS settled_at
FROM c2c_trades
WHERE status IN ('released', 'resolved_to_buyer') AND ledger_tx_id IS NULL
ORDER BY created_at
LIMIT 100;

-- ---- 管理员：概览 ----

-- name: CallsSince :one
SELECT count(*)::bigint AS calls,
       count(*) FILTER (WHERE outcome IN ('succeeded', 'succeeded_unbilled'))::bigint AS succeeded,
       count(*) FILTER (WHERE outcome = 'succeeded_unbilled')::bigint AS unbilled,
       coalesce(sum(CASE WHEN ledger_tx_id IS NULL THEN 0 ELSE cost_nano + fee_nano END), 0)::bigint AS spend_nano,
       coalesce(sum(CASE WHEN ledger_tx_id IS NULL THEN 0 ELSE fee_nano END), 0)::bigint AS fee_nano
FROM calls
WHERE created_at >= sqlc.arg(since)::timestamptz AND outcome <> 'in_progress';

-- name: FailingChannels :many
SELECT ch.id, ch.name, count(*)::bigint AS attempts,
       count(*) FILTER (WHERE att->>'end_reason' = 'completed')::bigint AS successes
FROM calls c
CROSS JOIN LATERAL jsonb_array_elements(c.attempts) att
JOIN channels ch ON ch.id = (att->'channel'->>'id')::uuid
WHERE c.created_at >= sqlc.arg(since)::timestamptz
  AND att->'channel' IS NOT NULL AND jsonb_typeof(att->'channel') = 'object'
  AND att->>'end_reason' NOT IN ('client_disconnected', 'client_error')
  AND ch.deleted_at IS NULL AND ch.status = 'listed'
GROUP BY ch.id, ch.name
HAVING count(*) >= 20 AND count(*) FILTER (WHERE att->>'end_reason' = 'completed') * 10 < count(*) * 8
ORDER BY ch.name;

-- name: C2COverview :one
SELECT (SELECT count(*) FROM c2c_orders WHERE status = 'open' AND available_nano > 0)::bigint AS open_orders,
       (SELECT count(*) FROM c2c_trades WHERE status = 'awaiting_payment')::bigint AS awaiting_payment,
       (SELECT count(*) FROM c2c_trades WHERE status = 'disputed')::bigint AS open_disputes;

-- name: C2CSince :one
SELECT count(*)::bigint AS trades, coalesce(sum(amount_nano), 0)::bigint AS volume_nano, coalesce(sum(total_fen), 0)::bigint AS total_fen
FROM c2c_trades
WHERE status IN ('released', 'resolved_to_buyer')
  AND coalesce(released_at, resolved_at) >= sqlc.arg(since)::timestamptz;

-- ---- 管理员：交易浏览 ----

-- name: ListLedgerTransactions :many
SELECT t.id, t.type, t.idempotency_key, t.related_type, t.related_id, t.actor_id,
       ac.username AS actor_username, ac.display_name AS actor_display_name, t.reason, t.created_at,
       coalesce(CASE t.related_type
          WHEN 'call' THEN (SELECT coalesce(c.model_id, c.requested_model) || ' · ' || coalesce(ch.name, '-')
                              FROM calls c LEFT JOIN channels ch ON ch.id = c.final_channel_id WHERE c.id = t.related_id)
          WHEN 'c2c_trade' THEN (SELECT 'C2C 交易 · ' || s.username || ' → ' || b.username || ' · ¥' || to_char(tr.total_fen / 100.0, 'FM999999990.00')
                                   FROM c2c_trades tr JOIN accounts s ON s.id = tr.seller_id JOIN accounts b ON b.id = tr.buyer_id WHERE tr.id = t.related_id)
          WHEN 'c2c_order' THEN (SELECT 'C2C 卖单 · ' || s.username || ' · ¥' || to_char(o.unit_price_fen / 100.0, 'FM999999990.00') || '/积分'
                                   FROM c2c_orders o JOIN accounts s ON s.id = o.seller_id WHERE o.id = t.related_id)
          WHEN 'account' THEN (SELECT '账户 · ' || u.username FROM accounts u WHERE u.id = t.related_id)
        END, '')::text AS related_summary
FROM ledger_transactions t
LEFT JOIN accounts ac ON ac.id = t.actor_id
WHERE (sqlc.narg(tx_id)::uuid IS NULL OR t.id = sqlc.narg(tx_id)::uuid)
  AND (sqlc.narg(type)::text IS NULL OR t.type = sqlc.narg(type)::text)
  AND (sqlc.narg(account_id)::uuid IS NULL OR EXISTS (
        SELECT 1 FROM ledger_entries e JOIN ledger_accounts la ON la.id = e.ledger_account_id
        WHERE e.transaction_id = t.id AND la.account_id = sqlc.narg(account_id)::uuid))
  AND (sqlc.narg(related_type)::text IS NULL OR (t.related_type = sqlc.narg(related_type)::text AND t.related_id = sqlc.narg(related_id)::uuid))
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR t.created_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR t.created_at < sqlc.narg(to_time)::timestamptz)
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (t.created_at, t.id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY t.created_at DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListEntriesByTransactions :many
SELECT e.transaction_id, e.ledger_account_id, la.kind, la.system_code, a.id AS account_id, a.username, a.display_name,
       e.amount_nano, e.balance_after_nano
FROM ledger_entries e
JOIN ledger_accounts la ON la.id = e.ledger_account_id
LEFT JOIN accounts a ON a.id = la.account_id
WHERE e.transaction_id = ANY(sqlc.arg(ids)::uuid[])
ORDER BY e.transaction_id, e.id;

-- name: GetCallPriceSnapshot :one
SELECT price_snapshot FROM calls WHERE id = $1;

-- ---- 管理员：补记 ----

-- name: LockCallForRepair :one
SELECT c.id, c.account_id, c.outcome, c.final_channel_id, c.cost_nano, c.fee_nano, c.ledger_tx_id, ch.owner_id AS channel_owner_id
FROM calls c
LEFT JOIN channels ch ON ch.id = c.final_channel_id
WHERE c.id = $1
FOR UPDATE OF c;

-- name: AttachCallLedgerTx :execrows
UPDATE calls SET ledger_tx_id = sqlc.arg(ledger_tx_id) WHERE id = sqlc.arg(id) AND ledger_tx_id IS NULL;

-- name: VoidCall :execrows
UPDATE calls SET outcome = 'interrupted', cost_nano = 0, fee_nano = 0 WHERE id = $1 AND ledger_tx_id IS NULL;

-- ---- Prometheus 积分指标 ----

-- name: LedgerTransactionTotals :many
SELECT t.type, count(DISTINCT t.id)::bigint AS transactions,
       coalesce(sum(e.amount_nano) FILTER (WHERE e.amount_nano > 0), 0)::bigint AS amount_nano
FROM ledger_transactions t
JOIN ledger_entries e ON e.transaction_id = t.id
GROUP BY t.type;
