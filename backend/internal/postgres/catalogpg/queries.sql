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
    supports_tools, supports_structured_output, supports_vision, parameter_info, token_prices, price_currency,
    max_output_tokens, unit_prices, geo_multipliers
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
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
    price_currency = $18,
    max_output_tokens = $19,
    unit_prices = $20, geo_multipliers = $21,
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
    cache_write_price_nano_per_million, cache_read_price_nano_per_million, token_prices, service_tier, thinking_mode, unit_prices, geo_multipliers
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18);

-- A currency switch resets the FX discount of every channel offering the model
-- to "on" for USD: a model whose figures were dollars all along keeps its prices.
-- name: SetChannelFXDiscount :exec
UPDATE channel_models SET fx_discount = $2 WHERE model_id = $1;

-- Same lock as channelpg.LockModelName: a name is never both custom and official.
-- name: LockModelName :exec
SELECT pg_advisory_xact_lock(hashtextextended('model_name:' || sqlc.arg(model_id)::text, 0));

-- A new catalog model takes over the channels' custom models of the same name:
-- they become disabled official rows waiting for the owner's multiplier (ADR-0034).
-- name: ConvertCustomModels :many
UPDATE channel_models SET
    is_custom = false,
    custom_input_price_nano_per_million = NULL,
    custom_output_price_nano_per_million = NULL,
    custom_cache_write_price_nano_per_million = NULL,
    custom_cache_read_price_nano_per_million = NULL,
    multiplier_nano = 1000000000,
    fx_discount = false,
    enabled = false,
    pending_multiplier = true
WHERE model_id = $1 AND is_custom
RETURNING channel_id;

-- name: InsertChannelEvent :exec
INSERT INTO channel_events (channel_id, kind, reason) VALUES ($1, $2, $3);
