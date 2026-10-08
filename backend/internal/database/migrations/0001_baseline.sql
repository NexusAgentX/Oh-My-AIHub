-- +goose Up

-- Oh-My-AIHub 数据库基线（ADR-0024、ADR-0026）。
--
-- 产品重写（Epic #170）时在未发布中间态原地重写为这一份基线，不追加 0002。
-- 按领域分节：身份与审计 -> 平台设置与模型目录 -> 渠道 -> API Key 与路由 -> 调用 -> 账本 -> C2C。
-- 全库约定：积分金额一律为 BIGINT 纳积分（1 积分 = 1e9）；人民币为整数分；费率为纳比率
-- （1e9 = 100%）；时间为 timestamptz。账本只用一个零和约束触发器与分录不可变触发器（ADR-0025）。

-- ============================================================================
-- 一、身份与审计
-- ============================================================================

-- 登录账户：用户名、口令哈希、角色、状态与信用额度（余额在 ledger_accounts）。
CREATE TABLE accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username text NOT NULL UNIQUE,
    display_name text NOT NULL,
    password_hash text NOT NULL,
    password_version bigint NOT NULL DEFAULT 1 CHECK (password_version > 0),
    must_change_password boolean NOT NULL DEFAULT true,
    is_admin boolean NOT NULL DEFAULT false,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    credit_limit_nano bigint NOT NULL DEFAULT 0 CHECK (credit_limit_nano >= 0),
    -- 首次自动创建默认 API Key 的时间（Feature B）；非空后不再自动创建。
    default_key_created_at timestamptz,
    password_changed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (username = lower(username)),
    CHECK (username ~ '^[a-z0-9][a-z0-9._-]{2,31}$'),
    CHECK (length(trim(display_name)) BETWEEN 1 AND 64)
);

