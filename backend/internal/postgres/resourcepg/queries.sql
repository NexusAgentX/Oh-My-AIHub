-- name: LookupCodexSession :one
SELECT group_id,channel_id,credential_version,credential_scope FROM codex_session_routes
WHERE account_id=$1 AND key_id=$2 AND session_id=$3;

-- Claim before sending anything upstream. The no-op update returns the winner
-- even when concurrent first requests propose different channels; no binding
-- field can be replaced. No post-response write or SQL in streaming loops.
-- name: ClaimCodexSession :one
INSERT INTO codex_session_routes(account_id,key_id,session_id,group_id,channel_id,credential_version,credential_scope)
VALUES($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT(account_id,key_id,session_id) DO UPDATE SET session_id=codex_session_routes.session_id
RETURNING group_id,channel_id,credential_version,credential_scope;
