-- 渠道领域全部 SQL。锁顺序（先渠道、再渠道模型/报价）由 store.go 中的调用顺序保证。

-- name: GetChannel :one
SELECT c.id, c.owner_account_id, owner.display_name AS owner_display_name,
	owner.status AS owner_status, owner.must_change_password AS owner_must_change_password,
	c.display_name, c.normalized_base_url,
	(credential.channel_id IS NOT NULL)::boolean AS credential_configured,
	c.credential_version, c.credential_updated_at, c.status, c.version,
	c.created_at, c.updated_at
FROM channels c
JOIN accounts owner ON owner.id = c.owner_account_id
LEFT JOIN channel_credentials credential ON credential.channel_id = c.id
WHERE c.id = @id;

-- name: ListOwnerChannelIDs :many
SELECT id FROM channels WHERE owner_account_id = @owner_account_id ORDER BY updated_at DESC, id;

-- name: ListAllChannelIDs :many
SELECT id FROM channels ORDER BY updated_at DESC, id;

-- name: IsChannelOwner :one
SELECT EXISTS (SELECT 1 FROM channels WHERE id = @id AND owner_account_id = @owner_account_id)::boolean;

-- 报价读取与指标连接：ListChannelOffers 与 GetOffer 的列表必须逐列相同，
-- store.go 靠结构体转换共用一个映射函数（sqlc 没有片段复用）。

-- name: ListChannelOffers :many
WITH offer_metrics AS (
	SELECT gateway_attempt.offer_id,
		round((count(*) FILTER (WHERE gateway_attempt.status = 'succeeded'))::numeric /
			NULLIF(count(*) FILTER (WHERE gateway_attempt.status IN ('succeeded', 'failed', 'cancelled', 'incomplete')), 0), 4)::text AS success_rate,
		round(avg(gateway_attempt.ttft_milliseconds) FILTER (WHERE gateway_attempt.status = 'succeeded'))::bigint AS ttft_milliseconds,
		round((avg(gateway_attempt.tokens_per_second_nano) FILTER (WHERE gateway_attempt.status = 'succeeded'))::numeric /
			1000000000, 3)::text AS tokens_per_second,
		NULLIF(count(*) FILTER (WHERE gateway_attempt.status IN ('succeeded', 'failed', 'cancelled', 'incomplete')), 0)::bigint AS call_count
	FROM api_call_attempts gateway_attempt
	WHERE gateway_attempt.offer_id IN (
		SELECT scoped_offer.id FROM channel_offers scoped_offer
		JOIN channel_models scoped_model ON scoped_model.id = scoped_offer.channel_model_id
		WHERE scoped_model.channel_id = @channel_id)
	GROUP BY gateway_attempt.offer_id
), offer_income AS (
	SELECT gateway_call.final_offer_id AS offer_id,
		NULLIF(sum(gateway_settlement.provider_charge_nano), 0)::bigint AS provider_income_nano
	FROM api_call_settlements gateway_settlement
	JOIN api_calls gateway_call ON gateway_call.id = gateway_settlement.call_id
	WHERE gateway_call.status = 'succeeded'
		AND gateway_call.final_offer_id IN (
			SELECT scoped_offer.id FROM channel_offers scoped_offer
			JOIN channel_models scoped_model ON scoped_model.id = scoped_offer.channel_model_id
			WHERE scoped_model.channel_id = @channel_id)
	GROUP BY gateway_call.final_offer_id
)
SELECT o.id, cm.channel_id, cm.model_id, m.name AS model_name, m.provider AS model_provider, o.protocol,
	o.upstream_model_id, COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano)::bigint AS multiplier_nano,
	o.status, o.validation_version, o.version,
	m.status AS model_status, m.context_window, m.input_price_nano_per_million, m.output_price_nano_per_million,
	m.cache_write_price_nano_per_million, m.cache_read_price_nano_per_million,
	attempt.id AS attempt_id, attempt.attempt_seq, attempt.actor_account_id AS attempt_actor_account_id,
	attempt.status AS attempt_status, attempt.error_category, COALESCE(attempt.http_status, 0)::integer AS http_status,
	attempt.raw_error, attempt.raw_error_truncated, attempt.duration_milliseconds,
	attempt.started_at, attempt.completed_at, o.created_at, o.updated_at,
	gateway_metrics.success_rate, gateway_metrics.ttft_milliseconds,
	gateway_metrics.tokens_per_second, gateway_metrics.call_count,
	gateway_income.provider_income_nano