-- 浏览器会话（ADR-0007）：只存令牌哈希；口令版本变化、停用账户或删除行即失效。
CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    password_version bigint NOT NULL CHECK (password_version > 0),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_account_id_idx ON sessions(account_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

-- 管理员与敏感操作审计（追加写入）。
CREATE TABLE audit_log (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id uuid REFERENCES accounts(id),
    action text NOT NULL CHECK (length(action) BETWEEN 1 AND 64),
    target_type text NOT NULL CHECK (length(target_type) BETWEEN 1 AND 32),
    target_id text NOT NULL,
    reason text NOT NULL DEFAULT '',
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_target_idx ON audit_log(target_type, target_id, id DESC);
CREATE INDEX audit_log_action_idx ON audit_log(action, id DESC);

-- ============================================================================
-- 二、平台设置与模型目录
-- ============================================================================

-- 平台设置：单行，seed 默认值。
CREATE TABLE settings (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    fee_rate_nano bigint NOT NULL DEFAULT 1000000 CHECK (fee_rate_nano BETWEEN 0 AND 1000000000),
    c2c_payment_timeout_minutes integer NOT NULL DEFAULT 30 CHECK (c2c_payment_timeout_minutes BETWEEN 5 AND 1440),
    default_credit_limit_nano bigint NOT NULL DEFAULT 0 CHECK (default_credit_limit_nano >= 0),
    default_max_attempts integer NOT NULL DEFAULT 3 CHECK (default_max_attempts BETWEEN 1 AND 10),
    default_ttft_timeout_ms integer NOT NULL DEFAULT 30000 CHECK (default_ttft_timeout_ms BETWEEN 1000 AND 600000),
    default_total_timeout_ms integer NOT NULL DEFAULT 600000 CHECK (default_total_timeout_ms BETWEEN 1000 AND 3600000),
    default_cooldown_failures integer NOT NULL DEFAULT 3 CHECK (default_cooldown_failures BETWEEN 1 AND 100),
    default_cooldown_seconds integer NOT NULL DEFAULT 300 CHECK (default_cooldown_seconds BETWEEN 10 AND 86400),
    -- 叠加在环境变量 UPSTREAM_BLOCKED_HOSTS 之上的出站禁用主机。
    extra_blocked_hosts text[] NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (default_total_timeout_ms >= default_ttft_timeout_ms)
);

INSERT INTO settings (id) VALUES (true);

-- 模型目录：id 即对外模型名；四个基准价为纳积分/百万 token（0～100000 积分）。
CREATE TABLE models (
    id text PRIMARY KEY CHECK (id ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
    display_name text NOT NULL CHECK (length(trim(display_name)) BETWEEN 1 AND 128),
    input_price_nano_per_million bigint NOT NULL CHECK (input_price_nano_per_million BETWEEN 0 AND 100000000000000),
    output_price_nano_per_million bigint NOT NULL CHECK (output_price_nano_per_million BETWEEN 0 AND 100000000000000),
    cache_write_price_nano_per_million bigint NOT NULL CHECK (cache_write_price_nano_per_million BETWEEN 0 AND 100000000000000),
    cache_read_price_nano_per_million bigint NOT NULL CHECK (cache_read_price_nano_per_million BETWEEN 0 AND 100000000000000),
    token_prices jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(token_prices)='object'),
    enabled boolean NOT NULL DEFAULT true,
    sort_order integer NOT NULL DEFAULT 0,
    -- 可选展示信息。
    provider text NOT NULL DEFAULT '',
    context_window bigint CHECK (context_window IS NULL OR context_window > 0),
    input_modalities text[] NOT NULL DEFAULT '{text}',
    output_modalities text[] NOT NULL DEFAULT '{text}',
    supports_tools boolean NOT NULL DEFAULT false,
    supports_structured_output boolean NOT NULL DEFAULT false,
    supports_vision boolean NOT NULL DEFAULT false,
    parameter_info text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Source records deliberately survive model deletion (ignored tombstones).
CREATE TABLE model_sources (
 source_key text PRIMARY KEY,
 model_id text NOT NULL UNIQUE,
 ignored boolean NOT NULL DEFAULT false,
 sync_enabled boolean NOT NULL DEFAULT true,
 raw_record jsonb NOT NULL,
 info jsonb NOT NULL,
 seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE catalog_sync (
 id boolean PRIMARY KEY DEFAULT true CHECK(id),
 exchange_rate text NOT NULL DEFAULT '',
 started_at timestamptz,
 finished_at timestamptz,
 status text NOT NULL DEFAULT 'idle',
 error text NOT NULL DEFAULT '',
 result jsonb NOT NULL DEFAULT '{}'::jsonb
);
INSERT INTO catalog_sync(id) VALUES(true);

CREATE INDEX models_enabled_order_idx ON models(enabled, sort_order, id);

-- 条件价格档（ADR-0012）：输入侧 token 区间 × 带时区每周时间窗，按 seq 首个命中整单生效。
CREATE TABLE model_price_tiers (
    model_id text NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    seq integer NOT NULL CHECK (seq BETWEEN 1 AND 16),
    token_prices jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(token_prices)='object'),
    service_tier text NOT NULL DEFAULT '',
    thinking_mode text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '' CHECK (length(name) <= 64),
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

-- ============================================================================
-- 三、渠道（Feature B）
-- ============================================================================

-- 用户共享的上游中转站。上游 Key 以 UPSTREAM_CREDENTIAL_KEYRING 加密（ADR-0009）。
-- 高级项为空即使用平台默认（settings.default_*）。
CREATE TABLE channels (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id uuid NOT NULL REFERENCES accounts(id),
    name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 64),
    base_url text NOT NULL CHECK (base_url ~ '^https://'),
    api_key_ciphertext bytea NOT NULL,
    api_key_nonce bytea NOT NULL,
    api_key_key_id text NOT NULL,
    status text NOT NULL DEFAULT 'listed' CHECK (status IN ('listed', 'unlisted', 'suspended')),
    suspended_reason text,
    user_agent text CHECK (user_agent IS NULL OR length(user_agent) BETWEEN 1 AND 512),
    header_rules jsonb NOT NULL DEFAULT '{"set": [], "remove": []}'::jsonb,
    concurrency_limit integer CHECK (concurrency_limit IS NULL OR concurrency_limit > 0),
    rpm_limit integer CHECK (rpm_limit IS NULL OR rpm_limit > 0),
    daily_revenue_cap_nano bigint CHECK (daily_revenue_cap_nano IS NULL OR daily_revenue_cap_nano > 0),
    ttft_timeout_ms integer CHECK (ttft_timeout_ms IS NULL OR ttft_timeout_ms BETWEEN 1000 AND 600000),
    total_timeout_ms integer CHECK (total_timeout_ms IS NULL OR total_timeout_ms BETWEEN 1000 AND 3600000),
    cooldown_failures integer CHECK (cooldown_failures IS NULL OR cooldown_failures BETWEEN 1 AND 100),
    cooldown_seconds integer CHECK (cooldown_seconds IS NULL OR cooldown_seconds BETWEEN 10 AND 86400),
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'suspended') = (suspended_reason IS NOT NULL))
);

CREATE INDEX channels_owner_idx ON channels(owner_id) WHERE deleted_at IS NULL;

-- 渠道为每个模型声明上游名称（模型重定向）、倍率与支持的格式。
CREATE TABLE channel_models (
    channel_id uuid NOT NULL REFERENCES channels(id),
    model_id text NOT NULL REFERENCES models(id),
    upstream_model text NOT NULL CHECK (length(upstream_model) BETWEEN 1 AND 256),
    multiplier_nano bigint NOT NULL DEFAULT 1000000000 CHECK (multiplier_nano BETWEEN 0 AND 1000000000000),
    formats text[] NOT NULL CHECK (
        cardinality(formats) > 0
        AND formats <@ ARRAY['openai_chat', 'openai_responses', 'anthropic', 'gemini']::text[]
    ),
    -- 每种格式最近一次测试结果与时间：{"<format>": {"ok": bool, "status": int, "error": text, "tested_at": ts}}。
    format_tests jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled boolean NOT NULL DEFAULT true,
    PRIMARY KEY (channel_id, model_id)
);

CREATE INDEX channel_models_model_idx ON channel_models(model_id) WHERE enabled;

-- 渠道健康事件。
CREATE TABLE channel_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    channel_id uuid NOT NULL REFERENCES channels(id),
    kind text NOT NULL CHECK (kind IN (
        'cooldown_started', 'cooldown_ended', 'limit_reached', 'suspended',
        'unsuspended', 'listed', 'unlisted', 'test_failed'
    )),
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX channel_events_channel_idx ON channel_events(channel_id, id DESC);

-- ============================================================================
-- 四、API Key 与路由（Feature B）
-- ============================================================================

-- 平台 API Key：key_hash 供网关查找；密文复用上游凭据 keyring，供用户再次复制。
-- 每个用户未删除的 Key 最多 20 把（应用层校验）。
CREATE TABLE api_keys (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id uuid NOT NULL REFERENCES accounts(id),
    name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 64),
    prefix text NOT NULL,
    key_hash bytea NOT NULL UNIQUE CHECK (octet_length(key_hash) = 32),
    key_ciphertext bytea NOT NULL,
    key_nonce bytea NOT NULL,
    key_key_id text NOT NULL,
    status text NOT NULL DEFAULT 'enabled' CHECK (status IN ('enabled', 'disabled')),
    expires_at timestamptz,
    allowed_models text[] NOT NULL DEFAULT '{}',
    budget_daily_nano bigint CHECK (budget_daily_nano IS NULL OR budget_daily_nano > 0),
    budget_monthly_nano bigint CHECK (budget_monthly_nano IS NULL OR budget_monthly_nano > 0),
    budget_total_nano bigint CHECK (budget_total_nano IS NULL OR budget_total_nano > 0),
    -- 客户端模型名 -> 平台模型 id。
    model_aliases jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_used_at timestamptz,
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX api_keys_owner_idx ON api_keys(owner_id) WHERE deleted_at IS NULL;

-- 路由设置：api_key_id 为空是账号级，非空是该 Key 的单独路由；没有记录即便宜优先 + 全部渠道。
CREATE TABLE route_prefs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id),
    api_key_id uuid REFERENCES api_keys(id) ON DELETE CASCADE,
    model_id text NOT NULL REFERENCES models(id),
    mode text NOT NULL DEFAULT 'cheapest' CHECK (mode IN ('cheapest', 'reliable', 'fastest', 'manual')),
    max_attempts integer CHECK (max_attempts IS NULL OR max_attempts BETWEEN 1 AND 10),
    ttft_timeout_ms integer CHECK (ttft_timeout_ms IS NULL OR ttft_timeout_ms BETWEEN 1000 AND 600000),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (account_id, api_key_id, model_id)
);

