-- +goose Up
ALTER TABLE accounts
    ADD COLUMN credit_frozen boolean NOT NULL DEFAULT false;

CREATE TABLE ledger_accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_account_id uuid UNIQUE REFERENCES accounts(id) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN ('user', 'platform_incentive', 'platform_loss')),
    system_code text UNIQUE,
    posted_balance_nano bigint NOT NULL DEFAULT 0,
    asset_reserved_nano bigint NOT NULL DEFAULT 0 CHECK (asset_reserved_nano >= 0),
    spend_authorized_nano bigint NOT NULL DEFAULT 0 CHECK (spend_authorized_nano >= 0),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (kind = 'user' AND identity_account_id IS NOT NULL AND system_code IS NULL)
        OR
        (kind <> 'user' AND identity_account_id IS NULL AND system_code = kind)
    ),
    CHECK (kind <> 'platform_incentive' OR posted_balance_nano >= 0)
);

INSERT INTO ledger_accounts (identity_account_id, kind)
SELECT id, 'user' FROM accounts;

INSERT INTO ledger_accounts (kind, system_code)
VALUES
    ('platform_incentive', 'platform_incentive'),
    ('platform_loss', 'platform_loss');

CREATE TABLE ledger_commands (
    idempotency_key text PRIMARY KEY CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    operation text NOT NULL,
    payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
    result_id uuid,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ledger_transactions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key text NOT NULL UNIQUE REFERENCES ledger_commands(idempotency_key) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN ('transfer', 'admin_adjustment', 'bad_debt_transfer', 'hold_capture', 'self_channel_usage', 'reversal')),
    reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 512),
    reference_type text NOT NULL CHECK (length(trim(reference_type)) BETWEEN 1 AND 64),
    reference_id text NOT NULL CHECK (length(trim(reference_id)) BETWEEN 1 AND 256),
    actor_account_id uuid REFERENCES accounts(id) ON DELETE RESTRICT,
    reversal_of_transaction_id uuid UNIQUE REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    sealed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (kind = 'reversal' AND reversal_of_transaction_id IS NOT NULL)
        OR (kind <> 'reversal' AND reversal_of_transaction_id IS NULL)
    )
);

CREATE INDEX ledger_transactions_reference_idx
    ON ledger_transactions(reference_type, reference_id, created_at DESC);

CREATE TABLE ledger_entries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transaction_id uuid NOT NULL REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    ledger_account_id uuid NOT NULL REFERENCES ledger_accounts(id) ON DELETE RESTRICT,
    entry_ordinal integer NOT NULL CHECK (entry_ordinal > 0),
    amount_nano bigint NOT NULL CHECK (amount_nano <> 0 AND amount_nano <> '-9223372036854775808'::bigint),
    posted_balance_before_nano bigint NOT NULL,
    posted_balance_after_nano bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (transaction_id, entry_ordinal),
    CHECK (posted_balance_after_nano::numeric = posted_balance_before_nano::numeric + amount_nano::numeric)
);

CREATE INDEX ledger_entries_account_idx
    ON ledger_entries(ledger_account_id, id DESC);

CREATE TABLE ledger_holds (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ledger_account_id uuid NOT NULL REFERENCES ledger_accounts(id) ON DELETE RESTRICT,
    create_idempotency_key text NOT NULL UNIQUE REFERENCES ledger_commands(idempotency_key) ON DELETE RESTRICT,
    purpose text NOT NULL CHECK (purpose IN ('asset_reservation', 'spend_authorization')),
    funding_policy text NOT NULL CHECK (funding_policy IN ('credit_allowed', 'settled_balance_only')),
    amount_nano bigint NOT NULL CHECK (amount_nano > 0),
    remaining_nano bigint NOT NULL CHECK (remaining_nano >= 0),
    captured_nano bigint NOT NULL DEFAULT 0 CHECK (captured_nano >= 0),
    released_nano bigint NOT NULL DEFAULT 0 CHECK (released_nano >= 0),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed')),
    reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 512),
    business_type text NOT NULL CHECK (length(trim(business_type)) BETWEEN 1 AND 64),
    business_id text NOT NULL CHECK (length(trim(business_id)) BETWEEN 1 AND 256),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (amount_nano = remaining_nano + captured_nano + released_nano),
    CHECK (
        (purpose = 'asset_reservation' AND funding_policy = 'settled_balance_only')
        OR (purpose = 'spend_authorization' AND funding_policy = 'credit_allowed')
    ),
    CHECK ((status = 'active' AND remaining_nano > 0) OR (status = 'closed' AND remaining_nano = 0))
);

CREATE INDEX ledger_holds_account_status_idx
    ON ledger_holds(ledger_account_id, status, created_at DESC);

CREATE TABLE ledger_hold_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    hold_id uuid NOT NULL REFERENCES ledger_holds(id) ON DELETE RESTRICT,
    idempotency_key text NOT NULL UNIQUE REFERENCES ledger_commands(idempotency_key) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN ('release', 'capture')),
    amount_nano bigint NOT NULL CHECK (amount_nano > 0),
    transaction_id uuid REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 512),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((kind = 'capture' AND transaction_id IS NOT NULL) OR (kind = 'release' AND transaction_id IS NULL))
);

