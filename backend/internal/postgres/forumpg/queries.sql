-- name: ListBoards :many
SELECT * FROM forum_boards ORDER BY sort_order, name, id;
-- name: CreateBoard :one
INSERT INTO forum_boards(name,description,sort_order) VALUES($1,$2,$3) RETURNING *;
-- name: UpdateBoard :one
UPDATE forum_boards SET name=$2,description=$3,sort_order=$4 WHERE id=$1 RETURNING *;
-- name: DeleteBoard :execrows
DELETE FROM forum_boards WHERE id=$1;

-- name: GetTopic :one
SELECT sqlc.embed(t), a.display_name, (SELECT count(*) FROM forum_replies r WHERE r.topic_id=t.id)::bigint AS reply_count
FROM forum_topics t JOIN accounts a ON a.id=t.author_id WHERE t.id=$1;
-- name: LockTopic :one
SELECT * FROM forum_topics WHERE id=$1 FOR UPDATE;
-- name: ListTopics :many
SELECT sqlc.embed(t), a.display_name, (SELECT count(*) FROM forum_replies r WHERE r.topic_id=t.id)::bigint AS reply_count
FROM forum_topics t JOIN accounts a ON a.id=t.author_id
WHERE t.kind=sqlc.arg(kind) AND (t.kind='discussion' OR t.author_id=sqlc.arg(actor_id) OR sqlc.arg(is_admin)::boolean)
AND (sqlc.narg(board_id)::uuid IS NULL OR t.board_id=sqlc.narg(board_id))
AND (sqlc.arg(search)::text='' OR strpos(lower(t.title || E'\n' || t.body),lower(sqlc.arg(search)))>0)
ORDER BY t.created_at DESC,t.id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);
-- name: CountTopics :one
SELECT count(*) FROM forum_topics t
WHERE t.kind=sqlc.arg(kind) AND (t.kind='discussion' OR t.author_id=sqlc.arg(actor_id) OR sqlc.arg(is_admin)::boolean)
AND (sqlc.narg(board_id)::uuid IS NULL OR t.board_id=sqlc.narg(board_id))
AND (sqlc.arg(search)::text='' OR strpos(lower(t.title || E'\n' || t.body),lower(sqlc.arg(search)))>0);
-- name: CreateTopic :one
INSERT INTO forum_topics(kind,board_id,author_id,title,body,status) VALUES($1,$2,$3,$4,$5,$6) RETURNING *;
-- name: UpdateTopic :exec
UPDATE forum_topics SET title=$2,body=$3,updated_at=now() WHERE id=$1;
-- name: UpdateStatus :exec
UPDATE forum_topics SET status=$2,updated_at=now() WHERE id=$1;
-- name: DeleteTopic :exec
DELETE FROM forum_topics WHERE id=$1;
-- name: ListReplies :many
SELECT sqlc.embed(r),a.display_name FROM forum_replies r JOIN accounts a ON a.id=r.author_id
WHERE r.topic_id=$1 ORDER BY r.created_at,r.id LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);
-- name: CountReplies :one
SELECT count(*) FROM forum_replies WHERE topic_id=$1;
-- name: GetReply :one
SELECT sqlc.embed(r),a.display_name FROM forum_replies r JOIN accounts a ON a.id=r.author_id WHERE r.id=$1;
-- name: CreateReply :one
INSERT INTO forum_replies(topic_id,author_id,body) VALUES($1,$2,$3) RETURNING *;
-- name: UpdateReply :exec
UPDATE forum_replies SET body=$2,updated_at=now() WHERE id=$1;
-- name: DeleteReply :exec
DELETE FROM forum_replies WHERE id=$1;

-- name: LockOwner :one
SELECT id FROM accounts WHERE id=$1 FOR UPDATE;
-- name: UsedBytes :one
SELECT COALESCE(sum(size),0)::bigint FROM forum_attachments WHERE owner_id=$1;
-- name: CreateAttachment :one
INSERT INTO forum_attachments(owner_id,name,media_type,inline,size,data) VALUES($1,$2,$3,$4,$5,$6)
RETURNING id,owner_id,topic_id,reply_id,name,media_type,inline,size,created_at;
-- name: LockAttachment :one
SELECT id,owner_id,topic_id,reply_id,name,media_type,inline,size,created_at FROM forum_attachments WHERE id=$1 FOR UPDATE;
-- name: GetAttachment :one
SELECT * FROM forum_attachments WHERE id=$1;
-- name: ListAttachments :many
SELECT id,owner_id,topic_id,reply_id,name,media_type,inline,size,created_at FROM forum_attachments
WHERE topic_id=$1 AND reply_id IS NOT DISTINCT FROM sqlc.narg(reply_id)::uuid ORDER BY created_at,id;
-- name: BindAttachment :exec
UPDATE forum_attachments SET topic_id=$2,reply_id=$3 WHERE id=$1;
-- name: DeleteRemovedAttachments :exec
DELETE FROM forum_attachments WHERE topic_id=$1 AND reply_id IS NOT DISTINCT FROM sqlc.narg(reply_id)::uuid AND NOT(id=ANY(sqlc.arg(keep_ids)::uuid[]));
-- name: CleanupAttachments :execrows
DELETE FROM forum_attachments WHERE topic_id IS NULL AND created_at < now()-interval '24 hours';
