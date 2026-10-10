#!/usr/bin/env bash
# 升级路径检查：最新正式发布 tag 建库 → 当前代码升级。
#
# 在 TEST_DATABASE_URL 所在的 PostgreSQL 实例中新建一个一次性数据库，然后：
#   1. 构建并运行最新正式 tag（见 latest-release-tag.sh）的 cmd/migrate，模拟已部署的数据库
#   2. 构建并运行当前代码的 cmd/migrate，必须成功且停在当前最新迁移版本
#   3. 再运行一次当前 cmd/migrate，必须仍成功且版本不变（重复执行是空操作）
# 结束时删除一次性数据库和临时目录。
#
# 用法：
#   TEST_DATABASE_URL='postgres://user:pass@host:5432/db?sslmode=disable' \
#     scripts/check-migration-upgrade.sh
# 可选环境变量：
#   BASELINE_TAG  指定基线 tag，仅用于排查
#   PSQL          覆盖 psql 命令（不含 -d），例如本机没有 psql 时：
#                 PSQL='docker compose exec -T database psql -U oh_my_aihub'
# 依赖：go、git、tar，以及 psql（或上面的 PSQL 覆盖）。需要本地存在完整的 tag。
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
base_url="${TEST_DATABASE_URL:?TEST_DATABASE_URL is required}"
baseline_tag="${BASELINE_TAG:-$("$repo_root/scripts/latest-release-tag.sh")}"
migrations_dir="backend/internal/database/migrations"

base_no_query="${base_url%%\?*}"
admin_db="${base_no_query##*/}"
scratch_db="aihub_upgrade_$(date +%s)_$$"

# url_for <数据库名>：把 TEST_DATABASE_URL 中的库名替换掉，保留其余连接参数。
url_for() {
  local query=""
  if [ "$base_no_query" != "$base_url" ]; then query="?${base_url#*\?}"; fi
  printf '%s/%s%s' "${base_no_query%/*}" "$1" "$query"
}

# sql <数据库名> <语句>：单值输出，出错即失败。
sql() {
  if [ -n "${PSQL:-}" ]; then
    # shellcheck disable=SC2086 # PSQL 是由多个词组成的命令
    $PSQL -d "$1" -v ON_ERROR_STOP=1 -tA -c "$2"
  else
    psql "$(url_for "$1")" -v ON_ERROR_STOP=1 -tA -c "$2"
  fi
}

# newest_version <目录>：目录中最大的迁移版本号（十进制）。
newest_version() {
  local newest=0 file version
  for file in "$1"/*.sql; do
    version="$(basename "$file" | sed -nE 's/^([0-9]+)_.*\.sql$/\1/p')"
    if [ -n "$version" ] && [ "$((10#$version))" -gt "$newest" ]; then newest=$((10#$version)); fi
  done
  echo "$newest"
}

# migration_count <目录>：目录中的迁移文件数。
migration_count() {
  find "$1" -maxdepth 1 -name '*.sql' | wc -l | tr -d ' '
}

work_dir="$(mktemp -d)"
cleanup() {
  sql "$admin_db" "DROP DATABASE IF EXISTS \"$scratch_db\" WITH (FORCE)" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

# assert_state <标签> <期望最大版本> <期望迁移数>：库里的 goose 记录必须与迁移文件一致。
assert_state() {
  local label="$1" want_version="$2" want_count="$3" got_version got_count
  got_version="$(sql "$scratch_db" "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied")"
  got_count="$(sql "$scratch_db" "SELECT count(*) FROM goose_db_version WHERE version_id > 0 AND is_applied")"
  if [ "$got_version" != "$want_version" ] || [ "$got_count" != "$want_count" ]; then
    echo "错误：$label 后数据库版本为 $got_version（已应用 $got_count 个），期望 $want_version（$want_count 个）。" >&2
    exit 1
  fi
  echo "$label：数据库停在迁移版本 $got_version，已应用 $got_count 个迁移。"
}

echo "升级路径检查：基线 $baseline_tag → 当前代码"
mkdir -p "$work_dir/baseline"
git -C "$repo_root" archive --format=tar "$baseline_tag" backend | tar -x -C "$work_dir/baseline"
go -C "$work_dir/baseline/backend" build -trimpath -o "$work_dir/migrate-baseline" ./cmd/migrate
go -C "$repo_root/backend" build -trimpath -o "$work_dir/migrate-current" ./cmd/migrate

baseline_version="$(newest_version "$work_dir/baseline/$migrations_dir")"
baseline_count="$(migration_count "$work_dir/baseline/$migrations_dir")"
current_version="$(newest_version "$repo_root/$migrations_dir")"
current_count="$(migration_count "$repo_root/$migrations_dir")"

sql "$admin_db" "CREATE DATABASE \"$scratch_db\"" >/dev/null
export DATABASE_URL
DATABASE_URL="$(url_for "$scratch_db")"

echo "--- $baseline_tag 的 migrate 建库"
"$work_dir/migrate-baseline"
assert_state "$baseline_tag 建库" "$baseline_version" "$baseline_count"

echo "--- 当前代码的 migrate 升级"
"$work_dir/migrate-current"
assert_state "当前代码升级" "$current_version" "$current_count"

echo "--- 当前代码的 migrate 重复执行"
"$work_dir/migrate-current"
assert_state "重复执行" "$current_version" "$current_count"

echo "升级路径检查通过：$baseline_tag（版本 $baseline_version）→ 当前代码（版本 $current_version）。"
