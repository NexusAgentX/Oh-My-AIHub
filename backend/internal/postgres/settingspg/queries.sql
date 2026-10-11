-- name: GetSettings :one
SELECT * FROM settings;

-- name: LockSettings :one
SELECT * FROM settings FOR UPDATE;

-- name: UpdateSettings :one
UPDATE settings
SET fee_rate_nano = $1,
    c2c_payment_timeout_minutes = $2,
    default_credit_limit_nano = $3,
    default_max_attempts = $4,
    default_ttft_timeout_ms = $5,
    default_total_timeout_ms = $6,
    default_cooldown_failures = $7,
    default_cooldown_seconds = $8,
    extra_blocked_hosts = $9,
    fx_auto_sync = sqlc.arg(fx_auto_sync),
    fx_sync_minute = sqlc.arg(fx_sync_minute),
    usd_cny_rate_source = CASE WHEN usd_cny_rate_nano = sqlc.arg(usd_cny_rate_nano)::bigint THEN usd_cny_rate_source ELSE 'manual' END,
    usd_cny_rate_updated_at = CASE WHEN usd_cny_rate_nano = sqlc.arg(usd_cny_rate_nano)::bigint THEN usd_cny_rate_updated_at ELSE now() END,
    usd_cny_rate_nano = sqlc.arg(usd_cny_rate_nano)::bigint,
    updated_at = now()
RETURNING *;

-- name: SaveSyncedRate :one
UPDATE settings
SET usd_cny_rate_source = 'auto',
    usd_cny_rate_updated_at = CASE WHEN usd_cny_rate_nano = sqlc.arg(usd_cny_rate_nano)::bigint AND usd_cny_rate_source = 'auto'
                                   THEN usd_cny_rate_updated_at ELSE sqlc.arg(at)::timestamptz END,
    usd_cny_rate_nano = sqlc.arg(usd_cny_rate_nano)::bigint,
    fx_sync_attempted_at = sqlc.arg(at)::timestamptz,
    fx_sync_error = NULL
RETURNING *;

-- name: SaveSyncFailure :one
UPDATE settings
SET fx_sync_attempted_at = sqlc.arg(at)::timestamptz,
    fx_sync_error = sqlc.arg(message)::text
RETURNING *;
