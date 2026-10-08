-- name: LockAccount :exec
SELECT id FROM accounts WHERE id = $1 FOR UPDATE;

-- name: CountOwnerKeys :one
SELECT count(*)::int FROM api_keys WHERE owner_id = $1 AND deleted_at IS NULL;

-- name: InsertKey :exec
INSERT INTO api_keys (
    id, owner_id, name, prefix, key_hash, key_ciphertext, key_nonce, key_key_id, status, expires_at,
    allowed_models, budget_daily_nano, budget_monthly_nano, budget_total_nano, model_aliases
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15);

-- Sets the default-key marker once; returns a row only for the first caller.
-- name: MarkDefaultKeyCreated :one
UPDATE accounts SET default_key_created_at = now()
WHERE id = $1 AND default_key_created_at IS NULL
RETURNING id;

-- name: ListOwnerKeys :many
SELECT * FROM api_keys WHERE owner_id = $1 AND deleted_at IS NULL ORDER BY created_at, id;

-- name: GetOwnerKey :one
SELECT * FROM api_keys WHERE owner_id = $1 AND id = $2 AND deleted_at IS NULL;

-- name: GetOwnerKeyForUpdate :one
SELECT * FROM api_keys WHERE owner_id = $1 AND id = $2 AND deleted_at IS NULL FOR UPDATE;

-- name: UpdateKey :exec
UPDATE api_keys SET
    name = $3, status = $4, expires_at = $5, allowed_models = $6,
    budget_daily_nano = $7, budget_monthly_nano = $8, budget_total_nano = $9,
    model_aliases = $10, updated_at = now()
WHERE owner_id = $1 AND id = $2 AND deleted_at IS NULL;

-- name: SoftDeleteKey :execrows
UPDATE api_keys SET deleted_at = now(), status = 'disabled', updated_at = now()
WHERE owner_id = $1 AND id = $2 AND deleted_at IS NULL;

-- name: DeleteKeyRoutes :exec
DELETE FROM route_prefs WHERE api_key_id = $1;

-- name: GetKeyCredential :one
SELECT key_ciphertext, key_nonce, key_key_id FROM api_keys WHERE owner_id = $1 AND id = $2 AND deleted_at IS NULL;

-- name: KeySpend :many
SELECT api_key_id,
       coalesce(sum(cost_nano + fee_nano) FILTER (WHERE created_at >= sqlc.arg(day_start)), 0)::bigint AS today_nano,
       coalesce(sum(cost_nano + fee_nano) FILTER (WHERE created_at >= sqlc.arg(month_start)), 0)::bigint AS month_nano,
       coalesce(sum(cost_nano + fee_nano), 0)::bigint AS total_nano
FROM calls
WHERE api_key_id = ANY(sqlc.arg(key_ids)::uuid[])
GROUP BY api_key_id;

-- name: KeyRoutedModels :many
SELECT api_key_id, model_id FROM route_prefs WHERE api_key_id = ANY(sqlc.arg(key_ids)::uuid[]) ORDER BY model_id;

-- name: UpsertRoutePref :one
INSERT INTO route_prefs (account_id, api_key_id, model_id, mode, max_attempts, ttft_timeout_ms)
VALUES ($1, sqlc.narg(api_key_id), $2, $3, $4, $5)
ON CONFLICT (account_id, api_key_id, model_id)
DO UPDATE SET mode = EXCLUDED.mode, max_attempts = EXCLUDED.max_attempts,
              ttft_timeout_ms = EXCLUDED.ttft_timeout_ms, updated_at = now()
RETURNING id, updated_at;

-- name: ClearRoutePrefChannels :exec
DELETE FROM route_pref_channels WHERE route_pref_id = $1;

-- name: InsertRoutePrefChannel :exec
INSERT INTO route_pref_channels (route_pref_id, channel_id, position, excluded) VALUES ($1, $2, $3, $4);

-- name: DeleteRoutePref :execrows
DELETE FROM route_prefs
WHERE account_id = $1 AND api_key_id IS NOT DISTINCT FROM sqlc.narg(api_key_id) AND model_id = $2;

-- Scope-exact lookups: key-level rows when api_key_id is given, account-level rows otherwise.
-- name: ListRoutePrefs :many
SELECT id, model_id, mode, max_attempts, ttft_timeout_ms, updated_at, api_key_id
FROM route_prefs
WHERE account_id = $1 AND api_key_id IS NOT DISTINCT FROM sqlc.narg(api_key_id)
  AND (sqlc.arg(model_id)::text = '' OR model_id = sqlc.arg(model_id))
ORDER BY model_id;

-- name: ListRoutePrefChannels :many
SELECT route_pref_id, channel_id, position, excluded
FROM route_pref_channels
WHERE route_pref_id = ANY(sqlc.arg(pref_ids)::uuid[])
ORDER BY route_pref_id, position;


-- name: LockKeyConfigWrites :exec
LOCK TABLE api_keys IN ROW EXCLUSIVE MODE;

-- name: LockReferencedModels :many
SELECT id FROM models WHERE id=ANY(sqlc.arg(ids)::text[]) ORDER BY id FOR KEY SHARE;
