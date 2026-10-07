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
		AND key_hash = @key_hash AND version = @next_version
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
-- 价格与指标放进 CTE 再 LEFT JOIN，sqlc 才会把这些列生成为可空。
-- name: ListPoolMembers :many
WITH pool_offers AS (
	SELECT offer_id FROM api_pool_members WHERE pool_id = @pool_id
), member_prices AS (
	SELECT offer.id AS offer_id,
		CASE WHEN model.input_price_nano_per_million BETWEEN 0 AND 100000000000000
			THEN ceil(model.input_price_nano_per_million::numeric * cm.multiplier_nano::numeric / 1000000000)::bigint END::bigint AS input_price,
		CASE WHEN model.output_price_nano_per_million BETWEEN 0 AND 100000000000000
			THEN ceil(model.output_price_nano_per_million::numeric * cm.multiplier_nano::numeric / 1000000000)::bigint END::bigint AS output_price,
		CASE WHEN model.cache_write_price_nano_per_million BETWEEN 0 AND 100000000000000
			THEN ceil(model.cache_write_price_nano_per_million::numeric * cm.multiplier_nano::numeric / 1000000000)::bigint END::bigint AS cache_write_price,
		CASE WHEN model.cache_read_price_nano_per_million BETWEEN 0 AND 100000000000000
			THEN ceil(model.cache_read_price_nano_per_million::numeric * cm.multiplier_nano::numeric / 1000000000)::bigint END::bigint AS cache_read_price
	FROM channel_offers offer
	JOIN channel_models cm ON cm.id = offer.channel_model_id
	JOIN models model ON model.id = cm.model_id
	WHERE offer.id IN (SELECT offer_id FROM pool_offers)
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
	price.input_price, price.output_price, price.cache_write_price, price.cache_read_price,
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
LEFT JOIN member_prices price ON price.offer_id = offer.id
LEFT JOIN metrics ON metrics.offer_id = offer.id
WHERE member.pool_id = @pool_id
ORDER BY member.priority;

-- 调用：BeginCall（REPEATABLE READ 快照）

-- name: GetKeyForCall :one
SELECT key.owner_account_id, ledger_account.id AS ledger_account_id, key.key_prefix, key.generation
FROM api_keys key
JOIN accounts owner ON owner.id = key.owner_account_id
JOIN ledger_accounts ledger_account ON ledger_account.identity_account_id = owner.id
WHERE key.id = @id AND key.key_hash = @key_hash AND key.generation = @generation AND key.status = 'active'
	AND owner.status = 'active' AND NOT owner.must_change_password;

-- name: LatestFeeRate :one
SELECT version, fee_rate_nano FROM api_fee_rates ORDER BY version DESC LIMIT 1;

-- name: AllocateUUID :one
SELECT gen_random_uuid()::text;

-- name: FindActivePool :one
SELECT id, version FROM api_model_pools
WHERE api_key_id = @api_key_id AND canonical_model_id = @canonical_model_id AND protocol = @protocol AND status = 'active';

-- name: InsertRejectedCall :exec
INSERT INTO api_calls (
	id, consumer_account_id, consumer_ledger_account_id, api_key_id, key_prefix, key_generation,
	canonical_model_id, protocol, status, decision_code, candidate_count,
	upstream_attempt_count, preauthorized_nano, zero_hold_reason,
	fee_rate_version, fee_rate_nano, completion_reason, completed_at
) VALUES (@id, @consumer_account_id, @consumer_ledger_account_id, @api_key_id, @key_prefix, @key_generation,
	@canonical_model_id, @protocol, 'rejected', @decision_code::text, 0, 0, 0,
	'business_precheck_rejected', @fee_rate_version, @fee_rate_nano, @decision_code::text, now());

-- name: InsertAuthorizedCall :exec
INSERT INTO api_calls (
	id, consumer_account_id, consumer_ledger_account_id, api_key_id, key_prefix, key_generation,
	pool_id, pool_version, canonical_model_id, protocol, status, decision_code,
	candidate_count, hold_id, preauthorized_nano, zero_hold_reason,
	fee_rate_version, fee_rate_nano, formula_version, lease_expires_at, heartbeat_at
) VALUES (@id, @consumer_account_id, @consumer_ledger_account_id, @api_key_id, @key_prefix, @key_generation,
	@pool_id, @pool_version, @canonical_model_id, @protocol, 'in_progress', 'authorized',
	@candidate_count, NULLIF(@hold_id::text, '')::uuid, @preauthorized_nano, @zero_hold_reason,
	@fee_rate_version, @fee_rate_nano, 'formula-v2', now() + @lease_duration::interval, now());

-- name: InsertCandidate :exec
INSERT INTO api_call_candidates (
	call_id, priority, offer_id, channel_id, provider_account_id,
	validation_version, credential_version, upstream_model_id, context_window,
	input_price_nano, output_price_nano, cache_write_price_nano, cache_read_price_nano,
	multiplier_nano, self_channel, net_debit_upper_bound_nano
) VALUES (@call_id, @priority, @offer_id, @channel_id, @provider_account_id,
	@validation_version, @credential_version, @upstream_model_id, @context_window,
	@input_price_nano, @output_price_nano, @cache_write_price_nano, @cache_read_price_nano,
	@multiplier_nano, @self_channel, @net_debit_upper_bound_nano);

-- name: InsertCallPriceTier :exec
INSERT INTO api_call_price_tiers (
	call_id, seq, name, min_prompt_tokens, max_prompt_tokens, timezone, weekdays,
	start_minute_of_day, end_minute_of_day,
	input_price_nano, output_price_nano, cache_write_price_nano, cache_read_price_nano
) VALUES (@call_id, @seq, @name, @min_prompt_tokens, @max_prompt_tokens, @timezone, @weekdays,
	@start_minute_of_day, @end_minute_of_day,
	@input_price_nano, @output_price_nano, @cache_write_price_nano, @cache_read_price_nano);

-- name: ListCallPriceTiers :many
SELECT name, min_prompt_tokens, max_prompt_tokens, timezone, weekdays,
	start_minute_of_day, end_minute_of_day,
	input_price_nano, output_price_nano, cache_write_price_nano, cache_read_price_nano
FROM api_call_price_tiers WHERE call_id = @call_id ORDER BY seq;

-- 提交确认丢失后的恢复读取

-- name: GetHoldFacts :one
SELECT status, amount_nano, remaining_nano, captured_nano, released_nano
FROM ledger_holds WHERE id = @id;

-- name: ListCallCandidates :many
SELECT priority, offer_id, channel_id, provider_account_id,
	validation_version, credential_version, upstream_model_id, context_window,
	input_price_nano, output_price_nano, cache_write_price_nano, cache_read_price_nano,
	multiplier_nano, self_channel, net_debit_upper_bound_nano
FROM api_call_candidates WHERE call_id = @call_id ORDER BY priority;

-- name: GetCandidateByOffer :one
SELECT offer_id, channel_id, provider_account_id,
	validation_version, credential_version, upstream_model_id, context_window,
	input_price_nano, output_price_nano, cache_write_price_nano, cache_read_price_nano,
	multiplier_nano, self_channel
FROM api_call_candidates WHERE call_id = @call_id AND offer_id = @offer_id;

-- name: GetCandidateProvider :one
SELECT provider_account_id FROM api_call_candidates
WHERE call_id = @call_id AND priority = @priority AND offer_id = @offer_id;

-- 调用读取

-- name: GetCall :one
SELECT call.id, call.consumer_account_id, call.api_key_id, call.key_prefix,
	call.key_generation, call.pool_id, call.pool_version, call.canonical_model_id, call.protocol,
	call.status, call.decision_code, call.candidate_count, call.upstream_attempt_count,
	call.hold_id, call.preauthorized_nano, call.zero_hold_reason,
	call.fee_rate_version, call.fee_rate_nano, call.lease_generation, call.final_offer_id,
	final_channel.display_name AS final_channel_name, call.completion_reason,
	call.input_tokens, call.output_tokens, call.cache_write_tokens, call.cache_read_tokens,
	call.provider_charge_nano, call.platform_fee_nano, call.final_http_status,
	call.settled_price_tier_seq,
	call.created_at, call.completed_at
FROM api_calls call
LEFT JOIN channel_offers final_offer ON final_offer.id = call.final_offer_id
LEFT JOIN channel_models final_model ON final_model.id = final_offer.channel_model_id
LEFT JOIN channels final_channel ON final_channel.id = final_model.channel_id
WHERE call.id = @id AND (@administrator::boolean OR call.consumer_account_id = @viewer_id::uuid OR EXISTS (
	SELECT 1 FROM api_call_attempts attempt
	WHERE attempt.call_id = call.id AND attempt.provider_account_id = @viewer_id::uuid
));

-- name: ListCallAttempts :many
SELECT attempt.id, attempt.call_id, attempt.sequence, attempt.offer_id,
	channel.display_name AS channel_display_name, attempt.provider_account_id, attempt.status,
	attempt.http_status, attempt.error_code,
	CASE WHEN @administrator::boolean OR @consumer::boolean OR attempt.provider_account_id = @viewer_id::uuid THEN attempt.raw_error ELSE '' END::text AS raw_error,
	CASE WHEN @administrator::boolean OR @consumer::boolean OR attempt.provider_account_id = @viewer_id::uuid THEN attempt.raw_error_truncated ELSE false END::boolean AS raw_error_truncated,
	attempt.semantic_committed, attempt.ttft_milliseconds, attempt.duration_milliseconds,
	attempt.input_tokens, attempt.output_tokens, attempt.cache_write_tokens, attempt.cache_read_tokens,
	attempt.tokens_per_second_nano, call.lease_generation, attempt.started_at, attempt.completed_at
FROM api_call_attempts attempt
JOIN api_calls call ON call.id = attempt.call_id
JOIN channel_offers offer ON offer.id = attempt.offer_id
JOIN channel_models model ON model.id = offer.channel_model_id
JOIN channels channel ON channel.id = model.channel_id
WHERE attempt.call_id = @call_id AND (@administrator::boolean OR @consumer::boolean OR attempt.provider_account_id = @viewer_id::uuid)
ORDER BY attempt.sequence;

-- name: ListVisibleCallIDs :many
SELECT DISTINCT call.id::text AS id, call.created_at
FROM api_calls call
LEFT JOIN api_call_attempts attempt ON attempt.call_id = call.id
WHERE @administrator::boolean OR call.consumer_account_id = @viewer_id::uuid OR attempt.provider_account_id = @viewer_id::uuid
ORDER BY call.created_at DESC, call.id::text DESC LIMIT @max_rows::bigint;

-- name: DashboardTotals :one
SELECT
	COALESCE((SELECT sum(call.provider_charge_nano + call.platform_fee_nano)
		FROM api_calls call
		JOIN api_call_candidates candidate ON candidate.call_id = call.id AND candidate.offer_id = call.final_offer_id
		WHERE call.consumer_account_id = @account_id AND call.status = 'succeeded'), 0)::bigint AS consumer_spent,
	COALESCE((SELECT sum(call.provider_charge_nano)
		FROM api_calls call
		JOIN api_call_candidates candidate ON candidate.call_id = call.id AND candidate.offer_id = call.final_offer_id
		WHERE candidate.provider_account_id = @account_id AND call.status = 'succeeded'), 0)::bigint AS provider_income,
	COALESCE((SELECT sum(call.provider_charge_nano + call.platform_fee_nano)
		FROM api_calls call
		JOIN api_call_candidates candidate ON candidate.call_id = call.id AND candidate.offer_id = call.final_offer_id
		WHERE call.consumer_account_id = @account_id AND call.status = 'succeeded'
			AND call.created_at >= date_trunc('day', timezone('UTC', now()))), 0)::bigint AS today_spent,
	COALESCE((SELECT count(*)
		FROM api_calls call
		WHERE call.consumer_account_id = @account_id AND call.status = 'succeeded'
			AND call.created_at >= date_trunc('day', timezone('UTC', now()))), 0)::bigint AS today_succeeded_calls,
	COALESCE((SELECT sum(call.provider_charge_nano)
		FROM api_calls call
		JOIN api_call_candidates candidate ON candidate.call_id = call.id AND candidate.offer_id = call.final_offer_id
		WHERE candidate.provider_account_id = @account_id AND call.status = 'succeeded'
			AND call.consumer_account_id <> @account_id
			AND call.created_at >= date_trunc('day', timezone('UTC', now()))), 0)::bigint AS today_external_income,
	(SELECT count(*) FROM api_keys WHERE owner_account_id = @account_id AND status = 'active')::bigint AS active_key_count,
	(SELECT count(*) FROM api_model_pools pool JOIN api_keys key ON key.id = pool.api_key_id
		WHERE key.owner_account_id = @account_id AND key.status <> 'deleted' AND pool.status = 'active')::bigint AS pool_count,
	(SELECT count(*) FROM channel_offers offer
		JOIN channel_models model ON model.id = offer.channel_model_id
		JOIN channels channel ON channel.id = model.channel_id
		LEFT JOIN channel_validation_attempts attempt ON attempt.offer_id = offer.id
			AND attempt.validation_version = offer.validation_version AND attempt.attempt_seq = offer.validation_attempt_seq
		WHERE channel.owner_account_id = @account_id AND channel.status = 'published' AND offer.status = 'active' AND attempt.status = 'passed')::bigint AS healthy_offer_count,
	(SELECT count(*) FROM channel_offers offer
		JOIN channel_models model ON model.id = offer.channel_model_id
		JOIN channels channel ON channel.id = model.channel_id
		LEFT JOIN channel_validation_attempts attempt ON attempt.offer_id = offer.id
			AND attempt.validation_version = offer.validation_version AND attempt.attempt_seq = offer.validation_attempt_seq
		WHERE channel.owner_account_id = @account_id AND offer.status <> 'deleted'
			AND NOT (channel.status = 'published' AND offer.status = 'active' AND attempt.status = 'passed'))::bigint AS unhealthy_offer_count,
	(SELECT count(*) FROM api_pool_members member
		JOIN api_model_pools pool ON pool.id = member.pool_id
		JOIN api_keys key ON key.id = pool.api_key_id
		JOIN channel_offers offer ON offer.id = member.offer_id
		JOIN channel_models channel_model ON channel_model.id = offer.channel_model_id
		JOIN channels channel ON channel.id = channel_model.channel_id
		JOIN accounts channel_owner ON channel_owner.id = channel.owner_account_id
		JOIN models catalog_model ON catalog_model.id = channel_model.model_id
		LEFT JOIN channel_credentials credential ON credential.channel_id = channel.id
			AND credential.credential_version = channel.credential_version
		LEFT JOIN channel_validation_attempts attempt ON attempt.offer_id = offer.id
			AND attempt.validation_version = offer.validation_version AND attempt.attempt_seq = offer.validation_attempt_seq
		WHERE key.owner_account_id = @account_id AND key.status <> 'deleted' AND pool.status = 'active'
			AND (member.added_validation_version <> offer.validation_version
				OR channel_owner.status <> 'active' OR channel_owner.must_change_password
				OR channel.status <> 'published' OR catalog_model.status <> 'active'
				OR offer.status <> 'active' OR credential.channel_id IS NULL
				OR attempt.status IS DISTINCT FROM 'passed'))::bigint AS pending_items;

-- 尝试

-- name: LockCallForAttempt :one
SELECT status, lease_generation FROM api_calls WHERE id = @id FOR UPDATE;

-- name: AttemptStats :one
SELECT (count(*) FILTER (WHERE status <> 'in_progress'))::bigint AS completed_attempts,
	(count(*) FILTER (WHERE status = 'in_progress'))::bigint AS in_progress_attempts,
	COALESCE(bool_or(semantic_committed), false)::boolean AS any_committed,
	COALESCE(bool_or(status = 'succeeded'), false)::boolean AS any_succeeded
FROM api_call_attempts WHERE call_id = @call_id;

-- name: InsertAttempt :one
WITH next_sequence AS (
	SELECT (COALESCE(max(sequence), 0) + 1)::integer AS value FROM api_call_attempts WHERE call_id = @call_id::uuid
)
INSERT INTO api_call_attempts (call_id, sequence, offer_id, provider_account_id, status)
SELECT @call_id::uuid, value, @offer_id::uuid, @provider_account_id::uuid, 'in_progress' FROM next_sequence
RETURNING id, call_id, sequence, offer_id, provider_account_id, status, started_at;

-- name: BumpAttemptCount :exec
UPDATE api_calls SET upstream_attempt_count = upstream_attempt_count + 1,
	heartbeat_at = now(), lease_expires_at = now() + @lease_duration::interval
WHERE id = @id AND lease_generation = @lease_generation;

-- name: LockAttemptWithCall :one
SELECT attempt.call_id, call.status AS call_status, attempt.status AS attempt_status, call.lease_generation
FROM api_call_attempts attempt
JOIN api_calls call ON call.id = attempt.call_id
WHERE attempt.id = @id FOR UPDATE OF call, attempt;

-- name: CompleteAttempt :one
UPDATE api_call_attempts
SET status = @status::text, http_status = NULLIF(@http_status::integer, 0), error_code = @error_code,
	raw_error = @raw_error, raw_error_truncated = @raw_error_truncated,
	semantic_committed = semantic_committed OR @semantic_committed::boolean,
	ttft_milliseconds = sqlc.narg('ttft_milliseconds'), duration_milliseconds = @duration_milliseconds,
	input_tokens = sqlc.narg('input_tokens'), output_tokens = sqlc.narg('output_tokens'),
	cache_write_tokens = sqlc.narg('cache_write_tokens'), cache_read_tokens = sqlc.narg('cache_read_tokens'),
	tokens_per_second_nano = NULLIF(@tokens_per_second_nano::bigint, 0::bigint),
	completed_at = CASE WHEN @status::text = 'pending_delivery' THEN NULL ELSE now() END
WHERE id = @id AND status = 'in_progress'
RETURNING id, call_id, sequence, offer_id, provider_account_id,
	status, http_status, error_code, raw_error, raw_error_truncated, semantic_committed,
	ttft_milliseconds, duration_milliseconds, tokens_per_second_nano, started_at, completed_at;

-- name: LockAttemptCommitState :one
SELECT call.status AS call_status, attempt.status AS attempt_status, call.lease_generation,
	attempt.semantic_committed, attempt.output_tokens
FROM api_call_attempts attempt JOIN api_calls call ON call.id = attempt.call_id
WHERE attempt.id = @id FOR UPDATE OF call, attempt;

-- name: MarkAttemptCommitted :exec
UPDATE api_call_attempts SET semantic_committed = true WHERE id = @id AND NOT semantic_committed;

-- name: MarkPendingAttemptCommitted :exec
UPDATE api_call_attempts
SET semantic_committed = true, ttft_milliseconds = @ttft_milliseconds,
	duration_milliseconds = @duration_milliseconds, tokens_per_second_nano = NULLIF(@tokens_per_second_nano::bigint, 0::bigint)
WHERE id = @id AND status = 'pending_delivery' AND NOT semantic_committed;

-- 心跳与终结

-- name: HeartbeatCall :execrows
UPDATE api_calls SET heartbeat_at = now(), lease_expires_at = now() + @lease_duration::interval
WHERE id = @id AND status IN ('in_progress', 'pending_delivery') AND lease_generation = @lease_generation;

-- name: LockCallForFinalize :one
SELECT consumer_account_id, hold_id, preauthorized_nano, status, lease_generation,
	finalizer_payload_hash, formula_version, created_at, fee_rate_nano
FROM api_calls WHERE id = @id FOR UPDATE;

-- name: ListAttemptsForFinalization :many
SELECT id, status, offer_id, COALESCE(http_status, 0)::integer AS http_status, error_code,
	semantic_committed, input_tokens, output_tokens, cache_write_tokens, cache_read_tokens
FROM api_call_attempts WHERE call_id = @call_id ORDER BY sequence;

-- name: MarkOrphanAttemptIncomplete :exec
UPDATE api_call_attempts
SET status = 'incomplete', error_code = @error_code, raw_error = @raw_error,
	raw_error_truncated = false, completed_at = now()
WHERE id = @id AND status = 'in_progress';

-- name: InsertSettlement :exec
INSERT INTO api_call_settlements (
	call_id, kind, provider_account_id, provider_charge_nano, platform_fee_nano,
	capture_transaction_id, self_transaction_id, hold_id
) VALUES (@call_id, @kind, NULLIF(@provider_account_id::text, '')::uuid, @provider_charge_nano, @platform_fee_nano,
	NULLIF(@capture_transaction_id::text, '')::uuid, NULLIF(@self_transaction_id::text, '')::uuid, NULLIF(@hold_id::text, '')::uuid);

-- name: FinalizeCall :exec
UPDATE api_calls SET status = @status::text, decision_code = @reason::text,
	final_offer_id = NULLIF(@final_offer_id::text, '')::uuid,
	completion_reason = @reason::text, input_tokens = sqlc.narg('input_tokens'), output_tokens = sqlc.narg('output_tokens'),
	cache_write_tokens = sqlc.narg('cache_write_tokens'), cache_read_tokens = sqlc.narg('cache_read_tokens'),
	provider_charge_nano = @provider_charge_nano, platform_fee_nano = @platform_fee_nano,
	final_http_status = NULLIF(@final_http_status::integer, 0),
	settled_price_tier_seq = @settled_price_tier_seq,
	heartbeat_at = CASE WHEN @status::text = 'pending_delivery' THEN now() ELSE NULL END,
	lease_expires_at = CASE WHEN @status::text = 'pending_delivery' THEN now() + @lease_duration::interval ELSE NULL END,
	finalizer_payload_hash = @finalizer_payload_hash, completed_at = sqlc.narg('completed_at')
WHERE id = @id AND status = 'in_progress' AND lease_generation = @lease_generation;

-- name: GetAttemptReplayFacts :one
SELECT call_id, offer_id, status, http_status, error_code, raw_error,
	semantic_committed, ttft_milliseconds, duration_milliseconds,
	input_tokens, output_tokens, cache_write_tokens, cache_read_tokens,
	tokens_per_second_nano
FROM api_call_attempts WHERE id = @id;

-- name: GetCallFinalizerState :one
SELECT status, lease_generation, finalizer_payload_hash FROM api_calls WHERE id = @id;

-- 送达确认与补偿

-- name: LockCallForDelivery :one
SELECT consumer_account_id, status, lease_generation FROM api_calls WHERE id = @id FOR UPDATE;

-- name: ConfirmPendingAttempt :execrows
UPDATE api_call_attempts
SET status = 'succeeded', semantic_committed = true, completed_at = now()
WHERE call_id = @call_id AND status = 'pending_delivery';

-- name: ConfirmPendingCall :execrows
UPDATE api_calls
SET status = 'succeeded', heartbeat_at = NULL, lease_expires_at = NULL, completed_at = now()
WHERE id = @id AND status = 'pending_delivery' AND lease_generation = @lease_generation;

-- name: LockCallForCompensation :one
SELECT consumer_account_id, status, lease_generation,
	COALESCE(final_offer_id::text, '')::text AS final_offer_id, COALESCE(final_http_status, 0)::integer AS final_http_status
FROM api_calls WHERE id = @id FOR UPDATE;

-- name: GetCompensationReason :one
SELECT reason FROM api_call_compensations WHERE call_id = @call_id;

-- name: LockSettlement :one
SELECT provider_charge_nano, platform_fee_nano, capture_transaction_id, self_transaction_id
FROM api_call_settlements WHERE call_id = @call_id FOR UPDATE;

-- name: InsertCompensation :exec
INSERT INTO api_call_compensations (
	call_id, reason, original_transaction_id, reversal_transaction_id,
	provider_charge_reversed_nano, platform_fee_reversed_nano
) VALUES (@call_id, @reason, NULLIF(@original_transaction_id::text, '')::uuid, NULLIF(@reversal_transaction_id::text, '')::uuid,
	@provider_charge_reversed_nano, @platform_fee_reversed_nano);

-- name: MarkPendingAttemptIncomplete :execrows
UPDATE api_call_attempts
SET status = 'incomplete', error_code = @error_code,
	raw_error = 'downstream delivery was not durably confirmed', raw_error_truncated = false,
	input_tokens = NULL, output_tokens = NULL, cache_write_tokens = NULL, cache_read_tokens = NULL,
	tokens_per_second_nano = NULL, completed_at = now()
WHERE call_id = @call_id AND status = 'pending_delivery';

-- name: MarkPendingCallIncomplete :execrows
UPDATE api_calls
SET status = 'incomplete', decision_code = @reason::text, completion_reason = @reason::text,
	input_tokens = NULL, output_tokens = NULL, cache_write_tokens = NULL, cache_read_tokens = NULL,
	provider_charge_nano = 0, platform_fee_nano = 0,
	heartbeat_at = NULL, lease_expires_at = NULL, finalizer_payload_hash = @finalizer_payload_hash, completed_at = now()
WHERE id = @id AND status = 'pending_delivery' AND lease_generation = @lease_generation;

-- name: GetCompensatedCallReason :one
SELECT compensation.reason
FROM api_calls call
JOIN api_call_compensations compensation ON compensation.call_id = call.id
WHERE call.id = @id AND call.status = 'incomplete' AND call.lease_generation = @lease_generation;

-- 孤儿恢复

-- name: ClaimOrphanCalls :many
SELECT id, status FROM api_calls
WHERE status IN ('in_progress', 'pending_delivery') AND (heartbeat_at < @cutoff::timestamptz OR lease_expires_at < now())
ORDER BY heartbeat_at NULLS FIRST
FOR UPDATE SKIP LOCKED LIMIT @max_rows::bigint;

-- name: BumpLeaseGeneration :one
UPDATE api_calls
SET lease_generation = lease_generation + 1,
	heartbeat_at = now(), lease_expires_at = now() + @lease_duration::interval
WHERE id = @id AND status = @status::text
RETURNING lease_generation;
