-- name: ListChannels :many
SELECT c.id,c.owner_id,c.name,c.summary,c.description,c.status,c.suspended_reason,
 c.market_group_id,c.api_key_ciphertext,c.api_key_nonce,c.api_key_key_id,
 c.created_at,c.updated_at,a.username,a.display_name,a.status AS owner_status,
 mc.revision,mc.credential_revision,mc.probe_epoch,mc.address_ids,mc.overrides
FROM market_channels mc JOIN channels c ON c.id=mc.channel_id JOIN accounts a ON a.id=c.owner_id
WHERE c.deleted_at IS NULL ORDER BY c.id;

-- name: LockChannel :one
SELECT c.id,c.owner_id,c.name,c.summary,c.description,c.status,c.suspended_reason,
 c.market_group_id,c.api_key_ciphertext,c.api_key_nonce,c.api_key_key_id,
 c.created_at,c.updated_at,a.username,a.display_name,a.status AS owner_status,
 mc.revision,mc.credential_revision,mc.probe_epoch,mc.address_ids,mc.overrides
FROM market_channels mc JOIN channels c ON c.id=mc.channel_id JOIN accounts a ON a.id=c.owner_id
WHERE c.id=$1 AND c.deleted_at IS NULL FOR UPDATE OF c,mc;

-- name: InsertChannel :exec
INSERT INTO channels(id,owner_id,name,summary,description,api_key_ciphertext,api_key_nonce,api_key_key_id,status,market_group_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);

-- name: InsertConfiguration :exec
INSERT INTO market_channels(channel_id,address_ids,overrides) VALUES($1,$2,$3);

-- name: UpdateChannel :execrows
UPDATE channels SET name=$2,summary=$3,description=$4,status=$5,market_group_id=$6,
 api_key_ciphertext=$7,api_key_nonce=$8,api_key_key_id=$9,updated_at=now()
WHERE id=$1 AND deleted_at IS NULL;

-- name: UpdateConfiguration :execrows
UPDATE market_channels SET revision=revision+1,credential_revision=$2,probe_epoch=$3,address_ids=$4,overrides=$5
WHERE channel_id=$1 AND revision=sqlc.arg(expected_revision);

-- name: DeleteChoices :exec
DELETE FROM market_channel_model_choices WHERE channel_id=$1;

-- name: PutChoice :exec
INSERT INTO market_channel_model_choices(channel_id,group_model_id,excluded,discount_nano) VALUES($1,$2,$3,$4)
ON CONFLICT(channel_id,group_model_id) DO UPDATE SET excluded=EXCLUDED.excluded,discount_nano=EXCLUDED.discount_nano;

-- name: ListChoices :many
SELECT ch.* FROM market_channel_model_choices ch JOIN channels c ON c.id=ch.channel_id JOIN market_group_models m ON m.id=ch.group_model_id WHERE c.deleted_at IS NULL AND m.deleted_at IS NULL ORDER BY ch.channel_id,ch.group_model_id;

-- name: ListProbes :many
SELECT p.* FROM market_channel_model_probes p JOIN channels c ON c.id=p.channel_id JOIN market_group_models m ON m.id=p.group_model_id WHERE c.deleted_at IS NULL AND m.deleted_at IS NULL ORDER BY p.channel_id,p.group_model_id;

-- name: SaveProbe :exec
INSERT INTO market_channel_model_probes(channel_id,group_model_id,binding,tests,tested_at) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(channel_id,group_model_id) DO UPDATE SET binding=excluded.binding,tests=excluded.tests,tested_at=excluded.tested_at;

-- name: DeleteChannel :execrows
UPDATE channels SET deleted_at=now(),updated_at=now(),status=CASE WHEN status='suspended' THEN status ELSE 'unlisted' END WHERE id=$1 AND deleted_at IS NULL;

-- name: ModerateChannel :execrows
UPDATE channels SET status=$2,suspended_reason=sqlc.narg(reason),updated_at=now() WHERE id=$1 AND deleted_at IS NULL;

-- name: BumpProbeEpoch :execrows
UPDATE market_channels SET probe_epoch=probe_epoch+1,revision=revision+1 WHERE channel_id=$1 AND revision=$2;

-- Only immutable reports that were approved with a publication are eligible.
-- name: LockGroup :one
SELECT capability_digest,enabled,deleted_at FROM market_groups WHERE id=$1 FOR SHARE;

-- name: LockModel :one
SELECT group_id,capability_digest,enabled,deleted_at FROM market_group_models WHERE id=$1 FOR SHARE;

-- name: ListEvents :many
SELECT id,kind,reason,created_at FROM channel_events WHERE channel_id=$1 ORDER BY id DESC LIMIT 50;

-- name: InsertEvent :exec
INSERT INTO channel_events(channel_id,kind,reason) VALUES($1,$2,$3);
