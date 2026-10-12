-- Feature G：调用、用量、渠道统计与积分观测的只读查询。
-- 所有“成功”都指 outcome IN ('succeeded', 'succeeded_unbilled')；“实际扣除”只计已有账本交易的调用
-- （自己的渠道调用不产生账本交易，见 ADR-0035）。

-- name: ListCalls :many
SELECT c.id, c.created_at, c.completed_at, c.account_id, a.username AS account_username, a.display_name AS account_display_name,
       c.api_key_id, k.name AS api_key_name, c.model_id, c.requested_model, c.format, c.stream, c.tag, c.outcome,
       c.final_channel_id, ch.name AS channel_name,
       c.input_tokens, c.output_tokens, c.cache_write_tokens, c.cache_read_tokens,
       c.cost_nano, c.fee_nano, c.ledger_tx_id, c.ttft_ms, c.duration_ms,
       jsonb_array_length(c.attempts)::int AS attempt_count,
       (SELECT coalesce(jsonb_agg(att), '[]'::jsonb) FROM jsonb_array_elements(c.attempts) att
         WHERE sqlc.narg(scope_channel_text)::text IS NOT NULL AND att->'channel'->>'id' = sqlc.narg(scope_channel_text)::text)::jsonb AS scope_attempts
FROM calls c
JOIN accounts a ON a.id = c.account_id
LEFT JOIN api_keys k ON k.id = c.api_key_id
LEFT JOIN channels ch ON ch.id = c.final_channel_id
WHERE (sqlc.narg(account_id)::uuid IS NULL OR c.account_id = sqlc.narg(account_id)::uuid)
  AND (sqlc.narg(scope_channel_id)::uuid IS NULL
       OR c.final_channel_id = sqlc.narg(scope_channel_id)::uuid
       OR c.attempts @> jsonb_build_array(jsonb_build_object('channel', jsonb_build_object('id', sqlc.narg(scope_channel_text)::text))))
  AND (sqlc.narg(final_channel_id)::uuid IS NULL OR c.final_channel_id = sqlc.narg(final_channel_id)::uuid)
  AND (sqlc.narg(api_key_id)::uuid IS NULL OR c.api_key_id = sqlc.narg(api_key_id)::uuid)
  AND (sqlc.narg(model)::text IS NULL OR c.model_id = sqlc.narg(model)::text OR c.requested_model = sqlc.narg(model)::text)
  AND (sqlc.narg(format)::text IS NULL OR c.format = sqlc.narg(format)::text)
  AND (sqlc.narg(outcome)::text IS NULL OR c.outcome = sqlc.narg(outcome)::text)
  AND (sqlc.narg(tag)::text IS NULL OR c.tag = sqlc.narg(tag)::text)
  AND (sqlc.narg(request_id)::uuid IS NULL OR c.id = sqlc.narg(request_id)::uuid)
  AND (sqlc.arg(dimension)::text = '' OR (CASE sqlc.arg(dimension)::text WHEN 'all' THEN '' WHEN 'model' THEN coalesce(c.model_id,c.requested_model) WHEN 'channel' THEN coalesce(c.final_channel_id::text,'') WHEN 'format' THEN c.format WHEN 'stream' THEN c.stream::text WHEN 'key' THEN coalesce(c.api_key_id::text,'') WHEN 'tag' THEN coalesce(c.tag,'') WHEN 'account' THEN c.account_id::text WHEN 'outcome' THEN c.outcome WHEN 'error' THEN coalesce(c.attempts->-1->>'end_reason','none') WHEN 'error_code' THEN coalesce(c.attempts->-1->>'error_code','none') WHEN 'status' THEN coalesce(c.attempts->-1->>'status_code','none') WHEN 'stop' THEN coalesce(c.diagnostics->>'stop_reason','') WHEN 'service_tier' THEN coalesce(c.price_snapshot->'detail'->>'service_tier','') ELSE '' END) = sqlc.arg(dimension_value)::text)
  AND (sqlc.arg(second_dimension)::text = '' OR (CASE sqlc.arg(second_dimension)::text WHEN 'all' THEN '' WHEN 'model' THEN coalesce(c.model_id,c.requested_model) WHEN 'channel' THEN coalesce(c.final_channel_id::text,'') WHEN 'format' THEN c.format WHEN 'stream' THEN c.stream::text WHEN 'key' THEN coalesce(c.api_key_id::text,'') WHEN 'tag' THEN coalesce(c.tag,'') WHEN 'account' THEN c.account_id::text WHEN 'outcome' THEN c.outcome WHEN 'error' THEN coalesce(c.attempts->-1->>'end_reason','none') WHEN 'error_code' THEN coalesce(c.attempts->-1->>'error_code','none') WHEN 'status' THEN coalesce(c.attempts->-1->>'status_code','none') WHEN 'stop' THEN coalesce(c.diagnostics->>'stop_reason','') WHEN 'service_tier' THEN coalesce(c.price_snapshot->'detail'->>'service_tier','') ELSE '' END) = sqlc.arg(second_value)::text)
  AND (sqlc.narg(stream)::boolean IS NULL OR c.stream=sqlc.narg(stream)::boolean)
  AND (sqlc.narg(min_attempts)::int IS NULL OR jsonb_array_length(c.attempts)>=sqlc.narg(min_attempts)::int)
  AND (sqlc.narg(max_attempts)::int IS NULL OR jsonb_array_length(c.attempts)<=sqlc.narg(max_attempts)::int)
  AND (sqlc.narg(billed)::boolean IS NULL OR (c.ledger_tx_id IS NOT NULL)=sqlc.narg(billed)::boolean)
  AND (sqlc.narg(delivery)::text IS NULL OR c.diagnostics->>'delivery'=sqlc.narg(delivery)::text)
  AND (sqlc.narg(usage_status)::text IS NULL OR c.diagnostics->>'usage_status'=sqlc.narg(usage_status)::text)
  AND ((sqlc.narg(status_code)::int IS NULL AND sqlc.narg(error_code)::text IS NULL AND sqlc.narg(end_reason)::text IS NULL AND sqlc.narg(attempt_channel)::text IS NULL)
    OR EXISTS (SELECT 1 FROM jsonb_array_elements(c.attempts) att
      WHERE (sqlc.narg(status_code)::int IS NULL OR (att->>'status_code')::int=sqlc.narg(status_code)::int)
      AND (sqlc.narg(error_code)::text IS NULL OR att->>'error_code'=sqlc.narg(error_code)::text)
      AND (sqlc.narg(end_reason)::text IS NULL OR att->>'end_reason'=sqlc.narg(end_reason)::text)
      AND (sqlc.narg(attempt_channel)::text IS NULL OR att->'channel'->>'id'=sqlc.narg(attempt_channel)::text)
      AND (sqlc.narg(scope_channel_text)::text IS NULL OR att->'channel'->>'id'=sqlc.narg(scope_channel_text)::text)))
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR c.created_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR c.created_at < sqlc.narg(to_time)::timestamptz)
  AND (sqlc.narg(min_duration)::int IS NULL OR c.duration_ms >= sqlc.narg(min_duration)::int)
  AND (sqlc.narg(max_duration)::int IS NULL OR c.duration_ms <= sqlc.narg(max_duration)::int)
  AND (sqlc.narg(min_tokens)::bigint IS NULL OR c.input_tokens + c.output_tokens + c.cache_write_tokens + c.cache_read_tokens >= sqlc.narg(min_tokens)::bigint)
  AND (sqlc.narg(max_tokens)::bigint IS NULL OR c.input_tokens + c.output_tokens + c.cache_write_tokens + c.cache_read_tokens <= sqlc.narg(max_tokens)::bigint)
  AND (sqlc.narg(min_charged)::bigint IS NULL OR (CASE WHEN c.ledger_tx_id IS NULL THEN 0 ELSE c.cost_nano + c.fee_nano END) >= sqlc.narg(min_charged)::bigint)
  AND (sqlc.narg(max_charged)::bigint IS NULL OR (CASE WHEN c.ledger_tx_id IS NULL THEN 0 ELSE c.cost_nano + c.fee_nano END) <= sqlc.narg(max_charged)::bigint)
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (c.created_at, c.id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg(row_limit);

-- name: CallStats :one
SELECT count(*)::bigint AS calls,
       count(*) FILTER (WHERE c.outcome IN ('succeeded', 'succeeded_unbilled'))::bigint AS succeeded,
       count(*) FILTER (WHERE c.outcome NOT IN ('succeeded', 'succeeded_unbilled', 'in_progress'))::bigint AS failed,
       coalesce(sum(CASE WHEN c.ledger_tx_id IS NULL THEN 0 ELSE c.cost_nano + c.fee_nano END), 0)::bigint AS charged_nano,
       coalesce(sum(c.input_tokens), 0)::bigint AS input_tokens,
       coalesce(sum(c.output_tokens), 0)::bigint AS output_tokens,
       coalesce(sum(c.cache_write_tokens + c.cache_read_tokens), 0)::bigint AS cache_tokens,
       coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY c.ttft_ms) FILTER (WHERE c.stream AND c.ttft_ms IS NOT NULL), -1)::double precision AS ttft_p50_ms,
       coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY c.ttft_ms) FILTER (WHERE c.stream AND c.ttft_ms IS NOT NULL), -1)::double precision AS ttft_p95_ms
