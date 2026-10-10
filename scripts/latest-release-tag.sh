#!/usr/bin/env bash
# 打印最新正式发布 tag。
# 正式 tag 形如 vMAJOR.MINOR.PATCH；带 -rc.N 等后缀的预发布 tag 一律忽略。
# 用法：scripts/latest-release-tag.sh
# 需要本地存在完整的 tag（CI 中使用 fetch-depth: 0）；找不到时以非零状态退出。
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
tag="$(git -C "$repo_root" tag --list 'v[0-9]*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1 || true)"
if [ -z "$tag" ]; then
  echo "找不到正式发布 tag（vMAJOR.MINOR.PATCH）。浅克隆或未拉取 tag 时请先执行 git fetch --tags --unshallow。" >&2
  exit 1
fi
printf '%s\n' "$tag"