-- Entries and transactions are assembled within one database transaction and
-- become immutable after sealing. The deferred trigger proves the final set is
-- balanced using numeric aggregation so even an overflowing BIGINT sum fails
-- closed instead of wrapping.
-- +goose StatementBegin
CREATE FUNCTION verify_ledger_transaction() RETURNS trigger AS $$
DECLARE
    target_id uuid;
    is_sealed boolean;
    entry_count bigint;
    entry_total numeric;
BEGIN
    IF TG_TABLE_NAME = 'ledger_transactions' THEN
        target_id := NEW.id;
    ELSE
        target_id := NEW.transaction_id;
    END IF;
    SELECT sealed INTO is_sealed FROM ledger_transactions WHERE id = target_id;
    IF is_sealed IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT count(*), COALESCE(sum(amount_nano::numeric), 0)
      INTO entry_count, entry_total
      FROM ledger_entries
     WHERE transaction_id = target_id;
    IF NOT is_sealed OR entry_count < 2 OR entry_total <> 0 THEN
        RAISE EXCEPTION 'ledger transaction % must be sealed with at least two balanced entries', target_id
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER ledger_transaction_balance
AFTER INSERT OR UPDATE ON ledger_transactions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_ledger_transaction();

CREATE CONSTRAINT TRIGGER ledger_entry_balance
AFTER INSERT ON ledger_entries
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_ledger_transaction();

-- +goose StatementBegin
CREATE FUNCTION protect_ledger_immutability() RETURNS trigger AS $$
DECLARE
    is_sealed boolean;
BEGIN
    IF TG_TABLE_NAME = 'ledger_entries' THEN
        IF TG_OP <> 'INSERT' THEN
            RAISE EXCEPTION 'ledger entries are immutable' USING ERRCODE = '55000';
        END IF;
        SELECT sealed INTO is_sealed FROM ledger_transactions WHERE id = NEW.transaction_id;
        IF is_sealed THEN
            RAISE EXCEPTION 'cannot append to sealed ledger transaction' USING ERRCODE = '55000';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.sealed = false AND NEW.sealed = true
       AND OLD.id = NEW.id
       AND OLD.idempotency_key = NEW.idempotency_key
       AND OLD.kind = NEW.kind
       AND OLD.reason = NEW.reason
       AND OLD.reference_type = NEW.reference_type
       AND OLD.reference_id = NEW.reference_id
       AND OLD.actor_account_id IS NOT DISTINCT FROM NEW.actor_account_id
       AND OLD.reversal_of_transaction_id IS NOT DISTINCT FROM NEW.reversal_of_transaction_id
       AND OLD.created_at = NEW.created_at THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'ledger transactions are immutable after creation' USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER ledger_entries_immutable
BEFORE INSERT OR UPDATE OR DELETE ON ledger_entries
FOR EACH ROW EXECUTE FUNCTION protect_ledger_immutability();

CREATE TRIGGER ledger_transactions_immutable
BEFORE UPDATE OR DELETE ON ledger_transactions
FOR EACH ROW EXECUTE FUNCTION protect_ledger_immutability();

-- +goose StatementBegin
CREATE FUNCTION protect_ledger_account_identity() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'ledger accounts cannot be deleted' USING ERRCODE = '55000';
    END IF;
    IF OLD.id <> NEW.id
       OR OLD.identity_account_id IS DISTINCT FROM NEW.identity_account_id
       OR OLD.kind <> NEW.kind
       OR OLD.system_code IS DISTINCT FROM NEW.system_code
       OR OLD.created_at <> NEW.created_at THEN
        RAISE EXCEPTION 'ledger account identity is immutable' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER ledger_account_identity_immutable
BEFORE UPDATE OR DELETE ON ledger_accounts
FOR EACH ROW EXECUTE FUNCTION protect_ledger_account_identity();

-- +goose StatementBegin
CREATE FUNCTION protect_hold_event_immutability() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ledger hold events are immutable' USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER ledger_hold_events_immutable
BEFORE UPDATE OR DELETE ON ledger_hold_events
FOR EACH ROW EXECUTE FUNCTION protect_hold_event_immutability();

-- +goose Down
DROP TRIGGER ledger_hold_events_immutable ON ledger_hold_events;
DROP FUNCTION protect_hold_event_immutability();
DROP TRIGGER ledger_account_identity_immutable ON ledger_accounts;
DROP FUNCTION protect_ledger_account_identity();
DROP TRIGGER ledger_transactions_immutable ON ledger_transactions;
DROP TRIGGER ledger_entries_immutable ON ledger_entries;
DROP FUNCTION protect_ledger_immutability();
DROP TRIGGER ledger_entry_balance ON ledger_entries;
DROP TRIGGER ledger_transaction_balance ON ledger_transactions;
DROP FUNCTION verify_ledger_transaction();
DROP TABLE ledger_hold_events;
DROP TABLE ledger_holds;
DROP TABLE ledger_entries;
DROP TABLE ledger_transactions;
DROP TABLE ledger_commands;
DROP TABLE ledger_accounts;
ALTER TABLE accounts DROP COLUMN credit_frozen;
