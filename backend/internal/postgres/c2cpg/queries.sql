-- C2C 领域全部 SQL。
-- 订单与成交的列表必须逐列相同：sqlc 没有片段复用，store.go 靠结构体转换共用一个映射函数
-- （GetOrder / GetOrderForUpdate / ListMarketSellOrders / ListOwnerOrders，
-- GetTrade / GetTradeForUpdate / ListAccountTrades / ListDisputedTrades）。
-- takeable 的判定必须与 GetAccountGate 加 store.go 中 ensureOwnerReady 的口径一致。

-- ---------------------------------------------------------------------------
-- 订单读取
-- ---------------------------------------------------------------------------

-- name: GetOrder :one
SELECT o.id, o.owner_account_id, owner.display_name AS owner_display_name,
	o.unit_price_fen, o.total_nano, o.available_nano, o.allocated_nano,
	o.settled_nano, o.closed_nano, o.minimum_nano, o.maximum_nano,
	o.status, COALESCE(o.parent_hold_id::text, '')::text AS parent_hold_id, o.created_at, o.updated_at,
	o.cancelled_at,
	((owner.status = 'active' AND NOT owner.must_change_password AND NOT owner.credit_frozen)
		AND o.status = 'open' AND o.available_nano > 0)::boolean AS takeable
FROM c2c_orders o
JOIN accounts owner ON owner.id = o.owner_account_id
WHERE o.id = @id;

-- name: GetOrderForUpdate :one
SELECT o.id, o.owner_account_id, owner.display_name AS owner_display_name,
	o.unit_price_fen, o.total_nano, o.available_nano, o.allocated_nano,
	o.settled_nano, o.closed_nano, o.minimum_nano, o.maximum_nano,
	o.status, COALESCE(o.parent_hold_id::text, '')::text AS parent_hold_id, o.created_at, o.updated_at,
	o.cancelled_at,
	((owner.status = 'active' AND NOT owner.must_change_password AND NOT owner.credit_frozen)
		AND o.status = 'open' AND o.available_nano > 0)::boolean AS takeable
FROM c2c_orders o
JOIN accounts owner ON owner.id = o.owner_account_id
WHERE o.id = @id
FOR UPDATE OF o;

-- name: ListMarketSellOrders :many
SELECT o.id, o.owner_account_id, owner.display_name AS owner_display_name,
	o.unit_price_fen, o.total_nano, o.available_nano, o.allocated_nano,
	o.settled_nano, o.closed_nano, o.minimum_nano, o.maximum_nano,
	o.status, COALESCE(o.parent_hold_id::text, '')::text AS parent_hold_id, o.created_at, o.updated_at,
	o.cancelled_at,
	((owner.status = 'active' AND NOT owner.must_change_password AND NOT owner.credit_frozen)
		AND o.status = 'open' AND o.available_nano > 0)::boolean AS takeable
FROM c2c_orders o JOIN accounts owner ON owner.id = o.owner_account_id
WHERE o.status = 'open' AND o.available_nano > 0
	AND (owner.status = 'active' AND NOT owner.must_change_password AND NOT owner.credit_frozen)
ORDER BY o.unit_price_fen ASC, o.created_at, o.id LIMIT 200;

-- name: ListOwnerOrders :many
SELECT o.id, o.owner_account_id, owner.display_name AS owner_display_name,
	o.unit_price_fen, o.total_nano, o.available_nano, o.allocated_nano,
	o.settled_nano, o.closed_nano, o.minimum_nano, o.maximum_nano,
	o.status, COALESCE(o.parent_hold_id::text, '')::text AS parent_hold_id, o.created_at, o.updated_at,
	o.cancelled_at,
	((owner.status = 'active' AND NOT owner.must_change_password AND NOT owner.credit_frozen)
		AND o.status = 'open' AND o.available_nano > 0)::boolean AS takeable
FROM c2c_orders o JOIN accounts owner ON owner.id = o.owner_account_id
-- 迁移 0009 之前遗留的已终结买单只保留为历史，不进入用户的订单列表。
WHERE o.owner_account_id = @owner_account_id AND o.side = 'sell'
ORDER BY o.updated_at DESC, o.id DESC LIMIT 200;

-- name: GetOrderOwnerID :one
SELECT owner_account_id FROM c2c_orders WHERE id = @id;

