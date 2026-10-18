-- name: InsertContribution :exec
INSERT INTO market_contributions(id,owner_id,kind,target_id,title,state,revision,document,base_document,report_ids,task_ids,pending_prices,review_reason,reviewed_by,submitted_at,published_at,created_at,updated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18);

-- name: GetContribution :one
SELECT * FROM market_contributions WHERE id=$1;

-- name: LockContribution :one
SELECT * FROM market_contributions WHERE id=$1 FOR UPDATE;

-- name: UpdateContribution :execrows
UPDATE market_contributions SET title=$2,state=$3,revision=$4,document=$5,base_document=$6,report_ids=$7,task_ids=$8,review_reason=$9,reviewed_by=$10,submitted_at=$11,published_at=$12,updated_at=$13,pending_prices=sqlc.arg(pending_prices)
WHERE id=$1 AND revision=sqlc.arg(base_revision);

-- name: ListContributions :many
SELECT id,title,kind,state,revision,created_at,updated_at FROM market_contributions
WHERE ((sqlc.arg(queue)::boolean AND state IN ('submitted','publication_pending')) OR (NOT sqlc.arg(queue)::boolean AND owner_id=sqlc.arg(owner_id)))
 AND (sqlc.narg(after_at)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(after_at)::timestamptz,sqlc.narg(after_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);

-- Evidence appears here only after the same transaction published its object.
-- name: ApprovedReportReferences :many
SELECT DISTINCT ON (b.target_id) b.id FROM market_report_bindings b
JOIN market_capability_evidence e ON e.report_id=b.id
WHERE b.target_id=ANY(sqlc.arg(target_ids)::uuid[])
ORDER BY b.target_id,b.created_at DESC,b.id DESC;

-- name: PublishEvidence :exec
INSERT INTO market_capability_evidence(report_id,capability,layer,state,reason)
SELECT sqlc.arg(report_id)::uuid,x.capability,x.layer,x.state,x.reason
FROM jsonb_to_recordset(sqlc.arg(evidence)::jsonb) AS x(capability text,layer text,state text,reason text)
ON CONFLICT(report_id,capability,layer) DO NOTHING;

-- name: LinkedChannelCount :one
SELECT count(*)::bigint FROM channels WHERE deleted_at IS NULL AND market_group_id=ANY(sqlc.arg(group_ids)::uuid[]);
