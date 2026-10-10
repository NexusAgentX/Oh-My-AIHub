#!/usr/bin/env bash
# 开工：把主工作区的 main 快进到最新 origin/main，再从 origin/main 创建
# 任务分支 <type>/<slug> 与独立 worktree
# <主工作区同级>/Oh-My-AIHub-worktrees/<type>-<slug>。
# type 为约定式提交类型：feat fix docs refactor perf test ci chore。
#
# 条件不满足时报告原因并以非零状态退出，不会 reset、stash 或强制：
#   - 任务名或 type 无效；分支或路径已存在
#   - 主工作区不在 main、不干净（含未跟踪文件），或 main 无法仅快进到 origin/main
#
# 用法：
#   scripts/task-start.sh <type>/<slug>
#   mise run task-start <type>/<slug>
# 示例：mise run task-start fix/251-key-layout
#       （分支 fix/251-key-layout，目录 Oh-My-AIHub-worktrees/fix-251-key-layout）
# 可在主工作区或任意 worktree 中运行。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=scripts/task-lib.sh
. "$SCRIPT_DIR/task-lib.sh"

if [ "$#" -ne 1 ] || [ "${1#-}" != "$1" ]; then
  echo "用法：scripts/task-start.sh <type>/<slug>   （如 fix/251-key-layout；type 为 $TASK_TYPES 之一）" >&2
  exit 2
fi

parse_task_name "$1"
resolve_main_workspace

branch="$TASK_BRANCH"
worktree_path="$WORKTREES_DIR/$TASK_DIR"

git check-ref-format --branch "$branch" >/dev/null 2>&1 || die "“$branch”不是合法的分支名。"
if git -C "$MAIN_WS" show-ref --verify --quiet "refs/heads/$branch"; then
  die "本地分支 $branch 已存在。恢复已有任务请直接进入它的 worktree（git worktree list 查看）；否则换一个任务名。"
fi
if [ -e "$worktree_path" ]; then
  die "路径 $worktree_path 已存在。"
fi
registered="$(git -C "$MAIN_WS" worktree list --porcelain)" || die "无法读取 git worktree 列表。"
if grep -Fxq -- "worktree $worktree_path" <<<"$registered"; then
  die "路径 $worktree_path 已登记为 worktree（目录可能已丢失）。请先确认该任务状态，必要时自行执行 git worktree prune。"
fi

require_main_ready
fetch_origin
if git -C "$MAIN_WS" show-ref --verify --quiet "refs/remotes/origin/$branch"; then
  die "远端已存在分支 origin/$branch。恢复已有任务请 fetch 后沿用它；否则换一个任务名。"
fi
fast_forward_main

expected="$(git -C "$MAIN_WS" rev-parse origin/main)"
git -C "$MAIN_WS" worktree add --no-track -b "$branch" "$worktree_path" origin/main >/dev/null 2>&1 \
  || die "git worktree add 失败，请检查 git worktree list 与分支状态。"
actual="$(git -C "$worktree_path" rev-parse HEAD)"
if [ "$actual" != "$expected" ]; then
  die "worktree 起点校验失败：HEAD 为 $actual，期望 origin/main 为 $expected。已保留 $worktree_path 供检查，未做任何强制处理。"
fi

echo "已创建任务 worktree："
echo "  分支：$branch"
echo "  路径：$worktree_path"
echo "  起点：origin/main $(git -C "$worktree_path" rev-parse --short HEAD)（已校验与 origin/main 一致）"
echo
echo "下一步：cd $worktree_path"
echo "首次需要前端依赖时执行：mise run install"
