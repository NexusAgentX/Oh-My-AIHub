-- +goose Up
-- Only the bounded shared lookup cache expires. Private IP/region snapshots in
-- calls.diagnostics follow their call's retention and are never auto-cleared.
CREATE TABLE geoip_cache (
    ip text PRIMARY KEY CHECK (length(ip) BETWEEN 2 AND 45),
    location jsonb,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX geoip_cache_updated_at_idx ON geoip_cache (updated_at);
CREATE TABLE geoip_daily_usage (
    day timestamptz PRIMARY KEY,
    queries integer NOT NULL CHECK (queries BETWEEN 1 AND 900)
);

-- +goose Down
DROP TABLE geoip_daily_usage;
DROP TABLE geoip_cache;
