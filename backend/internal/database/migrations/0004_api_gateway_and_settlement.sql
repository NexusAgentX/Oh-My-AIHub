-- +goose Up
CREATE TABLE api_fee_rates (
    version bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    fee_rate_nano bigint NOT NULL CHECK (fee_rate_nano BETWEEN 0 AND 1000000000),
    created_by uuid REFERENCES accounts(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO api_fee_rates (fee_rate_nano) VALUES (1000000);

CREATE TABLE api_keys (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    display_name text NOT NULL CHECK (length(trim(display_name)) BETWEEN 1 AND 80),
    key_prefix text NOT NULL CHECK (length(key_prefix) BETWEEN 12 AND 32),
    key_hash bytea NOT NULL UNIQUE CHECK (octet_length(key_hash) = 32),
    generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'deleted')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    last_used_at timestamptz,
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'deleted') = (deleted_at IS NOT NULL))
);

CREATE INDEX api_keys_owner_updated_idx ON api_keys(owner_account_id, updated_at DESC, id);

CREATE TABLE api_model_pools (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    api_key_id uuid NOT NULL REFERENCES api_keys(id) ON DELETE RESTRICT,
    canonical_model_id text NOT NULL REFERENCES models(id) ON DELETE RESTRICT,
    protocol text NOT NULL CHECK (protocol IN (
        'openai_chat_completions',
        'openai_responses',
        'anthropic_messages',
        'google_gemini_generate_content'
    )),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deleted')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'deleted') = (deleted_at IS NOT NULL))
);

CREATE UNIQUE INDEX api_model_pools_live_identity_unique
    ON api_model_pools(api_key_id, canonical_model_id, protocol)
    WHERE status = 'active';
CREATE INDEX api_model_pools_key_idx ON api_model_pools(api_key_id, status, created_at, id);

