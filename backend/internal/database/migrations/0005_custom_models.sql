-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- 渠道自定义模型（ADR-0034）：渠道可提供目录外的模型名并自定四个基准价（积分），
-- 不乘倍率、不乘汇率、无条件价格档。同名自定义模型按名称聚合路由。
-- 官方行仍通过生成列 catalog_model_id 引用 models(id)，删除仍被引用的模型照旧失败。
ALTER TABLE channel_models DROP CONSTRAINT channel_models_model_id_fkey;
ALTER TABLE channel_models
    ADD COLUMN is_custom boolean NOT NULL DEFAULT false,
    ADD COLUMN custom_input_price_nano_per_million bigint,
    ADD COLUMN custom_output_price_nano_per_million bigint,
    ADD COLUMN custom_cache_write_price_nano_per_million bigint,
    ADD COLUMN custom_cache_read_price_nano_per_million bigint,
    -- 自定义模型被管理员加入官方目录后转为官方行并停用，渠道主重新设置倍率后才恢复。
    ADD COLUMN pending_multiplier boolean NOT NULL DEFAULT false,
    ADD COLUMN catalog_model_id text GENERATED ALWAYS AS (CASE WHEN is_custom THEN NULL ELSE model_id END) STORED
        REFERENCES models(id),
    ADD CONSTRAINT channel_models_custom_check CHECK (
        (is_custom
            AND model_id ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'
            AND custom_input_price_nano_per_million BETWEEN 0 AND 100000000000000
            AND custom_output_price_nano_per_million BETWEEN 0 AND 100000000000000
            AND custom_cache_write_price_nano_per_million BETWEEN 0 AND 100000000000000
            AND custom_cache_read_price_nano_per_million BETWEEN 0 AND 100000000000000
            AND multiplier_nano = 1000000000 AND NOT fx_discount AND NOT pending_multiplier)
        OR (NOT is_custom
            AND custom_input_price_nano_per_million IS NULL
            AND custom_output_price_nano_per_million IS NULL
            AND custom_cache_write_price_nano_per_million IS NULL
            AND custom_cache_read_price_nano_per_million IS NULL)
    ),
    ADD CONSTRAINT channel_models_pending_check CHECK (NOT pending_multiplier OR NOT enabled);

CREATE INDEX channel_models_custom_idx ON channel_models(model_id) WHERE is_custom;

-- 路由偏好按模型名保存，自定义模型也可设置。
ALTER TABLE route_prefs DROP CONSTRAINT route_prefs_model_id_fkey;

ALTER TABLE channel_events DROP CONSTRAINT channel_events_kind_check;
ALTER TABLE channel_events ADD CONSTRAINT channel_events_kind_check CHECK (kind IN (
    'cooldown_started', 'cooldown_ended', 'limit_reached', 'suspended',
    'unsuspended', 'listed', 'unlisted', 'test_failed', 'model_pending_multiplier'
));

-- +goose Down
DELETE FROM channel_events WHERE kind = 'model_pending_multiplier';
ALTER TABLE channel_events DROP CONSTRAINT channel_events_kind_check;
ALTER TABLE channel_events ADD CONSTRAINT channel_events_kind_check CHECK (kind IN (
    'cooldown_started', 'cooldown_ended', 'limit_reached', 'suspended',
    'unsuspended', 'listed', 'unlisted', 'test_failed'
));
DELETE FROM route_prefs rp WHERE NOT EXISTS (SELECT 1 FROM models m WHERE m.id = rp.model_id);
ALTER TABLE route_prefs ADD CONSTRAINT route_prefs_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id);
DELETE FROM channel_models WHERE is_custom;
DROP INDEX channel_models_custom_idx;
ALTER TABLE channel_models
    DROP CONSTRAINT channel_models_pending_check,
    DROP CONSTRAINT channel_models_custom_check,
    DROP COLUMN catalog_model_id,
    DROP COLUMN pending_multiplier,
    DROP COLUMN custom_cache_read_price_nano_per_million,
    DROP COLUMN custom_cache_write_price_nano_per_million,
    DROP COLUMN custom_output_price_nano_per_million,
    DROP COLUMN custom_input_price_nano_per_million,
    DROP COLUMN is_custom;
ALTER TABLE channel_models ADD CONSTRAINT channel_models_model_id_fkey FOREIGN KEY (model_id) REFERENCES models(id);
