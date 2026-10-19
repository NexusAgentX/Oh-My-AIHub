-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- Exclude new writes during the final check and cleanup. Operators must stop
-- admission and drain the old server before invoking this migration.
LOCK TABLE calls, gateway_background_jobs, channels, route_pref_channels,
 channel_models, model_price_tiers, models, supplier_endpoints, suppliers
 IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM calls WHERE outcome='in_progress')
  OR EXISTS(SELECT 1 FROM gateway_background_jobs WHERE settled_at IS NULL)
 THEN
  RAISE EXCEPTION 'finish existing calls and background responses before cutover' USING ERRCODE='55000';
 END IF;
 IF (EXISTS(SELECT 1 FROM channels WHERE market_group_id IS NULL)
  OR EXISTS(SELECT 1 FROM models) OR EXISTS(SELECT 1 FROM model_price_tiers)
  OR EXISTS(SELECT 1 FROM channel_models) OR EXISTS(SELECT 1 FROM suppliers)
  OR EXISTS(SELECT 1 FROM supplier_endpoints))
  AND current_setting('aihub.allow_legacy_config_cleanup',true) IS DISTINCT FROM 'on'
 THEN
  RAISE EXCEPTION 'explicit cutover environment authorization is required' USING ERRCODE='55000';
 END IF;
END $$;
-- +goose StatementEnd

-- Only old channel selections are configuration. Retain every channel ID,
-- owner, name, creation time and sealed credential for historical references.
DELETE FROM route_pref_channels r USING channels c
 WHERE r.channel_id=c.id AND c.market_group_id IS NULL;
UPDATE channels SET deleted_at=coalesce(deleted_at,transaction_timestamp()),
 status='unlisted',suspended_reason=NULL,updated_at=transaction_timestamp()
 WHERE market_group_id IS NULL;
-- Flush this existing deferred relationship trigger before changing columns.
-- Deleted historical rows carry no live group configuration to validate.
SET CONSTRAINTS market_channel_owner_links IMMEDIATE;

DROP TABLE channel_models;
DROP TABLE model_price_tiers;
DROP TABLE models;
DROP TABLE supplier_endpoints;
DROP TABLE suppliers;

ALTER TABLE channels
 DROP COLUMN base_url,
 DROP COLUMN user_agent,
 DROP COLUMN header_rules,
 DROP COLUMN concurrency_limit,
 DROP COLUMN rpm_limit,
 DROP COLUMN ttft_timeout_ms,
 DROP COLUMN request_timeout_ms,
 DROP COLUMN idle_timeout_ms,
 DROP COLUMN cooldown_failures,
 DROP COLUMN cooldown_seconds,
 ADD CONSTRAINT channels_active_group_check CHECK (deleted_at IS NOT NULL OR market_group_id IS NOT NULL);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'legacy configuration cleanup cannot be downgraded; use the documented forward repair boundary' USING ERRCODE='55000';
END $$;
-- +goose StatementEnd