CREATE TABLE api_pool_members (
    pool_id uuid NOT NULL REFERENCES api_model_pools(id) ON DELETE RESTRICT,
    offer_id uuid NOT NULL REFERENCES channel_offers(id) ON DELETE RESTRICT,
    priority integer NOT NULL CHECK (priority > 0),
    added_validation_version bigint NOT NULL CHECK (added_validation_version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (pool_id, offer_id),
    UNIQUE (pool_id, priority)
);

CREATE TABLE api_calls (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    consumer_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    consumer_ledger_account_id uuid NOT NULL REFERENCES ledger_accounts(id) ON DELETE RESTRICT,
    api_key_id uuid NOT NULL REFERENCES api_keys(id) ON DELETE RESTRICT,
    key_prefix text NOT NULL CHECK (length(key_prefix) BETWEEN 12 AND 32),
    key_generation bigint NOT NULL CHECK (key_generation > 0),
    pool_id uuid REFERENCES api_model_pools(id) ON DELETE RESTRICT,
    pool_version bigint,
    canonical_model_id text NOT NULL,
    protocol text NOT NULL CHECK (protocol IN (
        'openai_chat_completions',
        'openai_responses',
        'anthropic_messages',
        'google_gemini_generate_content'
    )),
    status text NOT NULL CHECK (status IN (
        'rejected', 'in_progress', 'pending_delivery', 'succeeded', 'failed', 'incomplete', 'cancelled'
    )),
    decision_code text NOT NULL CHECK (length(trim(decision_code)) BETWEEN 1 AND 64),
    candidate_count integer NOT NULL DEFAULT 0 CHECK (candidate_count >= 0),
    upstream_attempt_count integer NOT NULL DEFAULT 0 CHECK (upstream_attempt_count >= 0),
    hold_id uuid REFERENCES ledger_holds(id) ON DELETE RESTRICT,
    preauthorized_nano bigint NOT NULL DEFAULT 0 CHECK (preauthorized_nano >= 0),
    zero_hold_reason text NOT NULL DEFAULT '',
    fee_rate_version bigint NOT NULL REFERENCES api_fee_rates(version) ON DELETE RESTRICT,
    fee_rate_nano bigint NOT NULL CHECK (fee_rate_nano BETWEEN 0 AND 1000000000),
    formula_version text NOT NULL DEFAULT 'formula-v1' CHECK (formula_version = 'formula-v1'),
    lease_generation bigint NOT NULL DEFAULT 1 CHECK (lease_generation > 0),
    lease_expires_at timestamptz,
    heartbeat_at timestamptz,
    final_offer_id uuid REFERENCES channel_offers(id) ON DELETE RESTRICT,
    completion_reason text NOT NULL DEFAULT '',
    input_tokens bigint CHECK (input_tokens >= 0),
    output_tokens bigint CHECK (output_tokens >= 0),
    cache_write_tokens bigint CHECK (cache_write_tokens >= 0),
    cache_read_tokens bigint CHECK (cache_read_tokens >= 0),
    provider_charge_nano bigint NOT NULL DEFAULT 0 CHECK (provider_charge_nano >= 0),
    platform_fee_nano bigint NOT NULL DEFAULT 0 CHECK (platform_fee_nano >= 0),
    final_http_status integer CHECK (final_http_status BETWEEN 100 AND 599),
    finalizer_payload_hash bytea CHECK (finalizer_payload_hash IS NULL OR octet_length(finalizer_payload_hash) = 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (
        (status = 'rejected' AND pool_id IS NULL AND pool_version IS NULL AND candidate_count = 0
            AND upstream_attempt_count = 0 AND hold_id IS NULL AND preauthorized_nano = 0
            AND completed_at IS NOT NULL)
        OR
        (status <> 'rejected' AND pool_id IS NOT NULL AND pool_version IS NOT NULL AND candidate_count > 0
            AND ((preauthorized_nano = 0 AND hold_id IS NULL) OR (preauthorized_nano > 0 AND hold_id IS NOT NULL)))
    ),
	CHECK ((status IN ('in_progress', 'pending_delivery')) = (completed_at IS NULL)),
	CHECK (status IN ('in_progress', 'pending_delivery') OR heartbeat_at IS NULL),
	CHECK ((status IN ('pending_delivery', 'succeeded', 'failed', 'incomplete', 'cancelled')) = (finalizer_payload_hash IS NOT NULL)),
	CHECK (provider_charge_nano <= preauthorized_nano OR preauthorized_nano = 0 OR final_offer_id IS NOT NULL),
	CHECK (
		status NOT IN ('pending_delivery', 'succeeded') OR (
			final_offer_id IS NOT NULL AND completion_reason = 'completed'
			AND input_tokens IS NOT NULL AND output_tokens IS NOT NULL
			AND cache_write_tokens IS NOT NULL AND cache_read_tokens IS NOT NULL
			AND final_http_status BETWEEN 200 AND 299
		)
	),
	CHECK (status IN ('pending_delivery', 'succeeded') OR (provider_charge_nano = 0 AND platform_fee_nano = 0))
);

CREATE INDEX api_calls_consumer_idx ON api_calls(consumer_account_id, created_at DESC, id DESC);
CREATE INDEX api_calls_key_idx ON api_calls(api_key_id, created_at DESC, id DESC);
CREATE INDEX api_calls_orphan_idx ON api_calls(heartbeat_at) WHERE status IN ('in_progress', 'pending_delivery');
CREATE INDEX api_calls_final_offer_idx ON api_calls(final_offer_id, completed_at DESC) WHERE final_offer_id IS NOT NULL;

CREATE TABLE api_call_candidates (
    call_id uuid NOT NULL REFERENCES api_calls(id) ON DELETE RESTRICT,
    priority integer NOT NULL CHECK (priority > 0),
    offer_id uuid NOT NULL REFERENCES channel_offers(id) ON DELETE RESTRICT,
    channel_id uuid NOT NULL REFERENCES channels(id) ON DELETE RESTRICT,
    provider_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    validation_version bigint NOT NULL CHECK (validation_version > 0),
    credential_version bigint NOT NULL CHECK (credential_version > 0),
    upstream_model_id text NOT NULL CHECK (octet_length(upstream_model_id) BETWEEN 1 AND 255),
    context_window bigint NOT NULL CHECK (context_window > 0),
    input_price_nano bigint NOT NULL CHECK (input_price_nano >= 0),
    output_price_nano bigint NOT NULL CHECK (output_price_nano >= 0),
    cache_write_price_nano bigint NOT NULL CHECK (cache_write_price_nano >= 0),
    cache_read_price_nano bigint NOT NULL CHECK (cache_read_price_nano >= 0),
    multiplier_nano bigint NOT NULL CHECK (multiplier_nano BETWEEN 0 AND 1000000000000),
    self_channel boolean NOT NULL,
    net_debit_upper_bound_nano bigint NOT NULL CHECK (net_debit_upper_bound_nano >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (call_id, priority),
    UNIQUE (call_id, offer_id)
);

CREATE INDEX api_call_candidates_provider_idx
    ON api_call_candidates(provider_account_id, created_at DESC, call_id);

CREATE TABLE api_call_attempts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    call_id uuid NOT NULL REFERENCES api_calls(id) ON DELETE RESTRICT,
    sequence integer NOT NULL CHECK (sequence > 0),
    offer_id uuid NOT NULL REFERENCES channel_offers(id) ON DELETE RESTRICT,
    provider_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    status text NOT NULL CHECK (status IN ('in_progress', 'pending_delivery', 'succeeded', 'failed', 'cancelled', 'incomplete')),
    http_status integer CHECK (http_status BETWEEN 100 AND 599),
    error_code text NOT NULL DEFAULT '' CHECK (length(error_code) <= 128),
    raw_error text NOT NULL DEFAULT '' CHECK (octet_length(raw_error) <= 4096),
    raw_error_truncated boolean NOT NULL DEFAULT false,
    semantic_committed boolean NOT NULL DEFAULT false,
    ttft_milliseconds bigint CHECK (ttft_milliseconds >= 0),
    duration_milliseconds bigint CHECK (duration_milliseconds >= 0),
    input_tokens bigint CHECK (input_tokens >= 0),
    output_tokens bigint CHECK (output_tokens >= 0),
    cache_write_tokens bigint CHECK (cache_write_tokens >= 0),
    cache_read_tokens bigint CHECK (cache_read_tokens >= 0),
    tokens_per_second_nano bigint CHECK (tokens_per_second_nano >= 0),
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (call_id, sequence),
    UNIQUE (call_id, offer_id),
	CHECK ((status IN ('in_progress', 'pending_delivery')) = (completed_at IS NULL)),
	CHECK (
		status NOT IN ('pending_delivery', 'succeeded') OR (
			http_status BETWEEN 200 AND 299
			AND error_code = '' AND raw_error = ''
			AND input_tokens IS NOT NULL AND output_tokens IS NOT NULL
			AND cache_write_tokens IS NOT NULL AND cache_read_tokens IS NOT NULL
			AND (status <> 'succeeded' OR semantic_committed)
		)
	),
	CHECK (
		status IN ('in_progress', 'pending_delivery', 'succeeded')
		OR (error_code <> '' AND input_tokens IS NULL AND output_tokens IS NULL
			AND cache_write_tokens IS NULL AND cache_read_tokens IS NULL)
	)
);

CREATE INDEX api_call_attempts_offer_metrics_idx ON api_call_attempts(offer_id, completed_at DESC);
CREATE INDEX api_call_attempts_provider_idx ON api_call_attempts(provider_account_id, completed_at DESC, call_id);
CREATE UNIQUE INDEX api_call_attempts_one_in_progress_per_call
    ON api_call_attempts(call_id) WHERE status IN ('in_progress', 'pending_delivery');

CREATE TABLE api_call_settlements (
    call_id uuid PRIMARY KEY REFERENCES api_calls(id) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN ('captured', 'self_usage', 'zero', 'released')),
    provider_account_id uuid REFERENCES accounts(id) ON DELETE RESTRICT,
    provider_charge_nano bigint NOT NULL CHECK (provider_charge_nano >= 0),
    platform_fee_nano bigint NOT NULL CHECK (platform_fee_nano >= 0),
    capture_transaction_id uuid REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    self_transaction_id uuid REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    hold_id uuid REFERENCES ledger_holds(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (kind = 'captured' AND capture_transaction_id IS NOT NULL AND self_transaction_id IS NULL
            AND provider_account_id IS NOT NULL AND provider_charge_nano > 0)
        OR
        (kind = 'self_usage' AND capture_transaction_id IS NULL AND self_transaction_id IS NOT NULL
            AND provider_account_id IS NOT NULL AND provider_charge_nano > 0 AND platform_fee_nano = 0)
        OR
        (kind IN ('zero', 'released') AND capture_transaction_id IS NULL AND self_transaction_id IS NULL
            AND provider_charge_nano = 0 AND platform_fee_nano = 0)
    )
);

CREATE TABLE api_call_compensations (
    call_id uuid PRIMARY KEY REFERENCES api_call_settlements(call_id) ON DELETE RESTRICT,
    reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 128),
    original_transaction_id uuid REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    reversal_transaction_id uuid UNIQUE REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    provider_charge_reversed_nano bigint NOT NULL CHECK (provider_charge_reversed_nano >= 0),
    platform_fee_reversed_nano bigint NOT NULL CHECK (platform_fee_reversed_nano >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (provider_charge_reversed_nano + platform_fee_reversed_nano = 0
            AND original_transaction_id IS NULL AND reversal_transaction_id IS NULL)
        OR
        (provider_charge_reversed_nano + platform_fee_reversed_nano > 0
            AND original_transaction_id IS NOT NULL AND reversal_transaction_id IS NOT NULL)
    )
);