FROM channel_offers o
JOIN channel_models cm ON cm.id = o.channel_model_id
JOIN models m ON m.id = cm.model_id
LEFT JOIN channel_validation_attempts attempt
	ON attempt.offer_id = o.id
	AND attempt.validation_version = o.validation_version
	AND attempt.attempt_seq = o.validation_attempt_seq
LEFT JOIN offer_metrics gateway_metrics ON gateway_metrics.offer_id = o.id
LEFT JOIN offer_income gateway_income ON gateway_income.offer_id = o.id
WHERE cm.channel_id = @channel_id AND (@include_deleted::boolean OR o.status <> 'deleted')
ORDER BY m.provider, m.name, o.protocol, o.created_at, o.id;

-- name: GetOffer :one
WITH offer_metrics AS (
	SELECT gateway_attempt.offer_id,
		round((count(*) FILTER (WHERE gateway_attempt.status = 'succeeded'))::numeric /
			NULLIF(count(*) FILTER (WHERE gateway_attempt.status IN ('succeeded', 'failed', 'cancelled', 'incomplete')), 0), 4)::text AS success_rate,
		round(avg(gateway_attempt.ttft_milliseconds) FILTER (WHERE gateway_attempt.status = 'succeeded'))::bigint AS ttft_milliseconds,
		round((avg(gateway_attempt.tokens_per_second_nano) FILTER (WHERE gateway_attempt.status = 'succeeded'))::numeric /
			1000000000, 3)::text AS tokens_per_second,
		NULLIF(count(*) FILTER (WHERE gateway_attempt.status IN ('succeeded', 'failed', 'cancelled', 'incomplete')), 0)::bigint AS call_count
	FROM api_call_attempts gateway_attempt
	WHERE gateway_attempt.offer_id = @id
	GROUP BY gateway_attempt.offer_id
), offer_income AS (
	SELECT gateway_call.final_offer_id AS offer_id,
		NULLIF(sum(gateway_settlement.provider_charge_nano), 0)::bigint AS provider_income_nano
	FROM api_call_settlements gateway_settlement
	JOIN api_calls gateway_call ON gateway_call.id = gateway_settlement.call_id
	WHERE gateway_call.status = 'succeeded'
		AND gateway_call.final_offer_id = @id
	GROUP BY gateway_call.final_offer_id
)
SELECT o.id, cm.channel_id, cm.model_id, m.name AS model_name, m.provider AS model_provider, o.protocol,
	o.upstream_model_id, COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano)::bigint AS multiplier_nano,
	o.status, o.validation_version, o.version,
	m.status AS model_status, m.context_window, m.input_price_nano_per_million, m.output_price_nano_per_million,
	m.cache_write_price_nano_per_million, m.cache_read_price_nano_per_million,
	attempt.id AS attempt_id, attempt.attempt_seq, attempt.actor_account_id AS attempt_actor_account_id,
	attempt.status AS attempt_status, attempt.error_category, COALESCE(attempt.http_status, 0)::integer AS http_status,
	attempt.raw_error, attempt.raw_error_truncated, attempt.duration_milliseconds,
	attempt.started_at, attempt.completed_at, o.created_at, o.updated_at,
	gateway_metrics.success_rate, gateway_metrics.ttft_milliseconds,
	gateway_metrics.tokens_per_second, gateway_metrics.call_count,
	gateway_income.provider_income_nano
FROM channel_offers o
JOIN channel_models cm ON cm.id = o.channel_model_id
JOIN models m ON m.id = cm.model_id
LEFT JOIN channel_validation_attempts attempt
	ON attempt.offer_id = o.id
	AND attempt.validation_version = o.validation_version
	AND attempt.attempt_seq = o.validation_attempt_seq
LEFT JOIN offer_metrics gateway_metrics ON gateway_metrics.offer_id = o.id
LEFT JOIN offer_income gateway_income ON gateway_income.offer_id = o.id
WHERE o.id = @id;

