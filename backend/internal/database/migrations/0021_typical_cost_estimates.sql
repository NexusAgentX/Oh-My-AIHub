-- +goose Up
-- Derived comparison data only. Existing calls, quotes, users and ledger rows
-- are never rewritten. Samples contain bounded numeric billing facts, no body.
CREATE TABLE typical_settings (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 window_days integer NOT NULL DEFAULT 30 CHECK(window_days BETWEEN 1 AND 90),
 min_samples integer NOT NULL DEFAULT 20 CHECK(min_samples BETWEEN 1 AND 20000),
 interval_seconds integer NOT NULL DEFAULT 300 CHECK(interval_seconds BETWEEN 30 AND 86400),
 typical_tokens bigint NOT NULL DEFAULT 1000000 CHECK(typical_tokens BETWEEN 1 AND 1000000000),
 typical_requests bigint NOT NULL DEFAULT 100 CHECK(typical_requests BETWEEN 1 AND 1000000),
 typical_images bigint NOT NULL DEFAULT 100 CHECK(typical_images BETWEEN 1 AND 1000000),
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO typical_settings(singleton)VALUES(true);
CREATE TABLE typical_state (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 generation bigint NOT NULL DEFAULT 1 CHECK(generation>0),
 checkpoint jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(checkpoint)='object' AND octet_length(checkpoint::text)<=4096),
 snapshot jsonb CHECK(snapshot IS NULL OR(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=8388608))
);
INSERT INTO typical_state(singleton)VALUES(true);
CREATE TABLE typical_samples (
 call_id uuid PRIMARY KEY REFERENCES calls(id),
 completed_at timestamptz NOT NULL,
 profile jsonb NOT NULL CHECK(jsonb_typeof(profile)='object' AND octet_length(profile::text)<=65536)
);
CREATE INDEX typical_samples_completed ON typical_samples(completed_at,call_id);
CREATE INDEX calls_typical_completed ON calls(completed_at,id)
 WHERE completed_at IS NOT NULL AND outcome='succeeded' AND price_snapshot ? 'group_quote';

-- +goose Down
-- These tables are disposable estimates; historical billing facts are calls.
DROP INDEX calls_typical_completed;
DROP TABLE typical_samples;
DROP TABLE typical_state;
DROP TABLE typical_settings;