-- +goose StatementBegin
CREATE FUNCTION guard_api_gateway_history() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'api gateway history cannot be physically deleted' USING ERRCODE = '23514';
    END IF;
    IF TG_TABLE_NAME = 'api_keys' THEN
        IF OLD.status = 'deleted' AND NEW IS DISTINCT FROM OLD THEN
            RAISE EXCEPTION 'deleted api key is immutable' USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_TABLE_NAME = 'api_model_pools' THEN
        IF OLD.status = 'deleted' AND NEW IS DISTINCT FROM OLD THEN
            RAISE EXCEPTION 'deleted api pool is immutable' USING ERRCODE = '23514';
        END IF;
        IF NEW.api_key_id <> OLD.api_key_id OR NEW.canonical_model_id <> OLD.canonical_model_id OR NEW.protocol <> OLD.protocol THEN
            RAISE EXCEPTION 'api pool identity is immutable' USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'api gateway fact is immutable' USING ERRCODE = '23514';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER api_keys_history_guard
BEFORE UPDATE OR DELETE ON api_keys FOR EACH ROW EXECUTE FUNCTION guard_api_gateway_history();
CREATE TRIGGER api_fee_rates_immutable
BEFORE UPDATE OR DELETE ON api_fee_rates FOR EACH ROW EXECUTE FUNCTION guard_api_gateway_history();
CREATE TRIGGER api_model_pools_history_guard
BEFORE UPDATE OR DELETE ON api_model_pools FOR EACH ROW EXECUTE FUNCTION guard_api_gateway_history();
CREATE TRIGGER api_call_candidates_immutable
BEFORE UPDATE OR DELETE ON api_call_candidates FOR EACH ROW EXECUTE FUNCTION guard_api_gateway_history();
CREATE TRIGGER api_call_settlements_immutable
BEFORE UPDATE OR DELETE ON api_call_settlements FOR EACH ROW EXECUTE FUNCTION guard_api_gateway_history();
CREATE TRIGGER api_call_compensations_immutable
BEFORE UPDATE OR DELETE ON api_call_compensations FOR EACH ROW EXECUTE FUNCTION guard_api_gateway_history();

