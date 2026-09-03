-- +goose Up
ALTER TABLE accounts
    ADD COLUMN credit_frozen boolean NOT NULL DEFAULT false;

CREATE TABLE ledger_accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_account_id uuid UNIQUE REFERENCES accounts(id) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN ('user', 'platform_incentive', 'platform_loss')),
    system_code text UNIQUE,
    posted_balance_nano bigint NOT NULL DEFAULT 0 CHECK (posted_balance_nano <> '-9223372036854775808'::bigint),
    asset_reserved_nano bigint NOT NULL DEFAULT 0 CHECK (asset_reserved_nano >= 0),
    spend_authorized_nano bigint NOT NULL DEFAULT 0 CHECK (spend_authorized_nano >= 0),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (kind = 'user' AND identity_account_id IS NOT NULL AND system_code IS NULL)
        OR
		(kind <> 'user' AND identity_account_id IS NULL AND system_code IS NOT NULL AND system_code = kind)
	)
);

CREATE UNIQUE INDEX ledger_accounts_system_kind_unique
    ON ledger_accounts(kind) WHERE kind <> 'user';

INSERT INTO ledger_accounts (identity_account_id, kind)
SELECT id, 'user' FROM accounts;

INSERT INTO ledger_accounts (kind, system_code)
VALUES
    ('platform_incentive', 'platform_incentive'),
    ('platform_loss', 'platform_loss');

CREATE TABLE ledger_commands (
    operation text NOT NULL CHECK (length(trim(operation)) BETWEEN 1 AND 64),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
    result_id uuid,
    result_payload jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (operation, idempotency_key),
    CHECK (
        (result_id IS NULL AND result_payload IS NULL AND completed_at IS NULL)
        OR (result_id IS NOT NULL AND result_payload IS NOT NULL AND completed_at IS NOT NULL)
    )
);

CREATE TABLE ledger_transactions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    command_operation text NOT NULL,
    idempotency_key text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('transfer', 'admin_adjustment', 'bad_debt_transfer', 'hold_capture', 'self_channel_usage', 'reversal')),
    reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 512),
    reference_type text NOT NULL CHECK (length(trim(reference_type)) BETWEEN 1 AND 64),
    reference_id text NOT NULL CHECK (length(trim(reference_id)) BETWEEN 1 AND 256),
    actor_account_id uuid REFERENCES accounts(id) ON DELETE RESTRICT,
    reversal_of_transaction_id uuid UNIQUE REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    sealed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (command_operation, idempotency_key),
    UNIQUE (reference_type, reference_id),
    FOREIGN KEY (command_operation, idempotency_key)
        REFERENCES ledger_commands(operation, idempotency_key) ON DELETE RESTRICT,
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
    business_role text NOT NULL CHECK (length(trim(business_role)) BETWEEN 1 AND 64),
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
    create_operation text NOT NULL DEFAULT 'hold.create' CHECK (create_operation = 'hold.create'),
    create_idempotency_key text NOT NULL,
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
    UNIQUE (create_operation, create_idempotency_key),
    UNIQUE (ledger_account_id, purpose, business_type, business_id),
    FOREIGN KEY (create_operation, create_idempotency_key)
        REFERENCES ledger_commands(operation, idempotency_key) ON DELETE RESTRICT,
    CHECK (amount_nano = remaining_nano + captured_nano + released_nano),
    CHECK (
        (purpose = 'asset_reservation' AND funding_policy = 'settled_balance_only')
        OR (purpose = 'spend_authorization' AND funding_policy = 'credit_allowed')
    ),
    CHECK ((status = 'active' AND remaining_nano > 0) OR (status = 'closed' AND remaining_nano = 0))
);

CREATE INDEX ledger_holds_account_status_idx
    ON ledger_holds(ledger_account_id, status, created_at DESC);

ALTER TABLE ledger_transactions
    ADD COLUMN hold_id uuid REFERENCES ledger_holds(id) ON DELETE RESTRICT,
    ADD CONSTRAINT ledger_transactions_capture_hold_check CHECK (
        (kind = 'hold_capture' AND command_operation = 'hold.capture' AND hold_id IS NOT NULL)
        OR (kind <> 'hold_capture' AND hold_id IS NULL)
    );

