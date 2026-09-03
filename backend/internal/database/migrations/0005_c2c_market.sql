-- +goose Up
CREATE TABLE c2c_commands (
    actor_key text NOT NULL CHECK (length(trim(actor_key)) BETWEEN 1 AND 128),
    actor_account_id uuid REFERENCES accounts(id) ON DELETE RESTRICT,
    operation text NOT NULL CHECK (length(trim(operation)) BETWEEN 1 AND 64),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
    result_payload jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (actor_key, operation, idempotency_key),
    CHECK (
        (actor_account_id IS NOT NULL AND actor_key = actor_account_id::text)
        OR (actor_account_id IS NULL AND actor_key LIKE 'system:%')
    ),
    CHECK ((result_payload IS NULL AND completed_at IS NULL) OR (result_payload IS NOT NULL AND completed_at IS NOT NULL))
);

CREATE TABLE c2c_orders (
    id uuid PRIMARY KEY,
    owner_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    side text NOT NULL CHECK (side IN ('sell', 'buy')),
    unit_price_fen bigint NOT NULL CHECK (unit_price_fen > 0),
    total_nano bigint NOT NULL CHECK (total_nano > 0),
    available_nano bigint NOT NULL CHECK (available_nano >= 0),
    allocated_nano bigint NOT NULL DEFAULT 0 CHECK (allocated_nano >= 0),
    settled_nano bigint NOT NULL DEFAULT 0 CHECK (settled_nano >= 0),
    closed_nano bigint NOT NULL DEFAULT 0 CHECK (closed_nano >= 0),
    minimum_nano bigint NOT NULL CHECK (minimum_nano > 0),
    maximum_nano bigint NOT NULL CHECK (maximum_nano > 0),
    status text NOT NULL CHECK (status IN ('open', 'allocated', 'filled', 'cancelled')),
    parent_hold_id uuid REFERENCES ledger_holds(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    cancelled_at timestamptz,
    CHECK (total_nano::numeric = available_nano::numeric + allocated_nano::numeric + settled_nano::numeric + closed_nano::numeric),
    CHECK (minimum_nano <= maximum_nano AND maximum_nano <= total_nano),
    CHECK ((side = 'sell' AND parent_hold_id IS NOT NULL) OR (side = 'buy' AND parent_hold_id IS NULL)),
    CHECK ((status = 'cancelled') = (cancelled_at IS NOT NULL))
);

CREATE INDEX c2c_orders_sell_book_idx ON c2c_orders(unit_price_fen, created_at, id) WHERE side = 'sell' AND status = 'open' AND available_nano > 0;
CREATE INDEX c2c_orders_buy_book_idx ON c2c_orders(unit_price_fen DESC, created_at, id) WHERE side = 'buy' AND status = 'open' AND available_nano > 0;
CREATE INDEX c2c_orders_owner_idx ON c2c_orders(owner_account_id, updated_at DESC, id DESC);

CREATE TABLE c2c_payment_methods (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES c2c_orders(id) ON DELETE RESTRICT,
    method_type text NOT NULL CHECK (method_type IN ('wechat', 'alipay', 'bank_transfer', 'other')),
    position integer NOT NULL CHECK (position BETWEEN 1 AND 5),
    qr_available boolean NOT NULL DEFAULT false,
    key_id text NOT NULL CHECK (length(trim(key_id)) BETWEEN 1 AND 64),
    nonce bytea NOT NULL CHECK (octet_length(nonce) = 12),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) > 16),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (order_id, position),
    UNIQUE (order_id, id)
);