FROM calls c
WHERE (sqlc.narg(account_id)::uuid IS NULL OR c.account_id = sqlc.narg(account_id)::uuid)
  AND (sqlc.narg(scope_channel_id)::uuid IS NULL
       OR c.final_channel_id = sqlc.narg(scope_channel_id)::uuid
       OR c.attempts @> jsonb_build_array(jsonb_build_object('channel', jsonb_build_object('id', sqlc.narg(scope_channel_text)::text))))
  AND (sqlc.narg(final_channel_id)::uuid IS NULL OR c.final_channel_id = sqlc.narg(final_channel_id)::uuid)
  AND (sqlc.narg(api_key_id)::uuid IS NULL OR c.api_key_id = sqlc.narg(api_key_id)::uuid)
  AND (sqlc.narg(model)::text IS NULL OR c.model_id = sqlc.narg(model)::text OR c.requested_model = sqlc.narg(model)::text)
  AND (sqlc.narg(format)::text IS NULL OR c.format = sqlc.narg(format)::text)
  AND (sqlc.narg(outcome)::text IS NULL OR c.outcome = sqlc.narg(outcome)::text)
  AND (sqlc.narg(tag)::text IS NULL OR c.tag = sqlc.narg(tag)::text)
  AND (sqlc.narg(request_id)::uuid IS NULL OR c.id = sqlc.narg(request_id)::uuid)
  AND (sqlc.arg(dimension)::text = '' OR (CASE sqlc.arg(dimension)::text WHEN 'all' THEN '' WHEN 'model' THEN coalesce(c.model_id,c.requested_model) WHEN 'channel' THEN coalesce(c.final_channel_id::text,'') WHEN 'format' THEN c.format WHEN 'stream' THEN c.stream::text WHEN 'key' THEN coalesce(c.api_key_id::text,'') WHEN 'tag' THEN coalesce(c.tag,'') WHEN 'account' THEN c.account_id::text WHEN 'outcome' THEN c.outcome WHEN 'error' THEN coalesce(c.attempts->-1->>'end_reason','none') WHEN 'error_code' THEN coalesce(c.attempts->-1->>'error_code','none') WHEN 'status' THEN coalesce(c.attempts->-1->>'status_code','none') WHEN 'stop' THEN coalesce(c.diagnostics->>'stop_reason','') WHEN 'service_tier' THEN coalesce(c.price_snapshot->'detail'->>'service_tier','') ELSE '' END) = sqlc.arg(dimension_value)::text)
  AND (sqlc.arg(second_dimension)::text = '' OR (CASE sqlc.arg(second_dimension)::text WHEN 'all' THEN '' WHEN 'model' THEN coalesce(c.model_id,c.requested_model) WHEN 'channel' THEN coalesce(c.final_channel_id::text,'') WHEN 'format' THEN c.format WHEN 'stream' THEN c.stream::text WHEN 'key' THEN coalesce(c.api_key_id::text,'') WHEN 'tag' THEN coalesce(c.tag,'') WHEN 'account' THEN c.account_id::text WHEN 'outcome' THEN c.outcome WHEN 'error' THEN coalesce(c.attempts->-1->>'end_reason','none') WHEN 'error_code' THEN coalesce(c.attempts->-1->>'error_code','none') WHEN 'status' THEN coalesce(c.attempts->-1->>'status_code','none') WHEN 'stop' THEN coalesce(c.diagnostics->>'stop_reason','') WHEN 'service_tier' THEN coalesce(c.price_snapshot->'detail'->>'service_tier','') ELSE '' END) = sqlc.arg(second_value)::text)
  AND (sqlc.narg(stream)::boolean IS NULL OR c.stream=sqlc.narg(stream)::boolean)
  AND (sqlc.narg(min_attempts)::int IS NULL OR jsonb_array_length(c.attempts)>=sqlc.narg(min_attempts)::int)
  AND (sqlc.narg(max_attempts)::int IS NULL OR jsonb_array_length(c.attempts)<=sqlc.narg(max_attempts)::int)
  AND (sqlc.narg(billed)::boolean IS NULL OR (c.ledger_tx_id IS NOT NULL)=sqlc.narg(billed)::boolean)
  AND (sqlc.narg(delivery)::text IS NULL OR c.diagnostics->>'delivery'=sqlc.narg(delivery)::text)
  AND (sqlc.narg(usage_status)::text IS NULL OR c.diagnostics->>'usage_status'=sqlc.narg(usage_status)::text)
  AND ((sqlc.narg(status_code)::int IS NULL AND sqlc.narg(error_code)::text IS NULL AND sqlc.narg(end_reason)::text IS NULL AND sqlc.narg(attempt_channel)::text IS NULL)
    OR EXISTS (SELECT 1 FROM jsonb_array_elements(c.attempts) att
      WHERE (sqlc.narg(status_code)::int IS NULL OR (att->>'status_code')::int=sqlc.narg(status_code)::int)
      AND (sqlc.narg(error_code)::text IS NULL OR att->>'error_code'=sqlc.narg(error_code)::text)
      AND (sqlc.narg(end_reason)::text IS NULL OR att->>'end_reason'=sqlc.narg(end_reason)::text)
      AND (sqlc.narg(attempt_channel)::text IS NULL OR att->'channel'->>'id'=sqlc.narg(attempt_channel)::text)
      AND (sqlc.narg(scope_channel_text)::text IS NULL OR att->'channel'->>'id'=sqlc.narg(scope_channel_text)::text)))
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR c.created_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR c.created_at < sqlc.narg(to_time)::timestamptz)
  AND (sqlc.narg(min_duration)::int IS NULL OR c.duration_ms >= sqlc.narg(min_duration)::int)
  AND (sqlc.narg(max_duration)::int IS NULL OR c.duration_ms <= sqlc.narg(max_duration)::int)
  AND (sqlc.narg(min_tokens)::bigint IS NULL OR c.input_tokens + c.output_tokens + c.cache_write_tokens + c.cache_read_tokens >= sqlc.narg(min_tokens)::bigint)
  AND (sqlc.narg(max_tokens)::bigint IS NULL OR c.input_tokens + c.output_tokens + c.cache_write_tokens + c.cache_read_tokens <= sqlc.narg(max_tokens)::bigint)
  AND (sqlc.narg(min_charged)::bigint IS NULL OR (CASE WHEN c.ledger_tx_id IS NULL THEN 0 ELSE c.cost_nano + c.fee_nano END) >= sqlc.narg(min_charged)::bigint)
  AND (sqlc.narg(max_charged)::bigint IS NULL OR (CASE WHEN c.ledger_tx_id IS NULL THEN 0 ELSE c.cost_nano + c.fee_nano END) <= sqlc.narg(max_charged)::bigint);

