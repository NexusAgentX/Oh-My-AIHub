-- name: InsertSupplier :one
INSERT INTO market_suppliers (id,contributor_id,name,kind,website_url,icon_data,icon_source,summary,description,docs_url,enabled,revision,deleted_at)
VALUES (sqlc.arg(id),sqlc.arg(contributor_id),sqlc.arg(name),sqlc.arg(kind),sqlc.arg(website_url),sqlc.arg(icon_data),sqlc.arg(icon_source),sqlc.arg(summary),sqlc.arg(description),sqlc.arg(docs_url),sqlc.arg(enabled),sqlc.arg(revision),sqlc.narg(deleted_at)) RETURNING *;

-- name: UpdateSupplier :one
UPDATE market_suppliers SET name=sqlc.arg(name),kind=sqlc.arg(kind),website_url=sqlc.arg(website_url),icon_data=sqlc.arg(icon_data),icon_source=sqlc.arg(icon_source),summary=sqlc.arg(summary),description=sqlc.arg(description),docs_url=sqlc.arg(docs_url),enabled=sqlc.arg(enabled),revision=sqlc.arg(revision),deleted_at=sqlc.narg(deleted_at),updated_at=now() WHERE id=sqlc.arg(id) AND revision=sqlc.arg(base_revision) RETURNING *;

-- name: GetSupplier :one
SELECT * FROM market_suppliers WHERE id=$1;

-- name: ListSuppliers :many
SELECT * FROM market_suppliers WHERE deleted_at IS NULL ORDER BY id;

-- name: InsertGroup :one
INSERT INTO market_groups (id,supplier_id,name,description,status_url,user_agent,header_rules,concurrency_limit,rpm_limit,ttft_timeout_ms,request_timeout_ms,idle_timeout_ms,cooldown_failures,cooldown_seconds,enabled,revision,connection_version,deleted_at)
VALUES (sqlc.arg(id),sqlc.arg(supplier_id),sqlc.arg(name),sqlc.arg(description),sqlc.arg(status_url),sqlc.narg(user_agent),sqlc.narg(header_rules),sqlc.narg(concurrency_limit),sqlc.narg(rpm_limit),sqlc.narg(ttft_timeout_ms),sqlc.narg(request_timeout_ms),sqlc.narg(idle_timeout_ms),sqlc.narg(cooldown_failures),sqlc.narg(cooldown_seconds),sqlc.arg(enabled),sqlc.arg(revision),sqlc.arg(connection_version),sqlc.narg(deleted_at)) RETURNING *;

-- name: UpdateGroup :one
UPDATE market_groups SET name=sqlc.arg(name),description=sqlc.arg(description),status_url=sqlc.arg(status_url),user_agent=sqlc.narg(user_agent),header_rules=sqlc.narg(header_rules),concurrency_limit=sqlc.narg(concurrency_limit),rpm_limit=sqlc.narg(rpm_limit),ttft_timeout_ms=sqlc.narg(ttft_timeout_ms),request_timeout_ms=sqlc.narg(request_timeout_ms),idle_timeout_ms=sqlc.narg(idle_timeout_ms),cooldown_failures=sqlc.narg(cooldown_failures),cooldown_seconds=sqlc.narg(cooldown_seconds),enabled=sqlc.arg(enabled),revision=sqlc.arg(revision),connection_version=sqlc.arg(connection_version),deleted_at=sqlc.narg(deleted_at),updated_at=now() WHERE id=sqlc.arg(id) AND revision=sqlc.arg(base_revision) AND supplier_id=sqlc.arg(supplier_id) RETURNING *;

-- name: GetGroup :one
SELECT * FROM market_groups WHERE id=$1;

-- name: ListGroups :many
SELECT * FROM market_groups WHERE deleted_at IS NULL ORDER BY id;

-- name: InsertAddress :one
INSERT INTO market_addresses (id,supplier_id,base_url,label,revision,deleted_at)
VALUES (sqlc.arg(id),sqlc.arg(supplier_id),sqlc.arg(base_url),sqlc.arg(label),sqlc.arg(revision),sqlc.narg(deleted_at)) RETURNING *;

-- name: UpdateAddress :one
UPDATE market_addresses SET base_url=sqlc.arg(base_url),label=sqlc.arg(label),revision=sqlc.arg(revision),deleted_at=sqlc.narg(deleted_at) WHERE id=sqlc.arg(id) AND revision=sqlc.arg(base_revision) AND supplier_id=sqlc.arg(supplier_id) RETURNING *;

