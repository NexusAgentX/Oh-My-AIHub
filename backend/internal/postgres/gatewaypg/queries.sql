-- 网关领域全部 SQL：平台 Key、模型池、调用快照、尝试、终结与结算事实。
-- 事务边界、隔离级别（BeginCall 为 REPEATABLE READ，终结类为 SERIALIZABLE）与保存点
-- 由 store.go / calls.go 的调用顺序决定，本文件只描述单条语句。

-- name: InsertAPIKey :one
INSERT INTO api_keys (owner_account_id, display_name, key_prefix, key_hash)
SELECT a.id, @display_name, @key_prefix, @key_hash
FROM accounts a
WHERE a.id = @owner_account_id AND a.status = 'active' AND NOT a.must_change_password
RETURNING id;

-- name: ListAPIKeyIDs :many
SELECT id FROM api_keys
WHERE owner_account_id = @owner_account_id AND status <> 'deleted'
ORDER BY updated_at DESC, id;

-- name: GetAPIKey :one
SELECT id, owner_account_id, display_name, key_prefix, generation, status, version,
	last_used_at, created_at, updated_at
FROM api_keys
WHERE id = @id AND owner_account_id = @owner_account_id AND status <> 'deleted';

-- name: ListAPIKeyPools :many
SELECT pool.id, pool.canonical_model_id, model.name AS model_name, pool.protocol, pool.version,
	pool.created_at, pool.updated_at
FROM api_model_pools pool
JOIN models model ON model.id = pool.canonical_model_id
WHERE pool.api_key_id = @api_key_id AND pool.status = 'active'
ORDER BY pool.created_at, pool.id;

-- name: LockAPIKey :one
SELECT version, status FROM api_keys
WHERE id = @id AND owner_account_id = @owner_account_id FOR UPDATE;

-- name: LockAPIKeyAtVersion :one
SELECT status FROM api_keys
WHERE id = @id AND owner_account_id = @owner_account_id AND version = @version FOR UPDATE;

-- name: RenameAPIKey :exec
UPDATE api_keys SET display_name = @display_name, version = version + 1, updated_at = now()
WHERE id = @id AND owner_account_id = @owner_account_id;

-- name: RotateAPIKey :execrows
UPDATE api_keys
SET key_prefix = @key_prefix, key_hash = @key_hash, generation = generation + 1,
	version = version + 1, updated_at = now()
WHERE id = @id AND owner_account_id = @owner_account_id AND version = @expected_version AND status <> 'deleted';

-- name: SetAPIKeyStatus :execrows
UPDATE api_keys
SET status = @status::text, version = version + 1, updated_at = now(),
	deleted_at = CASE WHEN @status::text = 'deleted' THEN now() ELSE NULL END
WHERE id = @id AND owner_account_id = @owner_account_id AND version = @expected_version;

-- name: AuthenticateAPIKey :one
SELECT k.id, k.owner_account_id, k.generation, k.key_hash
FROM api_keys k
JOIN accounts a ON a.id = k.owner_account_id
WHERE k.key_hash = @key_hash AND k.status = 'active'
	AND a.status = 'active' AND NOT a.must_change_password;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = now() WHERE id = @id;

-- 提交确认丢失后重读不可变事实：创建与轮换各自核对写入的内容。
-- name: CreatedAPIKeyMatches :one
SELECT EXISTS (
	SELECT 1 FROM api_keys
	WHERE id = @id AND owner_account_id = @owner_account_id AND display_name = @display_name
		AND key_prefix = @key_prefix AND key_hash = @key_hash AND generation = 1 AND version = 1
)::boolean;

-- name: RotatedAPIKeyMatches :one
SELECT EXISTS (
	SELECT 1 FROM api_keys
	WHERE id = @id AND owner_account_id = @owner_account_id AND key_prefix = @key_prefix
		AND key_hash = @key_hash AND version = @expected_version::bigint + 1
		AND generation > 1 AND status <> 'deleted'
)::boolean;

-- name: LockActivePool :one
SELECT id FROM api_model_pools
WHERE api_key_id = @api_key_id AND canonical_model_id = @canonical_model_id AND protocol = @protocol AND status = 'active'
FOR UPDATE;

-- name: InsertPool :one
INSERT INTO api_model_pools (api_key_id, canonical_model_id, protocol)
VALUES (@api_key_id, @canonical_model_id, @protocol) RETURNING id;

-- name: BumpPoolVersion :exec
UPDATE api_model_pools SET version = version + 1, updated_at = now() WHERE id = @id;

-- name: DeletePoolMembers :exec
DELETE FROM api_pool_members WHERE pool_id = @pool_id;

