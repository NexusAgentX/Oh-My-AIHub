-- name: ListModels :many
SELECT * FROM models
WHERE enabled OR sqlc.arg(include_disabled)::boolean
ORDER BY sort_order, id;

-- name: GetModel :one
SELECT * FROM models WHERE id = $1;

-- name: LockModel :one
SELECT * FROM models WHERE id = $1 FOR UPDATE;

-- name: ListTiers :many
SELECT * FROM model_price_tiers
WHERE model_id = ANY(sqlc.arg(model_ids)::text[])
ORDER BY model_id, seq;

-- name: InsertModel :one
INSERT INTO models (
    id, display_name,
    input_price_nano_per_million, output_price_nano_per_million,
    cache_write_price_nano_per_million, cache_read_price_nano_per_million,
    enabled, sort_order, provider, context_window, input_modalities, output_modalities,
    supports_tools, supports_structured_output, supports_vision, parameter_info, token_prices
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
RETURNING *;

-- name: UpdateModel :one
UPDATE models SET
    display_name = $2,
    input_price_nano_per_million = $3,
    output_price_nano_per_million = $4,
    cache_write_price_nano_per_million = $5,
    cache_read_price_nano_per_million = $6,
    enabled = $7,
    sort_order = $8,
    provider = $9,
    context_window = $10,
    input_modalities = $11,
    output_modalities = $12,
    supports_tools = $13,
    supports_structured_output = $14,
    supports_vision = $15,
    parameter_info = $16,
 token_prices = $17,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteTiers :exec
DELETE FROM model_price_tiers WHERE model_id = $1;

-- name: InsertTier :exec
INSERT INTO model_price_tiers (
    model_id, seq, name, min_prompt_tokens, max_prompt_tokens, timezone, weekdays,
    start_minute_of_day, end_minute_of_day,
    input_price_nano_per_million, output_price_nano_per_million,
    cache_write_price_nano_per_million, cache_read_price_nano_per_million, token_prices, service_tier, thinking_mode
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16);

-- name: ListSources :many
SELECT model_id, info, sync_enabled FROM model_sources WHERE model_id=ANY(sqlc.arg(ids)::text[]) AND NOT ignored;

-- name: GetSource :one
SELECT model_id, ignored, sync_enabled, info FROM model_sources WHERE source_key=$1;

-- name: GetSourceRaw :one
SELECT raw_record FROM model_sources WHERE model_id=$1 AND NOT ignored;

-- name: GetSyncStatus :one
SELECT exchange_rate, started_at, finished_at, status, error, result FROM catalog_sync WHERE id;

-- name: LockSyncRate :one
SELECT exchange_rate FROM catalog_sync WHERE id FOR UPDATE;

-- name: SetSyncRate :exec
UPDATE catalog_sync SET exchange_rate=$1 WHERE id;

-- name: StartSync :one
UPDATE catalog_sync SET status='running', started_at=now(), error='' WHERE id RETURNING exchange_rate;

-- name: FailSync :exec
UPDATE catalog_sync SET status='failed', finished_at=now(), error=$1 WHERE id;

-- name: FinishSync :exec
UPDATE catalog_sync SET status='succeeded', finished_at=now(), error='', result=$1 WHERE id;

-- name: TrySyncLock :one
SELECT pg_try_advisory_lock(201201)::boolean;

-- name: ReleaseSyncLock :exec
SELECT pg_advisory_unlock(201201);

-- name: LockModelWrites :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(model_id)::text,201));

-- name: InsertSourceConflict :exec
INSERT INTO model_sources(source_key,model_id,ignored,sync_enabled,raw_record,info,seen_at)
VALUES($1,$2,true,false,$3,$4,$5) ON CONFLICT DO NOTHING;

-- name: UpsertSource :exec
INSERT INTO model_sources(source_key,model_id,raw_record,info,seen_at) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(source_key) DO UPDATE SET raw_record=excluded.raw_record,info=excluded.info,seen_at=excluded.seen_at;

-- name: SeenSource :exec
UPDATE model_sources SET seen_at=$2 WHERE source_key=$1;

-- name: MarkMissingSources :exec
UPDATE model_sources SET info=jsonb_set(info,'{status}','"missing"'::jsonb)
WHERE NOT(source_key=ANY(sqlc.arg(keys)::text[])) AND NOT ignored AND sync_enabled;

-- name: UpdateSourceControl :exec
UPDATE model_sources SET sync_enabled=$2,info=$3 WHERE model_id=$1 AND NOT ignored;

-- name: IgnoreSource :exec
UPDATE model_sources SET ignored=true,sync_enabled=false WHERE model_id=$1;
