-- +goose Up
CREATE TABLE accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username text NOT NULL UNIQUE,
    display_name text NOT NULL,
    password_hash text NOT NULL,
	password_version bigint NOT NULL DEFAULT 1 CHECK (password_version > 0),
	version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    must_change_password boolean NOT NULL DEFAULT true,
    is_admin boolean NOT NULL DEFAULT false,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    credit_limit_nano bigint NOT NULL DEFAULT 0 CHECK (credit_limit_nano >= 0),
    password_changed_at timestamptz,
	disabled_at timestamptz,
	created_by uuid REFERENCES accounts(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (username = lower(username)),
    CHECK (username ~ '^[a-z0-9][a-z0-9._-]{2,31}$'),
    CHECK (length(trim(display_name)) > 0)
);

CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	password_version bigint NOT NULL CHECK (password_version > 0),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_account_id_idx ON sessions(account_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

CREATE TABLE models (
    internal_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	id text NOT NULL UNIQUE,
    name text NOT NULL,
    provider text NOT NULL,
    context_window bigint NOT NULL CHECK (context_window > 0),
    parameter_info text NOT NULL DEFAULT '',
    input_modalities text[] NOT NULL,
    output_modalities text[] NOT NULL,
    supports_tools boolean NOT NULL DEFAULT false,
    supports_structured_output boolean NOT NULL DEFAULT false,
    supports_vision boolean NOT NULL DEFAULT false,
    input_price_nano_per_million bigint NOT NULL CHECK (input_price_nano_per_million >= 0),
    output_price_nano_per_million bigint NOT NULL CHECK (output_price_nano_per_million >= 0),
    cache_write_price_nano_per_million bigint NOT NULL CHECK (cache_write_price_nano_per_million >= 0),
    cache_read_price_nano_per_million bigint NOT NULL CHECK (cache_read_price_nano_per_million >= 0),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
	version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
	price_updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (cardinality(input_modalities) > 0),
    CHECK (cardinality(output_modalities) > 0)
);

CREATE INDEX models_status_name_idx ON models(status, name);

CREATE TABLE audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_account_id uuid REFERENCES accounts(id),
    action text NOT NULL,
    target_type text NOT NULL,
    target_id text NOT NULL,
    reason text NOT NULL,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_target_idx ON audit_events(target_type, target_id, created_at DESC);

-- +goose Down
DROP TABLE audit_events;
DROP TABLE models;
DROP TABLE sessions;
DROP TABLE accounts;
