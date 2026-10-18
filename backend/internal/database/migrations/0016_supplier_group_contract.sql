-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- Additive only: legacy suppliers/models/channels remain usable until F12.
-- No user, balance, ledger, call, quote snapshot or credential is rewritten.
CREATE TABLE market_suppliers (
 id uuid PRIMARY KEY,
 contributor_id uuid NOT NULL REFERENCES accounts(id),
 name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
 kind text NOT NULL CHECK (kind IN ('official_api','official_subscription','relay')),
 website_url text NOT NULL CHECK (char_length(website_url) BETWEEN 1 AND 2048),
 icon_data text NOT NULL DEFAULT '' CHECK (octet_length(icon_data)<=350000),
 icon_source text NOT NULL DEFAULT '' CHECK (char_length(icon_source)<=2048),
 summary text NOT NULL DEFAULT '' CHECK (char_length(summary)<=160),
 description text NOT NULL DEFAULT '' CHECK (char_length(description)<=8000),
 docs_url text NOT NULL DEFAULT '' CHECK (char_length(docs_url)<=2048),
 enabled boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL CHECK (revision>0),
 deleted_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE market_groups (
 id uuid PRIMARY KEY,
 supplier_id uuid NOT NULL REFERENCES market_suppliers(id),
 name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
 description text NOT NULL DEFAULT '' CHECK (char_length(description)<=8000),
 status_url text NOT NULL DEFAULT '' CHECK (char_length(status_url)<=2048),
 user_agent text CHECK (char_length(user_agent) BETWEEN 1 AND 512),
 header_rules jsonb CHECK (header_rules IS NULL OR jsonb_typeof(header_rules)='object'),
 concurrency_limit integer CHECK (concurrency_limit BETWEEN 1 AND 1073741824),
 rpm_limit integer CHECK (rpm_limit BETWEEN 1 AND 1073741824),
 ttft_timeout_ms integer CHECK (ttft_timeout_ms BETWEEN 1000 AND 600000),
 request_timeout_ms integer CHECK (request_timeout_ms BETWEEN 1000 AND 3600000),
 idle_timeout_ms integer CHECK (idle_timeout_ms BETWEEN 1000 AND 3600000),
 cooldown_failures integer CHECK (cooldown_failures BETWEEN 1 AND 100),
 cooldown_seconds integer CHECK (cooldown_seconds BETWEEN 10 AND 86400),
 enabled boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL CHECK (revision>0),
 connection_version bigint NOT NULL CHECK (connection_version>0),
 capability_digest text NOT NULL CHECK (capability_digest ~ '^[a-f0-9]{64}$'),
 deleted_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (id,supplier_id)
);
CREATE INDEX market_groups_supplier ON market_groups(supplier_id) WHERE deleted_at IS NULL;
CREATE TABLE market_addresses (
 id uuid PRIMARY KEY,
 supplier_id uuid NOT NULL REFERENCES market_suppliers(id),
 base_url text NOT NULL CHECK (char_length(base_url) BETWEEN 1 AND 2048),
 label text NOT NULL DEFAULT '' CHECK (char_length(label)<=64),
 revision bigint NOT NULL CHECK (revision>0),
 deleted_at timestamptz,
 UNIQUE (id,supplier_id)
);
CREATE UNIQUE INDEX market_address_current_owner ON market_addresses(base_url) WHERE deleted_at IS NULL;
CREATE TABLE market_group_addresses (
 id uuid PRIMARY KEY,
 group_id uuid NOT NULL,
 address_id uuid NOT NULL,
 supplier_id uuid NOT NULL,
 position integer NOT NULL CHECK (position BETWEEN 0 AND 31),
 deleted_at timestamptz,
 FOREIGN KEY (group_id,supplier_id) REFERENCES market_groups(id,supplier_id),
 FOREIGN KEY (address_id,supplier_id) REFERENCES market_addresses(id,supplier_id)
);
CREATE UNIQUE INDEX market_group_address_current ON market_group_addresses(group_id,address_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX market_group_address_order ON market_group_addresses(group_id,position) WHERE deleted_at IS NULL;
CREATE TABLE market_group_models (
 id uuid PRIMARY KEY,
 group_id uuid NOT NULL REFERENCES market_groups(id),
 model_id text NOT NULL CHECK (octet_length(model_id) BETWEEN 1 AND 256 AND model_id ~ '^[A-Za-z0-9][A-Za-z0-9._/:@+-]*$' AND model_id !~ '(^|/)\.{1,2}(/|$)' AND model_id NOT LIKE '%//%' AND right(model_id,1)<>'/'),
 display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 128),
 upstream_model text NOT NULL CHECK (octet_length(upstream_model) BETWEEN 1 AND 256 AND upstream_model ~ '^[A-Za-z0-9][A-Za-z0-9._/:@+-]*$' AND upstream_model !~ '(^|/)\.{1,2}(/|$)' AND upstream_model NOT LIKE '%//%' AND right(upstream_model,1)<>'/'),
 model_type text NOT NULL CHECK (model_type IN ('text','image')),
 context_window bigint CHECK (context_window>0),
 max_output_tokens bigint CHECK (max_output_tokens>0),
 notes text NOT NULL DEFAULT '' CHECK (char_length(notes)<=2000),
 enabled boolean NOT NULL DEFAULT true,
 is_reference boolean NOT NULL DEFAULT false,
 current_quote_id uuid,
 revision bigint NOT NULL CHECK (revision>0),
 capability_digest text NOT NULL CHECK (capability_digest ~ '^[a-f0-9]{64}$'),
 deleted_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX market_group_model_current ON market_group_models(group_id,model_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX market_model_one_reference ON market_group_models(model_id) WHERE is_reference AND enabled AND deleted_at IS NULL;
CREATE INDEX market_model_aggregation ON market_group_models(model_id,id) WHERE enabled AND deleted_at IS NULL;
CREATE TABLE market_quotes (
 id uuid PRIMARY KEY,
 group_model_id uuid NOT NULL REFERENCES market_group_models(id),
 version bigint NOT NULL CHECK (version>0),
 currency text NOT NULL CHECK (currency IN ('CNY','USD')),
 billing_unit text NOT NULL CHECK (billing_unit IN ('per_million_tokens','per_request','per_successful_image')),
 input_price_nano bigint NOT NULL CHECK (input_price_nano BETWEEN 0 AND 100000000000000),
 output_price_nano bigint NOT NULL CHECK (output_price_nano BETWEEN 0 AND 100000000000000),
 cache_write_price_nano bigint NOT NULL CHECK (cache_write_price_nano BETWEEN 0 AND 100000000000000),
 cache_read_price_nano bigint NOT NULL CHECK (cache_read_price_nano BETWEEN 0 AND 100000000000000),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (group_model_id,version),
 UNIQUE (id,group_model_id)
);
ALTER TABLE market_group_models ADD CONSTRAINT market_current_quote_owner
 FOREIGN KEY (current_quote_id,id) REFERENCES market_quotes(id,group_model_id);
CREATE TABLE market_quote_token_prices (
 quote_id uuid NOT NULL REFERENCES market_quotes(id),
 dimension text NOT NULL CHECK (dimension IN ('input_text','input_image','input_audio','input_video','output_text','output_image','output_audio','output_video','cache_read_text','cache_read_image','cache_read_audio','cache_read_video','cache_write_5m','cache_write_1h')),
 price_nano bigint NOT NULL CHECK (price_nano BETWEEN 0 AND 100000000000000),
 PRIMARY KEY (quote_id,dimension)
);
CREATE TABLE market_quote_tiers (
 quote_id uuid NOT NULL REFERENCES market_quotes(id),
 seq smallint NOT NULL CHECK (seq BETWEEN 1 AND 16),
 name text NOT NULL DEFAULT '' CHECK (char_length(name)<=64),
 service_tier text NOT NULL DEFAULT '' CHECK (service_tier='' OR service_tier ~ '^(openai|anthropic|gemini):[A-Za-z0-9_-]{1,48}$'),
 min_prompt_tokens bigint CHECK (min_prompt_tokens>=0),
 max_prompt_tokens bigint CHECK (max_prompt_tokens>=0),
 timezone text NOT NULL DEFAULT '',
 weekdays integer[] NOT NULL DEFAULT '{}',
 start_minute smallint CHECK (start_minute BETWEEN 0 AND 1439),
 end_minute smallint CHECK (end_minute BETWEEN 1 AND 1440),
 input_price_nano bigint NOT NULL CHECK (input_price_nano BETWEEN 0 AND 100000000000000),
 output_price_nano bigint NOT NULL CHECK (output_price_nano BETWEEN 0 AND 100000000000000),
 cache_write_price_nano bigint NOT NULL CHECK (cache_write_price_nano BETWEEN 0 AND 100000000000000),
 cache_read_price_nano bigint NOT NULL CHECK (cache_read_price_nano BETWEEN 0 AND 100000000000000),
 PRIMARY KEY (quote_id,seq),
 CHECK (service_tier<>'openai:priority'),
 CHECK (min_prompt_tokens IS NULL OR max_prompt_tokens IS NULL OR min_prompt_tokens<max_prompt_tokens),
 CHECK ((start_minute IS NULL)=(end_minute IS NULL)),
 CHECK (start_minute IS NULL OR start_minute<>end_minute),
 CHECK (weekdays <@ ARRAY[1,2,3,4,5,6,7]),
 CHECK ((start_minute IS NULL AND cardinality(weekdays)=0) OR timezone<>''),
 CHECK (service_tier<>'' OR min_prompt_tokens IS NOT NULL OR max_prompt_tokens IS NOT NULL OR start_minute IS NOT NULL OR cardinality(weekdays)>0)
);
CREATE TABLE market_quote_tier_token_prices (
 quote_id uuid NOT NULL,
 seq smallint NOT NULL,
 dimension text NOT NULL CHECK (dimension IN ('input_text','input_image','input_audio','input_video','output_text','output_image','output_audio','output_video','cache_read_text','cache_read_image','cache_read_audio','cache_read_video','cache_write_5m','cache_write_1h')),
 price_nano bigint NOT NULL CHECK (price_nano BETWEEN 0 AND 100000000000000),
 PRIMARY KEY (quote_id,seq,dimension),
 FOREIGN KEY (quote_id,seq) REFERENCES market_quote_tiers(quote_id,seq)
);
CREATE TABLE market_quote_unit_prices (
 quote_id uuid NOT NULL REFERENCES market_quotes(id),
 seq smallint NOT NULL CHECK (seq BETWEEN 1 AND 128),
 unit text NOT NULL CHECK (unit IN ('request','successful_image','tool_call','tool_session','tool_second','tool_query')),
 tool text NOT NULL DEFAULT '' CHECK (char_length(tool)<=128),
 image_size text NOT NULL DEFAULT '' CHECK (char_length(image_size)<=64),
 image_quality text NOT NULL DEFAULT '' CHECK (char_length(image_quality)<=32),
 image_background text NOT NULL DEFAULT '' CHECK (char_length(image_background)<=32),
 service_tier text NOT NULL DEFAULT '' CHECK (service_tier='' OR service_tier ~ '^(openai|anthropic|gemini):[A-Za-z0-9_-]{1,48}$'),
 price_nano bigint NOT NULL CHECK (price_nano BETWEEN 0 AND 100000000000000),
 PRIMARY KEY (quote_id,seq),
 CHECK (service_tier<>'openai:priority'),
 CHECK ((unit IN ('request','successful_image') AND tool='') OR (unit LIKE 'tool_%' AND tool<>''))
);
CREATE TABLE market_model_governance (
 model_id text PRIMARY KEY CHECK (octet_length(model_id) BETWEEN 1 AND 256),
 disabled boolean NOT NULL,
 reason text NOT NULL DEFAULT '' CHECK (char_length(reason)<=1000),
 revision bigint NOT NULL CHECK (revision>0),
 actor_id uuid NOT NULL REFERENCES accounts(id),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE market_object_versions (
 target_kind text NOT NULL CHECK (target_kind IN ('supplier','group','group_model','address')),
 target_id uuid NOT NULL,
 revision bigint NOT NULL CHECK (revision>0),
 row_schema text NOT NULL DEFAULT 'market.row.v1',
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (target_kind,target_id,revision)
);
CREATE TABLE market_search_settings (
 id uuid PRIMARY KEY,
 target text NOT NULL CHECK (target IN ('context','max_output')),
 revision bigint NOT NULL CHECK (revision>0),
 method text NOT NULL CHECK (method IN ('binary','ternary')),
 low bigint NOT NULL CHECK (low>=0),
 high bigint NOT NULL,
 hard_maximum bigint NOT NULL,
 precision_mode text NOT NULL CHECK (precision_mode IN ('absolute','relative')),
 precision double precision NOT NULL CHECK (precision>0 AND precision<'Infinity'::double precision),
 rounds integer NOT NULL CHECK (rounds>0),
 rechecks integer NOT NULL CHECK (rechecks>=0),
 actor_id uuid NOT NULL REFERENCES accounts(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (target,revision),
 CHECK (low<high AND high<=hard_maximum),
 CHECK (precision_mode<>'relative' OR precision<=1)
);
-- Columns retained for immutable provenance; no protocol/request body columns.
CREATE TABLE market_report_bindings (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES accounts(id),
 target_kind text NOT NULL CHECK (target_kind IN ('group','group_model')),
 target_id uuid NOT NULL,
 target_revision bigint NOT NULL CHECK (target_revision>0),
 capability_digest text NOT NULL CHECK (capability_digest ~ '^[a-f0-9]{64}$'),
 manifest_version text NOT NULL,
 code_version text NOT NULL,
 parameter_version text NOT NULL,
 credential_scope text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE market_capability_evidence (
 report_id uuid NOT NULL REFERENCES market_report_bindings(id),
 capability text NOT NULL CHECK (char_length(capability) BETWEEN 1 AND 200),
 layer text NOT NULL CHECK (layer IN ('upstream_protocol','gateway_proxy','official_cli')),
 state text NOT NULL CHECK (state IN ('supported','unsupported','unknown','unobserved','invalidated')),
 reason text NOT NULL DEFAULT '' CHECK (char_length(reason)<=1000),
 PRIMARY KEY (report_id,capability,layer)
);
CREATE TABLE market_protected_keys (
 purpose text PRIMARY KEY CHECK (purpose IN ('codex-context-root-v1','cursor-root-v1','credential-scope-root-v1')),
 encryption_key_id text NOT NULL,
 credential_version bigint NOT NULL CHECK (credential_version>0),
 nonce bytea NOT NULL CHECK (octet_length(nonce)=12),
 ciphertext bytea NOT NULL CHECK (octet_length(ciphertext)>16),
 created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE channels ADD COLUMN market_group_id uuid REFERENCES market_groups(id);
CREATE INDEX channels_market_group ON channels(market_group_id) WHERE deleted_at IS NULL;

-- +goose StatementBegin
CREATE FUNCTION market_guard_supplier() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'market history requires business deletion' USING ERRCODE='23514'; END IF;
 IF NEW.deleted_at IS NOT NULL AND EXISTS(SELECT 1 FROM market_groups WHERE supplier_id=NEW.id AND deleted_at IS NULL) THEN
  RAISE EXCEPTION 'supplier still has groups' USING ERRCODE='23514';
 END IF;
 IF NEW.kind<>'official_api' AND EXISTS(SELECT 1 FROM market_group_models m JOIN market_groups g ON g.id=m.group_id WHERE g.supplier_id=NEW.id AND m.is_reference AND m.deleted_at IS NULL) THEN
  RAISE EXCEPTION 'reference source must be official API' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER market_supplier_guard BEFORE UPDATE OR DELETE ON market_suppliers FOR EACH ROW EXECUTE FUNCTION market_guard_supplier();
-- +goose StatementBegin
CREATE FUNCTION market_guard_group() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM market_suppliers WHERE id=NEW.supplier_id AND deleted_at IS NULL FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'supplier unavailable' USING ERRCODE='23514'; END IF;
 IF TG_OP='UPDATE' AND NEW.deleted_at IS NOT NULL AND OLD.deleted_at IS NULL THEN
  UPDATE channels SET deleted_at=NEW.deleted_at,status='unlisted',suspended_reason=NULL,updated_at=now() WHERE market_group_id=NEW.id AND deleted_at IS NULL;
  UPDATE market_group_models SET deleted_at=NEW.deleted_at,revision=revision+1,updated_at=now() WHERE group_id=NEW.id AND deleted_at IS NULL;
  UPDATE market_group_addresses SET deleted_at=NEW.deleted_at WHERE group_id=NEW.id AND deleted_at IS NULL;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER market_group_guard BEFORE INSERT OR UPDATE ON market_groups FOR EACH ROW EXECUTE FUNCTION market_guard_group();
-- +goose StatementBegin
CREATE FUNCTION market_guard_model() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE supplier_kind text;
BEGIN
 -- Parent row locks serialize deletion/restore/child creation.
 SELECT s.kind INTO supplier_kind FROM market_groups g JOIN market_suppliers s ON s.id=g.supplier_id
 WHERE g.id=NEW.group_id AND g.deleted_at IS NULL AND s.deleted_at IS NULL FOR SHARE OF g,s;
 IF NOT FOUND THEN RAISE EXCEPTION 'group unavailable' USING ERRCODE='23514'; END IF;
 IF NEW.is_reference AND supplier_kind<>'official_api' THEN RAISE EXCEPTION 'reference source must be official API' USING ERRCODE='23514'; END IF;
 IF NEW.enabled AND NEW.deleted_at IS NULL THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('market-model:'||NEW.model_id,0));
  IF EXISTS(SELECT 1 FROM market_group_models WHERE model_id=NEW.model_id AND id<>NEW.id AND enabled AND deleted_at IS NULL AND model_type<>NEW.model_type) THEN
   RAISE EXCEPTION 'conflicting model type' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER market_model_guard BEFORE INSERT OR UPDATE OF group_id,model_id,model_type,enabled,is_reference,deleted_at ON market_group_models FOR EACH ROW EXECUTE FUNCTION market_guard_model();
-- +goose StatementBegin
CREATE FUNCTION market_guard_group_address() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.deleted_at IS NOT NULL THEN RETURN NEW; END IF;
 PERFORM 1 FROM market_groups g JOIN market_addresses a ON a.id=NEW.address_id
 WHERE g.id=NEW.group_id AND g.deleted_at IS NULL AND a.deleted_at IS NULL FOR SHARE OF g,a;
 IF NOT FOUND THEN RAISE EXCEPTION 'group address unavailable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER market_group_address_guard BEFORE INSERT OR UPDATE ON market_group_addresses FOR EACH ROW EXECUTE FUNCTION market_guard_group_address();
-- +goose StatementBegin
CREATE FUNCTION market_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'market history is append-only' USING ERRCODE='23514';
END $$;
-- +goose StatementEnd
CREATE TRIGGER market_quote_immutable BEFORE UPDATE OR DELETE ON market_quotes FOR EACH ROW EXECUTE FUNCTION market_append_only();
CREATE TRIGGER market_token_price_immutable BEFORE UPDATE OR DELETE ON market_quote_token_prices FOR EACH ROW EXECUTE FUNCTION market_append_only();
CREATE TRIGGER market_tier_immutable BEFORE UPDATE OR DELETE ON market_quote_tiers FOR EACH ROW EXECUTE FUNCTION market_append_only();
CREATE TRIGGER market_tier_token_price_immutable BEFORE UPDATE OR DELETE ON market_quote_tier_token_prices FOR EACH ROW EXECUTE FUNCTION market_append_only();
CREATE TRIGGER market_unit_price_immutable BEFORE UPDATE OR DELETE ON market_quote_unit_prices FOR EACH ROW EXECUTE FUNCTION market_append_only();

-- Captured from a PostgreSQL row of a known table schema, not arbitrary JSON.
-- Retains old configuration even after business deletion and parent cascade.
-- +goose StatementBegin
CREATE FUNCTION market_archive_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO market_object_versions(target_kind,target_id,revision,payload)
 VALUES(TG_ARGV[0],NEW.id,NEW.revision,to_jsonb(NEW));
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER market_supplier_version AFTER INSERT OR UPDATE ON market_suppliers FOR EACH ROW EXECUTE FUNCTION market_archive_version('supplier');
CREATE TRIGGER market_group_version AFTER INSERT OR UPDATE ON market_groups FOR EACH ROW EXECUTE FUNCTION market_archive_version('group');
CREATE TRIGGER market_model_version AFTER INSERT OR UPDATE ON market_group_models FOR EACH ROW EXECUTE FUNCTION market_archive_version('group_model');
CREATE TRIGGER market_address_version AFTER INSERT OR UPDATE ON market_addresses FOR EACH ROW EXECUTE FUNCTION market_archive_version('address');
CREATE TRIGGER market_versions_immutable BEFORE UPDATE OR DELETE ON market_object_versions FOR EACH ROW EXECUTE FUNCTION market_append_only();
CREATE TRIGGER market_report_binding_immutable BEFORE UPDATE OR DELETE ON market_report_bindings FOR EACH ROW EXECUTE FUNCTION market_append_only();
CREATE TRIGGER market_search_setting_immutable BEFORE UPDATE OR DELETE ON market_search_settings FOR EACH ROW EXECUTE FUNCTION market_append_only();
CREATE TRIGGER market_group_no_delete BEFORE DELETE ON market_groups FOR EACH ROW EXECUTE FUNCTION market_append_only();
CREATE TRIGGER market_address_no_delete BEFORE DELETE ON market_addresses FOR EACH ROW EXECUTE FUNCTION market_append_only();
CREATE TRIGGER market_model_no_delete BEFORE DELETE ON market_group_models FOR EACH ROW EXECUTE FUNCTION market_append_only();
CREATE TRIGGER market_group_address_no_delete BEFORE DELETE ON market_group_addresses FOR EACH ROW EXECUTE FUNCTION market_append_only();

-- +goose Down
-- Only additive configuration is removed; protected pre-existing tables stay.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM market_suppliers) OR EXISTS(SELECT 1 FROM market_quotes) OR EXISTS(SELECT 1 FROM market_protected_keys) THEN
  RAISE EXCEPTION 'populated market metadata cannot be downgraded; restore an authorized backup';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE channels DROP COLUMN market_group_id;
DROP TABLE market_capability_evidence, market_report_bindings, market_search_settings, market_model_governance, market_protected_keys;
ALTER TABLE market_group_models DROP CONSTRAINT market_current_quote_owner;
DROP TABLE market_quote_tier_token_prices, market_quote_unit_prices, market_quote_tiers, market_quote_token_prices, market_quotes;
DROP TABLE market_group_models, market_group_addresses, market_addresses, market_groups, market_suppliers;
DROP TABLE market_object_versions;
DROP FUNCTION market_guard_model(), market_guard_group(), market_guard_supplier(), market_archive_version(), market_guard_group_address(), market_append_only();