CREATE TABLE c2c_trades (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES c2c_orders(id) ON DELETE RESTRICT,
    buyer_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    seller_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    quantity_nano bigint NOT NULL CHECK (quantity_nano > 0),
    unit_price_fen bigint NOT NULL CHECK (unit_price_fen > 0),
    fiat_amount_fen bigint NOT NULL CHECK (fiat_amount_fen > 0),
    status text NOT NULL CHECK (status IN (
        'awaiting_payment', 'paid', 'disputed', 'released_to_buyer',
        'returned_to_seller', 'cancelled', 'expired'
    )),
    hold_id uuid NOT NULL REFERENCES ledger_holds(id) ON DELETE RESTRICT,
    selected_payment_method_id uuid NOT NULL,
    payment_reference_chars integer NOT NULL DEFAULT 0 CHECK (payment_reference_chars BETWEEN 0 AND 256),
    payment_reference_key_id text CHECK (payment_reference_key_id IS NULL OR length(trim(payment_reference_key_id)) BETWEEN 1 AND 64),
    payment_reference_nonce bytea CHECK (payment_reference_nonce IS NULL OR octet_length(payment_reference_nonce) = 12),
    payment_reference_ciphertext bytea CHECK (payment_reference_ciphertext IS NULL OR octet_length(payment_reference_ciphertext) > 16),
    payment_reference_deleted_at timestamptz,
    payment_deadline timestamptz NOT NULL,
    review_due_at timestamptz,
    ledger_transaction_id uuid UNIQUE REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    paid_at timestamptz,
    resolved_at timestamptz,
    FOREIGN KEY (order_id, selected_payment_method_id)
        REFERENCES c2c_payment_methods(order_id, id) ON DELETE RESTRICT,
    CHECK (buyer_account_id <> seller_account_id),
    CHECK (payment_deadline > created_at),
    CHECK ((status IN ('paid', 'disputed', 'released_to_buyer', 'returned_to_seller') AND paid_at IS NOT NULL) OR (status IN ('awaiting_payment', 'cancelled', 'expired') AND paid_at IS NULL)),
    CHECK ((status IN ('released_to_buyer', 'returned_to_seller', 'cancelled', 'expired') AND resolved_at IS NOT NULL) OR (status NOT IN ('released_to_buyer', 'returned_to_seller', 'cancelled', 'expired') AND resolved_at IS NULL)),
    CHECK ((status = 'released_to_buyer' AND ledger_transaction_id IS NOT NULL) OR (status <> 'released_to_buyer' AND ledger_transaction_id IS NULL)),
    CHECK (
        (payment_reference_chars = 0 AND payment_reference_key_id IS NULL AND payment_reference_nonce IS NULL AND payment_reference_ciphertext IS NULL AND payment_reference_deleted_at IS NULL)
        OR (payment_reference_chars > 0 AND payment_reference_key_id IS NOT NULL AND payment_reference_nonce IS NOT NULL AND payment_reference_ciphertext IS NOT NULL AND payment_reference_deleted_at IS NULL)
        OR (payment_reference_chars > 0 AND payment_reference_key_id IS NULL AND payment_reference_nonce IS NULL AND payment_reference_ciphertext IS NULL AND payment_reference_deleted_at IS NOT NULL)
    )
);

CREATE INDEX c2c_trades_order_idx ON c2c_trades(order_id, created_at, id);
CREATE INDEX c2c_trades_buyer_idx ON c2c_trades(buyer_account_id, updated_at DESC, id DESC);
CREATE INDEX c2c_trades_seller_idx ON c2c_trades(seller_account_id, updated_at DESC, id DESC);
CREATE INDEX c2c_trades_due_idx ON c2c_trades(payment_deadline, id) WHERE status = 'awaiting_payment';
CREATE INDEX c2c_trades_disputed_idx ON c2c_trades(updated_at, id) WHERE status = 'disputed';

CREATE TABLE c2c_dispute_statements (
    id uuid PRIMARY KEY,
    trade_id uuid NOT NULL REFERENCES c2c_trades(id) ON DELETE RESTRICT,
    actor_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    character_count integer NOT NULL CHECK (character_count BETWEEN 1 AND 2000),
    key_id text CHECK (key_id IS NULL OR length(trim(key_id)) BETWEEN 1 AND 64),
    nonce bytea CHECK (nonce IS NULL OR octet_length(nonce) = 12),
    ciphertext bytea CHECK (ciphertext IS NULL OR octet_length(ciphertext) > 16),
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CHECK (
        (deleted_at IS NULL AND key_id IS NOT NULL AND nonce IS NOT NULL AND ciphertext IS NOT NULL)
        OR (deleted_at IS NOT NULL AND key_id IS NULL AND nonce IS NULL AND ciphertext IS NULL)
    )
);