-- 路由中的渠道：manual 模式的顺序与任何模式下取消勾选的渠道。
CREATE TABLE route_pref_channels (
    route_pref_id uuid NOT NULL REFERENCES route_prefs(id) ON DELETE CASCADE,
    channel_id uuid NOT NULL REFERENCES channels(id),
    position integer NOT NULL CHECK (position >= 0),
    excluded boolean NOT NULL DEFAULT false,
    PRIMARY KEY (route_pref_id, channel_id)
);

-- ============================================================================
-- 五、调用（Feature B 写入，Feature G 查询）
-- ============================================================================

-- 一次调用一行；id 即对外请求 ID。永远不保存请求或响应正文。
CREATE TABLE calls (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id),
    api_key_id uuid REFERENCES api_keys(id),
    model_id text,
    requested_model text NOT NULL DEFAULT '',
    format text NOT NULL CHECK (format IN ('openai_chat', 'openai_responses', 'anthropic', 'gemini')),
    stream boolean NOT NULL DEFAULT false,
    tag text CHECK (tag IS NULL OR length(tag) <= 64),
    client_user_agent text CHECK (client_user_agent IS NULL OR length(client_user_agent) <= 512),
    routing_mode text CHECK (routing_mode IS NULL OR routing_mode IN ('cheapest', 'reliable', 'fastest', 'manual')),
    routing_source text CHECK (routing_source IS NULL OR routing_source IN ('default', 'account', 'key')),
    outcome text NOT NULL DEFAULT 'in_progress' CHECK (outcome IN (
        'rejected_balance', 'rejected_budget', 'rejected_key', 'rejected_model', 'rejected_format',
        'rejected_no_channel', 'upstream_failed', 'interrupted', 'client_disconnected',
        'succeeded', 'succeeded_unbilled', 'in_progress'
    )),
    final_channel_id uuid REFERENCES channels(id),
    -- 每次尝试：channel、状态码、错误码、原始错误（≤4KB）、连接/首字/总耗时、字节数、结束原因。
    attempts jsonb NOT NULL DEFAULT '[]'::jsonb,
    input_tokens bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_write_tokens bigint NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    cache_read_tokens bigint NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    -- 四基准价、命中档位、倍率、费率。
    price_snapshot jsonb,
    cost_nano bigint NOT NULL DEFAULT 0 CHECK (cost_nano >= 0),
    fee_nano bigint NOT NULL DEFAULT 0 CHECK (fee_nano >= 0),
    ttft_ms integer CHECK (ttft_ms IS NULL OR ttft_ms >= 0),
    duration_ms integer CHECK (duration_ms IS NULL OR duration_ms >= 0),
    output_tokens_per_second double precision,
    inter_token_p50_ms integer,
    inter_token_p95_ms integer,
    response_bytes bigint CHECK (response_bytes IS NULL OR response_bytes >= 0),
    upstream_response_id text,
    ledger_tx_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE INDEX calls_account_created_idx ON calls(account_id, created_at DESC);
