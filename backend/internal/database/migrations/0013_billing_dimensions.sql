-- +goose Up
ALTER TABLE models ADD COLUMN geo_multipliers jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(geo_multipliers) = 'object');
ALTER TABLE model_price_tiers ADD COLUMN geo_multipliers jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(geo_multipliers) = 'object');

-- +goose Down
ALTER TABLE model_price_tiers DROP COLUMN geo_multipliers;
ALTER TABLE models DROP COLUMN geo_multipliers;