-- 最优卖价取自 ListMarketSellOrders 的首行（按价格排序、过滤条件与盘口一致），
-- 这里只需要最新成交价；没有成交时无行。
-- name: GetLatestTradePrice :one
SELECT unit_price_fen FROM c2c_trades
WHERE status = 'released_to_buyer' ORDER BY resolved_at DESC, id DESC LIMIT 1;

-- name: IsOrderParticipant :one
SELECT EXISTS (
	SELECT 1 FROM c2c_trades
	WHERE order_id = @order_id AND (buyer_account_id = @account_id OR seller_account_id = @account_id)
)::boolean;

-- ---------------------------------------------------------------------------
-- 收款方式
-- ---------------------------------------------------------------------------

-- name: ListPaymentMethods :many
SELECT id, order_id, method_type, position, qr_available, key_id, nonce, ciphertext, created_at
FROM c2c_payment_methods WHERE order_id = @order_id ORDER BY position;

-- name: GetSelectedPaymentMethod :one
SELECT pm.id, pm.order_id, pm.method_type, pm.position, pm.qr_available,
	pm.key_id, pm.nonce, pm.ciphertext, pm.created_at
FROM c2c_trades t
JOIN c2c_payment_methods pm ON pm.id = t.selected_payment_method_id
WHERE t.id = @trade_id;

-- name: InsertPaymentMethod :exec
INSERT INTO c2c_payment_methods (
	id, order_id, method_type, position, qr_available, key_id, nonce, ciphertext, created_at
) VALUES (
	@id, @order_id, @method_type, @position, @qr_available, @key_id, @nonce, @ciphertext, @created_at
);

-- ---------------------------------------------------------------------------
-- 成交读取
-- ---------------------------------------------------------------------------

-- name: GetTrade :one
SELECT t.id, t.order_id,
	t.buyer_account_id, buyer.display_name AS buyer_display_name,
	t.seller_account_id, seller.display_name AS seller_display_name,
	buyer.credit_frozen AS buyer_credit_frozen, seller.credit_frozen AS seller_credit_frozen,
	t.quantity_nano, t.unit_price_fen, t.fiat_amount_fen, t.status,
	t.hold_id, t.payment_reference_chars,
	COALESCE(t.payment_reference_key_id, '')::text AS payment_reference_key_id,
	COALESCE(t.payment_reference_nonce, ''::bytea)::bytea AS payment_reference_nonce,
	COALESCE(t.payment_reference_ciphertext, ''::bytea)::bytea AS payment_reference_ciphertext,
	t.payment_reference_deleted_at,
	t.payment_deadline, t.review_due_at,
	COALESCE(t.ledger_transaction_id::text, '')::text AS ledger_transaction_id,
	t.created_at, t.updated_at, t.paid_at, t.resolved_at
FROM c2c_trades t
JOIN accounts buyer ON buyer.id = t.buyer_account_id
JOIN accounts seller ON seller.id = t.seller_account_id
WHERE t.id = @id;

-- name: GetTradeForUpdate :one
SELECT t.id, t.order_id,
	t.buyer_account_id, buyer.display_name AS buyer_display_name,
	t.seller_account_id, seller.display_name AS seller_display_name,
	buyer.credit_frozen AS buyer_credit_frozen, seller.credit_frozen AS seller_credit_frozen,
	t.quantity_nano, t.unit_price_fen, t.fiat_amount_fen, t.status,
	t.hold_id, t.payment_reference_chars,
	COALESCE(t.payment_reference_key_id, '')::text AS payment_reference_key_id,
	COALESCE(t.payment_reference_nonce, ''::bytea)::bytea AS payment_reference_nonce,
	COALESCE(t.payment_reference_ciphertext, ''::bytea)::bytea AS payment_reference_ciphertext,
	t.payment_reference_deleted_at,
	t.payment_deadline, t.review_due_at,
	COALESCE(t.ledger_transaction_id::text, '')::text AS ledger_transaction_id,
	t.created_at, t.updated_at, t.paid_at, t.resolved_at
FROM c2c_trades t
JOIN accounts buyer ON buyer.id = t.buyer_account_id
JOIN accounts seller ON seller.id = t.seller_account_id
WHERE t.id = @id
FOR UPDATE OF t;

