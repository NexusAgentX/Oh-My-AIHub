-- name: ListSuppliers :many
SELECT * FROM suppliers ORDER BY name, id;
-- name: ListEndpoints :many
SELECT * FROM supplier_endpoints ORDER BY base_url;
-- name: InsertSupplier :exec
INSERT INTO suppliers (id,name,kind,summary,description,website_url,docs_url,status_url,icon_data,icon_source,visible)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11);
-- name: UpdateSupplier :execrows
UPDATE suppliers SET name=$2,kind=$3,summary=$4,description=$5,website_url=$6,docs_url=$7,status_url=$8,icon_data=$9,icon_source=$10,visible=$11,updated_at=now() WHERE id=$1;
-- name: DeleteEndpoints :exec
DELETE FROM supplier_endpoints WHERE supplier_id=$1;
-- name: InsertEndpoint :exec
INSERT INTO supplier_endpoints (supplier_id,base_url,label) VALUES ($1,$2,$3);
-- name: ListChannelListings :many
SELECT c.id,c.name,c.summary,c.description,c.base_url,c.owner_id,a.display_name AS owner_name,
 c.capabilities,c.concurrency_limit,c.rpm_limit,c.daily_revenue_cap_nano,c.cooldown_seconds
FROM channels c JOIN accounts a ON a.id=c.owner_id
WHERE c.deleted_at IS NULL AND c.status='listed' AND a.status='active'
ORDER BY c.id;
