-- 运营领域全部 SQL：只读聚合（跨账本、调用、渠道与 C2C 表直接 JOIN）与巡检写入。
-- 时间窗口由调用方以 UTC 显式传入；闲置天数使用数据库时钟 now()，不得为负（#107）。

-- name: ListNegativeBalances :many
WITH neg AS (
	SELECT la.id, la.identity_account_id, la.posted_balance_nano, a.username, a.credit_limit_nano, a.credit_frozen
	FROM ledger_accounts la
	JOIN accounts a ON a.id = la.identity_account_id
	WHERE la.kind = 'user' AND la.posted_balance_nano < 0
), entries AS (
	SELECT e.ledger_account_id, e.created_at, e.amount_nano,
		sum(e.amount_nano) OVER (PARTITION BY e.ledger_account_id ORDER BY e.created_at, e.id
			ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING) AS suffix_sum
	FROM ledger_entries e
	JOIN neg n ON n.id = e.ledger_account_id
), streak AS (
	SELECT n.id, n.posted_balance_nano, n.username, n.credit_limit_nano, n.credit_frozen,
		min(e.created_at) FILTER (WHERE e.suffix_sum < 0) AS negative_since,
		max(e.created_at) AS last_activity
	FROM neg n
	LEFT JOIN entries e ON e.ledger_account_id = n.id
	GROUP BY n.id, n.posted_balance_nano, n.username, n.credit_limit_nano, n.credit_frozen
)
-- sqlc 不推断聚合/子查询的可空性，故空值以哨兵值加 has_* 标志表达，Go 侧还原为 NULL 语义。
SELECT id::text AS id, username, posted_balance_nano,
	COALESCE(negative_since, 'epoch'::timestamptz)::timestamptz AS negative_since,
	(negative_since IS NOT NULL)::boolean AS has_negative_since,
	COALESCE(last_activity, 'epoch'::timestamptz)::timestamptz AS last_activity,
	(last_activity IS NOT NULL)::boolean AS has_last_activity,
	-- 账本时间戳来自数据库时钟，闲置天数也用它度量；后端时钟落后时不得得到 -1。
	COALESCE(greatest(0, floor(extract(epoch FROM now() - last_activity) / 86400)), 0)::bigint AS inactive_days,
	credit_limit_nano, credit_frozen
FROM streak ORDER BY posted_balance_nano ASC LIMIT 200;

-- name: GetAPIFunnel :one
SELECT
	count(*) FILTER (WHERE status = 'rejected')::bigint AS rejected,
	count(*) FILTER (WHERE upstream_attempt_count > 0)::bigint AS reached,
	count(*) FILTER (WHERE status = 'succeeded')::bigint AS succeeded,
	count(*) FILTER (WHERE status = 'failed')::bigint AS failed,
	count(*) FILTER (WHERE status = 'incomplete')::bigint AS incomplete,
	count(*) FILTER (WHERE status = 'cancelled')::bigint AS cancelled,
	count(*) FILTER (WHERE upstream_attempt_count > 0 AND status IN ('succeeded', 'failed', 'incomplete', 'cancelled'))::bigint AS terminal
FROM api_calls WHERE created_at >= @from_at AND created_at < @to_at;

-- name: GetAttemptStats :one
SELECT count(*)::bigint AS attempts,
	(count(*) FILTER (WHERE a.status = 'succeeded'))::bigint AS succeeded_attempts,
	COALESCE((avg(a.ttft_milliseconds) FILTER (WHERE a.status = 'succeeded' AND a.ttft_milliseconds IS NOT NULL))::text, '')::text AS avg_ttft,
	COALESCE((avg(a.tokens_per_second_nano) FILTER (WHERE a.status = 'succeeded' AND a.tokens_per_second_nano IS NOT NULL))::text, '')::text AS avg_tps
FROM api_call_attempts a
JOIN api_calls c ON c.id = a.call_id
WHERE c.created_at >= @from_at AND c.created_at < @to_at;

-- name: GetConsumptionWindow :one
SELECT
	COALESCE(sum(s.provider_charge_nano) FILTER (WHERE s.kind = 'captured'), 0)::bigint AS captured_charge,
	COALESCE(sum(s.platform_fee_nano) FILTER (WHERE s.kind = 'captured'), 0)::bigint AS captured_fee,
	COALESCE(sum(s.provider_charge_nano) FILTER (WHERE s.kind = 'self_usage'), 0)::bigint AS self_charge
