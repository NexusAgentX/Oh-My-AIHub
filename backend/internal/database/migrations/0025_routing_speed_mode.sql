-- +goose Up
CREATE INDEX calls_model_metric_completed ON calls(completed_at DESC,id DESC)
 WHERE completed_at IS NOT NULL AND attempts @> '[{"metrics_version":2}]'::jsonb;
-- Preserve the meaning of all historical mode values. fastest remains latency;
-- speed is the new reliable-output-rate mode. No call or preference is rewritten.
ALTER TABLE route_prefs DROP CONSTRAINT route_prefs_mode_check;
ALTER TABLE route_prefs ADD CONSTRAINT route_prefs_mode_check
 CHECK(mode IN ('cheapest','reliable','fastest','speed','manual'));
ALTER TABLE calls DROP CONSTRAINT calls_routing_mode_check;
ALTER TABLE calls ADD CONSTRAINT calls_routing_mode_check
 CHECK(routing_mode IS NULL OR routing_mode IN ('cheapest','reliable','fastest','speed','manual'));

-- +goose Down
DROP INDEX calls_model_metric_completed;
-- Refuse to erase/rename real preferences or historical call semantics.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM route_prefs WHERE mode='speed')
 OR EXISTS(SELECT 1 FROM calls WHERE routing_mode='speed') THEN
  RAISE EXCEPTION 'Cannot remove speed mode while preferences or historical calls use it';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE route_prefs DROP CONSTRAINT route_prefs_mode_check;
ALTER TABLE route_prefs ADD CONSTRAINT route_prefs_mode_check
 CHECK(mode IN ('cheapest','reliable','fastest','manual'));
ALTER TABLE calls DROP CONSTRAINT calls_routing_mode_check;
ALTER TABLE calls ADD CONSTRAINT calls_routing_mode_check
 CHECK(routing_mode IS NULL OR routing_mode IN ('cheapest','reliable','fastest','manual'));
