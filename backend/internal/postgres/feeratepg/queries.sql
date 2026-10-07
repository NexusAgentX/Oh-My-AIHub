-- name: ListFeeRates :many
SELECT rate.version, rate.fee_rate_nano, rate.created_at,
	COALESCE(rate.created_by::text, '')::text AS created_by_id,
	COALESCE(actor.username, '')::text AS created_by_username,
	COALESCE(audit.reason, '')::text AS reason
FROM api_fee_rates rate
LEFT JOIN accounts actor ON actor.id = rate.created_by
LEFT JOIN LATERAL (
	SELECT reason FROM audit_events
	WHERE target_type = @audit_target_type AND target_id = rate.version::text AND action = @audit_action
	ORDER BY id LIMIT 1
) audit ON true
ORDER BY rate.version DESC
LIMIT @row_limit::bigint;

-- name: LockFeeRates :exec
LOCK TABLE api_fee_rates IN SHARE ROW EXCLUSIVE MODE;

-- name: GetCurrentFeeRate :one
SELECT version, fee_rate_nano FROM api_fee_rates ORDER BY version DESC LIMIT 1;

-- name: InsertFeeRate :one
INSERT INTO api_fee_rates (fee_rate_nano, created_by) VALUES (@fee_rate_nano, @created_by)
RETURNING version, created_at;

-- name: GetActorUsername :one
SELECT username FROM accounts WHERE id = @id;
