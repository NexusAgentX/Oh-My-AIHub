-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
ALTER TABLE channels DROP CONSTRAINT channels_capabilities_check;
ALTER TABLE channel_models DROP CONSTRAINT channel_models_capabilities_check;
-- Existing choices are explicitly reset at the maintainer's request.
DELETE FROM route_pref_channels;
UPDATE route_prefs SET mode = 'manual', updated_at = now();
ALTER TABLE route_prefs ALTER COLUMN mode SET DEFAULT 'manual';
ALTER TABLE route_prefs DROP CONSTRAINT route_prefs_mode_check;
ALTER TABLE route_prefs ADD CONSTRAINT route_prefs_mode_check CHECK (mode = 'manual');

UPDATE models SET input_modalities = array_remove(input_modalities, 'file'),
 output_modalities = array_remove(output_modalities, 'file'), geo_multipliers = '{}';
UPDATE models SET input_modalities = ARRAY['text'] WHERE cardinality(input_modalities) = 0;
UPDATE models SET output_modalities = ARRAY['text'] WHERE cardinality(output_modalities) = 0;
-- Removed predicates must not become unconditional prices.
DELETE FROM model_price_tiers WHERE thinking_mode <> '' OR service_tier LIKE 'anthropic:%' OR service_tier LIKE 'gemini:%' OR service_tier NOT IN ('','openai:default','openai:priority','openai:fast','openai:ultrafast','openai:flex');
UPDATE model_price_tiers SET geo_multipliers = '{}', service_tier = CASE WHEN service_tier='openai:priority' THEN 'openai:fast' ELSE service_tier END;

ALTER TABLE models ADD CONSTRAINT models_geo_disabled CHECK (geo_multipliers = '{}'::jsonb);
ALTER TABLE model_price_tiers ADD CONSTRAINT price_tiers_geo_disabled CHECK (geo_multipliers = '{}'::jsonb);
ALTER TABLE model_price_tiers ADD CONSTRAINT price_tiers_thinking_disabled CHECK (thinking_mode = '');
ALTER TABLE model_price_tiers ADD CONSTRAINT price_tiers_service_tier_supported CHECK (service_tier IN ('','openai:default','openai:fast','openai:ultrafast','openai:flex'));
ALTER TABLE models ADD CONSTRAINT models_modalities_supported CHECK (
 NOT ('file'=ANY(input_modalities)) AND NOT ('file'=ANY(output_modalities)));

-- A previous model-level Responses declaration establishes channel-level HTTP
-- support only. New optional feature cells remain unsupported until tested or
-- explicitly edited. Existing explicitly declared model capabilities migrate.
UPDATE channels c SET capabilities =
 CASE WHEN EXISTS (SELECT 1 FROM channel_models cm WHERE cm.channel_id=c.id AND 'openai_responses'=ANY(cm.formats))
 THEN ARRAY['responses/inference','responses/nonstream','responses/stream']::text[] ELSE ARRAY[]::text[] END
 || CASE WHEN 'codex_backend'=ANY(c.capabilities) THEN ARRAY['codex/inference','codex/nonstream','codex/stream']::text[] ELSE ARRAY[]::text[] END
 || CASE WHEN 'codex_search'=ANY(c.capabilities) THEN ARRAY['codex/search']::text[] ELSE ARRAY[]::text[] END;
UPDATE channel_models cm SET capabilities = ARRAY(
 SELECT DISTINCT protocol || '/' || CASE WHEN feature='priority' THEN 'fast' ELSE feature END
 FROM unnest(cm.capabilities) feature CROSS JOIN unnest(ARRAY['responses','codex']) protocol
 WHERE (protocol='responses' AND 'openai_responses'=ANY(cm.formats))
 OR (protocol='codex' AND EXISTS (SELECT 1 FROM channels c WHERE c.id=cm.channel_id AND 'codex/inference'=ANY(c.capabilities)))
);
-- Old format probes do not establish the new feature-level contract.
UPDATE channel_models SET format_tests = '{}';
-- Protocol support is no longer independently configurable per model.
ALTER TABLE channel_models DROP COLUMN formats;

ALTER TABLE channels ADD CONSTRAINT channels_capabilities_check CHECK (capabilities <@ ARRAY['responses/inference','responses/nonstream','responses/stream','responses/background','responses/response_read','responses/response_delete','responses/response_cancel','responses/input_items','responses/conversations','responses/input_tokens','responses/compact','responses/zstd','codex/inference','codex/nonstream','codex/stream','codex/background','codex/response_read','codex/response_delete','codex/response_cancel','codex/input_items','codex/conversations','codex/input_tokens','codex/compact','codex/zstd','codex/search','codex/images_generate','codex/images_edit']::text[]);

ALTER TABLE channel_models ADD CONSTRAINT channel_models_capabilities_check CHECK (capabilities <@ ARRAY['responses/function_tools','responses/custom_tools','responses/tool_namespaces','responses/tool_search','responses/parallel_tools','responses/structured_output','responses/reasoning','responses/encrypted_reasoning','responses/reasoning_all_turns','responses/vision','responses/web_search','responses/image_generation','responses/responses_lite','responses/compact_v1','responses/compact_v2','responses/fast','responses/ultrafast','codex/function_tools','codex/custom_tools','codex/tool_namespaces','codex/tool_search','codex/parallel_tools','codex/structured_output','codex/reasoning','codex/encrypted_reasoning','codex/reasoning_all_turns','codex/vision','codex/web_search','codex/image_generation','codex/responses_lite','codex/compact_v1','codex/compact_v2','codex/fast','codex/ultrafast','responses/compaction_input','responses/file_search','responses/code_interpreter','responses/mcp','responses/computer','responses/shell','responses/apply_patch','codex/compaction_input','codex/file_search','codex/code_interpreter','codex/mcp','codex/computer','codex/shell','codex/apply_patch']::text[]);

-- +goose Down
-- This removes configuration and cannot recover previous selections or prices.
SELECT 1;
