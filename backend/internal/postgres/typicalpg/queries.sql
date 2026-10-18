-- name: GetTypicalSettings :one
SELECT * FROM typical_settings WHERE singleton;
-- name: UpdateTypicalSettings :one
UPDATE typical_settings SET version=version+1,window_days=$1,min_samples=$2,interval_seconds=$3,
 typical_tokens=$4,typical_requests=$5,typical_images=$6,updated_at=now()
WHERE singleton AND version=$7 RETURNING *;
-- name: LockTypicalState :one
SELECT generation,checkpoint FROM typical_state WHERE singleton FOR UPDATE;
-- name: GetTypicalState :one
SELECT generation,checkpoint FROM typical_state WHERE singleton;
-- name: UpdateTypicalCheckpoint :exec
UPDATE typical_state SET generation=generation+1,checkpoint=$1 WHERE singleton;
-- name: SaveTypicalSnapshot :execrows
UPDATE typical_state SET snapshot=$1 WHERE singleton AND EXISTS(SELECT 1 FROM typical_settings WHERE singleton AND version=$2);
-- name: ScanTypicalCalls :many
SELECT id,coalesce(model_id,'')::text AS model_id,format,outcome,created_at,completed_at,
 input_tokens,output_tokens,cache_write_tokens,cache_read_tokens,
 coalesce(diagnostics->>'usage_status','')::text AS usage_status,
 CASE WHEN octet_length(price_snapshot::text)<=262144 THEN price_snapshot ELSE '{}'::jsonb END AS snapshot
FROM calls WHERE completed_at IS NOT NULL AND outcome='succeeded' AND price_snapshot ? 'group_quote'
 AND completed_at>=sqlc.arg(window_floor)::timestamptz AND completed_at<=sqlc.arg(window_ceiling)::timestamptz
 AND (completed_at,id)>(sqlc.arg(after_at)::timestamptz,sqlc.arg(after_id)::uuid)
ORDER BY completed_at,id LIMIT sqlc.arg(page_limit);
-- name: CountTypicalSamples :one
SELECT count(*) FROM typical_samples;
-- name: IngestTypicalSamples :exec
INSERT INTO typical_samples(call_id,completed_at,profile)
SELECT x.call_id,x.completed_at,x.profile FROM jsonb_to_recordset(sqlc.arg(rows)::jsonb)AS x(call_id uuid,completed_at timestamptz,profile jsonb)
ON CONFLICT(call_id)DO UPDATE SET profile=excluded.profile,completed_at=excluded.completed_at;
-- name: ListTypicalSamples :many
SELECT call_id,completed_at,profile FROM typical_samples WHERE completed_at>=sqlc.arg(window_floor)::timestamptz
 AND (completed_at,call_id)>(sqlc.arg(after_at)::timestamptz,sqlc.arg(after_id)::uuid)
ORDER BY completed_at,call_id LIMIT sqlc.arg(page_limit);
-- name: PruneTypicalSamples :exec
DELETE FROM typical_samples t WHERE t.call_id IN(SELECT p.call_id FROM typical_samples p WHERE p.completed_at<$1 ORDER BY p.completed_at,p.call_id LIMIT $2);

-- name: LoadTypicalSnapshot :one
SELECT snapshot FROM typical_state WHERE singleton;
