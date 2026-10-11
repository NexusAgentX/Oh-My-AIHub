-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- 美元模型与汇率：积分按 1 积分 ≈ 1 元人民币宣传。人民币模型的基准价就是积分价；
-- 美元模型的基准价是美元，消费时乘以 settings.usd_cny_rate_nano 换算为积分。
ALTER TABLE models
    ADD COLUMN price_currency text NOT NULL DEFAULT 'CNY' CHECK (price_currency IN ('CNY', 'USD'));

-- 渠道的「汇率优惠」：只对美元模型生效，按「美元价 × 倍率」直接扣积分（不乘汇率），
-- 对应上游「1 元充 1 美元额度」的中转站。
ALTER TABLE channel_models
    ADD COLUMN fx_discount boolean NOT NULL DEFAULT false;

-- 美元兑人民币汇率（纳单位，1e9 = 1）。usd_cny_rate_source 为 manual（管理员填写）或 auto（自动同步）；
-- 开启自动同步时每天在 fx_sync_minute（Asia/Shanghai 当日分钟）之后同步一次。
ALTER TABLE settings
    ADD COLUMN usd_cny_rate_nano bigint NOT NULL DEFAULT 6800000000
        CHECK (usd_cny_rate_nano BETWEEN 10000000 AND 100000000000),
    ADD COLUMN usd_cny_rate_source text NOT NULL DEFAULT 'manual' CHECK (usd_cny_rate_source IN ('manual', 'auto')),
    ADD COLUMN usd_cny_rate_updated_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN fx_auto_sync boolean NOT NULL DEFAULT false,
    ADD COLUMN fx_sync_minute integer NOT NULL DEFAULT 570 CHECK (fx_sync_minute BETWEEN 0 AND 1439),
    ADD COLUMN fx_sync_attempted_at timestamptz,
    ADD COLUMN fx_sync_error text;

-- +goose Down
ALTER TABLE settings
    DROP COLUMN fx_sync_error,
    DROP COLUMN fx_sync_attempted_at,
    DROP COLUMN fx_sync_minute,
    DROP COLUMN fx_auto_sync,
    DROP COLUMN usd_cny_rate_updated_at,
    DROP COLUMN usd_cny_rate_source,
    DROP COLUMN usd_cny_rate_nano;
ALTER TABLE channel_models DROP COLUMN fx_discount;
ALTER TABLE models DROP COLUMN price_currency;