-- name: InsertChannel :execrows
INSERT INTO channels (
	id, owner_account_id, display_name, normalized_base_url,
	credential_version, credential_updated_at
)
SELECT @id::uuid, a.id, @display_name::text, @normalized_base_url::text, @credential_version::bigint, now()
FROM accounts a
WHERE a.id = @owner_account_id AND a.status = 'active' AND NOT a.must_change_password;

-- name: InsertCredential :exec
INSERT INTO channel_credentials (channel_id, credential_version, key_id, nonce, ciphertext)
VALUES (@channel_id, @credential_version, @key_id, @nonce, @ciphertext);

-- name: UpsertCredential :exec
INSERT INTO channel_credentials (channel_id, credential_version, key_id, nonce, ciphertext)
VALUES (@channel_id, @credential_version, @key_id, @nonce, @ciphertext)
ON CONFLICT (channel_id) DO UPDATE SET
	credential_version = EXCLUDED.credential_version,
	key_id = EXCLUDED.key_id,
	nonce = EXCLUDED.nonce,
	ciphertext = EXCLUDED.ciphertext,
	configured_at = now(), updated_at = now();

-- name: DeleteCredential :exec
DELETE FROM channel_credentials WHERE channel_id = @channel_id;

-- name: LockModelStatusShared :one
SELECT status FROM models WHERE id = @id FOR SHARE;

-- name: InsertChannelModel :one
INSERT INTO channel_models (channel_id, model_id, multiplier_nano)
VALUES (@channel_id, @model_id, @multiplier_nano)
RETURNING id;

-- name: GetChannelModelMultiplier :one
SELECT multiplier_nano FROM channel_models WHERE id = @id;

-- name: UpdateChannelModelMultiplier :exec
UPDATE channel_models SET multiplier_nano = @multiplier_nano, updated_at = now() WHERE id = @id;

-- name: InsertOffer :exec
INSERT INTO channel_offers (id, channel_model_id, protocol, upstream_model_id)
VALUES (@id, @channel_model_id, @protocol, @upstream_model_id);

-- name: LockOwnerChannel :one
SELECT c.version, c.credential_version, c.normalized_base_url, c.status
FROM channels c JOIN accounts owner ON owner.id = c.owner_account_id
WHERE c.id = @id AND c.owner_account_id = @owner_account_id
	AND owner.status = 'active' AND NOT owner.must_change_password
FOR UPDATE;

-- name: UpdateChannelConfig :exec
UPDATE channels SET display_name = @display_name, normalized_base_url = @normalized_base_url,
	credential_version = @credential_version,
	credential_updated_at = CASE WHEN @credential_changed::boolean THEN now() ELSE credential_updated_at END,
	version = version + 1, updated_at = now()
WHERE id = @id;

-- 配置、吊销凭据或删除渠道后，渠道下所有未删除报价的验证作废并重新计数。
-- name: ResetChannelOfferValidation :exec
UPDATE channel_offers o SET validation_version = validation_version + 1,
	validation_attempt_seq = 0, updated_at = now()
FROM channel_models cm
WHERE o.channel_model_id = cm.id AND cm.channel_id = @channel_id AND o.status <> 'deleted';

-- name: BumpChannelVersion :exec
UPDATE channels SET version = version + 1, updated_at = now() WHERE id = @id;

-- name: LockChannelForAdmin :one
SELECT c.status, c.version, c.owner_account_id
FROM channels c
WHERE c.id = @id AND EXISTS (
	SELECT 1 FROM accounts actor WHERE actor.id = @actor_account_id AND actor.status = 'active' AND actor.is_admin
) FOR UPDATE;

-- name: LockChannelForOwner :one
SELECT c.status, c.version, c.owner_account_id
FROM channels c JOIN accounts owner ON owner.id = c.owner_account_id
WHERE c.id = @id AND c.owner_account_id = @actor_account_id
	AND owner.status = 'active' AND NOT owner.must_change_password
FOR UPDATE;

