-- +goose Up
-- Preserve each existing combined timeout in both independent stages.
ALTER TABLE settings DROP CONSTRAINT settings_check;
ALTER TABLE settings RENAME COLUMN default_total_timeout_ms TO default_request_timeout_ms;
ALTER TABLE settings ADD COLUMN default_idle_timeout_ms integer NOT NULL DEFAULT 600000
    CHECK (default_idle_timeout_ms BETWEEN 1000 AND 3600000);
UPDATE settings SET default_idle_timeout_ms = default_request_timeout_ms;

ALTER TABLE channels RENAME COLUMN total_timeout_ms TO request_timeout_ms;
ALTER TABLE channels ADD COLUMN idle_timeout_ms integer
    CHECK (idle_timeout_ms BETWEEN 1000 AND 3600000);
UPDATE channels SET idle_timeout_ms = request_timeout_ms;

ALTER TABLE route_prefs
    ADD COLUMN idle_timeout_ms integer CHECK (idle_timeout_ms BETWEEN 1000 AND 3600000),
    ADD COLUMN request_timeout_ms integer CHECK (request_timeout_ms BETWEEN 1000 AND 3600000);

-- +goose Down
ALTER TABLE route_prefs DROP COLUMN idle_timeout_ms, DROP COLUMN request_timeout_ms;
ALTER TABLE channels DROP COLUMN idle_timeout_ms;
ALTER TABLE channels RENAME COLUMN request_timeout_ms TO total_timeout_ms;
ALTER TABLE settings DROP COLUMN default_idle_timeout_ms;
ALTER TABLE settings RENAME COLUMN default_request_timeout_ms TO default_total_timeout_ms;
-- The former schema required the combined timeout to cover the first-byte wait.
UPDATE settings SET default_total_timeout_ms = greatest(default_total_timeout_ms, default_ttft_timeout_ms);
ALTER TABLE settings ADD CONSTRAINT settings_check CHECK (default_total_timeout_ms >= default_ttft_timeout_ms);
