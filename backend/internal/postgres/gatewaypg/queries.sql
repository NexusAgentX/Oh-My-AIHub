-- name: LookupKey :one
SELECT k.id, k.owner_id, k.name, k.prefix, k.status, k.expires_at, k.allowed_models,
       k.budget_daily_nano, k.budget_monthly_nano, k.budget_total_nano, k.model_aliases,
       a.status AS account_status
FROM api_keys k
JOIN accounts a ON a.id = k.owner_id
WHERE k.key_hash = $1 AND k.deleted_at IS NULL;

-- name: TouchKey :exec
UPDATE api_keys SET last_used_at = $2 WHERE id = $1;

-- name: KeySpend :one
SELECT coalesce(sum(cost_nano + fee_nano) FILTER (WHERE created_at >= sqlc.arg(day_start)), 0)::bigint AS today_nano,
       coalesce(sum(cost_nano + fee_nano) FILTER (WHERE created_at >= sqlc.arg(month_start)), 0)::bigint AS month_nano,
       coalesce(sum(cost_nano + fee_nano), 0)::bigint AS total_nano
FROM calls
WHERE api_key_id = sqlc.arg(key_id);

-- name: ChannelRevenue :one
SELECT coalesce(sum(cost_nano), 0)::bigint FROM calls WHERE final_channel_id = $1 AND created_at >= $2;

-- name: ListCandidates :many
SELECT c.id, c.owner_id, c.name, c.base_url, c.api_key_ciphertext, c.api_key_nonce, c.api_key_key_id,
       c.user_agent, c.header_rules, c.concurrency_limit, c.rpm_limit, c.daily_revenue_cap_nano,
       c.ttft_timeout_ms, c.total_timeout_ms, c.cooldown_failures, c.cooldown_seconds,
       cm.upstream_model, cm.multiplier_nano
FROM channel_models cm
JOIN channels c ON c.id = cm.channel_id
JOIN accounts a ON a.id = c.owner_id
WHERE cm.model_id = $1 AND cm.enabled AND sqlc.arg(format)::text = ANY(cm.formats)
  AND c.status = 'listed' AND c.deleted_at IS NULL AND a.status = 'active'
ORDER BY c.id;

-- Health of channels over the last 24 hours, from the attempts of every call.
-- Attempts the client abandoned or that were rejected as the request's own fault
-- say nothing about the channel and are left out.
-- name: ChannelStats24h :many
SELECT (att->'channel'->>'id')::uuid AS channel_id,
       count(*)::bigint AS attempts,
       count(*) FILTER (WHERE att->>'end_reason' = 'completed')::bigint AS successes,
       coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY (att->>'ttft_ms')::double precision)
            FILTER (WHERE att->>'end_reason' = 'completed' AND att->>'ttft_ms' IS NOT NULL), -1)::double precision AS ttft_p50_ms
FROM calls c
CROSS JOIN LATERAL jsonb_array_elements(c.attempts) AS att
WHERE c.created_at >= now() - interval '24 hours'
  AND att->'channel' IS NOT NULL AND jsonb_typeof(att->'channel') = 'object'
  AND att->>'end_reason' NOT IN ('client_disconnected', 'client_error')
GROUP BY 1;

-- name: ResponseChannel :one
SELECT final_channel_id FROM calls
WHERE upstream_response_id = $1 AND account_id = $2 AND final_channel_id IS NOT NULL
ORDER BY created_at DESC LIMIT 1;

-- name: InsertCall :one
INSERT INTO calls (account_id, api_key_id, requested_model, format, stream, tag, client_user_agent, outcome, completed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, sqlc.arg(outcome)::text,
        CASE WHEN sqlc.arg(outcome)::text = 'in_progress' THEN NULL ELSE now() END)
RETURNING id;

