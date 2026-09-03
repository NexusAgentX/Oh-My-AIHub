#!/usr/bin/env bash
# 加密 PostgreSQL 备份（Feature #22）。
# 用法（二选一）：
#   DB_CONTAINER=<容器名> BACKUP_PASSPHRASE=... scripts/backup-database.sh [输出目录]
#   DATABASE_URL=... PGPASSWORD=... BACKUP_PASSPHRASE=... scripts/backup-database.sh [输出目录]
# 第一种方式在数据库容器内执行 pg_dump（宿主机无需安装客户端工具）。
# 产出：
#   <dir>/backup-<UTC时间>.dump.aes      加密的 pg_dump custom 格式备份
#   <dir>/backup-<UTC时间>.manifest.json 备份清单（含密文哈希与密钥环引用，绝不含密钥值）
# 防误提交：备份目录默认 backups/ 已被 .gitignore 排除；若解析为受 Git 跟踪的路径则拒绝执行。
set -euo pipefail

output_dir="${1:-backups}"
passphrase="${BACKUP_PASSPHRASE:?BACKUP_PASSPHRASE is required}"

if [ -n "${DB_CONTAINER:-}" ]; then
  dump_db() { docker exec "$DB_CONTAINER" pg_dump -U oh_my_aihub --format=custom --dbname=oh_my_aihub; }
  schema_version() { docker exec "$DB_CONTAINER" psql -U oh_my_aihub -d oh_my_aihub -tAc 'SELECT max(version_id) FROM goose_db_version'; }
else
  database_url="${DATABASE_URL:?需要 DATABASE_URL，或提供 DB_CONTAINER 在数据库容器内执行}"
  dump_db() { pg_dump "$database_url" --format=custom; }
  schema_version() { psql "$database_url" -tAc 'SELECT max(version_id) FROM goose_db_version'; }
fi

if git rev-parse --is-inside-work-tree >/dev/null 2>&1 \
  && ! git check-ignore -q "$output_dir" \
  && ! git check-ignore -q "$output_dir/"; then
  echo "拒绝写入：目录 $output_dir 未被 .gitignore 排除，备份可能被误提交。" >&2
  exit 1
fi

mkdir -p "$output_dir"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
cipher_path="$output_dir/backup-$stamp.dump.aes"
manifest_path="$output_dir/backup-$stamp.manifest.json"

schema_version="$(schema_version | tr -d '[:space:]')"
temp_dump="$(mktemp)"
trap 'rm -f "$temp_dump"' EXIT
dump_db > "$temp_dump"
if [ ! -s "$temp_dump" ]; then echo "pg_dump 未产出数据" >&2; exit 1; fi

openssl enc -aes-256-cbc -pbkdf2 -salt -in "$temp_dump" -out "$cipher_path" -pass env:BACKUP_PASSPHRASE
sha256="$(shasum -a 256 "$cipher_path" | awk '{print $1}')"
size="$(wc -c < "$cipher_path" | tr -d ' ')"

cat > "$manifest_path" <<JSON
{
  "created_at_utc": "$stamp",
  "cipher": "aes-256-cbc-pbkdf2",
  "schema_version": $schema_version,
  "sha256": "$sha256",
  "size_bytes": $size,
  "keyring_references": {
    "upstream_credentials": "env:UPSTREAM_CREDENTIAL_KEYRING + env:UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID",
    "c2c_private_data": "env:C2C_PRIVATE_DATA_KEYRING + env:C2C_PRIVATE_DATA_ACTIVE_KEY_ID"
  },
  "note": "清单只引用密钥环环境变量名。恢复前必须以同样方式提供密钥环本身；密文与清单需与密钥环分开保存。"
}
JSON

rm -f "$temp_dump"; trap - EXIT
echo "备份完成：$cipher_path"
echo "清单：$manifest_path"
echo "请将密文、清单与两份密钥环分别安全保存（密钥环不得与备份存放在同一位置）。"
