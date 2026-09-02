-- +goose Up
ALTER TABLE models ADD CONSTRAINT models_channel_price_bounds CHECK (
    input_price_nano_per_million <= 100000000000000
    AND output_price_nano_per_million <= 100000000000000
    AND cache_write_price_nano_per_million <= 100000000000000
    AND cache_read_price_nano_per_million <= 100000000000000
);

CREATE TABLE channels (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    display_name text NOT NULL CHECK (length(trim(display_name)) BETWEEN 1 AND 80),
    normalized_base_url text NOT NULL,
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'paused', 'deleted')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    credential_version bigint NOT NULL DEFAULT 0 CHECK (credential_version >= 0),
    credential_updated_at timestamptz,
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'deleted') = (deleted_at IS NOT NULL))
);

CREATE INDEX channels_owner_updated_idx ON channels(owner_account_id, updated_at DESC);
CREATE INDEX channels_status_updated_idx ON channels(status, updated_at DESC);
ALTER TABLE channels ADD CONSTRAINT channels_id_credential_version_unique UNIQUE (id, credential_version);

CREATE TABLE channel_credentials (
    channel_id uuid PRIMARY KEY REFERENCES channels(id) ON DELETE RESTRICT,
    credential_version bigint NOT NULL CHECK (credential_version > 0),
    key_id text NOT NULL CHECK (length(trim(key_id)) BETWEEN 1 AND 64),
    nonce bytea NOT NULL CHECK (octet_length(nonce) = 12),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) > 16),
    configured_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (channel_id, credential_version)
        REFERENCES channels(id, credential_version) ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE channel_models (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id uuid NOT NULL REFERENCES channels(id) ON DELETE RESTRICT,
    model_id text NOT NULL REFERENCES models(id) ON DELETE RESTRICT,
    multiplier_nano bigint NOT NULL CHECK (multiplier_nano >= 0 AND multiplier_nano <= 1000000000000),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (channel_id, model_id)
);

CREATE TABLE channel_offers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_model_id uuid NOT NULL REFERENCES channel_models(id) ON DELETE RESTRICT,
    protocol text NOT NULL CHECK (protocol IN (
        'openai_chat_completions',
        'openai_responses',
        'anthropic_messages',
        'google_gemini_generate_content'
    )),
    upstream_model_id text NOT NULL CHECK (octet_length(upstream_model_id) BETWEEN 1 AND 255),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'deleted')),
    validation_version bigint NOT NULL DEFAULT 1 CHECK (validation_version > 0),
    validation_attempt_seq bigint NOT NULL DEFAULT 0 CHECK (validation_attempt_seq >= 0),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    deleted_at timestamptz,
    deleted_multiplier_nano bigint CHECK (deleted_multiplier_nano IS NULL OR deleted_multiplier_nano BETWEEN 0 AND 1000000000000),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'deleted') = (deleted_at IS NOT NULL)),
    CHECK ((status = 'deleted') = (deleted_multiplier_nano IS NOT NULL))
);

CREATE UNIQUE INDEX channel_offers_live_identity_unique
    ON channel_offers(channel_model_id, protocol)
    WHERE status <> 'deleted';
CREATE INDEX channel_offers_channel_model_idx ON channel_offers(channel_model_id, created_at, id);
CREATE INDEX channel_offers_market_idx ON channel_offers(protocol, status, channel_model_id);

CREATE TABLE channel_validation_attempts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    offer_id uuid NOT NULL REFERENCES channel_offers(id) ON DELETE RESTRICT,
    validation_version bigint NOT NULL CHECK (validation_version > 0),
    attempt_seq bigint NOT NULL CHECK (attempt_seq > 0),
    actor_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    status text NOT NULL CHECK (status IN ('in_progress', 'passed', 'failed')),
    error_category text NOT NULL DEFAULT '' CHECK (error_category IN (
        '', 'auth_failure', 'upstream_error', 'transport_error', 'timeout',
        'response_too_large', 'invalid_response', 'configuration_error'
    )),
	http_status integer CHECK (http_status IS NULL OR http_status BETWEEN 100 AND 599),
    raw_error text NOT NULL DEFAULT '' CHECK (octet_length(raw_error) <= 4096),
    raw_error_truncated boolean NOT NULL DEFAULT false,
    duration_milliseconds bigint CHECK (duration_milliseconds IS NULL OR duration_milliseconds >= 0),
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (offer_id, validation_version, attempt_seq),
    CHECK (
        (status = 'in_progress' AND completed_at IS NULL AND duration_milliseconds IS NULL AND error_category = '' AND raw_error = '')
        OR
        (status = 'passed' AND completed_at IS NOT NULL AND completed_at >= started_at AND duration_milliseconds IS NOT NULL AND error_category = '' AND raw_error = '')
        OR
        (status = 'failed' AND completed_at IS NOT NULL AND completed_at >= started_at AND duration_milliseconds IS NOT NULL AND error_category <> '')
    )
);