FROM api_call_settlements s
JOIN api_calls c ON c.id = s.call_id
WHERE c.created_at >= @from_at AND c.created_at < @to_at;

-- name: ListC2COrderStatusCounts :many
SELECT status, count(*)::bigint AS count FROM c2c_orders GROUP BY status ORDER BY status;

-- name: ListC2CTradeStatusCounts :many
SELECT status, count(*)::bigint AS count FROM c2c_trades
WHERE created_at >= @from_at AND created_at < @to_at GROUP BY status ORDER BY status;

-- name: GetC2CQuote :one
WITH quote AS (
	SELECT
		(SELECT unit_price_fen FROM c2c_trades WHERE status = 'released_to_buyer' ORDER BY resolved_at DESC, id DESC LIMIT 1) AS last_price,
		(SELECT min(unit_price_fen) FROM c2c_orders WHERE status IN ('open', 'allocated') AND available_nano > 0) AS best_ask
)
SELECT COALESCE(last_price, 0)::bigint AS last_price, (last_price IS NOT NULL)::boolean AS has_last_price,
	COALESCE(best_ask, 0)::bigint AS best_ask, (best_ask IS NOT NULL)::boolean AS has_best_ask
FROM quote;

-- name: GetConcentration :one
-- sqlc 不支持标量子查询里的嵌套 CTE，故展开为同级 CTE；total = 0 时三个占比为 NULL。
WITH pos AS (
	SELECT posted_balance_nano FROM ledger_accounts WHERE kind = 'user' AND posted_balance_nano > 0
), totals AS (
	SELECT count(*) AS positive_count,
		COALESCE(sum(posted_balance_nano), 0)::numeric AS total,
		max(posted_balance_nano)::numeric AS top
	FROM pos
), top5 AS (
	SELECT COALESCE(sum(posted_balance_nano), 0)::numeric AS top_sum
	FROM (SELECT posted_balance_nano FROM pos ORDER BY posted_balance_nano DESC LIMIT 5) t
), hhi AS (
	SELECT sum((posted_balance_nano::numeric / NULLIF(totals.total, 0)) * (posted_balance_nano::numeric / NULLIF(totals.total, 0))) AS value
	FROM pos CROSS JOIN totals
)
SELECT totals.positive_count::bigint AS positive_count,
	totals.total::text AS total_positive,
	(CASE WHEN totals.total > 0 THEN round(totals.top / totals.total, 6)::text ELSE '' END)::text AS top1_share,
	(CASE WHEN totals.total > 0 THEN round(top5.top_sum / totals.total, 6)::text ELSE '' END)::text AS top5_share,
	(CASE WHEN totals.total > 0 THEN round(hhi.value, 6)::text ELSE '' END)::text AS hhi
FROM totals CROSS JOIN top5 CROSS JOIN hhi;

-- name: ListProviderIncome :many
WITH income AS (
	SELECT
		s.provider_account_id AS account_id,
		COALESCE(sum(s.provider_charge_nano) FILTER (WHERE s.kind IN ('captured', 'self_usage')), 0)::bigint AS total_income_nano,
		COALESCE(sum(s.provider_charge_nano) FILTER (WHERE s.kind = 'captured'), 0)::bigint AS other_consumer_income_nano,
		COALESCE(sum(s.provider_charge_nano) FILTER (WHERE s.kind = 'self_usage'), 0)::bigint AS own_usage_income_nano
	FROM api_call_settlements s
	JOIN api_calls c ON c.id = s.call_id
	WHERE c.created_at >= @from_at AND c.created_at < @to_at
		AND s.provider_account_id IS NOT NULL
		AND s.kind IN ('captured', 'self_usage')
	GROUP BY s.provider_account_id
), attempts AS (
	SELECT
		a.provider_account_id AS account_id,
		count(*) FILTER (WHERE a.status IN ('succeeded', 'failed', 'cancelled', 'incomplete')) AS terminal_attempts,
		count(*) FILTER (WHERE a.status = 'succeeded') AS succeeded_attempts
	FROM api_call_attempts a
	WHERE a.started_at >= @from_at AND a.started_at < @to_at
	GROUP BY a.provider_account_id
)
SELECT
	a.id::text AS account_id,
	a.display_name,
	COALESCE(income.total_income_nano, 0)::bigint AS total_income_nano,
	COALESCE(income.other_consumer_income_nano, 0)::bigint AS other_consumer_income_nano,
	COALESCE(income.own_usage_income_nano, 0)::bigint AS own_usage_income_nano,
	COALESCE(attempts.succeeded_attempts, 0)::bigint AS succeeded_attempts,
	COALESCE(attempts.terminal_attempts, 0)::bigint AS terminal_attempts
