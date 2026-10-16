-- +goose Up
ALTER TABLE models ADD COLUMN unit_prices jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(unit_prices) = 'object');
ALTER TABLE models ADD COLUMN geo_multipliers jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(geo_multipliers) = 'object');
ALTER TABLE model_price_tiers ADD COLUMN unit_prices jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(unit_prices) = 'object');
ALTER TABLE model_price_tiers ADD COLUMN geo_multipliers jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(geo_multipliers) = 'object');

CREATE TABLE gateway_unit_receipts (
 channel_id uuid NOT NULL REFERENCES channels(id),
 unit text NOT NULL,
 reference_id text NOT NULL CHECK (length(reference_id) BETWEEN 1 AND 256),
 call_id uuid NOT NULL REFERENCES calls(id),
 PRIMARY KEY (channel_id, unit, reference_id)
);

-- +goose Down
DROP TABLE gateway_unit_receipts;
ALTER TABLE model_price_tiers DROP COLUMN geo_multipliers, DROP COLUMN unit_prices;
ALTER TABLE models DROP COLUMN geo_multipliers, DROP COLUMN unit_prices;