CREATE TABLE ledger_hold_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    hold_id uuid NOT NULL REFERENCES ledger_holds(id) ON DELETE RESTRICT,
    command_operation text NOT NULL,
    idempotency_key text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('release', 'capture')),
    business_id text NOT NULL CHECK (length(trim(business_id)) BETWEEN 1 AND 256),
    amount_nano bigint NOT NULL CHECK (amount_nano > 0),
    transaction_id uuid REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 512),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (command_operation, idempotency_key),
    UNIQUE (hold_id, business_id),
    UNIQUE (transaction_id),
    FOREIGN KEY (command_operation, idempotency_key)
        REFERENCES ledger_commands(operation, idempotency_key) ON DELETE RESTRICT,
    CHECK (
        (kind = 'capture' AND command_operation = 'hold.capture' AND transaction_id IS NOT NULL)
        OR (kind = 'release' AND command_operation = 'hold.release' AND transaction_id IS NULL)
    )
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
    target_kind text;
    reversal_target uuid;
    entry_count bigint;
    entry_total numeric;
    chain_errors bigint;
    ordinal_errors bigint;
    original_entry_count bigint;
    reversal_errors bigint;
    loss_entries bigint;
    bad_debt_user_credits bigint;
    bad_debt_loss_debits bigint;
BEGIN
    IF TG_TABLE_NAME = 'ledger_transactions' THEN
        target_id := NEW.id;
    ELSE
        target_id := NEW.transaction_id;
    END IF;
    SELECT sealed, kind, reversal_of_transaction_id
      INTO is_sealed, target_kind, reversal_target
      FROM ledger_transactions WHERE id = target_id;
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

    WITH ordered AS (
        SELECT entry_ordinal,
               row_number() OVER (ORDER BY id) AS insertion_ordinal
          FROM ledger_entries
         WHERE transaction_id = target_id
    )
    SELECT count(*) INTO ordinal_errors
      FROM ordered
     WHERE entry_ordinal <> insertion_ordinal;
    IF ordinal_errors <> 0 THEN
        RAISE EXCEPTION 'ledger transaction % entry ordinals must be contiguous and match insertion order', target_id
            USING ERRCODE = '23514';
    END IF;

    WITH affected AS (
        SELECT DISTINCT ledger_account_id
          FROM ledger_entries
         WHERE transaction_id = target_id
    ), ordered AS (
        SELECT e.posted_balance_before_nano::numeric AS balance_before,
               lag(e.posted_balance_after_nano::numeric) OVER (
                   PARTITION BY e.ledger_account_id ORDER BY e.id
               ) AS previous_after,
               row_number() OVER (
                   PARTITION BY e.ledger_account_id ORDER BY e.id
               ) AS position
          FROM ledger_entries e
          JOIN affected a ON a.ledger_account_id = e.ledger_account_id
    )
    SELECT count(*) INTO chain_errors
      FROM ordered
     WHERE (position = 1 AND balance_before <> 0)
        OR (position > 1 AND balance_before <> previous_after);
    IF chain_errors <> 0 THEN
        RAISE EXCEPTION 'ledger transaction % has a broken account balance chain', target_id
            USING ERRCODE = '23514';
    END IF;

    SELECT count(*) INTO loss_entries
      FROM ledger_entries e
      JOIN ledger_accounts la ON la.id = e.ledger_account_id
     WHERE e.transaction_id = target_id AND la.kind = 'platform_loss';
    IF loss_entries > 0 AND target_kind NOT IN ('bad_debt_transfer', 'reversal') THEN
        RAISE EXCEPTION 'platform loss account may only participate in bad debt and its reversal'
            USING ERRCODE = '23514';
    END IF;
    IF loss_entries > 0 AND target_kind = 'reversal'
       AND NOT EXISTS (
           SELECT 1 FROM ledger_transactions
            WHERE id = reversal_target AND kind = 'bad_debt_transfer'
       ) THEN
        RAISE EXCEPTION 'platform loss reversal must reference a bad debt transfer'
            USING ERRCODE = '23514';
    END IF;
    IF target_kind = 'reversal' THEN
        SELECT count(*) INTO original_entry_count
          FROM ledger_entries
         WHERE transaction_id = reversal_target;
        SELECT count(*) INTO reversal_errors
          FROM ledger_entries reversed
         WHERE reversed.transaction_id = target_id
           AND (
               reversed.business_role <> 'reversal'
               OR NOT EXISTS (
                   SELECT 1
                     FROM ledger_entries original
                    WHERE original.transaction_id = reversal_target
                      AND original.entry_ordinal = reversed.entry_ordinal
                      AND original.ledger_account_id = reversed.ledger_account_id
                      AND original.amount_nano::numeric = -reversed.amount_nano::numeric
               )
           );
        IF NOT EXISTS (
               SELECT 1
                 FROM ledger_transactions
                WHERE id = reversal_target
                  AND sealed
                  AND kind <> 'reversal'
           )
           OR original_entry_count <> entry_count
           OR reversal_errors <> 0 THEN
            RAISE EXCEPTION 'reversal must exactly negate one sealed non-reversal transaction'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    IF target_kind = 'bad_debt_transfer' THEN
        SELECT
            count(*) FILTER (
                WHERE la.kind = 'user'
                  AND e.amount_nano > 0
                  AND e.posted_balance_after_nano <= 0
            ),
            count(*) FILTER (
                WHERE la.kind = 'platform_loss' AND e.amount_nano < 0
            )
          INTO bad_debt_user_credits, bad_debt_loss_debits
          FROM ledger_entries e
          JOIN ledger_accounts la ON la.id = e.ledger_account_id
         WHERE e.transaction_id = target_id;
        IF entry_count <> 2 OR bad_debt_user_credits <> 1 OR bad_debt_loss_debits <> 1 THEN
            RAISE EXCEPTION 'bad debt transfer must move an existing user debt to platform loss'
                USING ERRCODE = '23514';
        END IF;
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
       AND OLD.command_operation = NEW.command_operation
       AND OLD.idempotency_key = NEW.idempotency_key
       AND OLD.kind = NEW.kind
       AND OLD.reason = NEW.reason
       AND OLD.reference_type = NEW.reference_type
       AND OLD.reference_id = NEW.reference_id
       AND OLD.actor_account_id IS NOT DISTINCT FROM NEW.actor_account_id
       AND OLD.reversal_of_transaction_id IS NOT DISTINCT FROM NEW.reversal_of_transaction_id
       AND OLD.hold_id IS NOT DISTINCT FROM NEW.hold_id
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

