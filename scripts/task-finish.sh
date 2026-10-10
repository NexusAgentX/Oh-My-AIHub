#!/usr/bin/env bash
# 收尾：确认 <type>/<slug> 的 PR 已合并并进入 origin/main 后，清理本任务的
# worktree、本地分支和仍存在的远端分支，再把主工作区的 main 仅快进到 origin/main。
#
# 证据不足时停止，不会强制删除（不用 --force、branch -D），也不碰其他任务：
#   1. gh api 查到该分支的 PR 已合并（有 open 的 PR 则停止），且合并提交已在 origin/main
#   2. 本地分支与远端分支的提示提交都是 origin/main 的祖先（没有未合并或未推送的工作）
#   3. worktree 没有已跟踪改动、未跟踪文件，也没有除可再生缓存之外的被忽略文件
#      允许忽略（随 worktree 一并删除）的只有依赖与构建缓存，见 ignored_cache_reason
#   4. 主工作区在 main 且干净，且 main 能仅快进到 origin/main
# 之后依次：docker compose down（该 worktree 的 Compose 项目，有容器时）、
# git worktree remove、git branch -d、删除远端分支、git worktree prune，
# 最后确认 main...origin/main 为 0 0。
#
# 用法：
#   scripts/task-finish.sh [--dry-run] [--volumes] <type>/<slug>
#   mise run task-finish <type>/<slug> [--dry-run] [--volumes]
# 示例：mise run task-finish fix/251-key-layout
# 选项：
#   --dry-run  只做检查并打印将执行的清理动作，不修改任何东西（仍会 git fetch）
#   --volumes  同时删除该 Compose 项目的 Docker 卷（含数据库数据）；默认保留。
#              卷按 Compose 项目名匹配（默认等于 worktree 目录名 <type>-<slug>）
# 注意：合并 PR 须使用 merge commit，分支提示才会成为 origin/main 的祖先；
# squash 或 rebase 合并会因证据不足而停止。
# 清理已完成后可重复运行（已不存在的部分会被跳过），例如补加 --volumes。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=scripts/task-lib.sh
. "$SCRIPT_DIR/task-lib.sh"

usage() {
  echo "用法：scripts/task-finish.sh [--dry-run] [--volumes] <type>/<slug>   （如 fix/251-key-layout；type 为 $TASK_TYPES 之一）" >&2
}

dry_run=0
remove_volumes=0
task=""
for arg in "$@"; do
  case "$arg" in
    --dry-run) dry_run=1 ;;
    --volumes) remove_volumes=1 ;;
    -h | --help)
      usage
      exit 0
      ;;
    -*)
      usage
      die "未知选项 $arg。"
      ;;
    *)
      if [ -n "$task" ]; then
        usage
        die "只接受一个任务名。"
      fi
      task="$arg"
      ;;
  esac
done
if [ -z "$task" ]; then
  usage
  exit 2
fi

# 实际执行，或在 --dry-run 下只打印。
run() {
  if [ "$dry_run" -eq 1 ]; then
    printf '[dry-run] 将执行：'
    printf ' %q' "$@"
    printf '\n'
  else
    "$@"
  fi
}

# 仅这些被 .gitignore 忽略的路径可随 worktree 一起删除：它们都是依赖或构建缓存，
# 能由 mise run install / build 重新生成。其余被忽略的文件（.mise.local.toml、
# backups/、*.dump.aes、*.manifest.json 等）可能含本地配置或数据，一律停止并交人确认。
ignored_cache_reason() {
  case "$1" in
    frontend/node_modules/* | frontend/tools/openapi-types/node_modules/*)
      echo "npm 依赖缓存，mise run install 可重建"
      ;;
    frontend/dist/* | frontend/*.tsbuildinfo | frontend/vite.config.js | frontend/vite.config.d.ts)
      echo "前端构建产物，可重新生成"
      ;;
    backend/bin/*)
      echo "后端构建产物，可重新生成"
      ;;
    .DS_Store | */.DS_Store)
      echo "macOS 目录元数据"
      ;;
    *)
      return 1
      ;;
  esac
}