CREATE FUNCTION guard_api_call_history() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'api calls cannot be deleted' USING ERRCODE = '23514';
    END IF;
    IF OLD.status NOT IN ('in_progress', 'pending_delivery') THEN
        RAISE EXCEPTION 'completed api call is immutable' USING ERRCODE = '23514';
    END IF;
    IF NEW.id <> OLD.id
       OR NEW.consumer_account_id <> OLD.consumer_account_id
       OR NEW.consumer_ledger_account_id <> OLD.consumer_ledger_account_id
       OR NEW.api_key_id <> OLD.api_key_id
       OR NEW.key_prefix <> OLD.key_prefix
       OR NEW.key_generation <> OLD.key_generation
       OR NEW.pool_id IS DISTINCT FROM OLD.pool_id
       OR NEW.pool_version IS DISTINCT FROM OLD.pool_version
       OR NEW.canonical_model_id <> OLD.canonical_model_id
       OR NEW.protocol <> OLD.protocol
       OR NEW.candidate_count <> OLD.candidate_count
       OR NEW.hold_id IS DISTINCT FROM OLD.hold_id
       OR NEW.preauthorized_nano <> OLD.preauthorized_nano
       OR NEW.zero_hold_reason <> OLD.zero_hold_reason
       OR NEW.fee_rate_version <> OLD.fee_rate_version
       OR NEW.fee_rate_nano <> OLD.fee_rate_nano
       OR NEW.formula_version <> OLD.formula_version
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'api call snapshot is immutable' USING ERRCODE = '23514';
    END IF;
    IF NEW.status = OLD.status THEN
        IF NEW.decision_code <> OLD.decision_code
           OR NEW.upstream_attempt_count < OLD.upstream_attempt_count
           OR NEW.upstream_attempt_count > OLD.upstream_attempt_count + 1
           OR NEW.final_offer_id IS DISTINCT FROM OLD.final_offer_id
           OR NEW.completion_reason <> OLD.completion_reason
           OR NEW.input_tokens IS DISTINCT FROM OLD.input_tokens
           OR NEW.output_tokens IS DISTINCT FROM OLD.output_tokens
           OR NEW.cache_write_tokens IS DISTINCT FROM OLD.cache_write_tokens
           OR NEW.cache_read_tokens IS DISTINCT FROM OLD.cache_read_tokens
           OR NEW.provider_charge_nano <> OLD.provider_charge_nano
           OR NEW.platform_fee_nano <> OLD.platform_fee_nano
           OR NEW.final_http_status IS DISTINCT FROM OLD.final_http_status
           OR NEW.finalizer_payload_hash IS DISTINCT FROM OLD.finalizer_payload_hash
           OR NEW.completed_at IS DISTINCT FROM OLD.completed_at
           OR NEW.lease_generation < OLD.lease_generation
           OR NEW.lease_generation > OLD.lease_generation + 1
           OR (NEW.lease_generation <> OLD.lease_generation AND NEW.upstream_attempt_count <> OLD.upstream_attempt_count)
           OR (OLD.status = 'pending_delivery' AND NEW.upstream_attempt_count <> OLD.upstream_attempt_count) THEN
            RAISE EXCEPTION 'invalid active api call update' USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.upstream_attempt_count <> OLD.upstream_attempt_count
       OR NEW.lease_generation <> OLD.lease_generation THEN
        RAISE EXCEPTION 'invalid api call completion' USING ERRCODE = '23514';
    END IF;
    IF OLD.status = 'in_progress' AND NEW.status NOT IN ('pending_delivery', 'failed', 'incomplete', 'cancelled') THEN
        RAISE EXCEPTION 'invalid api call completion' USING ERRCODE = '23514';
    END IF;
    IF OLD.status = 'pending_delivery' AND NEW.status = 'succeeded' THEN
        IF NEW.decision_code <> OLD.decision_code
           OR NEW.final_offer_id IS DISTINCT FROM OLD.final_offer_id
           OR NEW.completion_reason <> OLD.completion_reason
           OR NEW.input_tokens IS DISTINCT FROM OLD.input_tokens
           OR NEW.output_tokens IS DISTINCT FROM OLD.output_tokens
           OR NEW.cache_write_tokens IS DISTINCT FROM OLD.cache_write_tokens
           OR NEW.cache_read_tokens IS DISTINCT FROM OLD.cache_read_tokens
           OR NEW.provider_charge_nano <> OLD.provider_charge_nano
           OR NEW.platform_fee_nano <> OLD.platform_fee_nano
           OR NEW.final_http_status IS DISTINCT FROM OLD.final_http_status
           OR NEW.finalizer_payload_hash IS DISTINCT FROM OLD.finalizer_payload_hash THEN
            RAISE EXCEPTION 'delivery confirmation cannot rewrite final facts' USING ERRCODE = '23514';
        END IF;
    ELSIF OLD.status = 'pending_delivery' AND NEW.status = 'incomplete' THEN
        IF NEW.provider_charge_nano <> 0 OR NEW.platform_fee_nano <> 0
           OR NEW.completion_reason = OLD.completion_reason
           OR NEW.finalizer_payload_hash IS NOT DISTINCT FROM OLD.finalizer_payload_hash
           OR NEW.final_offer_id IS DISTINCT FROM OLD.final_offer_id
           OR NEW.final_http_status IS DISTINCT FROM OLD.final_http_status
           OR NEW.input_tokens IS NOT NULL OR NEW.output_tokens IS NOT NULL
           OR NEW.cache_write_tokens IS NOT NULL OR NEW.cache_read_tokens IS NOT NULL THEN
            RAISE EXCEPTION 'invalid delivery compensation' USING ERRCODE = '23514';
        END IF;
    ELSIF OLD.status = 'pending_delivery' THEN
        RAISE EXCEPTION 'pending delivery can only be confirmed or compensated' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER api_calls_history_guard