CREATE INDEX channel_validation_latest_idx
    ON channel_validation_attempts(offer_id, validation_version, attempt_seq DESC);

CREATE TABLE channel_ratings (
    channel_id uuid NOT NULL REFERENCES channels(id) ON DELETE RESTRICT,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    score smallint NOT NULL CHECK (score BETWEEN 1 AND 5),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, account_id)
);

CREATE INDEX channel_ratings_channel_idx ON channel_ratings(channel_id);

-- +goose StatementBegin
CREATE FUNCTION guard_channel_history() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'channel history cannot be physically deleted' USING ERRCODE = '23514';
    END IF;
    IF TG_TABLE_NAME = 'channels' THEN
        IF OLD.status = 'deleted' AND NEW IS DISTINCT FROM OLD THEN
            RAISE EXCEPTION 'deleted channel is immutable' USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_TABLE_NAME = 'channel_models' THEN
        IF NEW.channel_id <> OLD.channel_id OR NEW.model_id <> OLD.model_id THEN
            RAISE EXCEPTION 'channel model identity is immutable' USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_TABLE_NAME = 'channel_offers' THEN
        IF NEW.channel_model_id <> OLD.channel_model_id OR NEW.protocol <> OLD.protocol THEN
            RAISE EXCEPTION 'channel offer identity is immutable' USING ERRCODE = '23514';
        END IF;
        IF OLD.status = 'deleted' AND NEW IS DISTINCT FROM OLD THEN
            RAISE EXCEPTION 'deleted channel offer is immutable' USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER channels_history_guard
BEFORE UPDATE OR DELETE ON channels FOR EACH ROW EXECUTE FUNCTION guard_channel_history();
CREATE TRIGGER channel_models_identity_guard
BEFORE UPDATE OR DELETE ON channel_models FOR EACH ROW EXECUTE FUNCTION guard_channel_history();
CREATE TRIGGER channel_offers_history_guard
BEFORE UPDATE OR DELETE ON channel_offers FOR EACH ROW EXECUTE FUNCTION guard_channel_history();

CREATE FUNCTION guard_validation_attempt_history() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'validation attempts cannot be deleted' USING ERRCODE = '23514';
    END IF;
    IF OLD.status <> 'in_progress' THEN
        RAISE EXCEPTION 'completed validation attempt is immutable' USING ERRCODE = '23514';
    END IF;
    IF NEW.id <> OLD.id OR NEW.offer_id <> OLD.offer_id
       OR NEW.validation_version <> OLD.validation_version
       OR NEW.attempt_seq <> OLD.attempt_seq
       OR NEW.actor_account_id <> OLD.actor_account_id
       OR NEW.started_at <> OLD.started_at
       OR NEW.status NOT IN ('passed', 'failed') THEN
        RAISE EXCEPTION 'validation attempt completion is invalid' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER channel_validation_attempts_history_guard
BEFORE UPDATE OR DELETE ON channel_validation_attempts
FOR EACH ROW EXECUTE FUNCTION guard_validation_attempt_history();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER channel_validation_attempts_history_guard ON channel_validation_attempts;
DROP FUNCTION guard_validation_attempt_history();
DROP TRIGGER channel_offers_history_guard ON channel_offers;
DROP TRIGGER channel_models_identity_guard ON channel_models;
DROP TRIGGER channels_history_guard ON channels;
DROP FUNCTION guard_channel_history();
DROP TABLE channel_ratings;
DROP TABLE channel_validation_attempts;
DROP TABLE channel_offers;
DROP TABLE channel_models;
DROP TABLE channel_credentials;
DROP TABLE channels;
ALTER TABLE models DROP CONSTRAINT models_channel_price_bounds;
