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
SELECT model_id, ignored, sync_enabled, info FROM model_sources WHERE model_id=$1;

-- name: GetSourceRaw :one
SELECT raw_record FROM model_sources WHERE model_id=$1 AND NOT ignored;

-- name: GetSyncStatus :one
SELECT exchange_rate, providers, providers_configured, available_providers, report, started_at, finished_at, status, error, result FROM catalog_sync WHERE id;

-- name: LockSyncConfig :one
SELECT exchange_rate, providers, providers_configured, config_version FROM catalog_sync WHERE id FOR UPDATE;

-- name: SetSyncConfig :exec
UPDATE catalog_sync SET exchange_rate=$1, providers=$2, providers_configured=true, config_version=config_version+1 WHERE id;

-- name: StartSync :one
UPDATE catalog_sync SET status='running', started_at=now(), error='' WHERE id RETURNING exchange_rate, providers, providers_configured, config_version;

-- name: FailSync :exec
UPDATE catalog_sync SET status='failed', finished_at=now(), error=$1 WHERE id;

-- name: FinishSync :exec
UPDATE catalog_sync SET status='succeeded', finished_at=now(), error='', result=$1, report=$2, available_providers=$3 WHERE id;

-- name: TrySyncLock :one
SELECT pg_try_advisory_lock(201201)::boolean;

-- name: ReleaseSyncLock :exec
SELECT pg_advisory_unlock(201201);

-- name: LockModelWrites :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(model_id)::text,201));

-- name: UpsertSource :exec
INSERT INTO model_sources(source_key,model_id,raw_record,info,seen_at) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(model_id) DO UPDATE SET source_key=excluded.source_key, ignored=false, raw_record=excluded.raw_record,info=excluded.info,seen_at=excluded.seen_at;

-- name: SeenSource :exec
UPDATE model_sources SET seen_at=$2 WHERE model_id=$1;

-- name: UpdateSourceControl :exec
UPDATE model_sources SET sync_enabled=$2,info=$3 WHERE model_id=$1 AND NOT ignored;

-- name: IgnoreSource :exec
UPDATE model_sources SET ignored=true,sync_enabled=false WHERE model_id=$1;


-- name: ListManagedSources :many
SELECT s.model_id,s.source_key,s.info,s.sync_enabled FROM model_sources s JOIN models m ON m.id=s.model_id
WHERE NOT s.ignored ORDER BY s.model_id;

-- name: DeletedModels :many
SELECT model_id FROM catalog_deleted_models;

-- name: RememberDeletedModel :exec
INSERT INTO catalog_deleted_models(model_id)
 SELECT sqlc.arg(target_id)::text
 UNION SELECT regexp_replace(s.source_key,'^.*/','') FROM model_sources s WHERE s.model_id=sqlc.arg(target_id)::text AND NOT s.ignored
 ON CONFLICT DO NOTHING;

-- name: RemoveOldConflictRecords :exec
DELETE FROM model_sources WHERE ignored AND info->>'status'='conflict';

-- name: RemoveSource :exec
DELETE FROM model_sources WHERE model_id=$1;

-- name: RemoveUnusedModel :exec
DELETE FROM models WHERE id=$1;

-- name: SetSourceRetention :exec
UPDATE model_sources SET info=jsonb_set(jsonb_set(info,'{status}',to_jsonb(sqlc.arg(status)::text)),'{retained_reason}',to_jsonb(sqlc.arg(reason)::text)) WHERE model_id=sqlc.arg(target_id)::text;

-- name: LockExplicitModelReferences :exec
LOCK TABLE calls, api_keys IN SHARE ROW EXCLUSIVE MODE;

-- name: ModelReferenceReasons :one
SELECT
 EXISTS(SELECT 1 FROM channel_models WHERE model_id=sqlc.arg(target_id)::text) AS channels,
 EXISTS(SELECT 1 FROM calls WHERE model_id=sqlc.arg(target_id)::text OR requested_model=sqlc.arg(target_id)::text) AS calls,
 EXISTS(SELECT 1 FROM route_prefs WHERE model_id=sqlc.arg(target_id)::text) AS routes,
 EXISTS(SELECT 1 FROM api_keys WHERE sqlc.arg(target_id)::text=ANY(allowed_models)
   OR model_aliases ? sqlc.arg(target_id)::text
   OR EXISTS(SELECT 1 FROM jsonb_each_text(model_aliases) alias WHERE alias.value=sqlc.arg(target_id)::text)) AS keys;

-- name: IsModelDeleted :one
SELECT EXISTS(SELECT 1 FROM catalog_deleted_models WHERE model_id=$1);
