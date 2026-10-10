# shellcheck shell=bash
# task-start.sh 与 task-finish.sh 共用的函数，由它们 source，不要直接执行。
# 调用方须先 set -euo pipefail 并设置 SCRIPT_DIR（脚本所在目录）。
# 约定：只做快进、普通删除；从不 reset、stash、强制切换或强制删除。

die() {
  printf '错误：%s\n' "$*" >&2
  exit 1
}

info() {
  printf '%s\n' "$*"
}

warn() {
  printf '警告：%s\n' "$*" >&2
}

# 任务名只用于分支名与目录名：小写字母、数字和单个连字符，如 265-task-scripts。
validate_slug() {
  local slug="${1-}"
  if [ -z "$slug" ]; then
    die "缺少任务名。"
  fi
  if [ "${#slug}" -gt 60 ] || ! printf '%s' "$slug" | LC_ALL=C grep -Eq '^[a-z0-9]+(-[a-z0-9]+)*$'; then
    die "任务名“$slug”无效：只能含小写字母、数字和单个连字符，不能以连字符开头或结尾，最长 60 个字符（如 265-task-scripts）。"
  fi
}

# 主工作区是 git worktree 列表的第一项；从主工作区或任何 worktree 运行结果相同。
# 设置 MAIN_WS 与 WORKTREES_DIR（主工作区同级的 Oh-My-AIHub-worktrees）。
resolve_main_workspace() {
  local listing
  listing="$(git -C "$SCRIPT_DIR" worktree list --porcelain)" || die "无法读取 git worktree 列表。"
  MAIN_WS="$(printf '%s\n' "$listing" | sed -n '1s/^worktree //p')"
  if [ -z "$MAIN_WS" ] || [ ! -e "$MAIN_WS/.git" ]; then
    die "无法确定主工作区（git worktree list 的第一项：“${MAIN_WS:-空}”）。"
  fi
  WORKTREES_DIR="$(dirname "$MAIN_WS")/Oh-My-AIHub-worktrees"
}

# 主工作区必须停在 main 且干净（含未跟踪文件）。脚本不会替你切换分支、stash 或清理。
require_main_ready() {
  local branch status
  branch="$(git -C "$MAIN_WS" symbolic-ref --quiet --short HEAD || true)"
  if [ "$branch" != "main" ]; then
    die "主工作区 $MAIN_WS 当前不在 main（${branch:-detached HEAD}）。主工作区只用于同步 main，请先自行恢复到 main 后重试。"
  fi
  status="$(git -C "$MAIN_WS" status --porcelain --untracked-files=all)" || die "无法读取主工作区状态。"
  if [ -n "$status" ]; then
    printf '%s\n' "$status" | head -n 20 >&2 || true
    die "主工作区 $MAIN_WS 不干净（有改动或未跟踪文件，见上）。请把这些改动迁移到任务 worktree 或自行处理；脚本不会 stash、reset 或清理。"
  fi
}

fetch_origin() {
  git -C "$MAIN_WS" fetch --prune --quiet origin || die "git fetch --prune origin 失败，请检查网络与远端权限。"
  git -C "$MAIN_WS" rev-parse --verify --quiet refs/remotes/origin/main >/dev/null || die "找不到 origin/main。"
}

# 本地 main 必须是 origin/main 的祖先（落后或相同），否则无法仅快进。
require_main_fast_forwardable() {
  if ! git -C "$MAIN_WS" merge-base --is-ancestor main origin/main; then
    die "本地 main 无法仅快进到 origin/main（有未推送的提交或已分叉）。请手动核对后再试；脚本不会 reset 或强制覆盖。"
  fi
}

fast_forward_main() {
  require_main_fast_forwardable
  git -C "$MAIN_WS" merge --ff-only --quiet origin/main || die "main 快进到 origin/main 失败。"
}

# 打印 main...origin/main 的左右提交数，形如“0 0”。
main_sync_counts() {
  git -C "$MAIN_WS" rev-list --left-right --count main...origin/main | tr '\t' ' '
}