CREATE INDEX calls_channel_created_idx ON calls(final_channel_id, created_at DESC) WHERE final_channel_id IS NOT NULL;
CREATE INDEX calls_key_created_idx ON calls(api_key_id, created_at DESC) WHERE api_key_id IS NOT NULL;
CREATE INDEX calls_created_idx ON calls(created_at DESC);
CREATE INDEX calls_upstream_response_idx ON calls(upstream_response_id) WHERE upstream_response_id IS NOT NULL;
-- 渠道统计与渠道所有者的调用列表按“尝试过该渠道”查找（Feature G）。
CREATE INDEX calls_attempts_idx ON calls USING gin (attempts jsonb_path_ops);
-- 漏记核对：已结束、应计费但没有账本交易的调用。
CREATE INDEX calls_unbilled_idx ON calls(created_at) WHERE ledger_tx_id IS NULL AND outcome IN ('succeeded', 'interrupted', 'client_disconnected') AND cost_nano + fee_nano > 0;

-- ============================================================================
-- 六、零和账本（ADR-0005、ADR-0025）
-- ============================================================================

-- 账本账户：每个用户一个，另有三个系统账户。余额只存一列，与过账同事务更新。
CREATE TABLE ledger_accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind text NOT NULL CHECK (kind IN ('user', 'system')),
    account_id uuid UNIQUE REFERENCES accounts(id),
    system_code text UNIQUE CHECK (system_code IS NULL OR system_code IN ('platform_revenue', 'c2c_escrow', 'bad_debt')),
    balance_nano bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (kind = 'user' AND account_id IS NOT NULL AND system_code IS NULL)
        OR (kind = 'system' AND account_id IS NULL AND system_code IS NOT NULL)
    )
);

