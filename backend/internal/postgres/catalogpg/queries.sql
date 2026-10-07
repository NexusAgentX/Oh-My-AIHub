-- name: ListModels :many
SELECT * FROM models
WHERE (sqlc.arg(include_disabled)::boolean OR status = 'active')
	AND (sqlc.arg(query)::text = '' OR id ILIKE '%' || sqlc.arg(query)::text || '%'
		OR name ILIKE '%' || sqlc.arg(query)::text || '%'
		OR provider ILIKE '%' || sqlc.arg(query)::text || '%')
ORDER BY provider, name, id;

-- name: GetModel :one
SELECT * FROM models
WHERE id = @id AND (sqlc.arg(include_disabled)::boolean OR status = 'active');

-- name: CreateModel :one
INSERT INTO models (
	id, name, provider, context_window, parameter_info,
	input_modalities, output_modalities, supports_tools,
	supports_structured_output, supports_vision,
	input_price_nano_per_million, output_price_nano_per_million,
	cache_write_price_nano_per_million, cache_read_price_nano_per_million,
	status
) VALUES (
	@id, @name, @provider, @context_window, @parameter_info,
	@input_modalities, @output_modalities, @supports_tools,
	@supports_structured_output, @supports_vision,
	@input_price_nano_per_million, @output_price_nano_per_million,
	@cache_write_price_nano_per_million, @cache_read_price_nano_per_million,
	@status
)
RETURNING *;

-- name: UpdateModel :one
UPDATE models SET
	name = @name,
	provider = @provider,
	context_window = @context_window,
	parameter_info = @parameter_info,
	input_modalities = @input_modalities,
	output_modalities = @output_modalities,
	supports_tools = @supports_tools,
	supports_structured_output = @supports_structured_output,
	supports_vision = @supports_vision,
	input_price_nano_per_million = @input_price_nano_per_million,
	output_price_nano_per_million = @output_price_nano_per_million,
	cache_write_price_nano_per_million = @cache_write_price_nano_per_million,
	cache_read_price_nano_per_million = @cache_read_price_nano_per_million,
	status = @status,
	version = version + 1,
	updated_at = now(),
	price_updated_at = CASE
		WHEN input_price_nano_per_million <> @input_price_nano_per_million
			OR output_price_nano_per_million <> @output_price_nano_per_million
			OR cache_write_price_nano_per_million <> @cache_write_price_nano_per_million
			OR cache_read_price_nano_per_million <> @cache_read_price_nano_per_million
			OR sqlc.arg(price_tiers_changed)::boolean
		THEN now()
		ELSE price_updated_at
	END
WHERE id = @id AND version = @expected_version
RETURNING *;

-- name: ModelExists :one
SELECT EXISTS (SELECT 1 FROM models WHERE id = @id);

-- name: ListModelPriceTiers :many
SELECT * FROM model_price_tiers
WHERE model_id = ANY(@model_ids::text[])
ORDER BY model_id, seq;

-- name: DeleteModelPriceTiers :exec
DELETE FROM model_price_tiers WHERE model_id = @model_id;

-- name: InsertModelPriceTier :exec
INSERT INTO model_price_tiers (
	model_id, seq, name, min_prompt_tokens, max_prompt_tokens, timezone, weekdays,
	start_minute_of_day, end_minute_of_day,
	input_price_nano_per_million, output_price_nano_per_million,
	cache_write_price_nano_per_million, cache_read_price_nano_per_million
) VALUES (
	@model_id, @seq, @name, @min_prompt_tokens, @max_prompt_tokens, @timezone, @weekdays,
	@start_minute_of_day, @end_minute_of_day,
	@input_price_nano_per_million, @output_price_nano_per_million,
	@cache_write_price_nano_per_million, @cache_read_price_nano_per_million
);
