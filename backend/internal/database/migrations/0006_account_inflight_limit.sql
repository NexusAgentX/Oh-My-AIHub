-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- 每个账号同时在途的网关调用数上限（ADR-0035 规则 2），超过时以 429 拒绝；0 表示不限。
-- 在途计数只在网关进程内，不写数据库、不预留余额。
ALTER TABLE settings
    ADD COLUMN account_max_inflight_calls integer NOT NULL DEFAULT 20
        CHECK (account_max_inflight_calls BETWEEN 0 AND 1000);

-- 被在途上限拒绝的调用记为 rejected_inflight，与余额、预算拒绝一样写一行 calls。
ALTER TABLE calls DROP CONSTRAINT calls_outcome_check;
ALTER TABLE calls ADD CONSTRAINT calls_outcome_check CHECK (outcome IN (
    'rejected_balance', 'rejected_budget', 'rejected_key', 'rejected_model', 'rejected_format',
    'rejected_no_channel', 'rejected_inflight', 'upstream_failed', 'interrupted', 'client_disconnected',
    'succeeded', 'succeeded_unbilled', 'in_progress'
));

-- +goose Down
DELETE FROM calls WHERE outcome = 'rejected_inflight';
ALTER TABLE calls DROP CONSTRAINT calls_outcome_check;
ALTER TABLE calls ADD CONSTRAINT calls_outcome_check CHECK (outcome IN (
    'rejected_balance', 'rejected_budget', 'rejected_key', 'rejected_model', 'rejected_format',
    'rejected_no_channel', 'upstream_failed', 'interrupted', 'client_disconnected',
    'succeeded', 'succeeded_unbilled', 'in_progress'
));
ALTER TABLE settings DROP COLUMN account_max_inflight_calls;