# 检查 worktree：有已跟踪改动、未跟踪文件或非缓存的被忽略文件则停止。
check_worktree_clean() {
  local wt="$1" status_file dirty="" bad_ignored="" ok_ignored="" entry code path reason
  status_file="$(mktemp)"
  if ! git -C "$wt" status --porcelain=v1 -z --untracked-files=normal --ignored >"$status_file"; then
    rm -f "$status_file"
    die "无法读取 $wt 的 git 状态。"
  fi
  while IFS= read -r -d '' entry; do
    code="${entry:0:2}"
    path="${entry:3}"
    if [ "$code" = '!!' ]; then
      if reason="$(ignored_cache_reason "$path")"; then
        ok_ignored="$ok_ignored  $path（$reason）"$'\n'
      else
        bad_ignored="$bad_ignored  $path"$'\n'
      fi
    else
      dirty="$dirty  $entry"$'\n'
    fi
  done <"$status_file"
  rm -f "$status_file"

  if [ -n "$dirty" ]; then
    printf '%s' "$dirty" | head -n 20 >&2 || true
    die "worktree $wt 有未提交的改动或未跟踪文件（见上）。请先提交、迁移或自行处理；脚本不会强制删除。"
  fi
  if [ -n "$bad_ignored" ]; then
    printf '%s' "$bad_ignored" | head -n 20 >&2 || true
    die "worktree $wt 含被 .gitignore 忽略、但不属于依赖或构建缓存的文件（见上）。它们可能是本地配置或数据，请确认后自行处理。"
  fi
  if [ -n "$ok_ignored" ]; then
    info "worktree 干净；以下被忽略的可再生缓存将随 worktree 一并删除："
    printf '%s' "$ok_ignored"
  else
    info "worktree 干净，没有未跟踪或被忽略的文件。"
  fi
}

# 找出检出了该分支的 worktree 路径（没有则输出空）。
find_worktree_for_branch() {
  git -C "$MAIN_WS" worktree list --porcelain | awk -v ref="refs/heads/$1" '
    $1 == "worktree" { sub(/^worktree /, ""); path = $0 }
    $1 == "branch" && $2 == ref { print path }'
}

# 查询 PR 并输出已合并 PR 的 “编号 合并提交”；有 open 的 PR 或没有已合并的 PR 则停止。
find_merged_pull_request() {
  local rows number state merged merge_sha url found=""
  command -v gh >/dev/null 2>&1 || die "需要 gh（GitHub CLI）来确认 PR 已合并。"
  rows="$(cd "$MAIN_WS" && gh api "repos/{owner}/{repo}/pulls?state=all&per_page=100&head={owner}:$1" \
    --jq '.[] | [.number, .state, (.merged_at // "-"), .head.sha, (.merge_commit_sha // "-"), .html_url] | @tsv')" \
    || die "gh api 查询 $1 的 PR 失败，无法确认合并状态；请检查 gh 登录与网络。"
  while IFS=$'\t' read -r number state merged _ merge_sha url; do
    [ -n "$number" ] || continue
    if [ "$state" = "open" ]; then
      die "PR #$number 仍是 open（$url），尚未合并。"
    fi
    if [ "$merged" != "-" ] && [ -z "$found" ]; then
      found="$number $merge_sha"
    fi
  done <<EOF_ROWS
$rows
EOF_ROWS
  [ -n "$found" ] || die "没有找到分支 $1 已合并的 PR（未合并或被关闭的 PR 不算）。"
  printf '%s\n' "$found"
}

# 停止该 worktree 的 Compose 项目；--volumes 时才删除卷，否则列出保留的卷。
cleanup_docker() {
  local wt="$1" dir_name="$2" ids project="" volumes
  if ! command -v docker >/dev/null 2>&1; then
    info "未找到 docker，跳过容器清理。"
    return 0
  fi
  if ! ids="$(docker ps -aq --filter "label=com.docker.compose.project.working_dir=$wt" 2>/dev/null)"; then
    warn "Docker 不可用，无法检查 $wt 的 Compose 容器，已跳过。"
    return 0
  fi
  if [ -n "$ids" ]; then
    # shellcheck disable=SC2086 # 容器 ID 不含空白，需按词拆分
    project="$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project" }}' $ids | sort -u | head -n 1)"
    info "发现 $(printf '%s\n' "$ids" | wc -l | tr -d ' ') 个 Compose 容器（项目 $project），停止并移除容器与网络。"
    if [ -d "$wt" ]; then
      (cd "$wt" && run docker compose -p "$project" down) || die "docker compose down 失败，已停止；worktree 与分支均未改动。"
    else
      run docker compose -p "$project" down || die "docker compose down 失败，已停止。"
    fi
  else
    info "没有该 worktree 的 Compose 容器。"
  fi

  # 卷由 Compose 项目名标记；没有容器时按默认项目名（目录名 <type>-<slug>）查找。
  volumes="$(docker volume ls -q --filter "label=com.docker.compose.project=${project:-$dir_name}" 2>/dev/null || true)"
  if [ -z "$volumes" ]; then
    return 0
  fi
  if [ "$remove_volumes" -eq 1 ]; then
    info "按 --volumes 删除 Docker 卷："
    # shellcheck disable=SC2086 # 卷名不含空白，需按词拆分
    printf '  %s\n' $volumes
    # shellcheck disable=SC2086
    run docker volume rm $volumes
  else
    info "保留 Docker 卷（含数据库数据）；确认不再需要后，重新运行本命令并加 --volumes 删除："
    # shellcheck disable=SC2086 # 卷名不含空白，需按词拆分
    printf '  %s\n' $volumes
  fi
}