-- name: ListAccountTrades :many
SELECT t.id, t.order_id,
	t.buyer_account_id, buyer.display_name AS buyer_display_name,
	t.seller_account_id, seller.display_name AS seller_display_name,
	buyer.credit_frozen AS buyer_credit_frozen, seller.credit_frozen AS seller_credit_frozen,
	t.quantity_nano, t.unit_price_fen, t.fiat_amount_fen, t.status,
	t.hold_id, t.payment_reference_chars,
	COALESCE(t.payment_reference_key_id, '')::text AS payment_reference_key_id,
	COALESCE(t.payment_reference_nonce, ''::bytea)::bytea AS payment_reference_nonce,
	COALESCE(t.payment_reference_ciphertext, ''::bytea)::bytea AS payment_reference_ciphertext,
	t.payment_reference_deleted_at,
	t.payment_deadline, t.review_due_at,
	COALESCE(t.ledger_transaction_id::text, '')::text AS ledger_transaction_id,
	t.created_at, t.updated_at, t.paid_at, t.resolved_at
FROM c2c_trades t
JOIN accounts buyer ON buyer.id = t.buyer_account_id
JOIN accounts seller ON seller.id = t.seller_account_id
WHERE t.buyer_account_id = @account_id OR t.seller_account_id = @account_id
ORDER BY t.updated_at DESC, t.id DESC LIMIT 200;

-- name: ListDisputedTrades :many
SELECT t.id, t.order_id,
	t.buyer_account_id, buyer.display_name AS buyer_display_name,
	t.seller_account_id, seller.display_name AS seller_display_name,
	buyer.credit_frozen AS buyer_credit_frozen, seller.credit_frozen AS seller_credit_frozen,
	t.quantity_nano, t.unit_price_fen, t.fiat_amount_fen, t.status,
	t.hold_id, t.payment_reference_chars,
	COALESCE(t.payment_reference_key_id, '')::text AS payment_reference_key_id,
	COALESCE(t.payment_reference_nonce, ''::bytea)::bytea AS payment_reference_nonce,
	COALESCE(t.payment_reference_ciphertext, ''::bytea)::bytea AS payment_reference_ciphertext,
	t.payment_reference_deleted_at,
	t.payment_deadline, t.review_due_at,
	COALESCE(t.ledger_transaction_id::text, '')::text AS ledger_transaction_id,
	t.created_at, t.updated_at, t.paid_at, t.resolved_at
FROM c2c_trades t
JOIN accounts buyer ON buyer.id = t.buyer_account_id
JOIN accounts seller ON seller.id = t.seller_account_id
WHERE t.status = 'disputed'
ORDER BY t.updated_at, t.id LIMIT 200;

-- name: GetTradeOrderID :one
SELECT order_id FROM c2c_trades WHERE id = @id;

-- name: GetTradeParties :one
SELECT buyer_account_id, seller_account_id FROM c2c_trades WHERE id = @id;

-- name: ListStatements :many
SELECT s.id, s.trade_id, s.actor_account_id, actor.display_name AS actor_display_name,
	s.character_count, COALESCE(s.key_id, '')::text AS key_id,
	COALESCE(s.nonce, ''::bytea)::bytea AS nonce,
	COALESCE(s.ciphertext, ''::bytea)::bytea AS ciphertext, s.created_at, s.deleted_at
FROM c2c_dispute_statements s JOIN accounts actor ON actor.id = s.actor_account_id
WHERE s.trade_id = @trade_id ORDER BY s.created_at, s.id;

-- trade_id 为空表示整张订单的全部事件。
-- name: ListEvents :many
SELECT id, order_id, COALESCE(trade_id::text, '')::text AS trade_id,
	COALESCE(actor_account_id::text, '')::text AS actor_account_id,
	action, reason, COALESCE(ledger_transaction_id::text, '')::text AS ledger_transaction_id,
	COALESCE(hold_business_id, '')::text AS hold_business_id, created_at
FROM c2c_events
WHERE order_id = @order_id AND (sqlc.narg('trade_id')::uuid IS NULL OR trade_id = sqlc.narg('trade_id')::uuid)
ORDER BY id;

-- name: ListEncryptionTargets :many
SELECT id::text AS record_id, 'payment_method'::text AS purpose, key_id, nonce, ciphertext FROM c2c_payment_methods
UNION ALL
SELECT id::text, 'payment_reference', payment_reference_key_id,
	payment_reference_nonce, payment_reference_ciphertext