CREATE INDEX c2c_dispute_statements_trade_idx ON c2c_dispute_statements(trade_id, created_at, id);

CREATE TABLE c2c_evidence (
    id uuid PRIMARY KEY,
    trade_id uuid NOT NULL REFERENCES c2c_trades(id) ON DELETE RESTRICT,
    uploader_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN ('payment', 'dispute')),
    mime_type text NOT NULL CHECK (mime_type IN ('image/jpeg', 'image/png')),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 5242880),
    width integer NOT NULL CHECK (width > 0),
    height integer NOT NULL CHECK (height > 0),
    sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
    key_id text CHECK (key_id IS NULL OR length(trim(key_id)) BETWEEN 1 AND 64),
    nonce bytea CHECK (nonce IS NULL OR octet_length(nonce) = 12),
    ciphertext bytea CHECK (ciphertext IS NULL OR octet_length(ciphertext) > 16),
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CHECK (width::bigint * height::bigint <= 20000000),
    CHECK (
        (deleted_at IS NULL AND key_id IS NOT NULL AND nonce IS NOT NULL AND ciphertext IS NOT NULL)
        OR (deleted_at IS NOT NULL AND key_id IS NULL AND nonce IS NULL AND ciphertext IS NULL)
    )
);

CREATE UNIQUE INDEX c2c_payment_evidence_unique ON c2c_evidence(trade_id) WHERE kind = 'payment';
CREATE INDEX c2c_evidence_trade_idx ON c2c_evidence(trade_id, created_at, id);
CREATE INDEX c2c_evidence_cleanup_idx ON c2c_evidence(created_at, id) WHERE deleted_at IS NULL;

CREATE TABLE c2c_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES c2c_orders(id) ON DELETE RESTRICT,
    trade_id uuid REFERENCES c2c_trades(id) ON DELETE RESTRICT,
    actor_account_id uuid REFERENCES accounts(id) ON DELETE RESTRICT,
    action text NOT NULL CHECK (length(trim(action)) BETWEEN 1 AND 64),
    reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 512),
    ledger_transaction_id uuid REFERENCES ledger_transactions(id) ON DELETE RESTRICT,
    hold_business_id text CHECK (hold_business_id IS NULL OR length(trim(hold_business_id)) BETWEEN 1 AND 256),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX c2c_events_order_idx ON c2c_events(order_id, id);
CREATE INDEX c2c_events_trade_idx ON c2c_events(trade_id, id) WHERE trade_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION verify_c2c_command_complete() RETURNS trigger AS $$
DECLARE
    is_completed boolean;
BEGIN
    SELECT completed_at IS NOT NULL AND result_payload IS NOT NULL
      INTO is_completed
      FROM c2c_commands
     WHERE actor_key = NEW.actor_key
       AND operation = NEW.operation
       AND idempotency_key = NEW.idempotency_key;
    IF NOT COALESCE(is_completed, false) THEN
        RAISE EXCEPTION 'C2C command must commit with a result snapshot' USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER c2c_command_complete
AFTER INSERT OR UPDATE ON c2c_commands
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_c2c_command_complete();

-- +goose StatementBegin
CREATE FUNCTION verify_c2c_order_invariants() RETURNS trigger AS $$
DECLARE
    target_order_id uuid;
    item c2c_orders%ROWTYPE;
    hold ledger_holds%ROWTYPE;
