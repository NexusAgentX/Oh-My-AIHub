-- name: LookupKey :one
SELECT k.id, k.owner_id, k.name, k.prefix, k.status, k.expires_at, k.allowed_models,
       k.budget_daily_nano, k.budget_monthly_nano, k.budget_total_nano, k.model_aliases,
       a.status AS account_status
FROM api_keys k
JOIN accounts a ON a.id = k.owner_id
WHERE k.key_hash = $1 AND k.deleted_at IS NULL;

-- Receipt work is absent on ordinary calls. Lock the call before claiming its
-- sorted receipt set, so duplicate finalizers cannot claim on a closed call.
-- name: LockPendingReceiptCall :one
SELECT id FROM calls WHERE id=$1 AND outcome='in_progress' FOR UPDATE;

-- name: ClaimExecutionReceipt :execrows
INSERT INTO call_execution_receipts(receipt_key,call_id) VALUES($1,$2)
ON CONFLICT(receipt_key) DO NOTHING;

-- name: TouchKey :exec
UPDATE api_keys SET last_used_at = $2 WHERE id = $1;

-- name: KeySpend :one
SELECT coalesce(sum(cost_nano + fee_nano) FILTER (WHERE created_at >= sqlc.arg(day_start)), 0)::bigint AS today_nano,
       coalesce(sum(cost_nano + fee_nano) FILTER (WHERE created_at >= sqlc.arg(month_start)), 0)::bigint AS month_nano,
       coalesce(sum(cost_nano + fee_nano), 0)::bigint AS total_nano
FROM calls
WHERE api_key_id = sqlc.arg(key_id);


-- Every key the gateway can recognise, for the key snapshot (ADR-0040): the
-- same row as LookupKey for each undeleted key, with the hash it is found by.
-- name: ListAllKeys :many
SELECT k.key_hash, k.id, k.owner_id, k.name, k.prefix, k.status, k.expires_at, k.allowed_models,
       k.budget_daily_nano, k.budget_monthly_nano, k.budget_total_nano, k.model_aliases,
       a.status AS account_status
FROM api_keys k
JOIN accounts a ON a.id = k.owner_id
WHERE k.deleted_at IS NULL;

-- name: AttemptHistory :many
SELECT (att->'channel'->>'id')::uuid AS channel_id,
       coalesce(c.model_id, '')::text AS model_id,
       floor(extract(epoch FROM c.created_at) / 300)::bigint AS bucket,
       coalesce(CASE WHEN c.stream AND att->>'end_reason' = 'completed' AND att->>'ttft_ms' IS NOT NULL
                THEN width_bucket(round(coalesce(att->>'selection_latency_ms',att->>'ttft_ms')::numeric)::int, sqlc.arg(edges)::int[]) - 1 END, -1)::int AS ttft_bin,
       count(*) FILTER (WHERE att->>'end_reason' <> 'client_error')::bigint AS counted,
       count(*) FILTER (WHERE att->>'end_reason' = 'completed')::bigint AS successes,
       count(*) FILTER (WHERE att->>'end_reason' = 'client_error')::bigint AS client_errors,
       count(*) FILTER (WHERE c.created_at < ch.updated_at)::bigint AS before_edit_attempts,
       count(*) FILTER (WHERE att->>'end_reason' = 'client_error' AND c.created_at < ch.updated_at)::bigint AS before_edit_client_errors
FROM calls c
CROSS JOIN LATERAL jsonb_array_elements(c.attempts) AS att
LEFT JOIN channels ch ON ch.id = (att->'channel'->>'id')::uuid
WHERE c.created_at >= sqlc.arg(since)::timestamptz
  AND att->'channel' IS NOT NULL AND jsonb_typeof(att->'channel') = 'object'
  AND att->>'end_reason' <> 'client_disconnected'
  AND att->>'selection_final' IS DISTINCT FROM 'false'
GROUP BY 1, 2, 3, 4;

-- name: ResponseChannel :one
SELECT final_channel_id FROM calls
WHERE upstream_response_id = $1 AND account_id = $2 AND final_channel_id IS NOT NULL
ORDER BY created_at DESC LIMIT 1;

-- One bounded startup read, never a background scan or request-path query.
-- Only v2 attempt facts populate model/protocol/tier/stream scopes. At most
-- 20k recently completed calls and caller-limited aggregate rows are examined;
-- missing older observations remain unknown rather than using legacy TTFT.
-- name: InsertCall :one
INSERT INTO calls (account_id, api_key_id, requested_model, format, stream, tag, client_user_agent, outcome, completed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, sqlc.arg(outcome)::text,
        CASE WHEN sqlc.arg(outcome)::text = 'in_progress' THEN NULL ELSE now() END)
RETURNING id;

-- Closes a call exactly once: a call already out of in_progress is left alone.
-- Keep scalar parameters before the JSON payloads and the snapshot last:
-- pgx appends encoded parameters to one buffer, so a smaller snapshot must not
-- cause another buffer growth when encoding the following scalar values.
-- name: FinishCall :execrows
UPDATE calls SET
    outcome = $1, model_id = $2, final_channel_id = $3,
    routing_mode = $4, routing_source = $5,
    attempts = $21, input_tokens = $6, output_tokens = $7,
    cache_write_tokens = $8, cache_read_tokens = $9,
    price_snapshot = $22, cost_nano = $10, fee_nano = $11,
    ttft_ms = $12, duration_ms = $13, output_tokens_per_second = $14,
    inter_token_p50_ms = $15, inter_token_p95_ms = $16,
    response_bytes = $17, upstream_response_id = $18,
    diagnostics = $20, completed_at = clock_timestamp()