-- name: InsertPoolMember :exec
INSERT INTO api_pool_members (pool_id, offer_id, priority, added_validation_version)
VALUES (@pool_id, @offer_id, @priority, @added_validation_version);

-- name: DeleteStalePools :exec
UPDATE api_model_pools SET status = 'deleted', deleted_at = now(), version = version + 1, updated_at = now()
WHERE api_key_id = @api_key_id AND status = 'active' AND NOT (id = ANY(@keep_pool_ids::uuid[]));

-- name: ListExistingPoolOffers :many
SELECT member.offer_id, member.added_validation_version
FROM api_pool_members member
JOIN api_model_pools pool ON pool.id = member.pool_id
WHERE pool.api_key_id = @api_key_id AND pool.status = 'active';

-- name: ListPoolOfferReferences :many
SELECT offer_id, added_validation_version FROM api_pool_members WHERE pool_id = @pool_id ORDER BY priority;

-- 池成员展示：路由资格、报价与近期指标。价格仅在可表示范围内计算，否则为 NULL；
-- NULLIF(... ELSE -1, -1) 让 sqlc 把这些列生成为可空（合法价格恒 >= 0，哨兵值不会冲突）。
-- name: ListPoolMembers :many
WITH pool_offers AS (
	SELECT offer_id FROM api_pool_members WHERE pool_id = @pool_id
), metrics AS (
	SELECT attempt.offer_id,
		round((count(*) FILTER (WHERE attempt.status = 'succeeded'))::numeric /
			NULLIF(count(*) FILTER (WHERE attempt.status IN ('succeeded', 'failed', 'cancelled', 'incomplete')), 0), 4)::text AS success_rate,
		round(avg(attempt.ttft_milliseconds) FILTER (WHERE attempt.status = 'succeeded'))::bigint AS ttft_milliseconds,
		round((avg(attempt.tokens_per_second_nano) FILTER (WHERE attempt.status = 'succeeded'))::numeric / 1000000000, 3)::text AS tokens_per_second
	FROM api_call_attempts attempt
	WHERE attempt.offer_id IN (SELECT offer_id FROM pool_offers)
	GROUP BY attempt.offer_id
)
SELECT member.priority, member.offer_id, cm.channel_id, c.display_name AS channel_display_name,
	owner.display_name AS owner_display_name,
	member.added_validation_version, offer.validation_version AS current_validation_version,
	model.context_window, cm.model_id, cm.multiplier_nano,
	owner.status AS owner_status, owner.must_change_password AS owner_must_change_password,
	c.status AS channel_status, model.status AS model_status, offer.status AS offer_status,
	latest.status AS validation_status, (credential.channel_id IS NOT NULL)::boolean AS credential_configured,
	NULLIF(CASE WHEN model.input_price_nano_per_million BETWEEN 0 AND 100000000000000
		THEN ceil(model.input_price_nano_per_million::numeric * cm.multiplier_nano::numeric / 1000000000)::bigint ELSE -1 END, -1)::bigint AS input_price,
	NULLIF(CASE WHEN model.output_price_nano_per_million BETWEEN 0 AND 100000000000000
		THEN ceil(model.output_price_nano_per_million::numeric * cm.multiplier_nano::numeric / 1000000000)::bigint ELSE -1 END, -1)::bigint AS output_price,
	NULLIF(CASE WHEN model.cache_write_price_nano_per_million BETWEEN 0 AND 100000000000000
		THEN ceil(model.cache_write_price_nano_per_million::numeric * cm.multiplier_nano::numeric / 1000000000)::bigint ELSE -1 END, -1)::bigint AS cache_write_price,
	NULLIF(CASE WHEN model.cache_read_price_nano_per_million BETWEEN 0 AND 100000000000000
		THEN ceil(model.cache_read_price_nano_per_million::numeric * cm.multiplier_nano::numeric / 1000000000)::bigint ELSE -1 END, -1)::bigint AS cache_read_price,
	metrics.success_rate, metrics.ttft_milliseconds, metrics.tokens_per_second
FROM api_pool_members member
JOIN channel_offers offer ON offer.id = member.offer_id
JOIN channel_models cm ON cm.id = offer.channel_model_id
JOIN channels c ON c.id = cm.channel_id
JOIN accounts owner ON owner.id = c.owner_account_id
JOIN models model ON model.id = cm.model_id
LEFT JOIN channel_credentials credential ON credential.channel_id = c.id AND credential.credential_version = c.credential_version
LEFT JOIN channel_validation_attempts latest ON latest.offer_id = offer.id
	AND latest.validation_version = offer.validation_version AND latest.attempt_seq = offer.validation_attempt_seq
LEFT JOIN metrics ON metrics.offer_id = offer.id
WHERE member.pool_id = @pool_id
ORDER BY member.priority;
