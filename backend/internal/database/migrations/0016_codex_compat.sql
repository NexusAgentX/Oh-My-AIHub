-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
ALTER TABLE settings ADD COLUMN codex jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(codex) = 'object');
ALTER TABLE channels ADD COLUMN capabilities text[] NOT NULL DEFAULT '{}' CHECK (capabilities <@ ARRAY['codex_backend','codex_search']::text[]);
ALTER TABLE channel_models ADD COLUMN capabilities text[] NOT NULL DEFAULT '{}' CHECK (capabilities <@ ARRAY['web_search','image_generation','responses_lite','compact_v1','compact_v2','priority','ultrafast']::text[]);