BEGIN
    IF TG_TABLE_NAME = 'c2c_orders' THEN
        target_order_id := NEW.id;
    ELSE
        SELECT id INTO target_order_id FROM c2c_orders WHERE parent_hold_id = NEW.id;
        IF target_order_id IS NULL THEN
            RETURN NULL;
        END IF;
    END IF;
    SELECT * INTO item FROM c2c_orders WHERE id = target_order_id;
    IF item.id IS NULL THEN
        RETURN NULL;
    END IF;
    IF item.status = 'cancelled' THEN
        IF item.available_nano <> 0 THEN
            RAISE EXCEPTION 'cancelled C2C order cannot remain matchable' USING ERRCODE = '23514';
        END IF;
    ELSIF item.available_nano > 0 AND item.status <> 'open' THEN
        RAISE EXCEPTION 'matchable C2C order must be open' USING ERRCODE = '23514';
    ELSIF item.available_nano = 0 AND item.allocated_nano > 0 AND item.status <> 'allocated' THEN
        RAISE EXCEPTION 'fully allocated C2C order has invalid state' USING ERRCODE = '23514';
    ELSIF item.available_nano = 0 AND item.allocated_nano = 0 AND item.settled_nano = item.total_nano AND item.status <> 'filled' THEN
        RAISE EXCEPTION 'fully settled C2C order must be filled' USING ERRCODE = '23514';
    END IF;
    IF item.side = 'sell' THEN
        SELECT * INTO hold FROM ledger_holds WHERE id = item.parent_hold_id;
        IF hold.id IS NULL
           OR hold.purpose <> 'asset_reservation'
           OR hold.funding_policy <> 'settled_balance_only'
           OR hold.business_type <> 'c2c_sell_order'
           OR hold.business_id <> item.id::text
           OR hold.amount_nano <> item.total_nano
           OR hold.remaining_nano <> item.available_nano + item.allocated_nano
           OR hold.captured_nano <> item.settled_nano
           OR hold.released_nano <> item.closed_nano
           OR NOT EXISTS (
               SELECT 1 FROM ledger_accounts la
                WHERE la.id = hold.ledger_account_id
                  AND la.identity_account_id = item.owner_account_id
           ) THEN
            RAISE EXCEPTION 'C2C sell order and asset hold diverged' USING ERRCODE = '23514';
        END IF;
    ELSIF item.parent_hold_id IS NOT NULL THEN
        RAISE EXCEPTION 'C2C buy order cannot own a parent hold' USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER c2c_order_invariants_from_order
AFTER INSERT OR UPDATE ON c2c_orders
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_c2c_order_invariants();

CREATE CONSTRAINT TRIGGER c2c_order_invariants_from_hold
AFTER INSERT OR UPDATE ON ledger_holds
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_c2c_order_invariants();

-- +goose StatementBegin
CREATE FUNCTION verify_c2c_trade_invariants_row(target_trade_id uuid) RETURNS void AS $$
DECLARE
    item c2c_trades%ROWTYPE;
    parent c2c_orders%ROWTYPE;
    hold ledger_holds%ROWTYPE;
    effect_kind text;
    effect_transaction uuid;