-- name: CountPublishableOffers :one
SELECT count(*)
FROM channel_offers offer
JOIN channel_models channel_model ON channel_model.id = offer.channel_model_id
JOIN models model ON model.id = channel_model.model_id
JOIN channel_credentials credential ON credential.channel_id = channel_model.channel_id
JOIN channel_validation_attempts attempt
	ON attempt.offer_id = offer.id
	AND attempt.validation_version = offer.validation_version
	AND attempt.attempt_seq = offer.validation_attempt_seq
WHERE channel_model.channel_id = @channel_id AND offer.status = 'active'
	AND model.status = 'active' AND attempt.status = 'passed';

-- name: UpdateChannelStatus :exec
UPDATE channels SET status = @status, version = version + 1, updated_at = now(),
	deleted_at = CASE WHEN @deleted::boolean THEN now() ELSE NULL END,
	credential_version = credential_version + CASE WHEN @deleted::boolean THEN 1 ELSE 0 END,
	credential_updated_at = CASE WHEN @deleted::boolean THEN now() ELSE credential_updated_at END
WHERE id = @id;

-- name: LockChannelWithCredential :one
SELECT c.owner_account_id, c.version
FROM channels c JOIN accounts owner ON owner.id = c.owner_account_id
JOIN channel_credentials credential ON credential.channel_id = c.id
WHERE c.id = @id AND c.owner_account_id = @actor_account_id AND c.status <> 'deleted'
	AND owner.status = 'active' AND NOT owner.must_change_password
FOR UPDATE;

-- name: BumpChannelCredentialRevoked :exec
UPDATE channels SET credential_version = credential_version + 1,
	credential_updated_at = now(), version = version + 1, updated_at = now()
WHERE id = @id;

-- name: LockLiveOwnerChannelVersion :one
SELECT c.version
FROM channels c JOIN accounts owner ON owner.id = c.owner_account_id
WHERE c.id = @id AND c.owner_account_id = @owner_account_id AND c.status <> 'deleted'
	AND owner.status = 'active' AND NOT owner.must_change_password
FOR UPDATE OF c;

-- name: LockChannelModel :one
SELECT cm.id, cm.multiplier_nano,
	(SELECT count(*) FROM channel_offers existing_offer
	 WHERE existing_offer.channel_model_id = cm.id AND existing_offer.status <> 'deleted')::bigint AS live_offer_count
FROM channel_models cm
WHERE cm.channel_id = @channel_id AND cm.model_id = @model_id
FOR UPDATE OF cm;

-- name: LockOfferForUpdate :one
SELECT o.version, o.upstream_model_id, o.protocol, o.channel_model_id, cm.multiplier_nano
FROM channel_offers o JOIN channel_models cm ON cm.id = o.channel_model_id
JOIN channels c ON c.id = cm.channel_id JOIN accounts owner ON owner.id = c.owner_account_id
WHERE o.id = @id AND c.owner_account_id = @owner_account_id AND o.status <> 'deleted' AND c.status <> 'deleted'
	AND owner.status = 'active' AND NOT owner.must_change_password
FOR UPDATE OF cm, o;

-- 倍率变化时同一渠道模型下所有未删除报价一起升版本；仅目标报价可能改上游模型并重置验证。
-- name: UpdateSiblingOffers :execrows
UPDATE channel_offers SET
	upstream_model_id = CASE WHEN id = @offer_id THEN @upstream_model_id::text ELSE upstream_model_id END,
	version = version + 1,
	validation_version = validation_version + CASE WHEN id = @offer_id AND @upstream_changed::boolean THEN 1 ELSE 0 END,
	validation_attempt_seq = CASE WHEN id = @offer_id AND @upstream_changed::boolean THEN 0 ELSE validation_attempt_seq END,
	updated_at = now()
WHERE channel_model_id = @channel_model_id AND status <> 'deleted';

-- name: UpdateOfferUpstream :exec
UPDATE channel_offers SET upstream_model_id = @upstream_model_id::text, version = version + 1,
	validation_version = validation_version + CASE WHEN @upstream_changed::boolean THEN 1 ELSE 0 END,
	validation_attempt_seq = CASE WHEN @upstream_changed::boolean THEN 0 ELSE validation_attempt_seq END,
	updated_at = now()
WHERE id = @id;

