#!/usr/bin/env bash
# 迁移守卫：已随正式版本发布的迁移只允许新增，不得修改、删除或重命名。
#
# 基线是最新正式发布 tag（见 latest-release-tag.sh，忽略预发布）。检查工作区中的迁移目录：
#   1. 基线 tag 中的每个迁移文件必须仍在原路径，且内容与 tag 完全一致
#   2. 新增的迁移文件必须是 goose 可识别的 <数字>_<名称>.sql，且版本号大于已发布的最大版本
# 结构或数据变更请追加新的编号迁移，而不是改写已发布迁移（AGENTS.md「数据库迁移」）。
#
# 用法：
#   scripts/check-migrations.sh
#   BASELINE_TAG=v0.10.1 scripts/check-migrations.sh   # 指定基线，仅用于排查
# 需要本地存在完整的 tag（CI 中使用 fetch-depth: 0）。
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"
migrations_dir="backend/internal/database/migrations"

baseline_tag="${BASELINE_TAG:-$("$repo_root/scripts/latest-release-tag.sh")}"
if ! git rev-parse --quiet --verify "refs/tags/$baseline_tag^{commit}" >/dev/null; then
  echo "基线 tag $baseline_tag 不存在。" >&2
  exit 1
fi

released_files="$(git ls-tree -r --name-only "$baseline_tag" -- "$migrations_dir")"
if [ -z "$released_files" ]; then
  echo "基线 tag $baseline_tag 中没有 $migrations_dir 下的迁移文件，无法作为守卫基线。" >&2
  exit 1
fi

failures=0
fail() {
  echo "错误：$1" >&2
  failures=$((failures + 1))
}

version_of() {
  basename "$1" | sed -nE 's/^([0-9]+)_.*\.sql$/\1/p'
}

released_count=0
max_released=0
while IFS= read -r path; do
  released_count=$((released_count + 1))
  version="$(version_of "$path")"
  if [ -n "$version" ] && [ "$((10#$version))" -gt "$max_released" ]; then
    max_released=$((10#$version))
  fi
  if [ ! -f "$path" ]; then
    fail "已发布迁移 $path（$baseline_tag）被删除或重命名。"
  elif [ "$(git hash-object -- "$path")" != "$(git rev-parse "$baseline_tag:$path")" ]; then
    fail "已发布迁移 $path 与 $baseline_tag 中的内容不一致（被修改）。"
  fi
done <<EOF_RELEASED
$released_files
EOF_RELEASED

added=""
for path in "$migrations_dir"/*; do
  [ -f "$path" ] || continue
  if grep -Fxq -- "$path" <<<"$released_files"; then
    continue
  fi
  added="$added $path"
  version="$(version_of "$path")"
  if [ -z "$version" ]; then
    fail "新增迁移 $path 的文件名必须形如 0003_name.sql。"
  elif [ "$((10#$version))" -le "$max_released" ]; then
    fail "新增迁移 $path 的版本号 $((10#$version)) 必须大于已发布的最大版本 $max_released。"
  fi
done

if [ "$failures" -ne 0 ]; then
  echo "迁移守卫失败（基线 $baseline_tag）：请还原已发布的迁移，并把变更写入版本号更大的新迁移。" >&2
  echo "若分支落后于 $baseline_tag，请先把 main 合并进来再检查。" >&2
  exit 1
fi

echo "迁移守卫通过：基线 $baseline_tag，已发布迁移 $released_count 个均未改动，新增:${added:- 无}"