-- Closes a call exactly once: a call already out of in_progress is left alone.
-- name: FinishCall :execrows
UPDATE calls SET
    outcome = sqlc.arg(outcome), model_id = sqlc.narg(model_id), final_channel_id = sqlc.narg(channel_id),
    routing_mode = sqlc.narg(routing_mode), routing_source = sqlc.narg(routing_source),
    attempts = sqlc.arg(attempts), input_tokens = sqlc.arg(input_tokens), output_tokens = sqlc.arg(output_tokens),
    cache_write_tokens = sqlc.arg(cache_write_tokens), cache_read_tokens = sqlc.arg(cache_read_tokens),
    price_snapshot = sqlc.narg(price_snapshot), cost_nano = sqlc.arg(cost_nano), fee_nano = sqlc.arg(fee_nano),
    ttft_ms = sqlc.narg(ttft_ms), duration_ms = sqlc.arg(duration_ms), output_tokens_per_second = sqlc.narg(tokens_per_second),
    inter_token_p50_ms = sqlc.narg(inter_token_p50_ms), inter_token_p95_ms = sqlc.narg(inter_token_p95_ms),
    response_bytes = sqlc.arg(response_bytes), upstream_response_id = sqlc.narg(upstream_response_id),
    completed_at = now()
WHERE id = sqlc.arg(id) AND outcome = 'in_progress';

-- name: SetCallLedgerTx :exec
UPDATE calls SET ledger_tx_id = $2 WHERE id = $1;

-- name: SweepStaleCalls :execrows
UPDATE calls SET outcome = 'interrupted', completed_at = now()
WHERE outcome = 'in_progress' AND created_at < $1;

-- name: ListOnlineModels :many
SELECT DISTINCT cm.model_id
FROM channel_models cm
JOIN channels c ON c.id = cm.channel_id
JOIN accounts a ON a.id = c.owner_id
WHERE cm.enabled AND c.status = 'listed' AND c.deleted_at IS NULL AND a.status = 'active'
ORDER BY cm.model_id;

-- name: ListOnlineChannelModels :many
SELECT cm.model_id, c.id AS channel_id, c.name AS channel_name, c.owner_id, a.display_name AS owner_name,
       cm.formats, cm.multiplier_nano, c.concurrency_limit, c.rpm_limit, c.daily_revenue_cap_nano,
       c.cooldown_failures, c.cooldown_seconds
FROM channel_models cm
JOIN channels c ON c.id = cm.channel_id
JOIN accounts a ON a.id = c.owner_id
WHERE cm.enabled AND c.status = 'listed' AND c.deleted_at IS NULL AND a.status = 'active'
ORDER BY cm.model_id, c.id;

-- name: HomeToday :one
SELECT coalesce(sum(cost_nano + fee_nano), 0)::bigint AS spend_nano,
       count(*)::bigint AS calls,
       count(*) FILTER (WHERE outcome IN ('succeeded', 'succeeded_unbilled'))::bigint AS succeeded
FROM calls WHERE account_id = $1 AND created_at >= $2;

-- name: HomeChannels :one
SELECT count(*) FILTER (WHERE status = 'listed')::bigint AS online, count(*)::bigint AS total
FROM channels WHERE owner_id = $1 AND deleted_at IS NULL;

-- name: HomeRevenue :one
SELECT coalesce(sum(c.cost_nano), 0)::bigint
FROM calls c JOIN channels ch ON ch.id = c.final_channel_id
WHERE ch.owner_id = $1 AND c.created_at >= $2;

-- name: HomePendingTrades :one
SELECT count(*)::bigint FROM c2c_trades
WHERE (buyer_id = $1 AND status = 'awaiting_payment') OR (seller_id = $1 AND status = 'paid');

-- name: ListRecentCalls :many
SELECT c.id, c.created_at, c.completed_at, c.model_id, c.requested_model, c.format, c.stream, c.tag,
       c.api_key_id, k.name AS key_name, c.outcome, c.final_channel_id, ch.name AS channel_name,
       c.input_tokens, c.output_tokens, c.cache_write_tokens, c.cache_read_tokens,
       c.cost_nano, c.fee_nano, c.ttft_ms, c.duration_ms
FROM calls c
LEFT JOIN api_keys k ON k.id = c.api_key_id
LEFT JOIN channels ch ON ch.id = c.final_channel_id
WHERE c.account_id = $1
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg(row_limit);