main() {
  parse_task_name "$task"
  resolve_main_workspace

  local branch="$TASK_BRANCH"
  local expected_path="$WORKTREES_DIR/$TASK_DIR"
  if [ "$dry_run" -eq 1 ]; then
    info "[dry-run] 只检查并打印将执行的动作，不修改任何东西。"
  fi

  require_main_ready
  fetch_origin
  require_main_fast_forwardable

  local pr_info pr_number merge_sha
  pr_info="$(find_merged_pull_request "$branch")"
  pr_number="${pr_info%% *}"
  merge_sha="${pr_info#* }"
  if [ "$merge_sha" = "-" ] || ! git -C "$MAIN_WS" merge-base --is-ancestor "$merge_sha" origin/main 2>/dev/null; then
    die "PR #$pr_number 已合并，但其合并提交（${merge_sha}）尚未出现在 origin/main。请稍后重试。"
  fi
  info "PR #$pr_number 已合并，合并提交 ${merge_sha:0:7} 已在 origin/main。"

  # 本地分支、远端分支：提示提交必须已是 origin/main 的祖先。
  local have_local=0 have_remote=0 tip
  if git -C "$MAIN_WS" show-ref --verify --quiet "refs/heads/$branch"; then
    have_local=1
    tip="$(git -C "$MAIN_WS" rev-parse "refs/heads/$branch")"
    if ! git -C "$MAIN_WS" merge-base --is-ancestor "$tip" origin/main; then
      git -C "$MAIN_WS" log --oneline --max-count=10 "origin/main..$branch" >&2
      die "本地分支 $branch 有未进入 origin/main 的提交（见上），可能含未合并或未推送的工作；脚本不会删除。"
    fi
    info "本地分支 $branch（${tip:0:7}）已在 origin/main 中。"
  fi
  if git -C "$MAIN_WS" show-ref --verify --quiet "refs/remotes/origin/$branch"; then
    have_remote=1
    tip="$(git -C "$MAIN_WS" rev-parse "refs/remotes/origin/$branch")"
    if ! git -C "$MAIN_WS" merge-base --is-ancestor "$tip" origin/main; then
      git -C "$MAIN_WS" log --oneline --max-count=10 "origin/main..origin/$branch" >&2
      die "远端分支 origin/$branch 有未进入 origin/main 的提交（见上）；脚本不会删除。"
    fi
    info "远端分支 origin/$branch（${tip:0:7}）已在 origin/main 中。"
  fi

  # worktree：以检出该分支的 worktree 为准。
  local worktree
  worktree="$(find_worktree_for_branch "$branch")"
  if [ -z "$worktree" ] && [ -e "$expected_path" ]; then
    die "路径 $expected_path 存在，但不是分支 $branch 的 worktree；脚本不会处理它。"
  fi
  if [ -n "$worktree" ] && [ "$worktree" = "$MAIN_WS" ]; then
    die "分支 $branch 被主工作区检出，脚本不会处理主工作区。"
  fi
  if [ -n "$worktree" ] && [ -d "$worktree" ]; then
    local here
    here="$(pwd -P)"
    case "$here/" in
      "$worktree"/*)
        if [ "$dry_run" -eq 1 ]; then
          warn "当前目录位于要删除的 worktree 内；实际执行时会拒绝，请先 cd $MAIN_WS。"
        else
          die "当前目录位于要删除的 worktree $worktree 内。请先 cd $MAIN_WS 再运行。"
        fi
        ;;
    esac
    check_worktree_clean "$worktree"
  elif [ -n "$worktree" ]; then
    info "worktree $worktree 的目录已不存在，将由 git worktree prune 清除登记。"
  fi

  # 检查都通过，开始清理。
  info
  if [ "$dry_run" -eq 1 ]; then
    info "[dry-run] 将把主工作区 main 仅快进到 origin/main $(git -C "$MAIN_WS" rev-parse --short origin/main)。"
  else
    fast_forward_main
  fi

  cleanup_docker "${worktree:-$expected_path}" "$TASK_DIR"

  if [ -n "$worktree" ] && [ -d "$worktree" ]; then
    run git -C "$MAIN_WS" worktree remove "$worktree"
  elif [ -z "$worktree" ]; then
    info "没有找到分支 $branch 的 worktree，无需删除。"
  fi
  if [ "$have_local" -eq 1 ]; then
    run git -C "$MAIN_WS" branch -d "$branch"
  else
    info "本地分支 $branch 已不存在，无需删除。"
  fi
  if [ "$have_remote" -eq 1 ]; then
    run git -C "$MAIN_WS" push origin --delete "$branch"
  else
    info "远端分支 origin/$branch 已不存在，无需删除。"
  fi
  run git -C "$MAIN_WS" worktree prune

  local counts
  counts="$(main_sync_counts)"
  if [ "$dry_run" -eq 1 ]; then
    info "[dry-run] 当前 main...origin/main：$counts（实际执行快进后应为 0 0）。"
    return 0
  fi
  if [ "$counts" != "0 0" ]; then
    die "清理后 main...origin/main 为“$counts”，不是“0 0”。请手动核对主工作区。"
  fi
  info "任务 $branch 已清理。main...origin/main：$counts"
}

main