-- name: GetAddress :one
SELECT * FROM market_addresses WHERE id=$1;

-- name: ListAddresses :many
SELECT * FROM market_addresses WHERE deleted_at IS NULL ORDER BY id;

-- name: InsertGroupModel :one
INSERT INTO market_group_models (id,group_id,model_id,display_name,upstream_model,model_type,context_window,max_output_tokens,notes,enabled,is_reference,revision,capability_digest,deleted_at)
VALUES (sqlc.arg(id),sqlc.arg(group_id),sqlc.arg(model_id),sqlc.arg(display_name),sqlc.arg(upstream_model),sqlc.arg(model_type),sqlc.narg(context_window),sqlc.narg(max_output_tokens),sqlc.arg(notes),sqlc.arg(enabled),sqlc.arg(is_reference),sqlc.arg(revision),sqlc.arg(capability_digest),sqlc.narg(deleted_at)) RETURNING *;

-- name: UpdateGroupModel :one
UPDATE market_group_models SET model_id=sqlc.arg(model_id),display_name=sqlc.arg(display_name),upstream_model=sqlc.arg(upstream_model),model_type=sqlc.arg(model_type),context_window=sqlc.narg(context_window),max_output_tokens=sqlc.narg(max_output_tokens),notes=sqlc.arg(notes),enabled=sqlc.arg(enabled),is_reference=sqlc.arg(is_reference),revision=sqlc.arg(revision),capability_digest=sqlc.arg(capability_digest),deleted_at=sqlc.narg(deleted_at),updated_at=now() WHERE id=sqlc.arg(id) AND revision=sqlc.arg(base_revision) AND group_id=sqlc.arg(group_id) RETURNING *;

-- name: GetGroupModel :one
SELECT * FROM market_group_models WHERE id=$1;

-- name: ListGroupModels :many
SELECT * FROM market_group_models WHERE deleted_at IS NULL ORDER BY id;

-- name: InsertQuote :one
INSERT INTO market_quotes (id,group_model_id,version,currency,billing_unit,input_price_nano,output_price_nano,cache_write_price_nano,cache_read_price_nano)
VALUES (sqlc.arg(id),sqlc.arg(group_model_id),sqlc.arg(version),sqlc.arg(currency),sqlc.arg(billing_unit),sqlc.arg(input_price_nano),sqlc.arg(output_price_nano),sqlc.arg(cache_write_price_nano),sqlc.arg(cache_read_price_nano)) RETURNING *;

-- name: GetQuote :one
SELECT * FROM market_quotes WHERE id=$1;

-- name: CurrentQuotes :many
SELECT q.* FROM market_quotes q JOIN market_group_models m ON m.current_quote_id=q.id WHERE m.deleted_at IS NULL ORDER BY q.group_model_id;

-- name: LockModel :one
SELECT * FROM market_group_models WHERE id=$1 AND deleted_at IS NULL FOR UPDATE;
-- name: QuoteVersion :one
SELECT COALESCE(max(version),0)::bigint AS version FROM market_quotes WHERE group_model_id=$1;
-- name: SetCurrentQuote :execrows
UPDATE market_group_models SET current_quote_id=$2,revision=revision+1,updated_at=now() WHERE id=$1 AND deleted_at IS NULL;
-- name: InsertTokenPrice :exec
INSERT INTO market_quote_token_prices(quote_id,dimension,price_nano) VALUES($1,$2,$3);
-- name: CurrentTokenPrices :many
SELECT p.* FROM market_quote_token_prices p JOIN market_group_models m ON m.current_quote_id=p.quote_id WHERE m.deleted_at IS NULL ORDER BY p.quote_id,p.dimension;
-- name: CurrentTiers :many
SELECT t.* FROM market_quote_tiers t JOIN market_group_models m ON m.current_quote_id=t.quote_id WHERE m.deleted_at IS NULL ORDER BY t.quote_id,t.seq;
-- name: CurrentTierTokenPrices :many
SELECT p.* FROM market_quote_tier_token_prices p JOIN market_group_models m ON m.current_quote_id=p.quote_id WHERE m.deleted_at IS NULL ORDER BY p.quote_id,p.seq,p.dimension;
-- name: CurrentUnitPrices :many
SELECT p.* FROM market_quote_unit_prices p JOIN market_group_models m ON m.current_quote_id=p.quote_id WHERE m.deleted_at IS NULL ORDER BY p.quote_id,p.seq;
-- name: ListGroupAddresses :many
SELECT * FROM market_group_addresses WHERE deleted_at IS NULL ORDER BY group_id,position;
-- name: RetireGroupAddresses :exec
UPDATE market_group_addresses SET deleted_at=now() WHERE group_id=$1 AND deleted_at IS NULL;
-- name: UpsertGroupAddress :execrows
INSERT INTO market_group_addresses(id,group_id,address_id,supplier_id,position,deleted_at) VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT(id) DO UPDATE SET position=EXCLUDED.position,deleted_at=EXCLUDED.deleted_at
WHERE market_group_addresses.group_id=EXCLUDED.group_id AND market_group_addresses.address_id=EXCLUDED.address_id AND market_group_addresses.supplier_id=EXCLUDED.supplier_id;
-- name: ObjectVersions :many
SELECT * FROM market_object_versions WHERE target_kind=$1 AND target_id=$2 ORDER BY revision DESC LIMIT $3;


