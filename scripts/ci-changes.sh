#!/usr/bin/env bash
# CI 改动范围判定：根据改动文件决定 ci.yml 需要运行哪些任务，输出可直接追加到 $GITHUB_OUTPUT 的 key=value 行：
#   frontend   前端门禁（lint、测试、构建、API 类型一致性）
#   backend    后端门禁与 PostgreSQL 集成测试（sqlc、gofmt、vet、staticcheck、-race 测试、迁移检查）
#   images     前后端镜像构建检查与 Compose 配置检查
#   docs_only  改动只涉及文档（仅供展示；此时上面三项均为 false）
#
# 判定规则（按顺序匹配，每个文件取第一条；所有文件的结果取并集）：
#   *.md、docs/**、licenses/**                         文档：不触发任何检查
#   .github/**、compose.yaml、mise.toml                 CI 定义与工具链：frontend + backend + images
#   backend/api/openapi.yaml、scripts/**                前后端共享输入：frontend + backend
#   两端的 Dockerfile、.dockerignore                    只影响镜像：images
#   backend/go.mod、go.sum                              backend + images（依赖会进入镜像）
#   frontend/package.json、package-lock.json、nginx.conf  frontend + images
#   backend/**、frontend/**                             所在端
#   其他任何文件（含无法识别、被引号转义的路径）           保守处理：frontend + backend + images
# 没有改动文件（空列表）同样全部运行。
# frontend/nginx.conf 同时被 frontend/vite.config.test.ts 读取，所以它也属于 frontend。
#
# 推送到 main（--push <before> <after> <父提交运行文件>）在上述规则之外多两层判定，并额外输出两行：
#   verified_by_pr  true 表示这次推送的代码树已在 PR 上完整验证过，无需再运行任何任务（四个标志全部为 false）
#   reason          判定依据：verified-by-pr、diff（按本次推送的改动分类），或以 full- 开头的保守全量原因
# 判定顺序：
#   1. 无法确认推送范围时全量运行：after 或 before 不是本仓库里的提交（含 before 缺失、全零即首次推送）、
#      before 与 after 相同、before 不是 after 的祖先（强制推送、历史被改写）。
#   2. 父提交未验证时全量运行（reason=full-parent-unverified）：只有 before（上一次的 main 提交，合并时即第一个父提交）
#      在 main 上已有完成且成功的 ci 运行，才允许下面的跳过或按范围运行。否则一律全量运行，
#      包括没有运行、排队中、进行中、失败、取消，以及查询失败（文件为空、不存在或不是有效响应；失败时关闭）。
#      <父提交运行文件> 是 “列出 ci.yml 工作流运行” 接口对 head_sha=<before>&event=push&branch=main 的响应，
#      由 ci.yml 查询后写入；判定直接调用 scripts/ci-reuse-check.sh（与发版复用同一份实现：只认 ci.yml 在 main 上
#      push 触发、提交号一致的运行，取最新一次，已完成且 success 才算），这里不重复实现。
#      作用：范围化与跳过都信任 before 的结果。有了这一条，main 上任何一个绿色提交都意味着它沿第一父提交向前
#      一路是绿的，直到某次全量运行；红色的 main 提交之后不会有后续的文档合并或已验证合并把它“洗绿”，
#      发版复用也就不会继承假的绿。代价：连续合并时，后一个合并常因父提交的运行还没结束而全量运行。
#   3. 已由 PR 验证（不运行任何任务）：after 是恰好两个父提交的合并提交，且
#        - 第一个父提交就是 before（这次推送只带来这一个合并，没有夹带其他提交）；
#        - 第一个父提交是第二个父提交（PR 头）的祖先（合并时 PR 已同步了 main，PR 头已包含 main 的全部内容）；
#        - after 的代码树与 PR 头的代码树完全相同。
#      安全性：main 规则集强制 gates 与 integration 在 PR 头上成功后才能合并；PR 头已包含 main 的全部内容，
#      所以 PR 上测过的合并结果（合并引用）就是 PR 头的代码树，也就是这个合并提交的代码树。
#      三个条件缺一不可：任何一个不满足，就按第 4 条处理。
#   4. 其余情况按 before..after 的两点比较（本次推送的净变化）分类，规则同上；列表为空同样全量运行。
#      未同步的合并、非合并提交的直接推送、一次推送多个提交都走这里。
# 这套机制依赖“main 上每个提交都有结论”：范围化运行信任 before 的结果，所以 ci.yml 在 main 上不取消运行。
#
# 用法：
#   scripts/ci-changes.sh --all                    # 全部为 true（其他非 PR 事件）
#   scripts/ci-changes.sh --diff <base> <head>     # 对 <base>...<head>（与合并基点比较）的改动文件分类
#   scripts/ci-changes.sh --push <before> <after> <父提交运行文件>   # 推送到 main：见上
#   scripts/ci-changes.sh --stdin                  # 对标准输入中按行给出的路径分类（本地验证用）
# 本地验证：scripts/test-ci.sh
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

print_flags() {
  printf 'frontend=%s\nbackend=%s\nimages=%s\ndocs_only=%s\n' "$1" "$2" "$3" "$4"
}