FROM c2c_trades WHERE payment_reference_deleted_at IS NULL AND payment_reference_ciphertext IS NOT NULL
UNION ALL
SELECT id::text, 'dispute_statement', key_id, nonce, ciphertext
FROM c2c_dispute_statements WHERE deleted_at IS NULL
ORDER BY 1;

-- ---------------------------------------------------------------------------
-- 幂等命令
-- ---------------------------------------------------------------------------

-- name: ReserveCommand :execrows
INSERT INTO c2c_commands (
	actor_key, actor_account_id, operation, idempotency_key, payload_hash, created_at
) VALUES (
	@actor_key, sqlc.narg('actor_account_id'), @operation, @idempotency_key, @payload_hash, @created_at
)
ON CONFLICT (actor_key, operation, idempotency_key) DO NOTHING;

-- name: GetCommandForUpdate :one
SELECT payload_hash, result_payload, completed_at
FROM c2c_commands
WHERE actor_key = @actor_key AND operation = @operation AND idempotency_key = @idempotency_key
FOR UPDATE;

-- name: CompleteCommand :execrows
UPDATE c2c_commands SET result_payload = @result_payload, completed_at = @completed_at
WHERE actor_key = @actor_key AND operation = @operation AND idempotency_key = @idempotency_key
	AND completed_at IS NULL;

-- ---------------------------------------------------------------------------
-- 账户闸门与锁
-- ---------------------------------------------------------------------------

-- 同一事务内多个账户的咨询锁由调用方按账户 ID 排序后逐个获取。
-- name: LockAccountMutationKey :exec
SELECT pg_advisory_xact_lock(hashtextextended('oh-my-aihub-account:' || @account_id::text, 0));

-- name: GetAccountGate :one
SELECT status, must_change_password, is_admin, credit_frozen FROM accounts WHERE id = @id;

-- 争议限制当事方：先锁账本账户行，再锁身份行（与 UpdateAccount 的账户策略锁序一致）。
-- name: LockLedgerAccountByIdentity :one
SELECT 1::int AS locked FROM ledger_accounts WHERE identity_account_id = @identity_account_id::uuid FOR UPDATE;

-- name: LockAccountForUpdate :one
SELECT credit_frozen, version FROM accounts WHERE id = @id FOR UPDATE;

-- name: FreezeAccountCredit :one
UPDATE accounts
SET credit_frozen = true, version = version + 1, updated_at = @updated_at
WHERE id = @id AND version = @version
RETURNING version;

-- ---------------------------------------------------------------------------
-- 订单写入
-- ---------------------------------------------------------------------------

-- name: InsertOrder :exec
INSERT INTO c2c_orders (
	id, owner_account_id, unit_price_fen, total_nano,
	available_nano, allocated_nano, settled_nano, closed_nano,
	minimum_nano, maximum_nano, status, parent_hold_id, created_at, updated_at
) VALUES (
	@id, @owner_account_id, @unit_price_fen, @total_nano,
	@total_nano, 0, 0, 0,
	@minimum_nano, @maximum_nano, 'open', @parent_hold_id, @created_at, @created_at
);

-- name: UpdateOrderAmounts :execrows
UPDATE c2c_orders
SET available_nano = @available_nano, allocated_nano = @allocated_nano, settled_nano = @settled_nano,
	closed_nano = @closed_nano, status = @status, updated_at = @updated_at
WHERE id = @id;

-- name: CancelOrder :execrows
UPDATE c2c_orders
SET available_nano = 0, closed_nano = closed_nano + @closing_nano,
	status = 'cancelled', cancelled_at = @cancelled_at, updated_at = @cancelled_at
WHERE id = @id;

-- ---------------------------------------------------------------------------
-- 成交写入
-- ---------------------------------------------------------------------------

-- name: InsertTrade :exec
INSERT INTO c2c_trades (
	id, order_id, buyer_account_id, seller_account_id, quantity_nano,
	unit_price_fen, fiat_amount_fen, status, hold_id, selected_payment_method_id,
	payment_deadline, created_at, updated_at
) VALUES (
	@id, @order_id, @buyer_account_id, @seller_account_id, @quantity_nano,
	@unit_price_fen, @fiat_amount_fen, 'awaiting_payment', @hold_id, @selected_payment_method_id,
	@payment_deadline, @created_at, @created_at
);