-- name: GetCall :one
SELECT sqlc.embed(c), a.username AS account_username, a.display_name AS account_display_name,
       k.name AS api_key_name, ch.name AS channel_name
FROM calls c
JOIN accounts a ON a.id = c.account_id
LEFT JOIN api_keys k ON k.id = c.api_key_id
LEFT JOIN channels ch ON ch.id = c.final_channel_id
WHERE c.id = $1;

-- name: ChannelOwners :many
SELECT id, owner_id, name FROM channels WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: UsageSpend :many
SELECT CASE sqlc.arg(group_by)::text
         WHEN 'day' THEN to_char(c.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD')
         WHEN 'model' THEN coalesce(c.model_id, c.requested_model)
         WHEN 'key' THEN coalesce(c.api_key_id::text, '')
         ELSE coalesce(c.tag, '')
       END::text AS group_key,
       coalesce(max(k.name), '')::text AS label,
       count(*)::bigint AS calls,
       count(*) FILTER (WHERE c.outcome IN ('succeeded', 'succeeded_unbilled'))::bigint AS succeeded,
       coalesce(sum(c.input_tokens), 0)::bigint AS input_tokens,
       coalesce(sum(c.output_tokens), 0)::bigint AS output_tokens,
       coalesce(sum(c.cache_write_tokens), 0)::bigint AS cache_write_tokens,
       coalesce(sum(c.cache_read_tokens), 0)::bigint AS cache_read_tokens,
       coalesce(sum(CASE WHEN c.ledger_tx_id IS NULL THEN 0 ELSE c.cost_nano + c.fee_nano END), 0)::bigint AS amount_nano
FROM calls c
LEFT JOIN api_keys k ON k.id = c.api_key_id
WHERE c.account_id = sqlc.arg(account_id)::uuid
  AND c.outcome <> 'in_progress'
  AND c.created_at >= sqlc.arg(from_time)::timestamptz AND c.created_at < sqlc.arg(to_time)::timestamptz
  AND (sqlc.narg(api_key_id)::uuid IS NULL OR c.api_key_id = sqlc.narg(api_key_id)::uuid)
  AND (sqlc.narg(model)::text IS NULL OR c.model_id = sqlc.narg(model)::text OR c.requested_model = sqlc.narg(model)::text)
  AND (sqlc.narg(tag)::text IS NULL OR c.tag = sqlc.narg(tag)::text)
GROUP BY 1
ORDER BY 1;

-- name: UsageRevenue :many
SELECT CASE sqlc.arg(group_by)::text
         WHEN 'day' THEN to_char(c.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD')
         WHEN 'model' THEN coalesce(c.model_id, c.requested_model)
         ELSE c.final_channel_id::text
       END::text AS group_key,
       coalesce(max(ch.name), '')::text AS label,
       count(*)::bigint AS calls,
       count(*) FILTER (WHERE c.outcome IN ('succeeded', 'succeeded_unbilled'))::bigint AS succeeded,
       coalesce(sum(c.input_tokens), 0)::bigint AS input_tokens,
       coalesce(sum(c.output_tokens), 0)::bigint AS output_tokens,
       coalesce(sum(c.cache_write_tokens), 0)::bigint AS cache_write_tokens,
       coalesce(sum(c.cache_read_tokens), 0)::bigint AS cache_read_tokens,
       coalesce(sum(CASE WHEN c.ledger_tx_id IS NULL THEN 0 ELSE c.cost_nano END), 0)::bigint AS amount_nano
FROM calls c
JOIN channels ch ON ch.id = c.final_channel_id
WHERE ch.owner_id = sqlc.arg(owner_id)::uuid
  AND c.outcome IN ('succeeded', 'succeeded_unbilled', 'interrupted', 'client_disconnected')
  AND c.created_at >= sqlc.arg(from_time)::timestamptz AND c.created_at < sqlc.arg(to_time)::timestamptz
  AND (sqlc.narg(channel_id)::uuid IS NULL OR c.final_channel_id = sqlc.narg(channel_id)::uuid)
  AND (sqlc.narg(model)::text IS NULL OR c.model_id = sqlc.narg(model)::text)
GROUP BY 1
ORDER BY 1;

-- 渠道统计以“到达该渠道的尝试”为单位：客户端自己取消或因请求本身有问题的尝试不计。
-- name: ChannelAttemptStats :one
SELECT count(*)::bigint AS attempts,
       count(*) FILTER (WHERE att->>'end_reason' = 'completed')::bigint AS successes,
       coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY (att->>'ttft_ms')::double precision)
         FILTER (WHERE att->>'end_reason' = 'completed' AND att->>'ttft_ms' IS NOT NULL), -1)::double precision AS ttft_p50_ms,
       coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY (att->>'ttft_ms')::double precision)
         FILTER (WHERE att->>'end_reason' = 'completed' AND att->>'ttft_ms' IS NOT NULL), -1)::double precision AS ttft_p95_ms
