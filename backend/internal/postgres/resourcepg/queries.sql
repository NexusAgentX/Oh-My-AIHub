-- name: LookupResource :many
SELECT r.* FROM gateway_resources r WHERE r.kind=$1 AND r.remote_id=$2
 AND (sqlc.arg(parent_filter)::text='' OR r.parent_id=sqlc.arg(parent_filter)::text)
 AND (r.kind<>'item' OR EXISTS(SELECT 1 FROM gateway_resources p WHERE p.kind IN ('conversation','response','stored_chat') AND p.remote_id=r.parent_id AND p.group_id=r.group_id AND p.credential_scope=r.credential_scope AND p.account_id=r.account_id AND p.key_id=r.key_id AND p.channel_id=r.channel_id AND p.credential_version=r.credential_version AND NOT p.deleted AND NOT p.quarantined))
ORDER BY group_id,credential_scope,parent_id LIMIT 17;

-- An identical creation observation is idempotent. A reused native ID in the
-- same engine/actual-credential collision domain permanently quarantines the
-- old record before either owner can read the newly overwritten resource.
-- name: ObserveResource :one
INSERT INTO gateway_resources(kind,remote_id,parent_id,group_id,credential_scope,account_id,key_id,channel_id,credential_version,creation_id,model_id,identity_hash)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT(identity_hash) DO UPDATE SET
 quarantined=gateway_resources.quarantined OR
 (gateway_resources.kind,gateway_resources.remote_id,gateway_resources.parent_id,gateway_resources.group_id,gateway_resources.credential_scope,gateway_resources.account_id,gateway_resources.key_id,gateway_resources.channel_id,gateway_resources.credential_version,gateway_resources.creation_id,gateway_resources.model_id)
 IS DISTINCT FROM
 (EXCLUDED.kind,EXCLUDED.remote_id,EXCLUDED.parent_id,EXCLUDED.group_id,EXCLUDED.credential_scope,EXCLUDED.account_id,EXCLUDED.key_id,EXCLUDED.channel_id,EXCLUDED.credential_version,EXCLUDED.creation_id,EXCLUDED.model_id),
 updated_at=now()
RETURNING quarantined;

-- name: DeleteOwnedResource :exec
UPDATE gateway_resources SET deleted=true,updated_at=now()
WHERE kind=$1 AND remote_id=$2 AND (sqlc.arg(parent_filter)::text='' OR parent_id=sqlc.arg(parent_filter)::text)
 AND account_id=$3 AND key_id=$4 AND group_id=$5 AND channel_id=$6 AND credential_version=$7;

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