INSERT INTO ledger_accounts (kind, system_code)
VALUES ('system', 'platform_revenue'), ('system', 'c2c_escrow'), ('system', 'bad_debt');

-- 账本交易：幂等键唯一，重复过账返回已有交易。
CREATE TABLE ledger_transactions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type text NOT NULL CHECK (type IN (
        'api_call', 'c2c_list', 'c2c_release', 'c2c_return', 'admin_adjust', 'bad_debt_writeoff'
    )),
    idempotency_key text NOT NULL UNIQUE CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    related_type text CHECK (related_type IS NULL OR related_type IN ('call', 'c2c_order', 'c2c_trade', 'account')),
    related_id uuid,
    actor_id uuid REFERENCES accounts(id),
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((related_type IS NULL) = (related_id IS NULL))
);

CREATE INDEX ledger_transactions_related_idx ON ledger_transactions(related_type, related_id);
CREATE INDEX ledger_transactions_created_idx ON ledger_transactions(created_at DESC, id);

-- 分录：非零金额与变动后余额。不可更新、不可删除。
CREATE TABLE ledger_entries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transaction_id uuid NOT NULL REFERENCES ledger_transactions(id),
    ledger_account_id uuid NOT NULL REFERENCES ledger_accounts(id),
    amount_nano bigint NOT NULL CHECK (amount_nano <> 0),
    balance_after_nano bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (transaction_id, ledger_account_id)
);

CREATE INDEX ledger_entries_account_idx ON ledger_entries(ledger_account_id, id DESC);

-- 唯一的零和约束：提交时每笔交易至少两条分录且合计为 0。
-- +goose StatementBegin
CREATE FUNCTION verify_ledger_transaction_balance() RETURNS trigger AS $$
DECLARE
    entry_count bigint;
    entry_sum numeric;
BEGIN
    SELECT count(*), coalesce(sum(amount_nano::numeric), 0)
      INTO entry_count, entry_sum
      FROM ledger_entries
     WHERE transaction_id = NEW.transaction_id;
    IF entry_count < 2 OR entry_sum <> 0 THEN
        RAISE EXCEPTION 'ledger transaction % is unbalanced (entries %, sum %)', NEW.transaction_id, entry_count, entry_sum
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER ledger_transaction_balance
    AFTER INSERT ON ledger_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION verify_ledger_transaction_balance();

-- +goose StatementBegin
CREATE FUNCTION reject_ledger_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ledger % rows are immutable', TG_TABLE_NAME
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER ledger_entries_immutable
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION reject_ledger_mutation();

CREATE TRIGGER ledger_transactions_immutable
    BEFORE UPDATE OR DELETE ON ledger_transactions
    FOR EACH ROW EXECUTE FUNCTION reject_ledger_mutation();

ALTER TABLE calls ADD CONSTRAINT calls_ledger_tx_fk FOREIGN KEY (ledger_tx_id) REFERENCES ledger_transactions(id);

-- ============================================================================
-- 七、C2C 卖单市场（Feature C）
-- ============================================================================

