-- name: AccountStatus :one
SELECT status FROM accounts WHERE id = $1;

-- name: InsertOrder :one
INSERT INTO c2c_orders (
    id, seller_id, total_nano, available_nano, unit_price_fen, min_per_trade_nano, max_per_trade_nano,
    payment_methods_ciphertext, payment_methods_nonce, payment_methods_key_id
) VALUES ($1, $2, $3, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO NOTHING
RETURNING id;

-- name: GetOrder :one
SELECT sqlc.embed(o), a.display_name AS seller_name
FROM c2c_orders o JOIN accounts a ON a.id = o.seller_id
WHERE o.id = $1;

-- name: LockOrder :one
SELECT sqlc.embed(o), a.display_name AS seller_name
FROM c2c_orders o JOIN accounts a ON a.id = o.seller_id
WHERE o.id = $1
FOR UPDATE OF o;

-- name: UpdateOrder :exec
UPDATE c2c_orders
SET available_nano = $2, in_trade_nano = $3, sold_nano = $4, closed_nano = $5, status = sqlc.arg(status)::text,
    closed_at = CASE WHEN sqlc.arg(status)::text = 'closed' THEN COALESCE(closed_at, now()) ELSE closed_at END,
    updated_at = now()
WHERE id = $1;

-- Open orders by unit price, then age. The keyset cursor is the last row
-- of the previous page.
-- name: ListMarketOrders :many
SELECT sqlc.embed(o), a.display_name AS seller_name
FROM c2c_orders o JOIN accounts a ON a.id = o.seller_id
WHERE o.status = 'open' AND o.available_nano > 0 AND o.seller_id <> sqlc.arg(viewer_id)
  AND (NOT sqlc.arg(has_cursor)::bool
       OR (o.unit_price_fen, o.created_at, o.id) > (sqlc.arg(after_price)::bigint, sqlc.arg(after_time)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY o.unit_price_fen, o.created_at, o.id
LIMIT sqlc.arg(row_limit);

-- name: ListSellerOrders :many
SELECT sqlc.embed(o), a.display_name AS seller_name
FROM c2c_orders o JOIN accounts a ON a.id = o.seller_id
WHERE o.seller_id = sqlc.arg(seller_id)
  AND (sqlc.arg(status)::text = '' OR o.status = sqlc.arg(status)::text)
  AND (NOT sqlc.arg(has_cursor)::bool OR (o.created_at, o.id) < (sqlc.arg(after_time)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY o.created_at DESC, o.id DESC
LIMIT sqlc.arg(row_limit);

-- name: OrderIDOfTrade :one
SELECT order_id FROM c2c_trades WHERE id = $1;

-- name: CountUnfinishedTrades :one
SELECT count(*) FROM c2c_trades
WHERE order_id = $1 AND buyer_id = $2 AND status IN ('awaiting_payment', 'paid', 'disputed');

-- The payment deadline comes from the platform setting at insert time.
-- name: InsertTrade :one
INSERT INTO c2c_trades (id, order_id, buyer_id, seller_id, amount_nano, unit_price_fen, total_fen, payment_deadline)
VALUES ($1, $2, $3, $4, $5, $6, $7,
        now() + make_interval(mins => (SELECT c2c_payment_timeout_minutes FROM settings)))
ON CONFLICT (id) DO NOTHING
RETURNING id;

-- name: GetTrade :one
SELECT sqlc.embed(t), b.display_name AS buyer_name, s.display_name AS seller_name,
       o.payment_methods_ciphertext, o.payment_methods_nonce, o.payment_methods_key_id
FROM c2c_trades t
JOIN c2c_orders o ON o.id = t.order_id
JOIN accounts b ON b.id = t.buyer_id
JOIN accounts s ON s.id = t.seller_id
WHERE t.id = $1;

-- name: LockTrade :one
SELECT id FROM c2c_trades WHERE id = $1 FOR UPDATE;

-- name: MarkTradePaid :exec
UPDATE c2c_trades SET status = 'paid', buyer_note = sqlc.narg(note), paid_at = now() WHERE id = $1;

-- Moves a trade to a terminal status. Releases and returns reference the
-- ledger transaction that moved the points.
-- name: FinishTrade :exec
UPDATE c2c_trades
SET status = sqlc.arg(status)::text,
    ledger_tx_id = sqlc.narg(ledger_tx_id),
    resolved_by = sqlc.narg(resolved_by),
    resolution_reason = sqlc.narg(resolution_reason),
    released_at = CASE WHEN sqlc.arg(status)::text = 'released' THEN now() ELSE released_at END,
    cancelled_at = CASE WHEN sqlc.arg(status)::text = 'cancelled' THEN now() ELSE cancelled_at END,
    resolved_at = CASE WHEN sqlc.arg(status)::text LIKE 'resolved\_%' THEN now() ELSE resolved_at END
WHERE id = sqlc.arg(id);

-- name: SetDisputeStatement :exec
UPDATE c2c_trades
SET status = 'disputed',
    dispute_opened_by = COALESCE(dispute_opened_by, sqlc.arg(actor_id)),
    disputed_at = COALESCE(disputed_at, now()),
    buyer_statement = CASE WHEN sqlc.arg(is_buyer)::bool THEN sqlc.arg(statement)::text ELSE buyer_statement END,
    seller_statement = CASE WHEN sqlc.arg(is_buyer)::bool THEN seller_statement ELSE sqlc.arg(statement)::text END
WHERE id = sqlc.arg(id);

-- name: ListUserTrades :many
SELECT sqlc.embed(t), b.display_name AS buyer_name, s.display_name AS seller_name,
       o.payment_methods_ciphertext, o.payment_methods_nonce, o.payment_methods_key_id
FROM c2c_trades t
JOIN c2c_orders o ON o.id = t.order_id
JOIN accounts b ON b.id = t.buyer_id
JOIN accounts s ON s.id = t.seller_id
WHERE (CASE sqlc.arg(role)::text
         WHEN 'buyer' THEN t.buyer_id = sqlc.arg(user_id)
         WHEN 'seller' THEN t.seller_id = sqlc.arg(user_id)
         ELSE t.buyer_id = sqlc.arg(user_id) OR t.seller_id = sqlc.arg(user_id) END)
  AND (sqlc.arg(status)::text = '' OR t.status = sqlc.arg(status)::text)
  AND (NOT sqlc.arg(pending)::bool
       OR (t.buyer_id = sqlc.arg(user_id) AND t.status = 'awaiting_payment')
       OR (t.seller_id = sqlc.arg(user_id) AND t.status = 'paid'))
  AND (NOT sqlc.arg(has_cursor)::bool OR (t.created_at, t.id) < (sqlc.arg(after_time)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY t.created_at DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListDisputedTrades :many
SELECT sqlc.embed(t), b.display_name AS buyer_name, s.display_name AS seller_name,
       o.payment_methods_ciphertext, o.payment_methods_nonce, o.payment_methods_key_id
FROM c2c_trades t
JOIN c2c_orders o ON o.id = t.order_id
JOIN accounts b ON b.id = t.buyer_id
JOIN accounts s ON s.id = t.seller_id
WHERE t.status = sqlc.arg(status)::text
  AND (NOT sqlc.arg(has_cursor)::bool OR (t.disputed_at, t.id) > (sqlc.arg(after_time)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY t.disputed_at, t.id
LIMIT sqlc.arg(row_limit);

-- name: ListDueTrades :many
SELECT id FROM c2c_trades
WHERE status = 'awaiting_payment' AND payment_deadline <= now()
ORDER BY payment_deadline
LIMIT $1;
