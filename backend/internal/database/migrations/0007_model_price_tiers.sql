-- +goose Up
CREATE TABLE model_price_tiers (
    model_id text NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    seq integer NOT NULL CHECK (seq BETWEEN 1 AND 16),
    name text NOT NULL DEFAULT '' CHECK (length(trim(name)) <= 64),
    min_prompt_tokens bigint CHECK (min_prompt_tokens IS NULL OR min_prompt_tokens >= 0),
    max_prompt_tokens bigint CHECK (max_prompt_tokens IS NULL OR max_prompt_tokens >= 0),
    timezone text NOT NULL DEFAULT 'UTC' CHECK (length(trim(timezone)) BETWEEN 1 AND 64),
    weekdays smallint[],
    start_minute_of_day smallint CHECK (start_minute_of_day IS NULL OR start_minute_of_day BETWEEN 0 AND 1439),
    end_minute_of_day smallint CHECK (end_minute_of_day IS NULL OR end_minute_of_day BETWEEN 1 AND 1440),
    input_price_nano_per_million bigint NOT NULL CHECK (input_price_nano_per_million BETWEEN 0 AND 100000000000000),
    output_price_nano_per_million bigint NOT NULL CHECK (output_price_nano_per_million BETWEEN 0 AND 100000000000000),
    cache_write_price_nano_per_million bigint NOT NULL CHECK (cache_write_price_nano_per_million BETWEEN 0 AND 100000000000000),
    cache_read_price_nano_per_million bigint NOT NULL CHECK (cache_read_price_nano_per_million BETWEEN 0 AND 100000000000000),
    PRIMARY KEY (model_id, seq),
    CHECK ((start_minute_of_day IS NULL) = (end_minute_of_day IS NULL)),
    CHECK (start_minute_of_day IS NULL OR start_minute_of_day <> end_minute_of_day),
    CHECK (min_prompt_tokens IS NULL OR max_prompt_tokens IS NULL OR min_prompt_tokens < max_prompt_tokens),
    CHECK (weekdays IS NULL OR (
        cardinality(weekdays) BETWEEN 1 AND 7
        AND 1 <= ALL(weekdays) AND 7 >= ALL(weekdays)
    ))
);

CREATE INDEX model_price_tiers_model_idx ON model_price_tiers(model_id);

CREATE TABLE api_call_price_tiers (
    call_id uuid NOT NULL REFERENCES api_calls(id) ON DELETE RESTRICT,
    seq integer NOT NULL CHECK (seq BETWEEN 1 AND 16),
    name text NOT NULL DEFAULT '',
    min_prompt_tokens bigint CHECK (min_prompt_tokens IS NULL OR min_prompt_tokens >= 0),
    max_prompt_tokens bigint CHECK (max_prompt_tokens IS NULL OR max_prompt_tokens >= 0),
    timezone text NOT NULL DEFAULT 'UTC',
    weekdays smallint[],
    start_minute_of_day smallint CHECK (start_minute_of_day IS NULL OR start_minute_of_day BETWEEN 0 AND 1439),
    end_minute_of_day smallint CHECK (end_minute_of_day IS NULL OR end_minute_of_day BETWEEN 1 AND 1440),
    input_price_nano bigint NOT NULL CHECK (input_price_nano >= 0),
    output_price_nano bigint NOT NULL CHECK (output_price_nano >= 0),
    cache_write_price_nano bigint NOT NULL CHECK (cache_write_price_nano >= 0),
    cache_read_price_nano bigint NOT NULL CHECK (cache_read_price_nano >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (call_id, seq),
    CHECK ((start_minute_of_day IS NULL) = (end_minute_of_day IS NULL))
);

ALTER TABLE api_calls DROP CONSTRAINT api_calls_formula_version_check;
ALTER TABLE api_calls ADD CONSTRAINT api_calls_formula_version_check
    CHECK (formula_version IN ('formula-v1', 'formula-v2'));

ALTER TABLE api_calls ADD COLUMN settled_price_tier_seq integer NOT NULL DEFAULT 0
    CHECK (settled_price_tier_seq BETWEEN 0 AND 16);

CREATE TRIGGER api_call_price_tiers_immutable
    BEFORE UPDATE OR DELETE ON api_call_price_tiers
    FOR EACH ROW EXECUTE FUNCTION guard_api_gateway_history();

-- +goose Down
DROP TRIGGER api_call_price_tiers_immutable ON api_call_price_tiers;
ALTER TABLE api_calls DROP COLUMN settled_price_tier_seq;
ALTER TABLE api_calls DROP CONSTRAINT api_calls_formula_version_check;
ALTER TABLE api_calls ADD CONSTRAINT api_calls_formula_version_check CHECK (formula_version = 'formula-v1');
DROP TABLE api_call_price_tiers;
DROP TABLE model_price_tiers;
