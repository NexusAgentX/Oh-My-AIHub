-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- Pending background responses do not block the upgrade: the retired job
-- table is dropped with the abandoned remainder reported in 0023.

-- Removing a predicate must not broaden the affected price tier.
DELETE FROM model_price_tiers WHERE thinking_mode <> '';
UPDATE model_price_tiers SET service_tier = 'openai:fast' WHERE service_tier = 'openai:priority';
ALTER TABLE model_price_tiers DROP COLUMN thinking_mode, DROP COLUMN geo_multipliers;
ALTER TABLE model_price_tiers ADD CONSTRAINT model_price_tiers_no_priority CHECK (service_tier <> 'openai:priority');

UPDATE models SET input_modalities = array_remove(input_modalities, 'file'),
 output_modalities = array_remove(output_modalities, 'file');
UPDATE models SET input_modalities = ARRAY['text'] WHERE cardinality(input_modalities) = 0;
UPDATE models SET output_modalities = ARRAY['text'] WHERE cardinality(output_modalities) = 0;
ALTER TABLE models DROP COLUMN geo_multipliers;
ALTER TABLE models ADD CONSTRAINT models_no_file_modality CHECK (
 NOT ('file' = ANY(input_modalities)) AND NOT ('file' = ANY(output_modalities)));

-- +goose Down
-- Configuration was removed intentionally; restore the pre-upgrade backup.
-- +goose StatementBegin
DO $$
BEGIN
 RAISE EXCEPTION 'pricing cleanup is irreversible; restore the pre-upgrade backup';
END $$;
-- +goose StatementEnd