classify() {
  local frontend=false backend=false images=false docs=true any=false path
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    any=true
    case "$path" in
      *.md | docs/* | licenses/*) continue ;;
    esac
    docs=false
    case "$path" in
      .github/* | compose.yaml | mise.toml)
        frontend=true; backend=true; images=true ;;
      backend/api/openapi.yaml | scripts/*)
        frontend=true; backend=true ;;
      backend/Dockerfile | backend/.dockerignore | frontend/Dockerfile | frontend/.dockerignore)
        images=true ;;
      backend/go.mod | backend/go.sum)
        backend=true; images=true ;;
      frontend/package.json | frontend/package-lock.json | frontend/nginx.conf)
        frontend=true; images=true ;;
      backend/*)
        backend=true ;;
      frontend/*)
        frontend=true ;;
      *)
        frontend=true; backend=true; images=true ;;
    esac
  done
  if [ "$any" = false ]; then
    print_flags true true true false
    return
  fi
  print_flags "$frontend" "$backend" "$images" "$docs"
}

# 无法确认推送范围时的保守结果：全部运行。
print_full() {
  print_flags true true true false
  printf 'verified_by_pr=false\nreason=full-%s\n' "$1"
}

# push_scope <before> <after> <父提交运行文件>：推送到 main 的范围判定（规则见文件开头）。必须在 git 仓库内运行。
push_scope() {
  local before="$1" after="$2" parent_runs="$3" files line p1 p2 parent_run
  local -a parents

  # 参数来自 CI 上下文；拒绝以 - 开头的值，避免被当成 git 选项。
  case "$before" in -*) echo "无效的提交：$before" >&2; exit 2 ;; esac
  case "$after" in -*) echo "无效的提交：$after" >&2; exit 2 ;; esac
  if ! after="$(git rev-parse --verify --quiet "$after^{commit}")"; then
    print_full after-unknown
    return
  fi
  # before 缺失或全零（新分支、首次推送）。
  if [ -z "$before" ] || [ -z "${before//0/}" ]; then
    print_full no-before
    return
  fi
  if ! before="$(git rev-parse --verify --quiet "$before^{commit}")"; then
    print_full before-unknown
    return
  fi
  if [ "$before" = "$after" ]; then
    print_full empty-range
    return
  fi
  if ! git merge-base --is-ancestor "$before" "$after"; then
    print_full before-not-ancestor
    return
  fi

  # 父提交未验证：before 在 main 上必须已有成功的 ci 运行，否则下面的跳过与范围化都不可信。
  # 运行文件缺失、为空或不是有效响应时，ci-reuse-check.sh 以非零状态退出，同样走这里（失败时关闭）。
  if ! parent_run="$("$script_dir/ci-reuse-check.sh" "$before" < "$parent_runs")"; then
    print_full parent-unverified
    return
  fi
  echo "父提交 $before 的 ci 运行已成功：$parent_run" >&2

  # 已由 PR 验证：见文件开头的三个条件。任何一步无法确认都落入下面的分类。
  if line="$(git rev-list --parents -n 1 "$after")"; then
    read -r -a parents <<<"$line"
    # parents[0] 是 after 自己；恰好两个父提交时共 3 项。
    if [ "${#parents[@]}" -eq 3 ]; then
      p1="${parents[1]}"
      p2="${parents[2]}"
      if [ "$p1" = "$before" ] \
        && git merge-base --is-ancestor "$p1" "$p2" \
        && [ "$(git rev-parse "$after^{tree}")" = "$(git rev-parse "$p2^{tree}")" ]; then
        echo "已由 PR 验证：$after 是已同步 main 的 PR 头 $p2 的合并，代码树相同。" >&2
        print_flags false false false false
        printf 'verified_by_pr=true\nreason=verified-by-pr\n'
        return
      fi
    fi
  fi

  if ! files="$(git -c core.quotepath=false diff --no-renames --name-only "$before" "$after")"; then
    print_full diff-failed
    return
  fi
  printf '%s\n' "$files" | sed 's/^/changed: /' >&2
  printf '%s\n' "$files" | classify
  printf 'verified_by_pr=false\nreason=diff\n'
}

case "${1:-}" in
  --all)
    print_flags true true true false
    ;;
  --diff)
    [ "$#" -eq 3 ] || { echo "用法：$0 --diff <base> <head>" >&2; exit 2; }
    # --no-renames 让重命名同时列出旧路径和新路径，避免把 backend 下的文件改名成文档而漏判。
    # 先把结果读入变量：git 失败时脚本直接退出，不会输出任何标志。
    files="$(git -c core.quotepath=false diff --no-renames --name-only "$2...$3")"
    # 改动文件列表写到 stderr，便于在 CI 日志中核对判定依据。
    printf '%s\n' "$files" | sed 's/^/changed: /' >&2
    printf '%s\n' "$files" | classify
    ;;
  --push)
    [ "$#" -eq 4 ] || { echo "用法：$0 --push <before> <after> <父提交运行文件>" >&2; exit 2; }
    push_scope "$2" "$3" "$4"
    ;;
  --stdin)
    classify
    ;;
  *)
    echo "用法：$0 --all | --diff <base> <head> | --push <before> <after> <父提交运行文件> | --stdin" >&2
    exit 2
    ;;
esac