FROM calls c
CROSS JOIN LATERAL jsonb_array_elements(c.attempts) att
WHERE c.created_at >= sqlc.arg(from_time)::timestamptz AND c.created_at < sqlc.arg(to_time)::timestamptz
  AND c.attempts @> jsonb_build_array(jsonb_build_object('channel', jsonb_build_object('id', sqlc.arg(channel_text)::text)))
  AND att->'channel'->>'id' = sqlc.arg(channel_text)::text
  AND att->>'end_reason' NOT IN ('client_disconnected', 'client_error');

-- name: ChannelSpeed :one
SELECT coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY c.output_tokens_per_second), -1)::double precision AS p50,
       coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY c.output_tokens_per_second), -1)::double precision AS p95
FROM calls c
WHERE c.final_channel_id = sqlc.arg(channel_id)::uuid AND c.output_tokens_per_second IS NOT NULL
  AND c.created_at >= sqlc.arg(from_time)::timestamptz AND c.created_at < sqlc.arg(to_time)::timestamptz;

-- name: ChannelHourly :many
SELECT date_trunc('hour', c.created_at)::timestamptz AS hour,
       count(*)::bigint AS attempts,
       count(*) FILTER (WHERE att->>'end_reason' = 'completed')::bigint AS successes
FROM calls c
CROSS JOIN LATERAL jsonb_array_elements(c.attempts) att
WHERE c.created_at >= sqlc.arg(from_time)::timestamptz AND c.created_at < sqlc.arg(to_time)::timestamptz
  AND c.attempts @> jsonb_build_array(jsonb_build_object('channel', jsonb_build_object('id', sqlc.arg(channel_text)::text)))
  AND att->'channel'->>'id' = sqlc.arg(channel_text)::text
  AND att->>'end_reason' NOT IN ('client_disconnected', 'client_error')
GROUP BY 1
ORDER BY 1;