WHERE id = $19 AND outcome = 'in_progress';

-- name: SetCallSettlements :exec
UPDATE calls c SET diagnostics = jsonb_set(c.diagnostics, '{stages}',
    coalesce(c.diagnostics->'stages', '[]'::jsonb) || jsonb_build_array(jsonb_build_object('stage', 'settlement', 'duration_ms', d.duration_ms)))
FROM (SELECT unnest(sqlc.arg(ids)::uuid[]) AS id,
             unnest(sqlc.arg(durations)::double precision[]) AS duration_ms) d
WHERE c.id = d.id AND c.outcome <> 'in_progress' AND c.diagnostics IS NOT NULL
    AND NOT (coalesce(c.diagnostics->'stages', '[]'::jsonb) @> '[{"stage":"settlement"}]'::jsonb);

-- name: SetCallLedgerTx :exec
UPDATE calls SET ledger_tx_id = $2 WHERE id = $1;

-- name: SweepStaleCalls :execrows
UPDATE calls SET outcome = 'interrupted', completed_at = now()
WHERE outcome = 'in_progress' AND created_at < $1
AND NOT EXISTS (SELECT 1 FROM gateway_background_jobs j WHERE j.call_id=calls.id AND j.settled_at IS NULL);

-- name: HomeToday :one
SELECT coalesce(sum(cost_nano + fee_nano), 0)::bigint AS spend_nano,
       count(*)::bigint AS calls,
       count(*) FILTER (WHERE outcome IN ('succeeded', 'succeeded_unbilled'))::bigint AS succeeded
FROM calls WHERE account_id = $1 AND created_at >= $2;

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
       c.cost_nano, c.fee_nano, c.ttft_ms, c.duration_ms,
       jsonb_array_length(c.attempts)::int AS attempt_count, (c.ledger_tx_id IS NOT NULL)::boolean AS booked
FROM calls c
LEFT JOIN api_keys k ON k.id = c.api_key_id
LEFT JOIN channels ch ON ch.id = c.final_channel_id
WHERE c.account_id = $1
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg(row_limit);

-- name: LoadGeoCache :many
SELECT ip, location, expires_at FROM geoip_cache
WHERE expires_at > now() ORDER BY updated_at DESC LIMIT 4096;

-- name: SaveGeoCache :exec
INSERT INTO geoip_cache (ip, location, expires_at) VALUES ($1, $2, $3)
ON CONFLICT (ip) DO UPDATE SET location = EXCLUDED.location, expires_at = EXCLUDED.expires_at, updated_at = now();

-- name: TrimGeoCache :exec
DELETE FROM geoip_cache WHERE ip IN (
 SELECT ip FROM geoip_cache ORDER BY updated_at DESC, ip OFFSET 4096
);

-- name: ReserveGeoLookup :one
INSERT INTO geoip_daily_usage (day, queries) VALUES ($1, 1)
ON CONFLICT (day) DO UPDATE SET queries = geoip_daily_usage.queries + 1
WHERE geoip_daily_usage.queries < sqlc.arg(daily_limit)::integer
RETURNING queries;

-- name: TrimGeoUsage :exec
DELETE FROM geoip_daily_usage WHERE day < now() - interval '7 days';

-- name: EnrichCallLocations :exec
UPDATE calls SET diagnostics = jsonb_set(
 jsonb_set(diagnostics, '{network,client,location}',
  COALESCE(sqlc.arg(locations)::jsonb -> (diagnostics #>> '{network,client,ip}'), diagnostics #> '{network,client,location}', 'null'::jsonb)),
 '{network,attempts}',
 COALESCE((SELECT jsonb_agg(jsonb_set(item, '{node,location}',
  COALESCE(sqlc.arg(locations)::jsonb -> (item #>> '{node,ip}'), item #> '{node,location}', 'null'::jsonb)) ORDER BY ord)
 FROM jsonb_array_elements(diagnostics #> '{network,attempts}') WITH ORDINALITY AS a(item, ord)), '[]'::jsonb))
WHERE id = $1 AND diagnostics #> '{network,client}' IS NOT NULL;

-- name: SaveBackground :exec
INSERT INTO gateway_background_jobs (call_id, account_id, key_id, channel_id, response_id, metadata, deadline_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (call_id) DO NOTHING;

-- name: LookupBackground :one
SELECT * FROM gateway_background_jobs WHERE account_id=$1 AND key_id=$2 AND response_id=$3;

-- name: PendingBackground :many
SELECT * FROM gateway_background_jobs WHERE settled_at IS NULL ORDER BY call_id LIMIT 257;

-- name: ClaimBackground :many
WITH due AS (
 SELECT call_id FROM gateway_background_jobs
 WHERE settled_at IS NULL AND next_poll_at <= sqlc.arg(now)::timestamptz
 ORDER BY next_poll_at,call_id LIMIT 16 FOR UPDATE SKIP LOCKED
)
UPDATE gateway_background_jobs j SET next_poll_at=sqlc.arg(now)::timestamptz + interval '30 seconds'
FROM due WHERE j.call_id=due.call_id RETURNING j.*;

-- name: RetryBackground :exec
UPDATE gateway_background_jobs SET next_poll_at=$2 WHERE call_id=$1 AND settled_at IS NULL;

-- name: SettleBackground :exec
UPDATE gateway_background_jobs SET settled_at=now() WHERE call_id=$1 AND settled_at IS NULL;

-- name: InsertChannelEvent :exec
INSERT INTO channel_events (channel_id,kind,reason) VALUES ($1,$2,$3);