BEFORE UPDATE OR DELETE ON api_calls FOR EACH ROW EXECUTE FUNCTION guard_api_call_history();

CREATE FUNCTION guard_api_call_attempt_history() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'api call attempts cannot be deleted' USING ERRCODE = '23514';
    END IF;
    IF OLD.status NOT IN ('in_progress', 'pending_delivery') THEN
        RAISE EXCEPTION 'terminal api call attempt is immutable' USING ERRCODE = '23514';
    END IF;
	IF NEW.status = 'in_progress'
       AND NOT OLD.semantic_committed AND NEW.semantic_committed
       AND NEW.id = OLD.id AND NEW.call_id = OLD.call_id AND NEW.sequence = OLD.sequence
       AND NEW.offer_id = OLD.offer_id AND NEW.provider_account_id = OLD.provider_account_id
       AND NEW.http_status IS NOT DISTINCT FROM OLD.http_status
       AND NEW.error_code = OLD.error_code AND NEW.raw_error = OLD.raw_error
       AND NEW.raw_error_truncated = OLD.raw_error_truncated
       AND NEW.ttft_milliseconds IS NOT DISTINCT FROM OLD.ttft_milliseconds
       AND NEW.duration_milliseconds IS NOT DISTINCT FROM OLD.duration_milliseconds
       AND NEW.input_tokens IS NOT DISTINCT FROM OLD.input_tokens
       AND NEW.output_tokens IS NOT DISTINCT FROM OLD.output_tokens
       AND NEW.cache_write_tokens IS NOT DISTINCT FROM OLD.cache_write_tokens
       AND NEW.cache_read_tokens IS NOT DISTINCT FROM OLD.cache_read_tokens
       AND NEW.tokens_per_second_nano IS NOT DISTINCT FROM OLD.tokens_per_second_nano
       AND NEW.started_at = OLD.started_at AND NEW.completed_at IS NULL THEN
		RETURN NEW;
	END IF;
	IF OLD.status = 'pending_delivery' AND NEW.status = 'pending_delivery'
	   AND NOT OLD.semantic_committed AND NEW.semantic_committed
	   AND NEW.id = OLD.id AND NEW.call_id = OLD.call_id AND NEW.sequence = OLD.sequence
	   AND NEW.offer_id = OLD.offer_id AND NEW.provider_account_id = OLD.provider_account_id
	   AND NEW.http_status IS NOT DISTINCT FROM OLD.http_status
	   AND NEW.error_code = OLD.error_code AND NEW.raw_error = OLD.raw_error
	   AND NEW.raw_error_truncated = OLD.raw_error_truncated
	   AND NEW.input_tokens IS NOT DISTINCT FROM OLD.input_tokens
	   AND NEW.output_tokens IS NOT DISTINCT FROM OLD.output_tokens
	   AND NEW.cache_write_tokens IS NOT DISTINCT FROM OLD.cache_write_tokens
	   AND NEW.cache_read_tokens IS NOT DISTINCT FROM OLD.cache_read_tokens
	   AND NEW.ttft_milliseconds IS NOT NULL
	   AND NEW.duration_milliseconds IS NOT NULL
	   AND NEW.duration_milliseconds >= NEW.ttft_milliseconds
	   AND (OLD.duration_milliseconds IS NULL OR NEW.duration_milliseconds >= OLD.duration_milliseconds)
	   AND (OLD.tokens_per_second_nano IS NULL OR NEW.tokens_per_second_nano IS NOT DISTINCT FROM OLD.tokens_per_second_nano)
	   AND NEW.started_at = OLD.started_at AND NEW.completed_at IS NULL THEN
		RETURN NEW;
	END IF;
    IF NEW.id <> OLD.id OR NEW.call_id <> OLD.call_id OR NEW.sequence <> OLD.sequence
       OR NEW.offer_id <> OLD.offer_id OR NEW.provider_account_id <> OLD.provider_account_id
       OR NEW.started_at <> OLD.started_at
       OR (OLD.semantic_committed AND NOT NEW.semantic_committed) THEN
        RAISE EXCEPTION 'invalid api call attempt completion' USING ERRCODE = '23514';
    END IF;
    IF OLD.status = 'in_progress' AND NEW.status NOT IN ('pending_delivery', 'failed', 'cancelled', 'incomplete') THEN
        RAISE EXCEPTION 'invalid api call attempt completion' USING ERRCODE = '23514';
    END IF;
    IF OLD.status = 'pending_delivery' AND NEW.status = 'succeeded' THEN
        IF NOT NEW.semantic_committed OR NEW.completed_at IS NULL
           OR NEW.http_status IS DISTINCT FROM OLD.http_status
           OR NEW.error_code <> OLD.error_code OR NEW.raw_error <> OLD.raw_error
           OR NEW.raw_error_truncated <> OLD.raw_error_truncated
           OR NEW.ttft_milliseconds IS DISTINCT FROM OLD.ttft_milliseconds
           OR NEW.duration_milliseconds IS DISTINCT FROM OLD.duration_milliseconds
           OR NEW.input_tokens IS DISTINCT FROM OLD.input_tokens
           OR NEW.output_tokens IS DISTINCT FROM OLD.output_tokens
           OR NEW.cache_write_tokens IS DISTINCT FROM OLD.cache_write_tokens
           OR NEW.cache_read_tokens IS DISTINCT FROM OLD.cache_read_tokens
           OR NEW.tokens_per_second_nano IS DISTINCT FROM OLD.tokens_per_second_nano THEN
            RAISE EXCEPTION 'delivery confirmation cannot rewrite attempt facts' USING ERRCODE = '23514';
        END IF;
    ELSIF OLD.status = 'pending_delivery' AND NEW.status = 'incomplete' THEN
        IF NEW.completed_at IS NULL OR NEW.error_code = ''
           OR NEW.input_tokens IS NOT NULL OR NEW.output_tokens IS NOT NULL
           OR NEW.cache_write_tokens IS NOT NULL OR NEW.cache_read_tokens IS NOT NULL
           OR NEW.tokens_per_second_nano IS NOT NULL THEN
            RAISE EXCEPTION 'invalid compensated attempt' USING ERRCODE = '23514';
        END IF;
    ELSIF OLD.status = 'pending_delivery' THEN
        RAISE EXCEPTION 'pending attempt can only be confirmed or compensated' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER api_call_attempts_history_guard
