-- +goose Up

-- Oh-My-AIHub 数据库基线（ADR-0024）。
--
-- 项目处于未发布中间态，早期十个迁移压缩为这一份基线；此后的结构变化从 0002 起追加。
-- 按领域分节：身份与目录 -> 账本 -> 渠道 -> API 网关 -> C2C -> 运维。
-- 全库约定：金额一律为 BIGINT 纳积分（ADR-0006）；不可变事实与状态机不变量由
-- 触发器在提交时强制，应用层不能绕过。

-- ============================================================================
-- 一、身份与模型目录
-- ============================================================================

-- 登录账户：用户名、口令哈希、角色、状态与信用额度。
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
    -- 额度冻结：冻结后不能再创建新的持有（见账本一节 verify_ledger_hold_creation）。
    credit_frozen boolean NOT NULL DEFAULT false,
    CHECK (username = lower(username)),
    CHECK (username ~ '^[a-z0-9][a-z0-9._-]{2,31}$'),
    CHECK (length(trim(display_name)) > 0)
);

-- 浏览器会话：只存令牌哈希，口令版本变化即失效。
CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    password_version bigint NOT NULL CHECK (password_version > 0),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_account_id_idx ON sessions(account_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

-- 模型目录：能力、默认价格（纳积分/百万 token）与状态。
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
    CHECK (cardinality(output_modalities) > 0),
    -- 渠道报价上限：每百万 token 单价不超过 1e14 纳积分。
    CONSTRAINT models_channel_price_bounds CHECK (
        input_price_nano_per_million <= 100000000000000
        AND output_price_nano_per_million <= 100000000000000
        AND cache_write_price_nano_per_million <= 100000000000000
        AND cache_read_price_nano_per_million <= 100000000000000
    )
);

CREATE INDEX models_status_name_idx ON models(status, name);

-- 管理员操作审计流水（追加写入）。
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

-- 模型条件价格档（ADR-0012）：最多 16 档，首个命中生效，无档位时使用 models 默认价。
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

-- ============================================================================
-- 二、零和账本（ADR-0008）
-- ============================================================================

-- 账本账户：用户账户与平台系统账户，余额为不可变分录的投影。
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

-- 系统账户：平台激励与平台损失。用户账本账户随账户创建由应用写入。
INSERT INTO ledger_accounts (kind, system_code)
VALUES
    ('platform_incentive', 'platform_incentive'),
    ('platform_loss', 'platform_loss');

-- 账本命令幂等表：同键同载荷永久重放首次结果快照。
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

-- 持有：冻结资产（asset_reservation）或授权消费（spend_authorization）。
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

-- 账本交易：封存后不可变，分录必须零和。
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
    hold_id uuid REFERENCES ledger_holds(id) ON DELETE RESTRICT,
    UNIQUE (command_operation, idempotency_key),
    UNIQUE (reference_type, reference_id),
    FOREIGN KEY (command_operation, idempotency_key)
        REFERENCES ledger_commands(operation, idempotency_key) ON DELETE RESTRICT,
    CHECK (
        (kind = 'reversal' AND reversal_of_transaction_id IS NOT NULL)
        OR (kind <> 'reversal' AND reversal_of_transaction_id IS NULL)
    ),
    CONSTRAINT ledger_transactions_capture_hold_check CHECK (
        (kind = 'hold_capture' AND command_operation = 'hold.capture' AND hold_id IS NOT NULL)
        OR (kind <> 'hold_capture' AND hold_id IS NULL)
    )
);

CREATE INDEX ledger_transactions_reference_idx
    ON ledger_transactions(reference_type, reference_id, created_at DESC);

-- 账本分录：追加写入，带账户余额链。
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

-- 持有事件：capture / release，不可变。
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

-- ============================================================================
-- 三、渠道、报价与验证
-- ============================================================================

-- 渠道：提供者的上游服务，凭据版本化。
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
    CHECK ((status = 'deleted') = (deleted_at IS NOT NULL)),
    CONSTRAINT channels_id_credential_version_unique UNIQUE (id, credential_version)
);

CREATE INDEX channels_owner_updated_idx ON channels(owner_account_id, updated_at DESC);
CREATE INDEX channels_status_updated_idx ON channels(status, updated_at DESC);

-- 渠道上游凭据（AES-256-GCM 密文，ADR-0009）。
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

-- 渠道上架的模型及倍率。
CREATE TABLE channel_models (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id uuid NOT NULL REFERENCES channels(id) ON DELETE RESTRICT,
    model_id text NOT NULL REFERENCES models(id) ON DELETE RESTRICT,
    multiplier_nano bigint NOT NULL CHECK (multiplier_nano >= 0 AND multiplier_nano <= 1000000000000),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (channel_id, model_id)
);

-- 渠道报价：模型 + 协议，带验证版本。
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

-- 报价验证尝试历史。
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