-- 卖单：挂单时积分转入 c2c_escrow 系统账户。恒等式 total = available + in_trade + sold + closed。
CREATE TABLE c2c_orders (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id uuid NOT NULL REFERENCES accounts(id),
    total_nano bigint NOT NULL CHECK (total_nano > 0),
    available_nano bigint NOT NULL CHECK (available_nano >= 0),
    in_trade_nano bigint NOT NULL DEFAULT 0 CHECK (in_trade_nano >= 0),
    sold_nano bigint NOT NULL DEFAULT 0 CHECK (sold_nano >= 0),
    closed_nano bigint NOT NULL DEFAULT 0 CHECK (closed_nano >= 0),
    unit_price_fen bigint NOT NULL CHECK (unit_price_fen > 0),
    min_per_trade_nano bigint NOT NULL CHECK (min_per_trade_nano > 0),
    max_per_trade_nano bigint CHECK (max_per_trade_nano IS NULL OR max_per_trade_nano >= min_per_trade_nano),
    -- C2C_PRIVATE_DATA_KEYRING 加密的 JSON：[{"channel": "...", "account": "..."}]。
    payment_methods_ciphertext bytea NOT NULL,
    payment_methods_nonce bytea NOT NULL,
    payment_methods_key_id text NOT NULL,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed', 'filled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    closed_at timestamptz,
    CHECK (total_nano = available_nano + in_trade_nano + sold_nano + closed_nano)
);

CREATE INDEX c2c_orders_open_price_idx ON c2c_orders(unit_price_fen, created_at) WHERE status = 'open';
CREATE INDEX c2c_orders_seller_idx ON c2c_orders(seller_id, created_at DESC);

-- 成交：买家下单锁定数量 -> 站外付款 -> 卖家放行（托管 -> 买家）。
CREATE TABLE c2c_trades (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id uuid NOT NULL REFERENCES c2c_orders(id),
    buyer_id uuid NOT NULL REFERENCES accounts(id),
    seller_id uuid NOT NULL REFERENCES accounts(id),
    amount_nano bigint NOT NULL CHECK (amount_nano > 0),
    unit_price_fen bigint NOT NULL CHECK (unit_price_fen > 0),
    total_fen bigint NOT NULL CHECK (total_fen > 0),
    status text NOT NULL DEFAULT 'awaiting_payment' CHECK (status IN (
        'awaiting_payment', 'paid', 'released', 'cancelled', 'disputed', 'resolved_to_buyer', 'resolved_to_seller'
    )),
    payment_deadline timestamptz NOT NULL,
    buyer_note text CHECK (buyer_note IS NULL OR length(buyer_note) <= 500),
    dispute_opened_by uuid REFERENCES accounts(id),
    buyer_statement text CHECK (buyer_statement IS NULL OR length(buyer_statement) <= 2000),
    seller_statement text CHECK (seller_statement IS NULL OR length(seller_statement) <= 2000),
    resolution_reason text,
    resolved_by uuid REFERENCES accounts(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    paid_at timestamptz,
    released_at timestamptz,
    cancelled_at timestamptz,
    disputed_at timestamptz,
    resolved_at timestamptz,
    ledger_tx_id uuid REFERENCES ledger_transactions(id),
    CHECK (buyer_id <> seller_id)
);

CREATE INDEX c2c_trades_order_idx ON c2c_trades(order_id);
CREATE INDEX c2c_trades_buyer_idx ON c2c_trades(buyer_id, created_at DESC);
CREATE INDEX c2c_trades_seller_idx ON c2c_trades(seller_id, created_at DESC);
CREATE INDEX c2c_trades_deadline_idx ON c2c_trades(payment_deadline) WHERE status = 'awaiting_payment';
CREATE INDEX c2c_trades_disputed_idx ON c2c_trades(disputed_at) WHERE status = 'disputed';

-- +goose Down
DROP TABLE c2c_trades;
DROP TABLE c2c_orders;
ALTER TABLE calls DROP CONSTRAINT calls_ledger_tx_fk;
DROP TABLE ledger_entries;
DROP TABLE ledger_transactions;
DROP TABLE ledger_accounts;
DROP FUNCTION reject_ledger_mutation();
DROP FUNCTION verify_ledger_transaction_balance();
DROP TABLE calls;
DROP TABLE route_pref_channels;
DROP TABLE route_prefs;
DROP TABLE api_keys;
DROP TABLE channel_events;
DROP TABLE channel_models;
DROP TABLE channels;
DROP TABLE model_sources;
DROP TABLE catalog_sync;
DROP TABLE model_price_tiers;
DROP TABLE models;
DROP TABLE settings;
DROP TABLE audit_log;
DROP TABLE sessions;
DROP TABLE accounts;