BEFORE UPDATE OR DELETE ON api_call_attempts FOR EACH ROW EXECUTE FUNCTION guard_api_call_attempt_history();

CREATE FUNCTION verify_api_call_settlement() RETURNS trigger AS $$
DECLARE
    target_call uuid;
    call_status text;
    call_provider_charge bigint;
	call_platform_fee bigint;
	call_final_offer uuid;
	call_http_status integer;
	call_input_tokens bigint;
	call_output_tokens bigint;
	call_cache_write_tokens bigint;
	call_cache_read_tokens bigint;
	settlement_count bigint;
    compensation_count bigint;
    matching_provider uuid;
    settlement_provider_charge bigint;
    settlement_platform_fee bigint;
    settlement_provider uuid;
    settlement_capture uuid;
    settlement_self uuid;
    compensation_provider_charge bigint;
    compensation_platform_fee bigint;
	compensation_original uuid;
	compensation_reversal uuid;
	final_attempt_count bigint;
	final_attempt_status text;
	final_attempt_provider uuid;
	final_attempt_http_status integer;
	final_attempt_input_tokens bigint;
	final_attempt_output_tokens bigint;
	final_attempt_cache_write_tokens bigint;
	final_attempt_cache_read_tokens bigint;
	final_attempt_semantic boolean;
