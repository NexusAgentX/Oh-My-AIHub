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
#      账本核对（余额合计为 0、余额与分录一致）通过
#   6. 输出演练结论并清理容器
# 后端输出写入临时日志：演练成功时保持安静；任何一步失败（含后端启动失败）时在 stderr
# 打印日志末尾 50 行，其中列出无法解密的记录类型、ID 与缺失的 key id。后端日志不含
# 明文、密文或密钥，脚本也不回显环境变量。
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
backend_port=$((port + 1))
backend_pid=""
backend_log_lines=50
temp_dump="$(mktemp)"
backend_log="$(mktemp)"

# 退出时：失败则先打印后端日志末尾（停止后端会追加关闭日志，须在此之前读取），
# 再停止后端、删除临时文件与容器。仅当本脚本启动的后端仍在运行时才按端口终止进程，
# 避免误杀占用该端口的其他进程。
cleanup() {
  local status=$?
  if [ "$status" -ne 0 ] && [ -s "$backend_log" ]; then
    echo "--- 后端日志末尾（最多 $backend_log_lines 行）---" >&2
    tail -n "$backend_log_lines" "$backend_log" >&2
    echo "--- 后端日志结束 ---" >&2
  fi
  if [ -n "$backend_pid" ] && kill -0 "$backend_pid" 2>/dev/null; then
    lsof -ti ":$backend_port" | xargs kill 2>/dev/null || true
    kill "$backend_pid" 2>/dev/null || true
  fi
  rm -f "$temp_dump" "$backend_log"
  docker rm -f "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

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

(
  cd "$repo_root"
  DATABASE_URL="$drill_url" PGPASSWORD=drill COOKIE_SECURE=false PORT="$backend_port" \
    UPSTREAM_CREDENTIAL_KEYRING="$UPSTREAM_CREDENTIAL_KEYRING" \
    UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID="$UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID" \
    C2C_PRIVATE_DATA_KEYRING="$C2C_PRIVATE_DATA_KEYRING" \
    C2C_PRIVATE_DATA_ACTIVE_KEY_ID="$C2C_PRIVATE_DATA_ACTIVE_KEY_ID" \
    exec mise exec -- go -C backend run ./cmd/server
) >"$backend_log" 2>&1 &
backend_pid=$!
# 脱离作业表：否则退出时 bash 会把被终止的后端作业（含完整命令行）报告到 stderr。
disown "$backend_pid"
health=""
for _ in $(seq 1 20); do
  health="$(curl -s "http://127.0.0.1:$backend_port/api/health" || true)"
  if [ -n "$health" ]; then break; fi
  # 后端已退出（如密钥环不完整时的致命日志）无需等满 20 秒。
  if ! kill -0 "$backend_pid" 2>/dev/null; then break; fi
  sleep 1
done
if [ -z "$health" ]; then
  echo "恢复后的后端未能在隔离环境启动（很可能是密钥环缺失或密文损坏）；后端有输出时，日志末尾附在下方" >&2
  exit 1
fi

ledger_check="$(docker exec "$container" psql -U drill -d drill -tAc \
  "SELECT coalesce(sum(a.balance_nano), 0) = 0 AND bool_and(a.balance_nano = coalesce(e.total, 0)) FROM ledger_accounts a LEFT JOIN (SELECT ledger_account_id, sum(amount_nano) AS total FROM ledger_entries GROUP BY ledger_account_id) e ON e.ledger_account_id = a.id")"
if [ "$ledger_check" != "t" ]; then
  echo "恢复后的账本核对未通过（余额合计不为 0 或余额与分录不一致）" >&2
  exit 1
fi

echo "恢复演练通过：清单哈希校验、数据恢复、旧会话清除、迁移、后端启动与账本核对全部成功。"