-- Hold identity and commercial meaning are immutable. Only the remaining,
-- captured and released totals, status and audit timestamp may advance.
-- +goose StatementBegin
CREATE FUNCTION protect_ledger_hold_identity() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'ledger holds cannot be deleted' USING ERRCODE = '55000';
    END IF;
    IF OLD.id <> NEW.id
       OR OLD.ledger_account_id <> NEW.ledger_account_id
       OR OLD.create_operation <> NEW.create_operation
       OR OLD.create_idempotency_key <> NEW.create_idempotency_key
       OR OLD.purpose <> NEW.purpose
       OR OLD.funding_policy <> NEW.funding_policy
       OR OLD.amount_nano <> NEW.amount_nano
       OR OLD.reason <> NEW.reason
       OR OLD.business_type <> NEW.business_type
       OR OLD.business_id <> NEW.business_id
       OR OLD.created_at <> NEW.created_at THEN
        RAISE EXCEPTION 'ledger hold identity is immutable' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER ledger_hold_identity_immutable
BEFORE UPDATE OR DELETE ON ledger_holds
FOR EACH ROW EXECUTE FUNCTION protect_ledger_hold_identity();

-- Commands are immutable once created. Their result may be completed exactly
-- once so a replay can return the first response snapshot forever.
-- +goose StatementBegin
CREATE FUNCTION protect_ledger_command_immutability() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'ledger commands cannot be deleted' USING ERRCODE = '55000';
    END IF;
    IF OLD.operation = NEW.operation
       AND OLD.idempotency_key = NEW.idempotency_key
       AND OLD.payload_hash = NEW.payload_hash
       AND OLD.created_at = NEW.created_at
       AND OLD.result_id IS NULL
       AND OLD.result_payload IS NULL
       AND OLD.completed_at IS NULL
       AND NEW.result_id IS NOT NULL
       AND NEW.result_payload IS NOT NULL
       AND NEW.completed_at IS NOT NULL THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'ledger commands are immutable after completion' USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER ledger_commands_immutable
BEFORE UPDATE OR DELETE ON ledger_commands
FOR EACH ROW EXECUTE FUNCTION protect_ledger_command_immutability();

