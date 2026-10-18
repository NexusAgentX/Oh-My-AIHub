-- name: InsertBinding :exec
INSERT INTO market_report_bindings(id,owner_id,target_kind,target_id,target_revision,capability_digest,manifest_version,code_version,parameter_version,credential_scope)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);
-- name: InsertTask :exec
INSERT INTO detection_tasks(id,owner_id,state,complete,metadata,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: GetTask :one
SELECT t.*,b.credential_scope FROM detection_tasks t JOIN market_report_bindings b ON b.id=t.id WHERE t.id=$1;
-- name: ListTasks :many
SELECT t.*,b.credential_scope FROM detection_tasks t JOIN market_report_bindings b ON b.id=t.id
WHERE (t.owner_id=sqlc.arg(owner_id) OR sqlc.arg(is_admin)::boolean) AND (sqlc.narg(after_at)::timestamptz IS NULL OR (t.created_at,t.id)<(sqlc.narg(after_at)::timestamptz,sqlc.narg(after_id)::uuid)) ORDER BY t.created_at DESC,t.id DESC LIMIT sqlc.arg(page_limit);
-- name: UpdateTask :execrows
UPDATE detection_tasks SET state=$2,complete=$3,metadata=$4,updated_at=$5 WHERE id=$1;
-- name: InterruptTasks :exec
UPDATE detection_tasks SET state='interrupted',complete=false,metadata=jsonb_set(jsonb_set(jsonb_set(jsonb_set(metadata,'{state}','"interrupted"'),'{complete}','false'),'{updated_at}',to_jsonb(now())),'{execution_active}','false'),updated_at=now() WHERE state IN ('queued','running');
-- name: InsertAttempt :exec
INSERT INTO detection_attempts(id,task_id,endpoint_id,method,path_template,status,reason,category,duration_ms,sent,response_received,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12);
-- name: FindAttempts :many
SELECT * FROM detection_attempts WHERE task_id=$1 AND id=ANY($2::uuid[]) ORDER BY created_at,id;
-- name: PageAttempts :many
SELECT * FROM detection_attempts WHERE task_id=sqlc.arg(task_id) AND (sqlc.narg(after_at)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(after_at)::timestamptz,sqlc.narg(after_id)::uuid)) ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);
-- name: InsertSearchSetting :one
INSERT INTO market_search_settings(id,target,revision,method,low,high,hard_maximum,precision_mode,precision,rounds,rechecks,actor_id)
VALUES(sqlc.arg(id),sqlc.arg(target),(SELECT coalesce(max(revision),0)+1 FROM market_search_settings WHERE target=sqlc.arg(target)),sqlc.arg(method),sqlc.arg(low),sqlc.arg(high),sqlc.arg(hard_maximum),sqlc.arg(precision_mode),sqlc.arg(precision),sqlc.arg(rounds),sqlc.arg(rechecks),sqlc.arg(actor_id)) RETURNING *;
-- name: LatestSearchSettings :many
SELECT DISTINCT ON(target) * FROM market_search_settings ORDER BY target,revision DESC;
-- name: LockSearchSetting :exec
SELECT pg_advisory_xact_lock(hashtextextended('detection-search:'||$1,0));

-- name: InsertArtifact :exec
INSERT INTO detection_artifacts(id,task_id,kind,native_id,delete_endpoint,delete_path,producer_attempt_id,removed,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9);
-- name: ListArtifacts :many
SELECT * FROM detection_artifacts WHERE task_id=$1 AND NOT removed ORDER BY id LIMIT 33;
-- name: UpdateArtifact :execrows
UPDATE detection_artifacts SET removed=$3,reason=$4 WHERE id=$1 AND task_id=$2;

-- name: InsertResult :exec
INSERT INTO detection_results(id,task_id,scenario_id,state,result) VALUES($1,$2,$3,$4,$5);

-- name: InsertReport :exec
INSERT INTO detection_reports(id,task_id,owner_id,state,complete,metadata,created_at) VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: GetReport :one
SELECT r.*,b.credential_scope FROM detection_reports r JOIN market_report_bindings b ON b.id=r.id WHERE r.id=$1;
-- name: PageReports :many
SELECT id,task_id,state,complete,created_at FROM detection_reports WHERE task_id=sqlc.arg(task_id) AND (sqlc.narg(after_at)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(after_at)::timestamptz,sqlc.narg(after_id)::uuid)) ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);