-- name: LockOfferForStatus :one
SELECT o.version, o.status
FROM channel_offers o JOIN channel_models cm ON cm.id = o.channel_model_id
JOIN channels c ON c.id = cm.channel_id JOIN accounts owner ON owner.id = c.owner_account_id
WHERE o.id = @id AND c.owner_account_id = @owner_account_id AND c.status <> 'deleted'
	AND owner.status = 'active' AND NOT owner.must_change_password
FOR UPDATE OF o;

-- name: UpdateOfferStatus :exec
UPDATE channel_offers offer SET status = @status::text, version = version + 1, updated_at = now(),
	deleted_at = CASE WHEN @status::text = 'deleted' THEN now() ELSE NULL END,
	deleted_multiplier_nano = CASE WHEN @status::text = 'deleted' THEN model.multiplier_nano ELSE NULL END
FROM channel_models model
WHERE offer.id = @id AND model.id = offer.channel_model_id;

-- 先锁渠道再锁报价，与配置变更同序，目标是一致的 Base URL、凭据与验证版本快照。
-- name: LockChannelForValidation :one
SELECT c.id
FROM channel_offers o
JOIN channel_models cm ON cm.id = o.channel_model_id
JOIN channels c ON c.id = cm.channel_id
JOIN accounts actor ON actor.id = @actor_account_id
WHERE o.id = @offer_id AND o.status <> 'deleted' AND c.status <> 'deleted'
	AND actor.status = 'active' AND NOT actor.must_change_password
	AND (c.owner_account_id = actor.id OR actor.is_admin)
FOR UPDATE OF c;

-- name: GetValidationTarget :one
SELECT cm.channel_id, c.owner_account_id, c.normalized_base_url,
	o.protocol, o.upstream_model_id, o.validation_version, o.validation_attempt_seq,
	credential.credential_version, credential.key_id, credential.nonce, credential.ciphertext
FROM channel_offers o
JOIN channel_models cm ON cm.id = o.channel_model_id
JOIN channels c ON c.id = cm.channel_id
JOIN channel_credentials credential
	ON credential.channel_id = c.id AND credential.credential_version = c.credential_version
JOIN accounts actor ON actor.id = @actor_account_id
WHERE o.id = @offer_id AND o.status <> 'deleted' AND c.status <> 'deleted'
	AND actor.status = 'active' AND NOT actor.must_change_password
	AND (c.owner_account_id = actor.id OR actor.is_admin)
	AND c.id = @channel_id
FOR UPDATE OF o;

-- name: InsertValidationAttempt :one
INSERT INTO channel_validation_attempts (
	offer_id, validation_version, attempt_seq, actor_account_id, status
) VALUES (@offer_id, @validation_version, @attempt_seq, @actor_account_id, 'in_progress')
RETURNING id, started_at;

-- name: SetOfferAttemptSeq :exec
UPDATE channel_offers SET validation_attempt_seq = @attempt_seq, updated_at = now()
WHERE id = @id;

-- name: CompleteValidationAttempt :execrows
UPDATE channel_validation_attempts SET
	status = @status::text, error_category = @error_category::text,
	http_status = NULLIF(@http_status::integer, 0), raw_error = @raw_error::text,
	raw_error_truncated = @raw_error_truncated::boolean, duration_milliseconds = @duration_milliseconds::bigint,
	completed_at = GREATEST(clock_timestamp(), started_at)
WHERE id = @id AND offer_id = @offer_id AND validation_version = @validation_version AND attempt_seq = @attempt_seq
	AND status = 'in_progress';

-- name: ExpireValidationAttempts :many
UPDATE channel_validation_attempts SET
	status = 'failed', error_category = 'timeout',
	raw_error = 'validation worker did not complete before recovery deadline',
	raw_error_truncated = false,
	duration_milliseconds = GREATEST(0, floor(extract(epoch FROM (clock_timestamp() - started_at)) * 1000))::bigint,
	completed_at = GREATEST(clock_timestamp(), started_at)
WHERE status = 'in_progress' AND started_at < @before::timestamptz
RETURNING offer_id, actor_account_id, validation_version, attempt_seq;