BEGIN
    SELECT * INTO item FROM c2c_trades WHERE id = target_trade_id;
    IF item.id IS NULL THEN
        RETURN;
    END IF;
    SELECT * INTO parent FROM c2c_orders WHERE id = item.order_id;
    SELECT * INTO hold FROM ledger_holds WHERE id = item.hold_id;
    IF parent.id IS NULL OR hold.id IS NULL THEN
        RAISE EXCEPTION 'C2C trade references missing order or hold' USING ERRCODE = '23514';
    END IF;
    IF (parent.side = 'sell' AND (item.seller_account_id <> parent.owner_account_id OR item.hold_id <> parent.parent_hold_id))
       OR (parent.side = 'buy' AND item.buyer_account_id <> parent.owner_account_id) THEN
        RAISE EXCEPTION 'C2C trade parties do not match parent order' USING ERRCODE = '23514';
    END IF;
    IF parent.side = 'buy' AND (
        hold.purpose <> 'asset_reservation'
        OR hold.funding_policy <> 'settled_balance_only'
        OR hold.business_type <> 'c2c_trade'
        OR hold.business_id <> item.id::text
        OR hold.amount_nano <> item.quantity_nano
        OR NOT EXISTS (
            SELECT 1 FROM ledger_accounts la
             WHERE la.id = hold.ledger_account_id
               AND la.identity_account_id = item.seller_account_id
        )
    ) THEN
        RAISE EXCEPTION 'C2C buy-order trade hold is invalid' USING ERRCODE = '23514';
    END IF;
    SELECT kind, transaction_id INTO effect_kind, effect_transaction
      FROM ledger_hold_events
     WHERE hold_id = item.hold_id AND business_id = item.id::text;
    IF item.status IN ('awaiting_payment', 'paid', 'disputed') AND effect_kind IS NOT NULL THEN
        RAISE EXCEPTION 'nonterminal C2C trade cannot have a hold effect' USING ERRCODE = '23514';
    END IF;
    IF item.status = 'released_to_buyer' AND (effect_kind <> 'capture' OR effect_transaction <> item.ledger_transaction_id) THEN
        RAISE EXCEPTION 'released C2C trade must match one hold capture' USING ERRCODE = '23514';
    END IF;
    IF parent.side = 'buy' THEN
        IF item.status IN ('awaiting_payment', 'paid', 'disputed')
           AND (hold.remaining_nano <> item.quantity_nano OR hold.captured_nano <> 0 OR hold.released_nano <> 0) THEN
            RAISE EXCEPTION 'active C2C buy-order trade hold diverged' USING ERRCODE = '23514';
        ELSIF item.status = 'released_to_buyer'
           AND (hold.captured_nano <> item.quantity_nano OR hold.remaining_nano <> 0) THEN
            RAISE EXCEPTION 'settled C2C buy-order trade hold diverged' USING ERRCODE = '23514';
        ELSIF item.status IN ('returned_to_seller', 'cancelled', 'expired')
           AND (hold.released_nano <> item.quantity_nano OR hold.remaining_nano <> 0 OR effect_kind <> 'release') THEN
            RAISE EXCEPTION 'returned C2C buy-order trade hold diverged' USING ERRCODE = '23514';
        END IF;
    ELSIF item.status IN ('returned_to_seller', 'cancelled', 'expired') THEN
        IF parent.status = 'cancelled' AND effect_kind <> 'release' THEN
            RAISE EXCEPTION 'trade under cancelled sell order must release its allocation' USING ERRCODE = '23514';
        ELSIF parent.status <> 'cancelled' AND effect_kind IS NOT NULL THEN
            RAISE EXCEPTION 'trade under open sell order must restore allocation without release' USING ERRCODE = '23514';
        END IF;
    END IF;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION verify_c2c_trade_invariants() RETURNS trigger AS $$
DECLARE
    target_trade_id uuid;
BEGIN
    IF TG_TABLE_NAME = 'c2c_trades' THEN
        target_trade_id := NEW.id;
    ELSE
        FOR target_trade_id IN SELECT id FROM c2c_trades WHERE hold_id = NEW.id LOOP
            PERFORM verify_c2c_trade_invariants_row(target_trade_id);
        END LOOP;
        RETURN NULL;
    END IF;
    PERFORM verify_c2c_trade_invariants_row(target_trade_id);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER c2c_trade_invariants_from_trade
AFTER INSERT OR UPDATE ON c2c_trades
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_c2c_trade_invariants();

CREATE CONSTRAINT TRIGGER c2c_trade_invariants_from_hold
AFTER INSERT OR UPDATE ON ledger_holds
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_c2c_trade_invariants();

