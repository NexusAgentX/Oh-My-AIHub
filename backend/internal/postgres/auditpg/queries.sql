-- name: InsertAudit :exec
INSERT INTO audit_log (actor_id, action, target_type, target_id, reason, detail)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListAudit :many
SELECT l.id, l.actor_id, a.username AS actor_username, a.display_name AS actor_display_name,
       l.action, l.target_type, l.target_id, l.reason, l.detail, l.created_at
FROM audit_log l
LEFT JOIN accounts a ON a.id = l.actor_id
WHERE (sqlc.arg(action)::text = '' OR l.action = sqlc.arg(action))
  AND (sqlc.arg(target_type)::text = '' OR l.target_type = sqlc.arg(target_type))
  AND (sqlc.arg(target_id)::text = '' OR l.target_id = sqlc.arg(target_id))
  AND (sqlc.arg(actor_id)::text = '' OR l.actor_id::text = sqlc.arg(actor_id))
  AND (sqlc.arg(before_id)::bigint = 0 OR l.id < sqlc.arg(before_id))
ORDER BY l.id DESC
LIMIT sqlc.arg(row_limit);
