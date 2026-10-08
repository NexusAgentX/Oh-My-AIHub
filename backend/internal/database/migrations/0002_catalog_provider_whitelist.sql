-- +goose Up
ALTER TABLE catalog_sync
 ADD COLUMN providers text[] NOT NULL DEFAULT '{}',
 ADD COLUMN providers_configured boolean NOT NULL DEFAULT false,
 ADD COLUMN config_version bigint NOT NULL DEFAULT 0,
 ADD COLUMN available_providers text[] NOT NULL DEFAULT '{}',
 ADD COLUMN report jsonb NOT NULL DEFAULT '[]';

-- A retained legacy ID and its replacement may refer to the same external key.
ALTER TABLE model_sources DROP CONSTRAINT model_sources_pkey;
ALTER TABLE model_sources DROP CONSTRAINT model_sources_model_id_key;
ALTER TABLE model_sources ADD PRIMARY KEY(model_id);
CREATE INDEX model_sources_source_key_idx ON model_sources(source_key);
CREATE TABLE catalog_deleted_models(model_id text PRIMARY KEY);
-- Conflicts in v0.9 were ignored too, but are not user deletion tombstones.
INSERT INTO catalog_deleted_models(model_id)
 SELECT DISTINCT regexp_replace(source_key, '^.*/', '') FROM model_sources
 WHERE ignored AND COALESCE(info->>'status','') <> 'conflict'
 ON CONFLICT DO NOTHING;
ALTER TABLE models DROP CONSTRAINT models_id_check;
ALTER TABLE models ADD CONSTRAINT models_id_check CHECK(id ~ '^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$');

-- +goose Down
-- Refuse a destructive rollback if new identifiers or duplicated source keys exist.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM models WHERE id ~ '[:@]') OR
    EXISTS(SELECT 1 FROM model_sources GROUP BY source_key HAVING count(*) > 1) THEN
  RAISE EXCEPTION 'Cannot downgrade catalog identity while new IDs or retained source copies exist';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE models DROP CONSTRAINT models_id_check;
ALTER TABLE models ADD CONSTRAINT models_id_check CHECK(id ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$');
DROP TABLE catalog_deleted_models;
DROP INDEX model_sources_source_key_idx;
ALTER TABLE model_sources DROP CONSTRAINT model_sources_pkey;
ALTER TABLE model_sources ADD PRIMARY KEY(source_key);
ALTER TABLE model_sources ADD UNIQUE(model_id);
ALTER TABLE catalog_sync DROP COLUMN providers, DROP COLUMN providers_configured, DROP COLUMN config_version,
 DROP COLUMN available_providers, DROP COLUMN report;