-- name: ListValidationAttempts :many
SELECT attempt.id, attempt.offer_id, attempt.validation_version,
	attempt.attempt_seq, attempt.actor_account_id, attempt.status,
	attempt.error_category, COALESCE(attempt.http_status, 0)::integer AS http_status,
	attempt.raw_error, attempt.raw_error_truncated,
	attempt.duration_milliseconds, attempt.started_at, attempt.completed_at
FROM channel_validation_attempts attempt
JOIN channel_offers offer ON offer.id = attempt.offer_id
JOIN channel_models channel_model ON channel_model.id = offer.channel_model_id
JOIN channels c ON c.id = channel_model.channel_id
JOIN accounts actor ON actor.id = @actor_account_id
WHERE attempt.offer_id = @offer_id
	AND actor.status = 'active' AND NOT actor.must_change_password
	AND (c.owner_account_id = actor.id OR actor.is_admin)
ORDER BY attempt.validation_version DESC, attempt.attempt_seq DESC
LIMIT @row_limit::bigint;

-- name: CanViewOfferValidation :one
SELECT EXISTS (
	SELECT 1 FROM channel_offers offer
	JOIN channel_models channel_model ON channel_model.id = offer.channel_model_id
	JOIN channels c ON c.id = channel_model.channel_id
	JOIN accounts actor ON actor.id = @actor_account_id
	WHERE offer.id = @offer_id AND actor.status = 'active' AND NOT actor.must_change_password
		AND (c.owner_account_id = actor.id OR actor.is_admin)
)::boolean;