-- Reserving an idempotency key is only an intermediate state inside the same
-- transaction. No caller may commit an incomplete command and poison replays.
-- +goose StatementBegin
CREATE FUNCTION verify_ledger_command_completion() RETURNS trigger AS $$
DECLARE
    is_completed boolean;
BEGIN
    SELECT result_id IS NOT NULL AND result_payload IS NOT NULL AND completed_at IS NOT NULL
      INTO is_completed
      FROM ledger_commands
     WHERE operation = NEW.operation AND idempotency_key = NEW.idempotency_key;
    IF NOT COALESCE(is_completed, false) THEN
        RAISE EXCEPTION 'ledger command %.% must be completed before commit', NEW.operation, NEW.idempotency_key
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER ledger_command_completion
AFTER INSERT OR UPDATE ON ledger_commands
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_ledger_command_completion();

-- Every projection must equal the immutable source records at commit. These
-- constraint triggers also catch a direct projection or hold total update.
-- +goose StatementBegin
CREATE FUNCTION verify_ledger_account_projection() RETURNS trigger AS $$
DECLARE
    target_id uuid;
    posted numeric;
    reserved numeric;
    authorized numeric;
    posted_source numeric;
    reserved_source numeric;
    authorized_source numeric;
BEGIN
    IF TG_TABLE_NAME = 'ledger_accounts' THEN
        target_id := NEW.id;
    ELSIF TG_TABLE_NAME = 'ledger_entries' THEN
        target_id := NEW.ledger_account_id;
    ELSE
        target_id := NEW.ledger_account_id;
    END IF;
    SELECT posted_balance_nano::numeric, asset_reserved_nano::numeric, spend_authorized_nano::numeric
      INTO posted, reserved, authorized
      FROM ledger_accounts WHERE id = target_id;
    IF posted IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT COALESCE(sum(amount_nano::numeric), 0)
      INTO posted_source FROM ledger_entries WHERE ledger_account_id = target_id;
    SELECT
        COALESCE(sum(remaining_nano::numeric) FILTER (WHERE purpose = 'asset_reservation'), 0),
        COALESCE(sum(remaining_nano::numeric) FILTER (WHERE purpose = 'spend_authorization'), 0)
      INTO reserved_source, authorized_source
      FROM ledger_holds WHERE ledger_account_id = target_id;
    IF posted <> posted_source OR reserved <> reserved_source OR authorized <> authorized_source THEN
        RAISE EXCEPTION 'ledger account % projection does not match immutable sources', target_id
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER ledger_account_projection_from_account
AFTER INSERT OR UPDATE ON ledger_accounts
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_ledger_account_projection();

CREATE CONSTRAINT TRIGGER ledger_account_projection_from_entry
AFTER INSERT ON ledger_entries
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_ledger_account_projection();

CREATE CONSTRAINT TRIGGER ledger_account_projection_from_hold
AFTER INSERT OR UPDATE ON ledger_holds
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_ledger_account_projection();

-- Mutable hold totals must be exactly the aggregate of their immutable events.
-- +goose StatementBegin
CREATE FUNCTION verify_ledger_hold_state() RETURNS trigger AS $$
DECLARE
    target_id uuid;
    captured numeric;
    released numeric;
    captured_source numeric;
    released_source numeric;
BEGIN
    IF TG_TABLE_NAME = 'ledger_holds' THEN
        target_id := NEW.id;
    ELSE
        target_id := NEW.hold_id;
    END IF;
    SELECT captured_nano::numeric, released_nano::numeric
      INTO captured, released FROM ledger_holds WHERE id = target_id;
    IF captured IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT
        COALESCE(sum(amount_nano::numeric) FILTER (WHERE kind = 'capture'), 0),
        COALESCE(sum(amount_nano::numeric) FILTER (WHERE kind = 'release'), 0)
      INTO captured_source, released_source
      FROM ledger_hold_events WHERE hold_id = target_id;
    IF captured <> captured_source OR released <> released_source THEN
        RAISE EXCEPTION 'ledger hold % totals do not match immutable events', target_id
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER ledger_hold_state_from_hold
AFTER INSERT OR UPDATE ON ledger_holds
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_ledger_hold_state();

CREATE CONSTRAINT TRIGGER ledger_hold_state_from_event
AFTER INSERT ON ledger_hold_events
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_ledger_hold_state();