FROM accounts a
LEFT JOIN income ON income.account_id = a.id
LEFT JOIN attempts ON attempts.account_id = a.id
WHERE COALESCE(income.total_income_nano, 0) > 0
	OR COALESCE(attempts.terminal_attempts, 0) > 0
ORDER BY COALESCE(income.total_income_nano, 0) DESC, a.display_name, a.id;

-- name: CountHardViolations :one
-- 硬异常的固定口径；OpsAnomalies 与巡检共用。
SELECT
	(SELECT count(*) FROM api_calls c LEFT JOIN api_call_settlements s ON s.call_id = c.id
		WHERE c.status = 'succeeded' AND s.call_id IS NULL)::bigint AS without_settlement,
	(SELECT count(*) FROM api_call_settlements s
		WHERE (s.kind = 'captured' AND NOT EXISTS (SELECT 1 FROM ledger_transactions t WHERE t.id = s.capture_transaction_id))
			OR (s.kind = 'self_usage' AND NOT EXISTS (SELECT 1 FROM ledger_transactions t WHERE t.id = s.self_transaction_id)))::bigint AS without_ledger_tx,
	(SELECT count(*) FROM c2c_orders WHERE total_nano <> available_nano + allocated_nano + settled_nano + closed_nano)::bigint AS quantity_violations,
	(SELECT count(*) FROM c2c_orders o LEFT JOIN ledger_holds h ON h.id = o.parent_hold_id
		WHERE o.side = 'sell' AND o.status IN ('open', 'allocated')
			AND (o.parent_hold_id IS NULL OR h.remaining_nano <> o.available_nano + o.allocated_nano))::bigint AS hold_violations;

-- name: CountDisputedTrades :one
SELECT count(*)::bigint FROM c2c_trades WHERE status = 'disputed';

-- name: InsertInspection :one
INSERT INTO ops_inspections (
	inspection_version, triggered_by,
	zero_sum_ok, projection_ok, call_settlement_ok, c2c_consistency_ok,
	zero_sum_difference_nano, posted_projection_difference_nano, asset_projection_difference_nano, authorization_projection_difference_nano,
	successful_calls_without_settlement, settlements_without_ledger_transaction,
	c2c_quantity_violations, c2c_hold_violations, notes
) VALUES (
	@inspection_version, @triggered_by,
	@zero_sum_ok, @projection_ok, @call_settlement_ok, @c2c_consistency_ok,
	@zero_sum_difference_nano, @posted_projection_difference_nano, @asset_projection_difference_nano, @authorization_projection_difference_nano,
	@successful_calls_without_settlement, @settlements_without_ledger_transaction,
	@c2c_quantity_violations, @c2c_hold_violations, '{}'
)
RETURNING id::text AS id, inspection_version, triggered_by, zero_sum_ok, projection_ok, call_settlement_ok, c2c_consistency_ok,
	zero_sum_difference_nano::text AS zero_sum_difference, posted_projection_difference_nano::text AS posted_projection_difference,
	asset_projection_difference_nano::text AS asset_projection_difference, authorization_projection_difference_nano::text AS authorization_projection_difference,
	successful_calls_without_settlement, settlements_without_ledger_transaction, c2c_quantity_violations, c2c_hold_violations, checked_at;

-- name: ListInspections :many
SELECT id::text AS id, inspection_version, triggered_by, zero_sum_ok, projection_ok, call_settlement_ok, c2c_consistency_ok,
	zero_sum_difference_nano::text AS zero_sum_difference, posted_projection_difference_nano::text AS posted_projection_difference,
	asset_projection_difference_nano::text AS asset_projection_difference, authorization_projection_difference_nano::text AS authorization_projection_difference,
	successful_calls_without_settlement, settlements_without_ledger_transaction, c2c_quantity_violations, c2c_hold_violations, checked_at
FROM ops_inspections ORDER BY checked_at DESC, id DESC LIMIT @row_limit;
