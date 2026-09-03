#!/usr/bin/env bash
# 隔离恢复演练（Feature #22）。
# 用法：
#   BACKUP_PASSPHRASE=... \
#   UPSTREAM_CREDENTIAL_KEYRING=... UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID=... \
#   C2C_PRIVATE_DATA_KEYRING=... C2C_PRIVATE_DATA_ACTIVE_KEY_ID=... \
#   scripts/restore-drill.sh backups/backup-<时间>.dump.aes
# 步骤：
#   1. 校验备份清单 SHA-256，解密到临时文件
#   2. 启动一次性隔离 PostgreSQL 容器并恢复
#   3. 清除恢复出来的旧会话（备份中的会话不得复活）
#   4. 运行迁移到最新版本
#   5. 用生产同款后端二进制对隔离库做启动自检：上游凭据与 C2C 私密数据可解密、
#      跨模块巡检（零和/投影/调用结算/C2C 一致性）全部通过
#   6. 输出演练结论并清理容器
set -euo pipefail

cipher_path="${1:?usage: restore-drill.sh <backup.dump.aes>}"
passphrase="${BACKUP_PASSPHRASE:?BACKUP_PASSPHRASE is required}"
for required in UPSTREAM_CREDENTIAL_KEYRING UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID C2C_PRIVATE_DATA_KEYRING C2C_PRIVATE_DATA_ACTIVE_KEY_ID; do
  if [ -z "${!required:-}" ]; then echo "缺少环境变量 $required（恢复演练需要与生产一致的密钥环）" >&2; exit 1; fi
done

manifest_path="${cipher_path%.dump.aes}.manifest.json"
if [ ! -f "$manifest_path" ]; then echo "找不到备份清单 $manifest_path" >&2; exit 1; fi
expected_sha="$(python3 -c "import json;print(json.load(open('$manifest_path'))['sha256'])")"
actual_sha="$(shasum -a 256 "$cipher_path" | awk '{print $1}')"
if [ "$expected_sha" != "$actual_sha" ]; then
  echo "备份密文哈希与清单不一致：$actual_sha != $expected_sha" >&2
  exit 1
fi

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
container="restore-drill-$$"
port=$(( 20000 + RANDOM % 20000 ))
temp_dump="$(mktemp)"
trap 'rm -f "$temp_dump"; docker rm -f "$container" >/dev/null 2>&1 || true' EXIT

openssl enc -d -aes-256-cbc -pbkdf2 -in "$cipher_path" -out "$temp_dump" -pass env:BACKUP_PASSPHRASE

docker run -d --name "$container" -e POSTGRES_USER=drill -e POSTGRES_PASSWORD=drill -e POSTGRES_DB=drill \
  -p "127.0.0.1:$port:5432" postgres:18-alpine >/dev/null
for _ in $(seq 1 30); do
  if docker exec "$container" pg_isready -U drill >/dev/null 2>&1; then break; fi
  sleep 1
done

drill_url="postgres://drill:drill@127.0.0.1:$port/drill?sslmode=disable"
docker cp "$temp_dump" "$container:/restore.dump" >/dev/null
docker exec "$container" pg_restore -U drill -d drill --no-owner /restore.dump >/dev/null

docker exec "$container" psql -U drill -d drill -qtc 'DELETE FROM sessions' >/dev/null
cleared="$(docker exec "$container" psql -U drill -d drill -tAc 'SELECT count(*) FROM sessions' | tr -d '[:space:]')"
echo "旧会话清除：sessions 剩余 $cleared 条"

(cd "$repo_root" && DATABASE_URL="$drill_url" PGPASSWORD=drill mise exec -- go -C backend run ./cmd/migrate >/dev/null)

(cd "$repo_root" && \
  DATABASE_URL="$drill_url" PGPASSWORD=drill COOKIE_SECURE=false PORT=$((port + 1)) \
  UPSTREAM_CREDENTIAL_KEYRING="$UPSTREAM_CREDENTIAL_KEYRING" \
  UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID="$UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID" \
  C2C_PRIVATE_DATA_KEYRING="$C2C_PRIVATE_DATA_KEYRING" \
  C2C_PRIVATE_DATA_ACTIVE_KEY_ID="$C2C_PRIVATE_DATA_ACTIVE_KEY_ID" \
  mise exec -- go -C backend run ./cmd/server >/dev/null 2>&1 &) 
backend_port=$((port + 1))
health=""
for _ in $(seq 1 20); do
  health="$(curl -s "http://127.0.0.1:$backend_port/api/health" || true)"
  if [ -n "$health" ]; then break; fi
  sleep 1
done
if [ -z "$health" ]; then echo "恢复后的后端未能在隔离环境启动（很可能是密钥环缺失或密文损坏）" >&2; exit 1; fi

inspection="$(docker exec "$container" psql -U drill -d drill -tAc \
  "SELECT zero_sum_ok AND projection_ok AND call_settlement_ok AND c2c_consistency_ok FROM ops_inspections ORDER BY checked_at DESC LIMIT 1")"
if [ "$inspection" != "t" ]; then
  echo "恢复后的跨模块巡检未通过（零和/投影/调用结算/C2C 一致性存在差异）" >&2
  exit 1
fi

lsof -ti ":$backend_port" | xargs kill 2>/dev/null || true
echo "恢复演练通过：清单哈希校验、数据恢复、旧会话清除、迁移、凭据可解密与跨模块巡检全部成功。"