-- +goose StatementBegin
CREATE FUNCTION guard_c2c_order_history() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'C2C order history cannot be physically deleted' USING ERRCODE = '23514';
    END IF;
    IF (
        NEW.id <> OLD.id OR NEW.owner_account_id <> OLD.owner_account_id OR NEW.side <> OLD.side
        OR NEW.unit_price_fen <> OLD.unit_price_fen OR NEW.total_nano <> OLD.total_nano
        OR NEW.minimum_nano <> OLD.minimum_nano OR NEW.maximum_nano <> OLD.maximum_nano
        OR NEW.parent_hold_id IS DISTINCT FROM OLD.parent_hold_id OR NEW.created_at <> OLD.created_at
    ) THEN
        RAISE EXCEPTION 'C2C order commercial identity is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION guard_c2c_trade_history() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'C2C trade history cannot be physically deleted' USING ERRCODE = '23514';
    END IF;
    IF (
        NEW.id <> OLD.id OR NEW.order_id <> OLD.order_id OR NEW.buyer_account_id <> OLD.buyer_account_id
        OR NEW.seller_account_id <> OLD.seller_account_id OR NEW.quantity_nano <> OLD.quantity_nano
        OR NEW.unit_price_fen <> OLD.unit_price_fen OR NEW.fiat_amount_fen <> OLD.fiat_amount_fen
        OR NEW.hold_id <> OLD.hold_id OR NEW.selected_payment_method_id <> OLD.selected_payment_method_id
        OR NEW.payment_deadline <> OLD.payment_deadline OR NEW.created_at <> OLD.created_at
    ) THEN
        RAISE EXCEPTION 'C2C trade commercial identity is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER c2c_orders_history_guard BEFORE UPDATE OR DELETE ON c2c_orders FOR EACH ROW EXECUTE FUNCTION guard_c2c_order_history();
CREATE TRIGGER c2c_trades_history_guard BEFORE UPDATE OR DELETE ON c2c_trades FOR EACH ROW EXECUTE FUNCTION guard_c2c_trade_history();

-- +goose StatementBegin
CREATE FUNCTION reject_c2c_immutable_change() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'C2C record is immutable' USING ERRCODE = '23514';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER c2c_payment_methods_immutable BEFORE UPDATE OR DELETE ON c2c_payment_methods FOR EACH ROW EXECUTE FUNCTION reject_c2c_immutable_change();
CREATE TRIGGER c2c_events_immutable BEFORE UPDATE OR DELETE ON c2c_events FOR EACH ROW EXECUTE FUNCTION reject_c2c_immutable_change();

-- +goose StatementBegin
CREATE FUNCTION guard_c2c_statement() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'C2C statement metadata cannot be deleted' USING ERRCODE = '23514';
    END IF;
    IF OLD.deleted_at IS NOT NULL OR NEW.id <> OLD.id OR NEW.trade_id <> OLD.trade_id
       OR NEW.actor_account_id <> OLD.actor_account_id OR NEW.character_count <> OLD.character_count
       OR NEW.created_at <> OLD.created_at OR NEW.deleted_at IS NULL
       OR NEW.key_id IS NOT NULL OR NEW.nonce IS NOT NULL OR NEW.ciphertext IS NOT NULL THEN
        RAISE EXCEPTION 'only C2C statement ciphertext cleanup is allowed' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER c2c_statement_guard BEFORE UPDATE OR DELETE ON c2c_dispute_statements FOR EACH ROW EXECUTE FUNCTION guard_c2c_statement();

-- +goose StatementBegin
CREATE FUNCTION guard_c2c_evidence() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'C2C evidence metadata cannot be deleted' USING ERRCODE = '23514';
    END IF;
    IF OLD.deleted_at IS NOT NULL OR NEW.id <> OLD.id OR NEW.trade_id <> OLD.trade_id
       OR NEW.uploader_account_id <> OLD.uploader_account_id OR NEW.kind <> OLD.kind
       OR NEW.mime_type <> OLD.mime_type OR NEW.size_bytes <> OLD.size_bytes
       OR NEW.width <> OLD.width OR NEW.height <> OLD.height OR NEW.sha256 <> OLD.sha256
       OR NEW.created_at <> OLD.created_at OR NEW.deleted_at IS NULL
       OR NEW.key_id IS NOT NULL OR NEW.nonce IS NOT NULL OR NEW.ciphertext IS NOT NULL THEN
        RAISE EXCEPTION 'only C2C evidence ciphertext cleanup is allowed' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER c2c_evidence_guard BEFORE UPDATE OR DELETE ON c2c_evidence FOR EACH ROW EXECUTE FUNCTION guard_c2c_evidence();