-- name: ChannelDaily :many
SELECT to_char(c.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD')::text AS day,
       count(*)::bigint AS attempts,
       count(*) FILTER (WHERE att->>'end_reason' = 'completed')::bigint AS successes
FROM calls c
CROSS JOIN LATERAL jsonb_array_elements(c.attempts) att
WHERE c.created_at >= sqlc.arg(from_time)::timestamptz AND c.created_at < sqlc.arg(to_time)::timestamptz
  AND c.attempts @> jsonb_build_array(jsonb_build_object('channel', jsonb_build_object('id', sqlc.arg(channel_text)::text)))
  AND att->'channel'->>'id' = sqlc.arg(channel_text)::text
  AND att->>'end_reason' NOT IN ('client_disconnected', 'client_error')
GROUP BY 1
ORDER BY 1;

-- name: ChannelModels :many
SELECT coalesce(c.model_id, c.requested_model)::text AS model_id,
       count(*)::bigint AS attempts,
       count(*) FILTER (WHERE att->>'end_reason' = 'completed')::bigint AS successes
FROM calls c
CROSS JOIN LATERAL jsonb_array_elements(c.attempts) att
WHERE c.created_at >= sqlc.arg(from_time)::timestamptz AND c.created_at < sqlc.arg(to_time)::timestamptz
  AND c.attempts @> jsonb_build_array(jsonb_build_object('channel', jsonb_build_object('id', sqlc.arg(channel_text)::text)))
  AND att->'channel'->>'id' = sqlc.arg(channel_text)::text
  AND att->>'end_reason' NOT IN ('client_disconnected', 'client_error')
GROUP BY 1
ORDER BY 1;

-- name: ChannelStatusCodes :many
SELECT coalesce((att->>'status_code')::int, 0)::int AS status_code, count(*)::bigint AS count
FROM calls c
CROSS JOIN LATERAL jsonb_array_elements(c.attempts) att
WHERE c.created_at >= sqlc.arg(from_time)::timestamptz AND c.created_at < sqlc.arg(to_time)::timestamptz
  AND c.attempts @> jsonb_build_array(jsonb_build_object('channel', jsonb_build_object('id', sqlc.arg(channel_text)::text)))
  AND att->'channel'->>'id' = sqlc.arg(channel_text)::text
  AND att->>'end_reason' NOT IN ('client_disconnected', 'client_error')
  AND att->>'end_reason' <> 'completed'
GROUP BY 1
ORDER BY 2 DESC, 1;

-- name: ChannelRecentFailures :many
SELECT c.id, c.created_at, c.model_id, att::jsonb AS attempt
FROM calls c
CROSS JOIN LATERAL jsonb_array_elements(c.attempts) att
WHERE c.created_at >= sqlc.arg(from_time)::timestamptz AND c.created_at < sqlc.arg(to_time)::timestamptz
  AND c.attempts @> jsonb_build_array(jsonb_build_object('channel', jsonb_build_object('id', sqlc.arg(channel_text)::text)))
  AND att->'channel'->>'id' = sqlc.arg(channel_text)::text
  AND att->>'end_reason' NOT IN ('client_disconnected', 'client_error')
  AND att->>'end_reason' <> 'completed'
ORDER BY c.created_at DESC, c.id DESC
LIMIT 20;

-- 渠道收入只计有账本交易的调用（自己的渠道调用没有交易，不算收入）。
-- name: ChannelRevenueByDay :many
SELECT to_char(c.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD')::text AS day,
       coalesce(sum(c.cost_nano), 0)::bigint AS revenue_nano
FROM calls c
WHERE c.final_channel_id = sqlc.arg(channel_id)::uuid AND c.ledger_tx_id IS NOT NULL
  AND c.created_at >= sqlc.arg(from_time)::timestamptz AND c.created_at < sqlc.arg(to_time)::timestamptz
GROUP BY 1;

-- name: ChannelRevenueByModel :many
SELECT coalesce(c.model_id, c.requested_model)::text AS model_id, coalesce(sum(c.cost_nano), 0)::bigint AS revenue_nano
FROM calls c
WHERE c.final_channel_id = sqlc.arg(channel_id)::uuid AND c.ledger_tx_id IS NOT NULL
  AND c.created_at >= sqlc.arg(from_time)::timestamptz AND c.created_at < sqlc.arg(to_time)::timestamptz
GROUP BY 1;

-- name: ChannelRevenueSince :one
SELECT coalesce(sum(c.cost_nano), 0)::bigint AS revenue_nano
FROM calls c
WHERE c.final_channel_id = sqlc.arg(channel_id)::uuid AND c.ledger_tx_id IS NOT NULL
  AND c.created_at >= sqlc.arg(since)::timestamptz;

-- name: GetChannelBrief :one
SELECT id, owner_id, name, daily_revenue_cap_nano FROM channels WHERE id = $1 AND deleted_at IS NULL;

-- name: ListChannelEvents :many
SELECT id, kind, reason, created_at FROM channel_events WHERE channel_id = $1 ORDER BY id DESC LIMIT 50;

-- 原始错误只保留 30 天：清空 attempts 中的 error_message，保留状态码与错误码。
-- name: ScrubAttemptErrors :execrows
WITH due AS (
    SELECT id FROM calls
    WHERE created_at < sqlc.arg(before)::timestamptz
      AND EXISTS (SELECT 1 FROM jsonb_array_elements(attempts) a WHERE jsonb_typeof(a->'error_message') = 'string')
    ORDER BY created_at
    LIMIT sqlc.arg(row_limit)
)
UPDATE calls c
SET attempts = (
    SELECT coalesce(jsonb_agg(CASE WHEN jsonb_typeof(t.a->'error_message') = 'string' THEN jsonb_set(t.a, '{error_message}', 'null'::jsonb) ELSE t.a END ORDER BY t.ord), '[]'::jsonb)
    FROM jsonb_array_elements(c.attempts) WITH ORDINALITY AS t(a, ord)
)
FROM due
WHERE c.id = due.id;

-- name: ListChannelsForMetrics :many
SELECT id, status FROM channels WHERE deleted_at IS NULL;

-- name: AnalyzeCalls :many
WITH filtered AS (
 SELECT c.*, CASE sqlc.arg(group_by)::text WHEN 'all' THEN '' WHEN 'time' THEN to_char(date_bin((sqlc.arg(bucket_seconds)::int::text || ' seconds')::interval,c.created_at,'2000-01-01 00:00:00+00'::timestamptz) AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') WHEN 'model' THEN coalesce(c.model_id,c.requested_model) WHEN 'channel' THEN coalesce(c.final_channel_id::text,'') WHEN 'format' THEN c.format WHEN 'stream' THEN c.stream::text WHEN 'key' THEN coalesce(c.api_key_id::text,'') WHEN 'tag' THEN coalesce(c.tag,'') WHEN 'account' THEN c.account_id::text WHEN 'outcome' THEN c.outcome WHEN 'error' THEN coalesce(c.attempts->-1->>'end_reason','none') WHEN 'error_code' THEN coalesce(c.attempts->-1->>'error_code','none') WHEN 'status' THEN coalesce(c.attempts->-1->>'status_code','none') WHEN 'stop' THEN coalesce(c.diagnostics->>'stop_reason','') WHEN 'service_tier' THEN coalesce(c.price_snapshot->'detail'->>'service_tier','') ELSE '' END AS group_key, CASE sqlc.arg(split_by)::text WHEN 'all' THEN '' WHEN 'time' THEN to_char(date_bin((sqlc.arg(bucket_seconds)::int::text || ' seconds')::interval,c.created_at,'2000-01-01 00:00:00+00'::timestamptz) AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') WHEN 'model' THEN coalesce(c.model_id,c.requested_model) WHEN 'channel' THEN coalesce(c.final_channel_id::text,'') WHEN 'format' THEN c.format WHEN 'stream' THEN c.stream::text WHEN 'key' THEN coalesce(c.api_key_id::text,'') WHEN 'tag' THEN coalesce(c.tag,'') WHEN 'account' THEN c.account_id::text WHEN 'outcome' THEN c.outcome WHEN 'error' THEN coalesce(c.attempts->-1->>'end_reason','none') WHEN 'error_code' THEN coalesce(c.attempts->-1->>'error_code','none') WHEN 'status' THEN coalesce(c.attempts->-1->>'status_code','none') WHEN 'stop' THEN coalesce(c.diagnostics->>'stop_reason','') WHEN 'service_tier' THEN coalesce(c.price_snapshot->'detail'->>'service_tier','') ELSE '' END AS split_key,
 coalesce(ch.name,'') AS channel_name,coalesce(k.name,'') AS key_name,a.display_name AS account_name,
 CASE WHEN c.ledger_tx_id IS NULL THEN 0 ELSE c.cost_nano+c.fee_nano END AS charged,
 (SELECT coalesce(sum((t.value->>'duration_ms')::int),0) FROM jsonb_array_elements(c.attempts) WITH ORDINALITY t(value,position) WHERE t.position<jsonb_array_length(c.attempts)) AS fallback_ms,
 jsonb_array_length(c.attempts) AS attempt_count,
 (SELECT count(*) FROM jsonb_array_elements(c.attempts) t WHERE t->>'end_reason'='completed') AS attempt_success,
 (SELECT coalesce(sum((t->>'duration_ms')::int),0) FROM jsonb_array_elements(c.attempts) t) AS upstream_ms
 FROM calls c
 LEFT JOIN channels ch ON ch.id=c.final_channel_id
 LEFT JOIN api_keys k ON k.id=c.api_key_id
 JOIN accounts a ON a.id=c.account_id
 WHERE (sqlc.narg(account_id)::uuid IS NULL OR c.account_id = sqlc.narg(account_id)::uuid)
  AND (sqlc.narg(scope_channel_id)::uuid IS NULL
       OR c.final_channel_id = sqlc.narg(scope_channel_id)::uuid
       OR c.attempts @> jsonb_build_array(jsonb_build_object('channel', jsonb_build_object('id', sqlc.narg(scope_channel_text)::text))))
  AND (sqlc.narg(final_channel_id)::uuid IS NULL OR c.final_channel_id = sqlc.narg(final_channel_id)::uuid)
  AND (sqlc.narg(api_key_id)::uuid IS NULL OR c.api_key_id = sqlc.narg(api_key_id)::uuid)
  AND (sqlc.narg(model)::text IS NULL OR c.model_id = sqlc.narg(model)::text OR c.requested_model = sqlc.narg(model)::text)
  AND (sqlc.narg(format)::text IS NULL OR c.format = sqlc.narg(format)::text)
  AND (sqlc.narg(outcome)::text IS NULL OR c.outcome = sqlc.narg(outcome)::text)
  AND (sqlc.narg(tag)::text IS NULL OR c.tag = sqlc.narg(tag)::text)
  AND (sqlc.narg(request_id)::uuid IS NULL OR c.id = sqlc.narg(request_id)::uuid)
  AND (sqlc.arg(dimension)::text = '' OR (CASE sqlc.arg(dimension)::text WHEN 'all' THEN '' WHEN 'model' THEN coalesce(c.model_id,c.requested_model) WHEN 'channel' THEN coalesce(c.final_channel_id::text,'') WHEN 'format' THEN c.format WHEN 'stream' THEN c.stream::text WHEN 'key' THEN coalesce(c.api_key_id::text,'') WHEN 'tag' THEN coalesce(c.tag,'') WHEN 'account' THEN c.account_id::text WHEN 'outcome' THEN c.outcome WHEN 'error' THEN coalesce(c.attempts->-1->>'end_reason','none') WHEN 'error_code' THEN coalesce(c.attempts->-1->>'error_code','none') WHEN 'status' THEN coalesce(c.attempts->-1->>'status_code','none') WHEN 'stop' THEN coalesce(c.diagnostics->>'stop_reason','') WHEN 'service_tier' THEN coalesce(c.price_snapshot->'detail'->>'service_tier','') ELSE '' END) = sqlc.arg(dimension_value)::text)
  AND (sqlc.arg(second_dimension)::text = '' OR (CASE sqlc.arg(second_dimension)::text WHEN 'all' THEN '' WHEN 'model' THEN coalesce(c.model_id,c.requested_model) WHEN 'channel' THEN coalesce(c.final_channel_id::text,'') WHEN 'format' THEN c.format WHEN 'stream' THEN c.stream::text WHEN 'key' THEN coalesce(c.api_key_id::text,'') WHEN 'tag' THEN coalesce(c.tag,'') WHEN 'account' THEN c.account_id::text WHEN 'outcome' THEN c.outcome WHEN 'error' THEN coalesce(c.attempts->-1->>'end_reason','none') WHEN 'error_code' THEN coalesce(c.attempts->-1->>'error_code','none') WHEN 'status' THEN coalesce(c.attempts->-1->>'status_code','none') WHEN 'stop' THEN coalesce(c.diagnostics->>'stop_reason','') WHEN 'service_tier' THEN coalesce(c.price_snapshot->'detail'->>'service_tier','') ELSE '' END) = sqlc.arg(second_value)::text)
  AND (sqlc.narg(stream)::boolean IS NULL OR c.stream=sqlc.narg(stream)::boolean)
  AND (sqlc.narg(min_attempts)::int IS NULL OR jsonb_array_length(c.attempts)>=sqlc.narg(min_attempts)::int)
  AND (sqlc.narg(max_attempts)::int IS NULL OR jsonb_array_length(c.attempts)<=sqlc.narg(max_attempts)::int)
  AND (sqlc.narg(billed)::boolean IS NULL OR (c.ledger_tx_id IS NOT NULL)=sqlc.narg(billed)::boolean)
  AND (sqlc.narg(delivery)::text IS NULL OR c.diagnostics->>'delivery'=sqlc.narg(delivery)::text)
  AND (sqlc.narg(usage_status)::text IS NULL OR c.diagnostics->>'usage_status'=sqlc.narg(usage_status)::text)
  AND ((sqlc.narg(status_code)::int IS NULL AND sqlc.narg(error_code)::text IS NULL AND sqlc.narg(end_reason)::text IS NULL AND sqlc.narg(attempt_channel)::text IS NULL)
    OR EXISTS (SELECT 1 FROM jsonb_array_elements(c.attempts) att
      WHERE (sqlc.narg(status_code)::int IS NULL OR (att->>'status_code')::int=sqlc.narg(status_code)::int)
      AND (sqlc.narg(error_code)::text IS NULL OR att->>'error_code'=sqlc.narg(error_code)::text)
      AND (sqlc.narg(end_reason)::text IS NULL OR att->>'end_reason'=sqlc.narg(end_reason)::text)
      AND (sqlc.narg(attempt_channel)::text IS NULL OR att->'channel'->>'id'=sqlc.narg(attempt_channel)::text)
      AND (sqlc.narg(scope_channel_text)::text IS NULL OR att->'channel'->>'id'=sqlc.narg(scope_channel_text)::text)))
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR c.created_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR c.created_at < sqlc.narg(to_time)::timestamptz)
  AND (sqlc.narg(min_duration)::int IS NULL OR c.duration_ms >= sqlc.narg(min_duration)::int)
  AND (sqlc.narg(max_duration)::int IS NULL OR c.duration_ms <= sqlc.narg(max_duration)::int)
  AND (sqlc.narg(min_tokens)::bigint IS NULL OR c.input_tokens + c.output_tokens + c.cache_write_tokens + c.cache_read_tokens >= sqlc.narg(min_tokens)::bigint)
  AND (sqlc.narg(max_tokens)::bigint IS NULL OR c.input_tokens + c.output_tokens + c.cache_write_tokens + c.cache_read_tokens <= sqlc.narg(max_tokens)::bigint)
  AND (sqlc.narg(min_charged)::bigint IS NULL OR (CASE WHEN c.ledger_tx_id IS NULL THEN 0 ELSE c.cost_nano + c.fee_nano END) >= sqlc.narg(min_charged)::bigint)
  AND (sqlc.narg(max_charged)::bigint IS NULL OR (CASE WHEN c.ledger_tx_id IS NULL THEN 0 ELSE c.cost_nano + c.fee_nano END) <= sqlc.narg(max_charged)::bigint)
)
SELECT group_key,split_key,
 CASE sqlc.arg(group_by)::text WHEN 'channel' THEN max(channel_name) WHEN 'key' THEN max(key_name) WHEN 'account' THEN max(account_name) ELSE group_key END::text AS label,
 CASE sqlc.arg(split_by)::text WHEN 'channel' THEN max(channel_name) WHEN 'key' THEN max(key_name) WHEN 'account' THEN max(account_name) ELSE split_key END::text AS split_label,
 (count(*) FILTER (WHERE attempt_count>0 AND outcome<>'in_progress'))::bigint AS attempted,
 (count(*) FILTER (WHERE stream AND outcome<>'in_progress'))::bigint AS stream_finished,
 (count(*) FILTER (WHERE stream AND outcome IN ('interrupted','client_disconnected')))::bigint AS stream_interrupted,
 (coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY fallback_ms) FILTER (WHERE attempt_count>1),-1))::double precision AS fallback_p50,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY fallback_ms) FILTER (WHERE attempt_count>1),-1))::double precision AS fallback_p95,
 (count(*))::bigint AS calls,
 (count(*) FILTER (WHERE outcome<>'in_progress'))::bigint AS finished,
 (count(*) FILTER (WHERE outcome IN ('succeeded','succeeded_unbilled')))::bigint AS succeeded,
 (count(*) FILTER (WHERE attempts->0->>'end_reason'='completed'))::bigint AS first_succeeded,
 (count(*) FILTER (WHERE jsonb_array_length(attempts)>1))::bigint AS retried,
 (count(*) FILTER (WHERE jsonb_array_length(attempts)>1 AND outcome IN ('succeeded','succeeded_unbilled')))::bigint AS recovered,
 (coalesce(sum(attempt_count),0))::bigint AS attempts,
 (coalesce(sum(attempt_success),0))::bigint AS attempt_succeeded,
 (count(*) FILTER (WHERE diagnostics->>'delivery'='complete'))::bigint AS delivered,
 (count(*) FILTER (WHERE diagnostics->>'delivery' IN ('complete','interrupted','client_disconnected')))::bigint AS delivery_known,
 (count(*) FILTER (WHERE ledger_tx_id IS NOT NULL))::bigint AS billed,
 (count(*) FILTER (WHERE diagnostics->>'usage_status'='reported'))::bigint AS usage_reported,
 (count(*) FILTER (WHERE diagnostics->>'usage_status' IN ('missing','parse_limit')))::bigint AS usage_missing,
 (count(*) FILTER (WHERE stream))::bigint AS streamed,
 (coalesce(sum(input_tokens),0))::bigint AS input_tokens,
 (coalesce(sum(output_tokens),0))::bigint AS output_tokens,
 (coalesce(sum(cache_read_tokens),0))::bigint AS cache_read_tokens,
 (coalesce(sum(cache_write_tokens),0))::bigint AS cache_write_tokens,
 (coalesce(sum(charged),0))::bigint AS charged_nano,
 (coalesce(sum(CASE WHEN ledger_tx_id IS NOT NULL THEN fee_nano ELSE 0 END),0))::bigint AS fee_nano,
 (count(*) FILTER (WHERE outcome='interrupted'))::bigint AS interrupted,
 (count(*) FILTER (WHERE outcome='client_disconnected'))::bigint AS disconnected,
 (count(*) FILTER (WHERE outcome='succeeded_unbilled'))::bigint AS unbilled,
 (coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY ttft_ms) FILTER (WHERE stream),-1))::double precision AS ttft_p50,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY ttft_ms) FILTER (WHERE stream),-1))::double precision AS ttft_p95,
 (coalesce(percentile_cont(0.99) WITHIN GROUP (ORDER BY ttft_ms) FILTER (WHERE stream),-1))::double precision AS ttft_p99,
 (coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE true),-1))::double precision AS duration_p50,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE true),-1))::double precision AS duration_p95,
 (coalesce(percentile_cont(0.99) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE true),-1))::double precision AS duration_p99,
 (coalesce(percentile_cont(0.1) WITHIN GROUP (ORDER BY output_tokens_per_second) FILTER (WHERE stream),-1))::double precision AS speed_p10,
 (coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY output_tokens_per_second) FILTER (WHERE stream),-1))::double precision AS speed_p50,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY output_tokens_per_second) FILTER (WHERE stream),-1))::double precision AS speed_p95,
 (coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY inter_token_p95_ms) FILTER (WHERE stream),-1))::double precision AS gap_p50,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY inter_token_p95_ms) FILTER (WHERE stream),-1))::double precision AS gap_p95,
 (coalesce(percentile_cont(0.99) WITHIN GROUP (ORDER BY inter_token_p95_ms) FILTER (WHERE stream),-1))::double precision AS gap_p99,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY (diagnostics->>'max_frame_gap_ms')::double precision) FILTER (WHERE stream),-1))::double precision AS max_gap_p95,
 (coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY charged/1000000000.0) FILTER (WHERE outcome<>'in_progress'),-1))::double precision AS cost_p50,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY charged/1000000000.0) FILTER (WHERE outcome<>'in_progress'),-1))::double precision AS cost_p95,
 (coalesce(percentile_cont(0.99) WITHIN GROUP (ORDER BY charged/1000000000.0) FILTER (WHERE outcome<>'in_progress'),-1))::double precision AS cost_p99,
 (coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY jsonb_array_length(attempts)-1) FILTER (WHERE jsonb_array_length(attempts)>0),-1))::double precision AS retry_p50,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY jsonb_array_length(attempts)-1) FILTER (WHERE jsonb_array_length(attempts)>0),-1))::double precision AS retry_p95,
 (coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY greatest(0,duration_ms-upstream_ms)) FILTER (WHERE duration_ms IS NOT NULL),-1))::double precision AS overhead_p50,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY greatest(0,duration_ms-upstream_ms)) FILTER (WHERE duration_ms IS NOT NULL),-1))::double precision AS overhead_p95,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY (SELECT (s->>'duration_ms')::double precision FROM jsonb_array_elements(coalesce(diagnostics->'stages','[]'::jsonb)) s WHERE s->>'stage'='authentication' LIMIT 1)),-1))::double precision AS authentication_p95,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY (SELECT (s->>'duration_ms')::double precision FROM jsonb_array_elements(coalesce(diagnostics->'stages','[]'::jsonb)) s WHERE s->>'stage'='request' LIMIT 1)),-1))::double precision AS request_p95,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY (SELECT (s->>'duration_ms')::double precision FROM jsonb_array_elements(coalesce(diagnostics->'stages','[]'::jsonb)) s WHERE s->>'stage'='open_call' LIMIT 1)),-1))::double precision AS open_call_p95,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY (SELECT (s->>'duration_ms')::double precision FROM jsonb_array_elements(coalesce(diagnostics->'stages','[]'::jsonb)) s WHERE s->>'stage'='admission' LIMIT 1)),-1))::double precision AS admission_p95,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY (SELECT (s->>'duration_ms')::double precision FROM jsonb_array_elements(coalesce(diagnostics->'stages','[]'::jsonb)) s WHERE s->>'stage'='routing' LIMIT 1)),-1))::double precision AS routing_p95,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY (SELECT (s->>'duration_ms')::double precision FROM jsonb_array_elements(coalesce(diagnostics->'stages','[]'::jsonb)) s WHERE s->>'stage'='pricing' LIMIT 1)),-1))::double precision AS pricing_p95,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY (SELECT (s->>'duration_ms')::double precision FROM jsonb_array_elements(coalesce(diagnostics->'stages','[]'::jsonb)) s WHERE s->>'stage'='settlement' LIMIT 1)),-1))::double precision AS settlement_p95,
 (coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY (SELECT (s->>'duration_ms')::double precision FROM jsonb_array_elements(coalesce(diagnostics->'stages','[]'::jsonb)) s WHERE s->>'stage'='client_write' LIMIT 1)),-1))::double precision AS client_write_p95
FROM filtered
GROUP BY group_key,split_key
ORDER BY group_key,split_key
LIMIT 1001;