-- 退回分配量：待付款 -> cancelled/expired，已付款或争议中 -> returned_to_seller。
-- name: ReturnTrade :execrows
UPDATE c2c_trades SET status = @new_status, resolved_at = @resolved_at, updated_at = @resolved_at
WHERE id = @id AND status = @expected_status;

-- name: ReleaseTrade :execrows
UPDATE c2c_trades
SET status = 'released_to_buyer', ledger_transaction_id = @ledger_transaction_id,
	resolved_at = @resolved_at, updated_at = @resolved_at
WHERE id = @id AND status = @expected_status;

-- 买家声明已付款；收款凭据为空时三个密文字段均为 NULL。
-- name: MarkTradePaid :execrows
UPDATE c2c_trades
SET status = 'paid', payment_reference_chars = @payment_reference_chars,
	payment_reference_key_id = sqlc.narg('payment_reference_key_id'),
	payment_reference_nonce = sqlc.narg('payment_reference_nonce'),
	payment_reference_ciphertext = sqlc.narg('payment_reference_ciphertext'),
	paid_at = @paid_at, updated_at = @paid_at
WHERE id = @id AND status = 'awaiting_payment';

-- name: OpenTradeDispute :execrows
UPDATE c2c_trades SET status = 'disputed', review_due_at = @review_due_at, updated_at = @updated_at
WHERE id = @id AND status = 'paid';

-- name: ExtendTradeReview :execrows
UPDATE c2c_trades SET review_due_at = @review_due_at, updated_at = @updated_at
WHERE id = @id AND status = 'disputed';

-- name: TouchTrade :exec
UPDATE c2c_trades SET updated_at = @updated_at WHERE id = @id;

-- name: InsertDisputeStatement :exec
INSERT INTO c2c_dispute_statements (
	id, trade_id, actor_account_id, character_count, key_id, nonce, ciphertext, created_at
) VALUES (
	@id, @trade_id, @actor_account_id, @character_count, @key_id, @nonce, @ciphertext, @created_at
);

-- name: InsertEvent :exec
INSERT INTO c2c_events (
	order_id, trade_id, actor_account_id, action, reason,
	ledger_transaction_id, hold_business_id, created_at
) VALUES (
	@order_id, sqlc.narg('trade_id'), sqlc.narg('actor_account_id'), @action, @reason,
	sqlc.narg('ledger_transaction_id'), sqlc.narg('hold_business_id'), @created_at
);

-- ---------------------------------------------------------------------------
-- 超时与私密数据清理
-- ---------------------------------------------------------------------------

-- name: ListDueTradeIDs :many
SELECT id FROM c2c_trades
WHERE status = 'awaiting_payment' AND payment_deadline <= @now
ORDER BY payment_deadline, id LIMIT @batch_limit;

-- 终态成交的争议陈述密文超过保留期后清除。
-- name: CleanupDisputeStatements :execrows
WITH victims AS (
	SELECT s.id FROM c2c_dispute_statements s
	JOIN c2c_trades t ON t.id = s.trade_id
	WHERE s.deleted_at IS NULL
		AND t.status IN ('released_to_buyer', 'returned_to_seller', 'cancelled', 'expired')
		AND t.resolved_at <= @cutoff
	ORDER BY t.resolved_at, s.id
	LIMIT @batch_limit
	FOR UPDATE OF s SKIP LOCKED
)
UPDATE c2c_dispute_statements s
SET key_id = NULL, nonce = NULL, ciphertext = NULL, deleted_at = @deleted_at
FROM victims WHERE s.id = victims.id;

-- name: CleanupPaymentReferences :execrows
WITH victims AS (
	SELECT candidate.id FROM c2c_trades candidate
	WHERE candidate.payment_reference_ciphertext IS NOT NULL
		AND candidate.status IN ('released_to_buyer', 'returned_to_seller', 'cancelled', 'expired')
		AND candidate.resolved_at <= @cutoff
	ORDER BY candidate.resolved_at, candidate.id
	LIMIT @batch_limit
	FOR UPDATE OF candidate SKIP LOCKED
)
UPDATE c2c_trades t
SET payment_reference_key_id = NULL, payment_reference_nonce = NULL,
	payment_reference_ciphertext = NULL, payment_reference_deleted_at = @deleted_at
FROM victims WHERE t.id = victims.id;