-- Only a currently active, unfrozen user may create a hold, and the aggregate
-- projection after insertion must remain within the policy's funding source.
-- Later credit reductions or freezes do not invalidate an already-created hold.
-- +goose StatementBegin
CREATE FUNCTION verify_ledger_hold_creation() RETURNS trigger AS $$
DECLARE
    account_kind text;
    posted numeric;
    reserved numeric;
    authorized numeric;
    credit numeric;
    frozen boolean;
    account_status text;
BEGIN
    SELECT la.kind,
           la.posted_balance_nano::numeric,
           la.asset_reserved_nano::numeric,
           la.spend_authorized_nano::numeric,
           COALESCE(a.credit_limit_nano::numeric, 0),
           COALESCE(a.credit_frozen, false),
           COALESCE(a.status, '')
      INTO account_kind, posted, reserved, authorized, credit, frozen, account_status
      FROM ledger_accounts la
      LEFT JOIN accounts a ON a.id = la.identity_account_id
     WHERE la.id = NEW.ledger_account_id;
    IF account_kind <> 'user' OR account_status <> 'active' OR frozen THEN
        RAISE EXCEPTION 'new ledger hold requires an active unfrozen user account'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.funding_policy = 'settled_balance_only' AND posted - reserved - authorized < 0 THEN
        RAISE EXCEPTION 'asset reservation exceeds settled balance capacity'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.funding_policy = 'credit_allowed' AND posted + credit - reserved - authorized < 0 THEN
        RAISE EXCEPTION 'spend authorization exceeds credit-backed capacity'
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER ledger_hold_creation_eligibility
AFTER INSERT ON ledger_holds
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_ledger_hold_creation();

-- A capture transaction and its immutable hold event are one atomic fact. The
-- transaction identifies the hold, command and source debit; the event records
-- the exact captured amount. Either side without its counterpart is invalid.
-- +goose StatementBegin
CREATE FUNCTION verify_ledger_capture_link() RETURNS trigger AS $$
DECLARE
    target_transaction_id uuid;
    target_kind text;
    target_hold_id uuid;
    target_operation text;
    target_key text;
    event_count bigint;
    event_amount numeric;
    source_account_id uuid;
    hold_purpose text;
    negative_entry_count bigint;
    source_debit_count bigint;
    source_debit_total numeric;
    source_role_errors bigint;
    provider_entries bigint;
    buyer_entries bigint;
    fee_entries bigint;
    positive_role_errors bigint;
    positive_account_errors bigint;