-- 市场列表保留为单条查询：七种排序用 sort 参数选择排序键，ORDER BY 与键集游标条件
-- 都以 CASE 分支表达。原手写版本按排序拼接 SQL 文本，这里改为固定文本以便 sqlc 检查。
-- 排序键与方向（与旧实现一致，末位总是 offer_id ASC）：
--   价格类（input/output/cache_write/cache_read，其余未知值按 input_price）：price ASC
--   success_rate / tps：metric DESC NULLS LAST；ttft：metric ASC NULLS LAST
-- name: ListMarketOffers :many
WITH offer_metrics AS (
	SELECT gateway_attempt.offer_id,
		round((count(*) FILTER (WHERE gateway_attempt.status = 'succeeded'))::numeric /
			NULLIF(count(*) FILTER (WHERE gateway_attempt.status IN ('succeeded', 'failed', 'cancelled', 'incomplete')), 0), 4)::text AS success_rate,
		round(avg(gateway_attempt.ttft_milliseconds) FILTER (WHERE gateway_attempt.status = 'succeeded'))::bigint AS ttft_milliseconds,
		round((avg(gateway_attempt.tokens_per_second_nano) FILTER (WHERE gateway_attempt.status = 'succeeded'))::numeric /
			1000000000, 3)::text AS tokens_per_second,
		NULLIF(count(*) FILTER (WHERE gateway_attempt.status IN ('succeeded', 'failed', 'cancelled', 'incomplete')), 0)::bigint AS call_count
	FROM api_call_attempts gateway_attempt
	GROUP BY gateway_attempt.offer_id
), candidates AS (
	SELECT o.id AS offer_id, cm.channel_id, c.display_name AS channel_name,
		owner.id AS owner_account_id, owner.display_name AS owner_name, cm.model_id, m.name AS model_name,
		m.provider AS model_provider, o.protocol, COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano) AS multiplier_nano,
		CASE WHEN m.input_price_nano_per_million BETWEEN 0 AND 100000000000000
				AND COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano) BETWEEN 0 AND 1000000000000
			THEN ceil(m.input_price_nano_per_million::numeric * COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano)::numeric / 1000000000)::bigint END AS input_price_nano,
		CASE WHEN m.output_price_nano_per_million BETWEEN 0 AND 100000000000000
				AND COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano) BETWEEN 0 AND 1000000000000
			THEN ceil(m.output_price_nano_per_million::numeric * COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano)::numeric / 1000000000)::bigint END AS output_price_nano,
		CASE WHEN m.cache_write_price_nano_per_million BETWEEN 0 AND 100000000000000
				AND COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano) BETWEEN 0 AND 1000000000000
			THEN ceil(m.cache_write_price_nano_per_million::numeric * COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano)::numeric / 1000000000)::bigint END AS cache_write_price_nano,
		CASE WHEN m.cache_read_price_nano_per_million BETWEEN 0 AND 100000000000000
				AND COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano) BETWEEN 0 AND 1000000000000
			THEN ceil(m.cache_read_price_nano_per_million::numeric * COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano)::numeric / 1000000000)::bigint END AS cache_read_price_nano,
		attempt.completed_at AS last_tested_at,
		gateway_metrics.success_rate,
		gateway_metrics.ttft_milliseconds,
		gateway_metrics.tokens_per_second,
		gateway_metrics.call_count,
		(owner.status = 'active' AND NOT owner.must_change_password
			AND c.status = 'published' AND m.status = 'active' AND o.status = 'active'
			AND m.input_price_nano_per_million BETWEEN 0 AND 100000000000000
			AND m.output_price_nano_per_million BETWEEN 0 AND 100000000000000
			AND m.cache_write_price_nano_per_million BETWEEN 0 AND 100000000000000
			AND m.cache_read_price_nano_per_million BETWEEN 0 AND 100000000000000
			AND COALESCE(o.deleted_multiplier_nano, cm.multiplier_nano) BETWEEN 0 AND 1000000000000
			AND credential.channel_id IS NOT NULL AND attempt.status = 'passed')::boolean AS eligible
	FROM channel_offers o
	JOIN channel_models cm ON cm.id = o.channel_model_id
	JOIN channels c ON c.id = cm.channel_id
	JOIN accounts owner ON owner.id = c.owner_account_id
	JOIN models m ON m.id = cm.model_id
	LEFT JOIN channel_credentials credential
		ON credential.channel_id = c.id AND credential.credential_version = c.credential_version
	LEFT JOIN channel_validation_attempts attempt
		ON attempt.offer_id = o.id AND attempt.validation_version = o.validation_version
		AND attempt.attempt_seq = o.validation_attempt_seq
	LEFT JOIN offer_metrics gateway_metrics ON gateway_metrics.offer_id = o.id
), keyed AS (
	SELECT offer_id, channel_id, channel_name, owner_account_id, owner_name, model_id, model_name,
		model_provider, protocol, multiplier_nano,
		input_price_nano, output_price_nano, cache_write_price_nano, cache_read_price_nano,
		last_tested_at, success_rate, ttft_milliseconds, tokens_per_second,
		call_count, eligible,
		CASE sqlc.arg('sort')::text
			WHEN 'output_price' THEN output_price_nano
			WHEN 'cache_write_price' THEN cache_write_price_nano
			WHEN 'cache_read_price' THEN cache_read_price_nano
			ELSE input_price_nano
		END AS price_key,
		CASE sqlc.arg('sort')::text
			WHEN 'success_rate' THEN success_rate::numeric
			WHEN 'ttft' THEN ttft_milliseconds::numeric
			WHEN 'tps' THEN tokens_per_second::numeric
		END AS metric_key
	FROM candidates
)
SELECT offer_id, channel_id, channel_name, owner_account_id, owner_name,
	model_id, model_name, model_provider, protocol, multiplier_nano::bigint AS multiplier_nano,
	COALESCE(input_price_nano, 0)::bigint AS input_price_nano,
	COALESCE(output_price_nano, 0)::bigint AS output_price_nano,
	COALESCE(cache_write_price_nano, 0)::bigint AS cache_write_price_nano,
	COALESCE(cache_read_price_nano, 0)::bigint AS cache_read_price_nano,
	last_tested_at,
	success_rate, ttft_milliseconds, tokens_per_second, call_count