BEGIN
    IF TG_TABLE_NAME = 'api_calls' THEN
        target_call := NEW.id;
    ELSE
        target_call := NEW.call_id;
    END IF;
	SELECT status, provider_charge_nano, platform_fee_nano, final_offer_id,
	       final_http_status, input_tokens, output_tokens, cache_write_tokens, cache_read_tokens
	  INTO call_status, call_provider_charge, call_platform_fee, call_final_offer,
	       call_http_status, call_input_tokens, call_output_tokens, call_cache_write_tokens, call_cache_read_tokens
      FROM api_calls WHERE id = target_call;
    SELECT count(*) INTO settlement_count FROM api_call_settlements WHERE call_id = target_call;
    SELECT count(*) INTO compensation_count FROM api_call_compensations WHERE call_id = target_call;
    IF call_status IN ('pending_delivery', 'succeeded', 'failed', 'incomplete', 'cancelled') AND settlement_count <> 1 THEN
        RAISE EXCEPTION 'finalizing api call must have exactly one settlement fact' USING ERRCODE = '23514';
    END IF;
    IF call_status IN ('rejected', 'in_progress') AND (settlement_count <> 0 OR compensation_count <> 0) THEN
        RAISE EXCEPTION 'unfinalized api call cannot have settlement facts' USING ERRCODE = '23514';
    END IF;
    IF settlement_count = 1 THEN
        SELECT provider_charge_nano, platform_fee_nano, provider_account_id,
               capture_transaction_id, self_transaction_id
          INTO settlement_provider_charge, settlement_platform_fee, settlement_provider,
               settlement_capture, settlement_self
          FROM api_call_settlements WHERE call_id = target_call;
		IF call_status IN ('pending_delivery', 'succeeded') THEN
            IF compensation_count <> 0
               OR settlement_provider_charge <> call_provider_charge
               OR settlement_platform_fee <> call_platform_fee THEN
                RAISE EXCEPTION 'active success settlement amounts must match call' USING ERRCODE = '23514';
            END IF;
            SELECT provider_account_id INTO matching_provider
              FROM api_call_candidates
             WHERE call_id = target_call AND offer_id = call_final_offer;
			IF matching_provider IS NULL OR settlement_provider IS DISTINCT FROM matching_provider THEN
				RAISE EXCEPTION 'successful api call settlement must match its final candidate' USING ERRCODE = '23514';
			END IF;
			SELECT count(*) INTO final_attempt_count
			  FROM api_call_attempts
			 WHERE call_id = target_call AND offer_id = call_final_offer;
			IF final_attempt_count <> 1 THEN
				RAISE EXCEPTION 'successful api call must have exactly one final attempt' USING ERRCODE = '23514';
			END IF;
			SELECT status, provider_account_id, http_status,
			       input_tokens, output_tokens, cache_write_tokens, cache_read_tokens, semantic_committed
			  INTO final_attempt_status, final_attempt_provider, final_attempt_http_status,
			       final_attempt_input_tokens, final_attempt_output_tokens,
			       final_attempt_cache_write_tokens, final_attempt_cache_read_tokens, final_attempt_semantic
			  FROM api_call_attempts
			 WHERE call_id = target_call AND offer_id = call_final_offer;
			IF final_attempt_provider IS DISTINCT FROM matching_provider
			   OR final_attempt_http_status IS DISTINCT FROM call_http_status
			   OR final_attempt_input_tokens IS DISTINCT FROM call_input_tokens
			   OR final_attempt_output_tokens IS DISTINCT FROM call_output_tokens
			   OR final_attempt_cache_write_tokens IS DISTINCT FROM call_cache_write_tokens
			   OR final_attempt_cache_read_tokens IS DISTINCT FROM call_cache_read_tokens THEN
				RAISE EXCEPTION 'successful api call facts must match its final attempt' USING ERRCODE = '23514';
			END IF;
			IF call_status = 'pending_delivery' AND final_attempt_status <> 'pending_delivery' THEN
				RAISE EXCEPTION 'pending call must have a pending success attempt' USING ERRCODE = '23514';
			END IF;
			IF call_status = 'succeeded' AND (final_attempt_status <> 'succeeded' OR NOT final_attempt_semantic) THEN
				RAISE EXCEPTION 'successful call must have a delivered success attempt' USING ERRCODE = '23514';
			END IF;
        ELSIF call_status IN ('failed', 'cancelled', 'incomplete') THEN
            IF call_provider_charge <> 0 OR call_platform_fee <> 0 THEN
                RAISE EXCEPTION 'non-success call must expose zero net charge' USING ERRCODE = '23514';
            END IF;
            IF compensation_count > 0 AND call_status <> 'incomplete' THEN
                RAISE EXCEPTION 'only incomplete delivery can have compensation' USING ERRCODE = '23514';
            END IF;
            IF settlement_provider_charge + settlement_platform_fee = 0 THEN
                IF compensation_count = 1 AND NOT EXISTS (
                    SELECT 1 FROM api_call_compensations
                     WHERE call_id = target_call
                       AND provider_charge_reversed_nano = 0
                       AND platform_fee_reversed_nano = 0
                       AND original_transaction_id IS NULL
                       AND reversal_transaction_id IS NULL
                ) THEN
                    RAISE EXCEPTION 'zero compensation fact is malformed' USING ERRCODE = '23514';
                ELSIF compensation_count > 1 THEN
                    RAISE EXCEPTION 'zero settlement has too many compensation facts' USING ERRCODE = '23514';
                END IF;
            ELSE
                IF call_status <> 'incomplete' OR compensation_count <> 1 THEN
                    RAISE EXCEPTION 'charged settlement requires one incomplete compensation' USING ERRCODE = '23514';
                END IF;
                SELECT provider_charge_reversed_nano, platform_fee_reversed_nano,
                       original_transaction_id, reversal_transaction_id
                  INTO compensation_provider_charge, compensation_platform_fee,
                       compensation_original, compensation_reversal
                  FROM api_call_compensations WHERE call_id = target_call;
                IF compensation_provider_charge <> settlement_provider_charge
                   OR compensation_platform_fee <> settlement_platform_fee
                   OR compensation_original IS DISTINCT FROM COALESCE(settlement_capture, settlement_self)
                   OR NOT EXISTS (
                       SELECT 1 FROM ledger_transactions
                        WHERE id = compensation_reversal
                          AND reversal_of_transaction_id = compensation_original
                          AND kind = 'reversal' AND sealed
                   ) THEN
                    RAISE EXCEPTION 'compensation must strictly reverse original settlement' USING ERRCODE = '23514';
                END IF;
            END IF;
        END IF;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER api_call_settlement_from_call
