-- name: InsertAuditEvent :exec
INSERT INTO audit_events (actor_account_id, action, target_type, target_id, reason, details)
VALUES (sqlc.narg(actor_account_id), @action, @target_type, @target_id, @reason, @details);
