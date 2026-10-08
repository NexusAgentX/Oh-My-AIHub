-- name: InsertChannel :exec
INSERT INTO channels (
    id, owner_id, name, base_url, api_key_ciphertext, api_key_nonce, api_key_key_id, status,
    user_agent, header_rules, concurrency_limit, rpm_limit, daily_revenue_cap_nano,
    ttft_timeout_ms, total_timeout_ms, cooldown_failures, cooldown_seconds
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17);

-- name: InsertChannelModel :exec
INSERT INTO channel_models (channel_id, model_id, upstream_model, multiplier_nano, formats, format_tests, enabled)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: DeleteChannelModels :exec
DELETE FROM channel_models WHERE channel_id = $1;

-- name: GetChannel :one
SELECT c.id, c.owner_id, c.name, c.base_url, c.status, c.suspended_reason, c.user_agent, c.header_rules,
       c.concurrency_limit, c.rpm_limit, c.daily_revenue_cap_nano, c.ttft_timeout_ms, c.total_timeout_ms,
       c.cooldown_failures, c.cooldown_seconds, c.created_at, c.updated_at,
       a.username AS owner_username, a.display_name AS owner_display_name
FROM channels c
JOIN accounts a ON a.id = c.owner_id
WHERE c.id = $1 AND c.deleted_at IS NULL;

-- name: GetChannelForUpdate :one
SELECT status FROM channels WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: ListOwnerChannels :many
SELECT c.id, c.owner_id, c.name, c.base_url, c.status, c.suspended_reason, c.user_agent, c.header_rules,
       c.concurrency_limit, c.rpm_limit, c.daily_revenue_cap_nano, c.ttft_timeout_ms, c.total_timeout_ms,
       c.cooldown_failures, c.cooldown_seconds, c.created_at, c.updated_at,
       a.username AS owner_username, a.display_name AS owner_display_name
FROM channels c
JOIN accounts a ON a.id = c.owner_id
WHERE c.owner_id = $1 AND c.deleted_at IS NULL
ORDER BY c.created_at DESC, c.id DESC;

-- name: ListAdminChannels :many
SELECT c.id, c.owner_id, c.name, c.base_url, c.status, c.suspended_reason, c.user_agent, c.header_rules,
       c.concurrency_limit, c.rpm_limit, c.daily_revenue_cap_nano, c.ttft_timeout_ms, c.total_timeout_ms,
       c.cooldown_failures, c.cooldown_seconds, c.created_at, c.updated_at,
       a.username AS owner_username, a.display_name AS owner_display_name
FROM channels c
JOIN accounts a ON a.id = c.owner_id
WHERE c.deleted_at IS NULL
  AND (sqlc.arg(status)::text = '' OR c.status = sqlc.arg(status))
  AND (sqlc.arg(owner_id)::text = '' OR c.owner_id::text = sqlc.arg(owner_id))
  AND (sqlc.arg(query)::text = '' OR c.name ILIKE '%' || sqlc.arg(query) || '%'
       OR a.username ILIKE '%' || sqlc.arg(query) || '%' OR a.display_name ILIKE '%' || sqlc.arg(query) || '%')
  AND (sqlc.narg(before_time)::timestamptz IS NULL
       OR (c.created_at, c.id) < (sqlc.narg(before_time)::timestamptz, sqlc.arg(before_id)::uuid))
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListModelsOfChannels :many
SELECT cm.channel_id, cm.model_id, cm.upstream_model, cm.multiplier_nano, cm.formats, cm.format_tests, cm.enabled
FROM channel_models cm
WHERE cm.channel_id = ANY(sqlc.arg(channel_ids)::uuid[])
ORDER BY cm.channel_id, cm.model_id;

-- name: UpdateChannelFields :exec
UPDATE channels SET
    name = coalesce(sqlc.narg(name), name),
    base_url = coalesce(sqlc.narg(base_url), base_url),
    status = coalesce(sqlc.narg(status), status),
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateChannelCredential :exec
UPDATE channels SET api_key_ciphertext = $2, api_key_nonce = $3, api_key_key_id = $4, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateChannelAdvanced :exec
UPDATE channels SET
    user_agent = $2, header_rules = $3, concurrency_limit = $4, rpm_limit = $5, daily_revenue_cap_nano = $6,
    ttft_timeout_ms = $7, total_timeout_ms = $8, cooldown_failures = $9, cooldown_seconds = $10, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteChannel :execrows
UPDATE channels SET deleted_at = now(), status = CASE WHEN status = 'suspended' THEN status ELSE 'unlisted' END, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetChannelCredential :one
SELECT api_key_ciphertext, api_key_nonce, api_key_key_id FROM channels WHERE id = $1 AND deleted_at IS NULL;

-- name: InsertChannelEvent :exec
INSERT INTO channel_events (channel_id, kind, reason) VALUES ($1, $2, $3);

-- name: ListChannelEvents :many
SELECT id, kind, reason, created_at FROM channel_events WHERE channel_id = $1 ORDER BY id DESC LIMIT sqlc.arg(row_limit);

-- name: SetChannelModelTests :exec
UPDATE channel_models SET format_tests = $3, formats = $4 WHERE channel_id = $1 AND model_id = $2;

-- name: SuspendChannel :exec
UPDATE channels SET status = 'suspended', suspended_reason = $2, updated_at = now() WHERE id = $1;

-- name: UnsuspendChannel :exec
UPDATE channels SET status = 'listed', suspended_reason = NULL, updated_at = now() WHERE id = $1;

-- Statistics since sqlc.arg(since) of the calls whose final channel is one of ids.
-- name: ChannelTodayStats :many
SELECT final_channel_id AS channel_id,
       count(*)::bigint AS calls,
       count(*) FILTER (WHERE outcome IN ('succeeded', 'succeeded_unbilled'))::bigint AS ok_calls,
       coalesce(sum(cost_nano), 0)::bigint AS revenue_nano
FROM calls
WHERE final_channel_id = ANY(sqlc.arg(channel_ids)::uuid[]) AND created_at >= sqlc.arg(since)
GROUP BY final_channel_id;