FROM keyed
WHERE eligible
	AND (sqlc.arg('model_id')::text = '' OR model_id = sqlc.arg('model_id')::text)
	AND (sqlc.arg('protocol')::text = '' OR protocol = sqlc.arg('protocol')::text)
	AND (sqlc.arg('owner_query')::text = '' OR owner_name ILIKE '%' || sqlc.arg('owner_query')::text || '%')
	AND (sqlc.narg('cursor_offer_id')::uuid IS NULL OR CASE
		WHEN sqlc.arg('sort')::text IN ('success_rate', 'ttft', 'tps') THEN (
			(sqlc.narg('cursor_metric')::text::numeric IS NOT NULL AND (
				metric_key IS NULL
				OR CASE WHEN sqlc.arg('sort')::text = 'ttft'
					THEN metric_key > sqlc.narg('cursor_metric')::text::numeric
					ELSE metric_key < sqlc.narg('cursor_metric')::text::numeric END
				OR (metric_key = sqlc.narg('cursor_metric')::text::numeric AND offer_id > sqlc.narg('cursor_offer_id')::uuid)))
			OR (sqlc.narg('cursor_metric')::text::numeric IS NULL AND metric_key IS NULL AND offer_id > sqlc.narg('cursor_offer_id')::uuid)
		)
		ELSE (price_key > sqlc.arg('cursor_price')::bigint
			OR (price_key = sqlc.arg('cursor_price')::bigint AND offer_id > sqlc.narg('cursor_offer_id')::uuid))
	END)
ORDER BY
	CASE WHEN sqlc.arg('sort')::text NOT IN ('success_rate', 'ttft', 'tps') THEN price_key END ASC,
	CASE WHEN sqlc.arg('sort')::text IN ('success_rate', 'tps') THEN metric_key END DESC NULLS LAST,
	CASE WHEN sqlc.arg('sort')::text = 'ttft' THEN metric_key END ASC NULLS LAST,
	offer_id ASC
LIMIT sqlc.arg('row_limit')::bigint;

-- name: CredentialInventory :many
SELECT credential.channel_id, credential.credential_version,
	credential.key_id, credential.nonce, credential.ciphertext
FROM channel_credentials credential
JOIN channels c ON c.id = credential.channel_id
WHERE c.status <> 'deleted'
ORDER BY credential.channel_id;

-- name: CredentialTargetsForReencrypt :many
SELECT credential.channel_id, credential.credential_version,
	credential.key_id, credential.nonce, credential.ciphertext
FROM channel_credentials credential
JOIN channels c ON c.id = credential.channel_id
WHERE credential.key_id <> @active_key_id AND c.status <> 'deleted'
ORDER BY credential.updated_at, credential.channel_id
LIMIT @row_limit::bigint;

-- name: LockChannelForReencrypt :one
SELECT c.version FROM channels c
JOIN accounts actor ON actor.id = @actor_account_id
WHERE c.id = @id AND c.status <> 'deleted' AND actor.status = 'active' AND actor.is_admin
FOR UPDATE OF c;

-- 仅当库内密文仍等于读取时的旧值才替换（compare-and-swap）。
-- name: ReplaceCredentialCiphertext :execrows
UPDATE channel_credentials SET key_id = @key_id, nonce = @nonce, ciphertext = @ciphertext, updated_at = now()
WHERE channel_id = @channel_id AND credential_version = @credential_version
	AND key_id = @old_key_id AND nonce = @old_nonce AND ciphertext = @old_ciphertext;

-- name: GetRoutingRow :one
SELECT cm.channel_id, c.display_name AS channel_display_name, c.owner_account_id, owner.display_name AS owner_display_name,
	owner.status AS owner_status, owner.must_change_password AS owner_must_change_password, c.status AS channel_status,
	cm.model_id, m.name AS model_name, m.provider AS model_provider, m.status AS model_status,
	o.protocol, o.status AS offer_status, cm.multiplier_nano, o.validation_version,
	m.context_window, m.input_price_nano_per_million, m.output_price_nano_per_million,
	m.cache_write_price_nano_per_million, m.cache_read_price_nano_per_million,
	attempt.status AS validation_status, credential.credential_version, credential.key_id,
	credential.nonce, credential.ciphertext, c.normalized_base_url, o.upstream_model_id
FROM channel_offers o
JOIN channel_models cm ON cm.id = o.channel_model_id
JOIN channels c ON c.id = cm.channel_id
JOIN accounts owner ON owner.id = c.owner_account_id
JOIN models m ON m.id = cm.model_id
LEFT JOIN channel_credentials credential
	ON credential.channel_id = c.id AND credential.credential_version = c.credential_version
LEFT JOIN channel_validation_attempts attempt
	ON attempt.offer_id = o.id AND attempt.validation_version = o.validation_version
	AND attempt.attempt_seq = o.validation_attempt_seq
WHERE o.id = @offer_id;
