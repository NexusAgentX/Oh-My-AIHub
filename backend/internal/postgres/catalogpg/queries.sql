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