-- ============================================================================
-- 四、API 网关与调用结算（ADR-0010、ADR-0012）
-- ============================================================================

-- 平台手续费率版本（追加写入）。
CREATE TABLE api_fee_rates (
    version bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    fee_rate_nano bigint NOT NULL CHECK (fee_rate_nano BETWEEN 0 AND 1000000000),
    created_by uuid REFERENCES accounts(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO api_fee_rates (fee_rate_nano) VALUES (1000000);

-- API Key：只存哈希与前缀。
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

-- Key 下的模型池。
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

-- 模型池成员及优先级。
CREATE TABLE api_pool_members (
    pool_id uuid NOT NULL REFERENCES api_model_pools(id) ON DELETE RESTRICT,
    offer_id uuid NOT NULL REFERENCES channel_offers(id) ON DELETE RESTRICT,
    priority integer NOT NULL CHECK (priority > 0),
    added_validation_version bigint NOT NULL CHECK (added_validation_version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (pool_id, offer_id),
    UNIQUE (pool_id, priority)
);

-- API 调用：快照、租约、结果与计费字段。
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
    formula_version text NOT NULL DEFAULT 'formula-v1' CHECK (formula_version IN ('formula-v1', 'formula-v2')),
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
    -- formula-v2 结算命中的价格档序号；0 表示默认档。
    settled_price_tier_seq integer NOT NULL DEFAULT 0 CHECK (settled_price_tier_seq BETWEEN 0 AND 16),
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

-- 调用时的候选报价快照（不可变）。
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

-- 上游尝试记录。
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

-- 调用结算事实（不可变）。
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

-- 调用补偿事实（不可变）。
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

-- 调用级价格档快照：formula-v2 结算时按这里的快照选档，不回读 models。
CREATE TABLE api_call_price_tiers(
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

CREATE TRIGGER api_call_price_tiers_immutable
    BEFORE UPDATE OR DELETE ON api_call_price_tiers
    FOR EACH ROW EXECUTE FUNCTION guard_api_gateway_history();

-- ============================================================================
-- 五、C2C 卖单市场（ADR-0011、ADR-0023）
-- ============================================================================

-- C2C 命令幂等表。
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

-- C2C 卖单：发布时冻结父持有，数量恒等式 total = available + allocated + settled + closed。
CREATE TABLE c2c_orders (
    id uuid PRIMARY KEY,
    owner_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    unit_price_fen bigint NOT NULL CHECK (unit_price_fen > 0),
    total_nano bigint NOT NULL CHECK (total_nano > 0),
    available_nano bigint NOT NULL CHECK (available_nano >= 0),
    allocated_nano bigint NOT NULL DEFAULT 0 CHECK (allocated_nano >= 0),
    settled_nano bigint NOT NULL DEFAULT 0 CHECK (settled_nano >= 0),
    closed_nano bigint NOT NULL DEFAULT 0 CHECK (closed_nano >= 0),
    minimum_nano bigint NOT NULL CHECK (minimum_nano > 0),
    maximum_nano bigint NOT NULL CHECK (maximum_nano > 0),
    status text NOT NULL CHECK (status IN ('open', 'allocated', 'filled', 'cancelled')),
    -- 卖单发布时冻结的 asset_reservation 父持有（ADR-0023：C2C 只有卖单）。
    parent_hold_id uuid NOT NULL REFERENCES ledger_holds(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    cancelled_at timestamptz,
    CHECK (total_nano::numeric = available_nano::numeric + allocated_nano::numeric + settled_nano::numeric + closed_nano::numeric),
    CHECK (minimum_nano <= maximum_nano AND maximum_nano <= total_nano),
    CHECK ((status = 'cancelled') = (cancelled_at IS NOT NULL))
);

CREATE INDEX c2c_orders_sell_book_idx ON c2c_orders(unit_price_fen, created_at, id) WHERE status = 'open' AND available_nano > 0;
CREATE INDEX c2c_orders_owner_idx ON c2c_orders(owner_account_id, updated_at DESC, id DESC);

-- 卖单收款方式（加密保存，不可变）。
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

-- C2C 成交：订单下的部分成交，绑定同一父持有。
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

-- 争议陈述（密文，可清理）。
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

-- C2C 状态事件（不可变）。
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
    effect_kind text;
    effect_transaction uuid;
BEGIN
    SELECT * INTO item FROM c2c_trades WHERE id = target_trade_id;
    IF item.id IS NULL THEN
        RETURN;
    END IF;
    SELECT * INTO parent FROM c2c_orders WHERE id = item.order_id;
    IF parent.id IS NULL THEN
        RAISE EXCEPTION 'C2C trade references missing order' USING ERRCODE = '23514';
    END IF;
    IF item.seller_account_id <> parent.owner_account_id OR item.hold_id <> parent.parent_hold_id THEN
        RAISE EXCEPTION 'C2C trade parties do not match parent order' USING ERRCODE = '23514';
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
    IF item.status IN ('returned_to_seller', 'cancelled', 'expired') THEN
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
        NEW.id <> OLD.id OR NEW.owner_account_id <> OLD.owner_account_id
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

CREATE CONSTRAINT TRIGGER c2c_statement_limits
AFTER INSERT ON c2c_dispute_statements
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION verify_c2c_statement_limits();

-- ============================================================================
-- 六、运维巡检
-- ============================================================================

-- 运维巡检历史：每行是一次固定不变量集合（账本零和与投影、调用结算链接、
-- C2C 数量与持有一致性）的执行结果，只存非敏感的汇总差值。
CREATE TABLE ops_inspections (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    inspection_version text NOT NULL CHECK (inspection_version = 'ops-v1'),
    triggered_by text NOT NULL CHECK (triggered_by IN ('startup', 'periodic', 'manual')),
    zero_sum_ok boolean NOT NULL,
    projection_ok boolean NOT NULL,
    call_settlement_ok boolean NOT NULL,
    c2c_consistency_ok boolean NOT NULL,
    zero_sum_difference_nano bigint NOT NULL DEFAULT 0,
    posted_projection_difference_nano bigint NOT NULL DEFAULT 0,
    asset_projection_difference_nano bigint NOT NULL DEFAULT 0,
    authorization_projection_difference_nano bigint NOT NULL DEFAULT 0,
    successful_calls_without_settlement bigint NOT NULL DEFAULT 0,
    settlements_without_ledger_transaction bigint NOT NULL DEFAULT 0,
    c2c_quantity_violations bigint NOT NULL DEFAULT 0,
    c2c_hold_violations bigint NOT NULL DEFAULT 0,
    notes jsonb NOT NULL DEFAULT '{}'::jsonb,
    checked_at timestamptz NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(notes) = 'object')
);

CREATE INDEX ops_inspections_checked_at_idx ON ops_inspections(checked_at DESC, id);

-- +goose Down
-- 基线之前没有任何对象：按依赖逆序删除全部表（触发器随表删除），再删除函数。
DROP TABLE ops_inspections;
DROP TABLE c2c_events;
DROP TABLE c2c_dispute_statements;
DROP TABLE c2c_trades;
DROP TABLE c2c_payment_methods;
DROP TABLE c2c_orders;
DROP TABLE c2c_commands;
DROP TABLE api_call_price_tiers;
DROP TABLE api_call_compensations;
DROP TABLE api_call_settlements;
DROP TABLE api_call_attempts;
DROP TABLE api_call_candidates;
DROP TABLE api_calls;
DROP TABLE api_pool_members;
DROP TABLE api_model_pools;
DROP TABLE api_keys;
DROP TABLE api_fee_rates;
DROP TABLE channel_validation_attempts;
DROP TABLE channel_offers;
DROP TABLE channel_models;
DROP TABLE channel_credentials;
DROP TABLE channels;
DROP TABLE ledger_hold_events;
DROP TABLE ledger_entries;
DROP TABLE ledger_transactions;
DROP TABLE ledger_holds;
DROP TABLE ledger_commands;
DROP TABLE ledger_accounts;
DROP TABLE model_price_tiers;
DROP TABLE audit_events;
DROP TABLE models;
DROP TABLE sessions;
DROP TABLE accounts;
DROP FUNCTION verify_ledger_transaction();
DROP FUNCTION protect_ledger_immutability();
DROP FUNCTION protect_ledger_account_identity();
DROP FUNCTION protect_ledger_hold_identity();
DROP FUNCTION protect_ledger_command_immutability();
DROP FUNCTION verify_ledger_command_completion();
DROP FUNCTION verify_ledger_account_projection();
DROP FUNCTION verify_ledger_hold_state();
DROP FUNCTION verify_ledger_hold_creation();
DROP FUNCTION verify_ledger_capture_link();
DROP FUNCTION protect_hold_event_immutability();
DROP FUNCTION guard_channel_history();
DROP FUNCTION guard_validation_attempt_history();
DROP FUNCTION guard_api_gateway_history();
DROP FUNCTION guard_api_call_history();
DROP FUNCTION guard_api_call_attempt_history();
DROP FUNCTION verify_api_call_settlement();
DROP FUNCTION verify_c2c_command_complete();
DROP FUNCTION verify_c2c_order_invariants();
DROP FUNCTION verify_c2c_trade_invariants_row(target_trade_id uuid);
DROP FUNCTION verify_c2c_trade_invariants();
DROP FUNCTION guard_c2c_order_history();
DROP FUNCTION guard_c2c_trade_history();
DROP FUNCTION reject_c2c_immutable_change();
DROP FUNCTION guard_c2c_statement();
DROP FUNCTION verify_c2c_statement_limits();
