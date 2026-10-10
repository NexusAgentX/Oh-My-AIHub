#!/usr/bin/env bash
# ci-changes.sh（改动范围判定）与 ci-gate.sh（汇总判定）的样例测试，纯 bash，不依赖网络。
# 用法：scripts/test-ci.sh      失败时以非零状态退出。
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
changes="$repo_root/scripts/ci-changes.sh"
gate="$repo_root/scripts/ci-gate.sh"
failures=0

pass() { echo "ok    $1"; }
fail() { echo "FAIL  $1" >&2; failures=$((failures + 1)); }

# flags <路径...>：输出 "frontend backend images docs_only" 四个取值。
flags() {
  printf '%s\n' "$@" | "$changes" --stdin | sed 's/^[a-z_]*=//' | tr '\n' ' ' | sed 's/ $//'
}

# expect_flags <名称> <期望：frontend backend images docs_only> <路径...>
expect_flags() {
  local name="$1" expected="$2" actual
  shift 2
  actual="$(flags "$@")"
  if [ "$actual" = "$expected" ]; then pass "$name"; else fail "$name：期望 [$expected]，实际 [$actual]"; fi
}

#                 名称                                  frontend backend images docs_only
expect_flags "仅文档"                       "false false false true"  README.md docs/adr/0001-x.md licenses/a.txt
expect_flags "子目录中的 markdown"           "false false false true"  .github/pull_request_template.md frontend/README.md
expect_flags "仅前端源码"                    "true false false false"  frontend/src/App.tsx frontend/src/App.test.tsx
expect_flags "前端 vite 配置"                "true false false false"  frontend/vite.config.ts
expect_flags "前端工具的锁文件"              "true false false false"  frontend/tools/openapi-types/package-lock.json
expect_flags "仅后端源码"                    "false true false false"  backend/internal/api/handler.go
expect_flags "后端迁移"                      "false true false false"  backend/internal/database/migrations/0042_add_x.sql
expect_flags "OpenAPI"                       "true true false false"   backend/api/openapi.yaml
expect_flags "脚本"                          "true true false false"   scripts/check-migrations.sh
expect_flags "workflow"                      "true true true false"    .github/workflows/ci.yml
expect_flags "dependabot 配置"               "true true true false"    .github/dependabot.yml
expect_flags "mise.toml"                     "true true true false"    mise.toml
expect_flags "compose.yaml"                  "true true true false"    compose.yaml
expect_flags "后端 Dockerfile"               "false true true false"   backend/Dockerfile
expect_flags "后端 go.sum"                   "false true true false"   backend/go.sum
expect_flags "前端 Dockerfile"               "true false true false"   frontend/Dockerfile
expect_flags "nginx.conf"                    "true false true false"   frontend/nginx.conf
expect_flags "前端锁文件"                    "true false true false"   frontend/package-lock.json
expect_flags "前后端各改一处"                "true true false false"   frontend/src/a.ts backend/internal/b.go
expect_flags "文档与前端混合"                "true false false false"  README.md frontend/src/a.ts
expect_flags "文档与 Dockerfile 混合"        "false true true false"   docs/runbooks/deployment.md backend/Dockerfile
expect_flags "无法识别的根目录文件"          "true true true false"    .gitignore
expect_flags "被引号转义的路径"              "true true true false"    '"backend/internal/\344\275\240.go"'
expect_flags "空列表"                        "true true true false"    ""

all="$("$changes" --all | tr '\n' ' ' | sed 's/ $//')"
if [ "$all" = "frontend=true backend=true images=true docs_only=false" ]; then pass "--all"; else fail "--all：$all"; fi

# --diff：在临时仓库中验证合并基点语义、重命名与失败处理。
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
git_t() { git -C "$tmp" -c user.name=ci -c user.email=ci@example.invalid "$@"; }
git_t init -q -b main
mkdir -p "$tmp/backend/internal" "$tmp/frontend/src" "$tmp/docs"
echo a > "$tmp/backend/internal/a.go"
echo a > "$tmp/frontend/src/a.ts"
echo a > "$tmp/docs/a.txt"
git_t add -A && git_t commit -q -m base
git_t checkout -q -b feature
echo b > "$tmp/docs/b.md"
git_t add -A && git_t commit -q -m "docs only"
git_t checkout -q main
echo b > "$tmp/frontend/src/b.ts"
git_t add -A && git_t commit -q -m "main moved on (frontend)"

diff_flags() { (cd "$tmp" && "$changes" --diff "$1" "$2" 2>/dev/null | tr '\n' ' ' | sed 's/ $//'); }

# 三点比较：main 上后来的前端改动不能算进功能分支。
actual="$(diff_flags main feature)"
if [ "$actual" = "frontend=false backend=false images=false docs_only=true" ]; then pass "--diff 使用合并基点"; else fail "--diff 使用合并基点：$actual"; fi

# 重命名：backend 下的文件改名为文档，旧路径也必须计入。
git_t checkout -q -b rename feature
git_t mv backend/internal/a.go docs/moved.md
git_t commit -q -m "rename code to docs"
actual="$(diff_flags main rename)"
if [ "$actual" = "frontend=false backend=true images=false docs_only=false" ]; then pass "--diff 重命名计入旧路径"; else fail "--diff 重命名计入旧路径：$actual"; fi

# git 失败（提交不存在）必须使脚本失败，而不是输出任何标志。
if out="$(cd "$tmp" && "$changes" --diff main does-not-exist 2>/dev/null)"; then
  fail "--diff 遇到无效提交应失败，实际输出 [$out]"
elif [ -n "$out" ]; then
  fail "--diff 失败时不应输出标志，实际 [$out]"
else
  pass "--diff 无效提交时失败且无输出"
fi

# gate：只有“需要且成功”或“无需且被跳过”才通过。
expect_gate() {
  local name="$1" expected="$2" status=0
  shift 2
  "$gate" "$@" >/dev/null 2>&1 || status=$?
  if [ "$status" = "$expected" ]; then pass "gate：$name"; else fail "gate：$name：期望退出码 $expected，实际 $status"; fi
}

expect_gate "全部需要且成功"                    0 changes=true:success frontend=true:success backend=true:success integration=true:success images=true:success
expect_gate "纯文档：其余全部跳过"              0 changes=true:success frontend=false:skipped backend=false:skipped integration=false:skipped images=false:skipped
expect_gate "仅前端：后端与集成测试跳过"        0 changes=true:success frontend=true:success backend=false:skipped integration=false:skipped images=false:skipped
expect_gate "需要的任务失败"                    1 changes=true:success frontend=true:failure backend=false:skipped
expect_gate "需要的任务被取消"                  1 changes=true:success frontend=true:cancelled backend=false:skipped
expect_gate "不需要的任务失败也不放过"          1 changes=true:success frontend=false:failure
expect_gate "需要的任务被连带跳过"              1 changes=true:success frontend=true:skipped
expect_gate "changes 失败"                      1 changes=true:failure frontend=:skipped backend=:skipped
expect_gate "changes 被取消"                    1 changes=true:cancelled frontend=:skipped
expect_gate "输出为空视为失败"                  1 changes=true:success frontend=:success
expect_gate "没有参数"                          2

if [ "$failures" -ne 0 ]; then
  echo "$failures 项失败。" >&2
  exit 1
fi
echo "全部通过。"