AFTER INSERT OR UPDATE ON api_calls
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_api_call_settlement();
CREATE CONSTRAINT TRIGGER api_call_settlement_from_fact
AFTER INSERT ON api_call_settlements
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_api_call_settlement();
CREATE CONSTRAINT TRIGGER api_call_settlement_from_compensation
AFTER INSERT ON api_call_compensations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_api_call_settlement();
CREATE CONSTRAINT TRIGGER api_call_settlement_from_attempt
AFTER INSERT OR UPDATE ON api_call_attempts
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_api_call_settlement();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER api_call_attempts_history_guard ON api_call_attempts;
DROP FUNCTION guard_api_call_attempt_history();
DROP TRIGGER api_call_settlement_from_attempt ON api_call_attempts;
DROP TRIGGER api_call_settlement_from_compensation ON api_call_compensations;
DROP TRIGGER api_call_settlement_from_fact ON api_call_settlements;
DROP TRIGGER api_call_settlement_from_call ON api_calls;
DROP FUNCTION verify_api_call_settlement();
DROP TRIGGER api_call_compensations_immutable ON api_call_compensations;
DROP TRIGGER api_call_settlements_immutable ON api_call_settlements;
DROP TRIGGER api_call_candidates_immutable ON api_call_candidates;
DROP TRIGGER api_model_pools_history_guard ON api_model_pools;
DROP TRIGGER api_fee_rates_immutable ON api_fee_rates;
DROP TRIGGER api_keys_history_guard ON api_keys;
DROP FUNCTION guard_api_gateway_history();
DROP TABLE api_call_compensations;
DROP TABLE api_call_settlements;
DROP TABLE api_call_attempts;
DROP TABLE api_call_candidates;
DROP TABLE api_calls;
DROP TABLE api_pool_members;
DROP TABLE api_model_pools;
DROP TABLE api_keys;
DROP TABLE api_fee_rates;