-- name: InsertTier :exec
INSERT INTO market_quote_tiers(quote_id,seq,name,service_tier,min_prompt_tokens,max_prompt_tokens,timezone,weekdays,start_minute,end_minute,input_price_nano,output_price_nano,cache_write_price_nano,cache_read_price_nano) VALUES(sqlc.arg(quote_id),sqlc.arg(seq),sqlc.arg(name),sqlc.arg(service_tier),sqlc.narg(min_prompt_tokens),sqlc.narg(max_prompt_tokens),sqlc.arg(timezone),sqlc.arg(weekdays),sqlc.narg(start_minute),sqlc.narg(end_minute),sqlc.arg(input_price_nano),sqlc.arg(output_price_nano),sqlc.arg(cache_write_price_nano),sqlc.arg(cache_read_price_nano));

-- name: InsertTierTokenPrice :exec
INSERT INTO market_quote_tier_token_prices(quote_id,seq,dimension,price_nano) VALUES(sqlc.arg(quote_id),sqlc.arg(seq),sqlc.arg(dimension),sqlc.arg(price_nano));

-- name: InsertUnitPrice :exec
INSERT INTO market_quote_unit_prices(quote_id,seq,unit,tool,image_size,image_quality,image_background,service_tier,price_nano) VALUES(sqlc.arg(quote_id),sqlc.arg(seq),sqlc.arg(unit),sqlc.arg(tool),sqlc.arg(image_size),sqlc.arg(image_quality),sqlc.arg(image_background),sqlc.arg(service_tier),sqlc.arg(price_nano));

-- name: BumpGroupConnection :execrows
UPDATE market_groups SET connection_version=connection_version+1,revision=revision+1,updated_at=now() WHERE id=$1 AND deleted_at IS NULL;

-- name: GroupsForAddress :many
SELECT DISTINCT group_id FROM market_group_addresses WHERE address_id=$1 AND deleted_at IS NULL ORDER BY group_id;
-- name: RetireAddressLinks :exec
UPDATE market_group_addresses SET deleted_at=now() WHERE address_id=$1 AND deleted_at IS NULL;

-- name: LockProtectedRoot :exec
SELECT pg_advisory_xact_lock(hashtextextended('market-protected:'||$1,0));
-- name: GetProtectedRoot :one
SELECT * FROM market_protected_keys WHERE purpose=$1;
-- name: ListProtectedRoots :many
SELECT * FROM market_protected_keys ORDER BY purpose;
-- name: InsertProtectedRoot :exec
INSERT INTO market_protected_keys(purpose,encryption_key_id,credential_version,nonce,ciphertext) VALUES($1,$2,$3,$4,$5);
-- name: RewrapProtectedRoot :exec
UPDATE market_protected_keys SET encryption_key_id=$2,credential_version=$3,nonce=$4,ciphertext=$5 WHERE purpose=$1;

-- name: ListModelRules :many
SELECT * FROM market_model_governance ORDER BY model_id;

-- name: InsertModelRule :execrows
INSERT INTO market_model_governance(model_id,disabled,reason,revision,actor_id) VALUES($1,$2,$3,1,$4)
ON CONFLICT DO NOTHING;

-- name: UpdateModelRule :execrows
UPDATE market_model_governance SET disabled=$2,reason=$3,revision=revision+1,actor_id=$4,updated_at=now()
WHERE model_id=$1 AND revision=sqlc.arg(base_revision);