-- +goose StatementBegin
CREATE FUNCTION verify_c2c_evidence_limits() RETURNS trigger AS $$
DECLARE
    dispute_count bigint;
    buyer_id uuid;
    seller_id uuid;
BEGIN
    IF NEW.kind = 'dispute' THEN
        SELECT buyer_account_id, seller_account_id INTO buyer_id, seller_id FROM c2c_trades WHERE id = NEW.trade_id;
        IF NEW.uploader_account_id NOT IN (buyer_id, seller_id) THEN
            RAISE EXCEPTION 'only trade participants may upload C2C evidence' USING ERRCODE = '23514';
        END IF;
        SELECT count(*) INTO dispute_count FROM c2c_evidence
         WHERE trade_id = NEW.trade_id AND uploader_account_id = NEW.uploader_account_id AND kind = 'dispute';
        IF dispute_count > 5 THEN
            RAISE EXCEPTION 'C2C dispute evidence limit exceeded' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION verify_c2c_statement_limits() RETURNS trigger AS $$
DECLARE
    statement_characters bigint;
    buyer_id uuid;
    seller_id uuid;
BEGIN
    SELECT buyer_account_id, seller_account_id INTO buyer_id, seller_id FROM c2c_trades WHERE id = NEW.trade_id;
    IF NEW.actor_account_id NOT IN (buyer_id, seller_id) THEN
        RAISE EXCEPTION 'only trade participants may submit C2C statements' USING ERRCODE = '23514';
    END IF;
    SELECT COALESCE(sum(character_count), 0) INTO statement_characters
      FROM c2c_dispute_statements
     WHERE trade_id = NEW.trade_id AND actor_account_id = NEW.actor_account_id;
    IF statement_characters > 2000 THEN
        RAISE EXCEPTION 'C2C dispute statement limit exceeded' USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER c2c_evidence_limits
AFTER INSERT ON c2c_evidence
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_c2c_evidence_limits();

CREATE CONSTRAINT TRIGGER c2c_statement_limits
AFTER INSERT ON c2c_dispute_statements
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_c2c_statement_limits();

-- +goose Down
DROP TRIGGER c2c_statement_limits ON c2c_dispute_statements;
DROP TRIGGER c2c_evidence_limits ON c2c_evidence;
DROP FUNCTION verify_c2c_statement_limits();
DROP FUNCTION verify_c2c_evidence_limits();
DROP TRIGGER c2c_evidence_guard ON c2c_evidence;
DROP FUNCTION guard_c2c_evidence();
DROP TRIGGER c2c_statement_guard ON c2c_dispute_statements;
DROP FUNCTION guard_c2c_statement();
DROP TRIGGER c2c_events_immutable ON c2c_events;
DROP TRIGGER c2c_payment_methods_immutable ON c2c_payment_methods;
DROP FUNCTION reject_c2c_immutable_change();
DROP TRIGGER c2c_trades_history_guard ON c2c_trades;
DROP TRIGGER c2c_orders_history_guard ON c2c_orders;
DROP FUNCTION guard_c2c_trade_history();
DROP FUNCTION guard_c2c_order_history();
DROP TRIGGER c2c_trade_invariants_from_hold ON ledger_holds;
DROP TRIGGER c2c_trade_invariants_from_trade ON c2c_trades;
DROP FUNCTION verify_c2c_trade_invariants();
DROP FUNCTION verify_c2c_trade_invariants_row(uuid);
DROP TRIGGER c2c_order_invariants_from_hold ON ledger_holds;
DROP TRIGGER c2c_order_invariants_from_order ON c2c_orders;
DROP FUNCTION verify_c2c_order_invariants();
DROP TRIGGER c2c_command_complete ON c2c_commands;
DROP FUNCTION verify_c2c_command_complete();
DROP TABLE c2c_events;
DROP TABLE c2c_evidence;
DROP TABLE c2c_dispute_statements;
DROP TABLE c2c_trades;
DROP TABLE c2c_payment_methods;
DROP TABLE c2c_orders;
DROP TABLE c2c_commands;