BEGIN
    IF TG_TABLE_NAME = 'ledger_hold_events' THEN
        IF NEW.transaction_id IS NULL THEN
            RETURN NULL;
        END IF;
        target_transaction_id := NEW.transaction_id;
    ELSE
        target_transaction_id := NEW.id;
    END IF;

    SELECT kind, hold_id, command_operation, idempotency_key
      INTO target_kind, target_hold_id, target_operation, target_key
      FROM ledger_transactions
     WHERE id = target_transaction_id;
    IF target_kind IS NULL THEN
        RETURN NULL;
    END IF;

    SELECT count(*), COALESCE(max(amount_nano::numeric), 0)
      INTO event_count, event_amount
      FROM ledger_hold_events
     WHERE transaction_id = target_transaction_id
       AND kind = 'capture'
       AND hold_id = target_hold_id
       AND command_operation = target_operation
       AND idempotency_key = target_key;

    IF target_kind <> 'hold_capture' THEN
        IF EXISTS (
            SELECT 1 FROM ledger_hold_events
             WHERE transaction_id = target_transaction_id
        ) THEN
            RAISE EXCEPTION 'only hold capture transactions may be referenced by capture events'
                USING ERRCODE = '23514';
        END IF;
        RETURN NULL;
    END IF;

    IF event_count <> 1 THEN
        RAISE EXCEPTION 'hold capture transaction % must have one matching capture event', target_transaction_id
            USING ERRCODE = '23514';
    END IF;

    SELECT ledger_account_id, purpose
      INTO source_account_id, hold_purpose
      FROM ledger_holds
     WHERE id = target_hold_id;
    SELECT
        count(*) FILTER (WHERE amount_nano < 0),
        count(*) FILTER (WHERE ledger_account_id = source_account_id AND amount_nano < 0),
        COALESCE(-sum(amount_nano::numeric) FILTER (
            WHERE ledger_account_id = source_account_id AND amount_nano < 0
        ), 0),
        count(*) FILTER (
            WHERE ledger_account_id = source_account_id
              AND amount_nano < 0
              AND business_role <> CASE
                  WHEN hold_purpose = 'asset_reservation' THEN 'seller'
                  ELSE 'consumer'
              END
        )
      INTO negative_entry_count, source_debit_count, source_debit_total, source_role_errors
      FROM ledger_entries
     WHERE transaction_id = target_transaction_id;
    IF negative_entry_count <> 1
       OR source_debit_count <> 1
       OR source_debit_total <> event_amount
       OR source_role_errors <> 0 THEN
        RAISE EXCEPTION 'hold capture transaction % does not match its source hold debit', target_transaction_id
            USING ERRCODE = '23514';
    END IF;
    SELECT
        count(*) FILTER (WHERE e.amount_nano > 0 AND e.business_role = 'provider'),
        count(*) FILTER (WHERE e.amount_nano > 0 AND e.business_role = 'buyer'),
        count(*) FILTER (WHERE e.amount_nano > 0 AND e.business_role = 'platform_fee'),
        count(*) FILTER (
            WHERE e.amount_nano > 0
              AND e.business_role NOT IN ('provider', 'buyer', 'platform_fee')
        ),
        count(*) FILTER (
            WHERE e.amount_nano > 0
              AND (
                  (e.business_role IN ('provider', 'buyer') AND (
                      la.kind <> 'user' OR e.ledger_account_id = source_account_id
                  ))
                  OR (e.business_role = 'platform_fee' AND la.kind <> 'platform_incentive')
              )
        )
      INTO provider_entries, buyer_entries, fee_entries, positive_role_errors, positive_account_errors
      FROM ledger_entries e
      JOIN ledger_accounts la ON la.id = e.ledger_account_id
     WHERE e.transaction_id = target_transaction_id;
    IF positive_role_errors <> 0 OR positive_account_errors <> 0
       OR (hold_purpose = 'spend_authorization' AND (provider_entries <> 1 OR buyer_entries <> 0 OR fee_entries > 1))
       OR (hold_purpose = 'asset_reservation' AND (buyer_entries <> 1 OR provider_entries <> 0 OR fee_entries <> 0)) THEN
        RAISE EXCEPTION 'hold capture transaction % has an invalid destination shape', target_transaction_id
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER ledger_capture_link_from_transaction
AFTER INSERT OR UPDATE ON ledger_transactions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_ledger_capture_link();

CREATE CONSTRAINT TRIGGER ledger_capture_link_from_event
AFTER INSERT ON ledger_hold_events
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_ledger_capture_link();

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
DROP TRIGGER ledger_capture_link_from_event ON ledger_hold_events;
DROP TRIGGER ledger_capture_link_from_transaction ON ledger_transactions;
DROP FUNCTION verify_ledger_capture_link();
DROP TRIGGER ledger_hold_creation_eligibility ON ledger_holds;
DROP FUNCTION verify_ledger_hold_creation();
DROP TRIGGER ledger_hold_state_from_event ON ledger_hold_events;
DROP TRIGGER ledger_hold_state_from_hold ON ledger_holds;
DROP FUNCTION verify_ledger_hold_state();
DROP TRIGGER ledger_account_projection_from_hold ON ledger_holds;
DROP TRIGGER ledger_account_projection_from_entry ON ledger_entries;
DROP TRIGGER ledger_account_projection_from_account ON ledger_accounts;
DROP FUNCTION verify_ledger_account_projection();
DROP TRIGGER ledger_command_completion ON ledger_commands;
DROP FUNCTION verify_ledger_command_completion();
DROP TRIGGER ledger_commands_immutable ON ledger_commands;
DROP FUNCTION protect_ledger_command_immutability();
DROP TRIGGER ledger_hold_identity_immutable ON ledger_holds;
DROP FUNCTION protect_ledger_hold_identity();
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
DROP TABLE ledger_entries;
DROP TABLE ledger_transactions;
DROP TABLE ledger_holds;
DROP TABLE ledger_commands;
DROP INDEX ledger_accounts_system_kind_unique;
DROP TABLE ledger_accounts;
ALTER TABLE accounts DROP COLUMN credit_frozen;
