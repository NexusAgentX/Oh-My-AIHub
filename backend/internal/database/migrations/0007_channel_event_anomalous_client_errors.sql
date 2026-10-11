-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- 渠道在某个模型上几乎每次都返回 4xx、而同模型其他渠道没有这个问题时，
-- 网关把它排到最后并记一条 anomalous_client_errors 渠道事件（原因里写模型与次数）。
ALTER TABLE channel_events DROP CONSTRAINT channel_events_kind_check;
ALTER TABLE channel_events ADD CONSTRAINT channel_events_kind_check CHECK (kind IN (
    'cooldown_started', 'cooldown_ended', 'limit_reached', 'suspended',
    'unsuspended', 'listed', 'unlisted', 'test_failed', 'model_pending_multiplier',
    'anomalous_client_errors'
));

-- +goose Down
DELETE FROM channel_events WHERE kind = 'anomalous_client_errors';
ALTER TABLE channel_events DROP CONSTRAINT channel_events_kind_check;
ALTER TABLE channel_events ADD CONSTRAINT channel_events_kind_check CHECK (kind IN (
    'cooldown_started', 'cooldown_ended', 'limit_reached', 'suspended',
    'unsuspended', 'listed', 'unlisted', 'test_failed', 'model_pending_multiplier'
));
