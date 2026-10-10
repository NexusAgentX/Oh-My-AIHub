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
#   backend 与 frontend 的 Dockerfile、.dockerignore、依赖清单与锁文件（go.mod、go.sum、package.json、
#     package-lock.json）、frontend/nginx.conf           镜像输入：所在端 + images
#   backend/**、frontend/**                             所在端
#   其他任何文件（含无法识别、被引号转义的路径）           保守处理：frontend + backend + images
# 没有改动文件（空列表）同样全部运行；推送到 main（--all）始终全部运行。
# frontend/nginx.conf 同时被 frontend/vite.config.test.ts 读取，所以它也属于 frontend。
#
# 用法：
#   scripts/ci-changes.sh --all                    # 全部为 true（push 到 main 等非 PR 事件）
#   scripts/ci-changes.sh --diff <base> <head>     # 对 <base>...<head>（与合并基点比较）的改动文件分类
#   scripts/ci-changes.sh --stdin                  # 对标准输入中按行给出的路径分类（本地验证用）
# 本地验证：scripts/test-ci.sh
set -euo pipefail

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
      backend/Dockerfile | backend/.dockerignore | backend/go.mod | backend/go.sum)
        backend=true; images=true ;;
      frontend/Dockerfile | frontend/.dockerignore | frontend/nginx.conf | frontend/package.json | frontend/package-lock.json)
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
  --stdin)
    classify
    ;;
  *)
    echo "用法：$0 --all | --diff <base> <head> | --stdin" >&2
    exit 2
    ;;
esac
