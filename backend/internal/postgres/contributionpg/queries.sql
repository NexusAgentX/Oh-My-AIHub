-- name: InsertContribution :exec
INSERT INTO market_contributions(id,owner_id,kind,target_id,title,state,revision,document,base_document,pending_prices,review_reason,reviewed_by,submitted_at,published_at,created_at,updated_at,price_blanks)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,sqlc.arg(price_blanks));

-- name: GetContribution :one
SELECT * FROM market_contributions WHERE id=$1;

-- name: LockContribution :one
SELECT * FROM market_contributions WHERE id=$1 FOR UPDATE;

-- name: UpdateContribution :execrows
UPDATE market_contributions SET title=$2,state=$3,revision=$4,document=$5,base_document=$6,review_reason=$7,reviewed_by=$8,submitted_at=$9,published_at=$10,updated_at=$11,pending_prices=sqlc.arg(pending_prices),price_blanks=sqlc.arg(price_blanks)
WHERE id=$1 AND revision=sqlc.arg(base_revision);

-- name: ListContributions :many
SELECT id,title,kind,target_id,state,revision,review_reason,created_at,updated_at FROM market_contributions
WHERE ((sqlc.arg(queue)::boolean AND state IN ('submitted','publication_pending')) OR (NOT sqlc.arg(queue)::boolean AND owner_id=sqlc.arg(owner_id)))
 AND (sqlc.narg(after_at)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(after_at)::timestamptz,sqlc.narg(after_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);

-- Same-entity proposals cannot be submitted twice while one is awaiting review.
-- name: PendingContribution :one
SELECT id FROM market_contributions
WHERE kind=$1 AND target_id=$2 AND state IN ('submitted','publication_pending')
ORDER BY created_at DESC,id DESC LIMIT 1;

-- name: LinkedChannelCount :one
SELECT count(*)::bigint FROM channels WHERE deleted_at IS NULL AND market_group_id=ANY(sqlc.arg(group_ids)::uuid[]);
