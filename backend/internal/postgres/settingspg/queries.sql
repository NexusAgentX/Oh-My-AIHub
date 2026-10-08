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
    updated_at = now()
RETURNING *;
