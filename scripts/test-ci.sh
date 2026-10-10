#!/usr/bin/env bash
# ci-changes.sh（改动范围判定，含推送到 main 的 --push）、ci-gate.sh（汇总判定）与
# ci-reuse-check.sh（发版复用判定）的样例测试，不依赖网络；需要 git 与 jq。
# 用法：scripts/test-ci.sh      失败时以非零状态退出。
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
changes="$repo_root/scripts/ci-changes.sh"
gate="$repo_root/scripts/ci-gate.sh"
reuse="$repo_root/scripts/ci-reuse-check.sh"
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
  if [ "$actual" = "$expected" ]; then pass "$name"; else fail "${name}：期望 [$expected]，实际 [$actual]"; fi
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
expect_flags "后端 Dockerfile 只影响镜像"    "false false true false"  backend/Dockerfile
expect_flags "前端 Dockerfile 只影响镜像"    "false false true false"  frontend/Dockerfile
expect_flags ".dockerignore"                 "false false true false"  frontend/.dockerignore backend/.dockerignore
expect_flags "后端 go.sum"                   "false true true false"   backend/go.sum
expect_flags "后端 go.mod"                   "false true true false"   backend/go.mod
expect_flags "nginx.conf"                    "true false true false"   frontend/nginx.conf
expect_flags "前端锁文件"                    "true false true false"   frontend/package-lock.json
expect_flags "前端 package.json"             "true false true false"   frontend/package.json
expect_flags "前后端各改一处"                "true true false false"   frontend/src/a.ts backend/internal/b.go
expect_flags "文档与前端混合"                "true false false false"  README.md frontend/src/a.ts
expect_flags "文档与 Dockerfile 混合"        "false false true false"  docs/runbooks/deployment.md backend/Dockerfile
expect_flags "Dockerfile 与后端源码混合"     "false true true false"   backend/Dockerfile backend/internal/a.go
expect_flags "无法识别的根目录文件"          "true true true false"    .gitignore
expect_flags "被引号转义的路径"              "true true true false"    '"backend/internal/\344\275\240.go"'
expect_flags "空列表"                        "true true true false"    ""

all="$("$changes" --all | tr '\n' ' ' | sed 's/ $//')"
if [ "$all" = "frontend=true backend=true images=true docs_only=false" ]; then pass "--all"; else fail "--all：$all"; fi

# --diff：在临时仓库中验证合并基点语义、重命名与失败处理。
tmp="$(mktemp -d)"
ptmp="$(mktemp -d)"
pfile="$(mktemp)"
trap 'rm -rf "$tmp" "$ptmp" "$pfile"' EXIT
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

# 用 jq 构造 “列出 ci.yml 工作流运行” 接口的响应，作为 ci-reuse-check.sh 的输入，也作为 --push 的父提交运行文件。
SHA=0123456789abcdef0123456789abcdef01234567
OTHER_SHA=fedcba9876543210fedcba9876543210fedcba98
# run <id> <status> <conclusion|null> [sha] [event] [branch] [path]：一条工作流运行。
run() {
  jq -n --argjson id "$1" --arg status "$2" --argjson conclusion "$([ "$3" = null ] && echo null || echo "\"$3\"")" \
    --arg sha "${4:-$SHA}" --arg event "${5:-push}" --arg branch "${6:-main}" --arg path "${7:-.github/workflows/ci.yml}" \
    '{id:$id, name:"ci", path:$path, event:$event, head_branch:$branch, head_sha:$sha, status:$status, conclusion:$conclusion,
      html_url:("https://github.com/o/r/actions/runs/" + ($id|tostring))}'
}
runs() { jq -n --argjson runs "$(printf '%s\n' "$@" | jq -s '.')" '{total_count: ($runs|length), workflow_runs: $runs}'; }

# --push：推送到 main。在临时仓库里搭出各种历史形状，核对判定与原因。
# main 依次是：base，已同步的 PR 合并（M1），未同步的 PR 合并（M2），未同步的文档 PR 合并（M3），
# 直接推送（D1），代码树被改过的合并（M4），以及“与 main 内容相同的改动”的合并（M5）；每个合并之前 main 都先前进一步。
pgit() { git -C "$ptmp" -c user.name=ci -c user.email=ci@example.invalid -c commit.gpgsign=false "$@"; }
pcommit() { # <文件> <内容> <提交说明>
  mkdir -p "$ptmp/$(dirname "$1")"
  echo "$2" > "$ptmp/$1"
  pgit add -A && pgit commit -q -m "$3"
}
pgit init -q -b main
pcommit backend/internal/a.go base "base"
pcommit frontend/src/a.ts base "base-frontend"
B="$(pgit rev-parse HEAD)"

# 已同步的 PR：从 B 开分支改前端，main 之后前进（后端），PR 把 main 合并进来后再合并回 main。
pgit checkout -q -b pr-synced
pcommit frontend/src/s.ts s "pr-synced: frontend"
pgit checkout -q main
pcommit backend/internal/c1.go c1 "main moves on (backend)"
C1="$(pgit rev-parse HEAD)"
pgit checkout -q pr-synced
pgit merge -q --no-ff -m "sync main into pr-synced" main
pgit checkout -q main
pgit merge -q --no-ff -m "Merge pr-synced" pr-synced
M1="$(pgit rev-parse HEAD)"

# 未同步的 PR：从 M1 开分支改前端，main 之后前进（后端），PR 不同步直接合并。
pgit checkout -q -b pr-unsynced
pcommit frontend/src/u.ts u "pr-unsynced: frontend"
pgit checkout -q main
pcommit backend/internal/c2.go c2 "main moves on again (backend)"
C2="$(pgit rev-parse HEAD)"
pgit merge -q --no-ff -m "Merge pr-unsynced" pr-unsynced
M2="$(pgit rev-parse HEAD)"

# 未同步的文档 PR。
pgit checkout -q -b pr-docs "$M2"
pcommit docs/d.md d "pr-docs: docs"
pgit checkout -q main
pcommit backend/internal/c3.go c3 "main moves on a third time (backend)"
C3="$(pgit rev-parse HEAD)"
pgit merge -q --no-ff -m "Merge pr-docs" pr-docs
M3="$(pgit rev-parse HEAD)"

# 非合并提交直接推送到 main（前端）。
pcommit frontend/src/direct.ts direct "direct push to main"
D1="$(pgit rev-parse HEAD)"

# 已同步，但合并提交的代码树与 PR 头不同（手工改过的合并）。
pgit checkout -q -b pr-evil
pcommit frontend/src/e.ts e "pr-evil: frontend"
pgit checkout -q main
pgit merge -q --no-ff --no-commit pr-evil >/dev/null 2>&1
echo tampered > "$ptmp/backend/internal/tampered.go"
pgit add -A && pgit commit -q -m "Merge pr-evil (tree differs from PR head)"
M4="$(pgit rev-parse HEAD)"
E_BEFORE="$(pgit rev-parse HEAD^)"

# 第一个父提交不是 PR 头的祖先，但合并后的代码树恰好与 PR 头相同：main 上独立做了与 PR 里完全相同的一处改动。
# 这不属于“PR 已同步 main”，不能跳过。
pgit checkout -q -b pr-same
pcommit backend/internal/same.go same "pr-same: same.go"
pcommit frontend/src/same.ts same "pr-same: frontend"
pgit checkout -q main
pcommit backend/internal/same.go same "main: the identical same.go"
S_BEFORE="$(pgit rev-parse HEAD)"
pgit merge -q --no-ff -m "Merge pr-same" pr-same
M5="$(pgit rev-parse HEAD)"

# 与 main 分叉的提交，用来模拟强制推送后 before 不再是 after 的祖先。
pgit checkout -q -b diverged "$B"
pcommit docs/diverged.md x "diverged"
DIVERGED="$(pgit rev-parse HEAD)"
pgit checkout -q main

# 三个父提交的合并（章鱼合并）：第一个父提交是 PR 头的祖先、代码树也与 PR 头相同，但多出的父提交无法按同样的论证验证，不能跳过。
pgit checkout -q -b pr-octo
pcommit frontend/src/o.ts o "pr-octo: frontend"
pgit checkout -q main
O_BEFORE="$(pgit rev-parse HEAD)"
O_AFTER="$(pgit commit-tree "$(pgit rev-parse pr-octo^{tree})" -p "$O_BEFORE" -p pr-octo -p "$DIVERGED" -m "octopus merge")"
pgit merge -q --ff-only "$O_AFTER" >/dev/null 2>&1

# push_scope <before> <after> [父提交运行的 JSON]：输出 "frontend backend images docs_only verified_by_pr reason"。
# 不给 JSON 时，视为 before 在 main 上有一次已成功的 ci 运行；给出（含空字符串）时原样写入父提交运行文件。
push_scope() {
  local json
  if [ "$#" -ge 3 ]; then json="$3"; else json="$(runs "$(run 1 completed success "$1")")"; fi
  printf '%s' "$json" > "$pfile"
  (cd "$ptmp" && "$changes" --push "$1" "$2" "$pfile" 2>/dev/null | sed 's/^[a-z_]*=//' | tr '\n' ' ' | sed 's/ $//')
}
expect_push() { # <名称> <期望> <before> <after> [父提交运行的 JSON]
  local name="$1" expected="$2" actual
  actual="$(push_scope "$3" "$4" "${@:5}")"
  if [ "$actual" = "$expected" ]; then pass "--push $name"; else fail "--push ${name}：期望 [$expected]，实际 [$actual]"; fi
}

ZERO=0000000000000000000000000000000000000000
#                名称                                 期望：frontend backend images docs_only verified_by_pr reason                before  after
expect_push "已同步的合并：代码树与 PR 头相同，全部跳过"  "false false false false true verified-by-pr"     "$C1"  "$M1"
expect_push "未同步的合并：只按 PR 带来的改动运行"        "true false false false false diff"              "$C2"  "$M2"
expect_push "未同步的纯文档合并：不运行任何重型任务"      "false false false true false diff"              "$C3"  "$M3"
expect_push "非合并提交的直接推送"                        "true false false false false diff"              "$M3"  "$D1"
expect_push "同一次推送多个提交：按整个范围分类"          "true true false false false diff"               "$M1"  "$M3"
expect_push "合并之前还夹带了别的提交：不能整体跳过"      "true true false false false diff"               "$B"   "$M1"
expect_push "代码树被改过的合并：不跳过"                  "true true false false false diff"               "$E_BEFORE" "$M4"
expect_push "代码树恰好相同但 PR 未同步 main：不跳过"     "true false false false false diff"              "$S_BEFORE" "$M5"
expect_push "三个父提交的合并：不跳过"                    "true false false false false diff"              "$O_BEFORE" "$O_AFTER"
expect_push "before 全零（首次推送）"                     "true true true false false full-no-before"      "$ZERO" "$M1"
expect_push "before 为空"                                 "true true true false false full-no-before"      ""      "$M1"
expect_push "before 不是提交"                             "true true true false false full-before-unknown" "1111111111111111111111111111111111111111" "$M1"
expect_push "before 不是 after 的祖先"                    "true true true false false full-before-not-ancestor" "$DIVERGED" "$M1"
expect_push "before 与 after 相同"                        "true true true false false full-empty-range"    "$M1"  "$M1"
expect_push "after 不是提交"                              "true true true false false full-after-unknown"  "$M1"  "1111111111111111111111111111111111111111"

# 已同步的合并：额外核对四个标志确实都是 false，而不仅仅是原因文字。
printf '%s' "$(runs "$(run 1 completed success "$C1")")" > "$pfile"
out="$(cd "$ptmp" && "$changes" --push "$C1" "$M1" "$pfile" 2>/dev/null)"
if [ "$out" = "$(printf 'frontend=false\nbackend=false\nimages=false\ndocs_only=false\nverified_by_pr=true\nreason=verified-by-pr')" ]; then
  pass "--push 已验证时输出完整的 key=value 行"
else
  fail "--push 已验证时输出完整的 key=value 行：$out"
fi

# 父提交（before）必须在 main 上有已完成且成功的 ci 运行，否则无论能否跳过或缩小范围都全量运行（失败时关闭）。
#   C1→M1 是已同步的合并（本应跳过），C3→M3 是纯文档合并（本应不运行重型任务），D1 之前的 M3→D1 是直接推送（本应按范围运行）。
FULL="true true true false false full-parent-unverified"
expect_push "父提交的 ci 运行成功：已同步的合并仍然跳过"  "false false false false true verified-by-pr" "$C1" "$M1" "$(runs "$(run 1 completed success "$C1")")"
expect_push "父提交较旧的运行失败、较新的成功：视为成功"   "false false false false true verified-by-pr" "$C1" "$M1" "$(runs "$(run 1 completed failure "$C1")" "$(run 2 completed success "$C1")")"
expect_push "父提交的运行失败：已同步的合并也全量运行"     "$FULL" "$C1" "$M1" "$(runs "$(run 1 completed failure "$C1")")"
expect_push "父提交的运行失败：纯文档合并也全量运行"       "$FULL" "$C3" "$M3" "$(runs "$(run 1 completed failure "$C3")")"
expect_push "父提交的运行失败：直接推送也全量运行"         "$FULL" "$M3" "$D1" "$(runs "$(run 1 completed failure "$M3")")"
expect_push "父提交的运行被取消"                           "$FULL" "$C1" "$M1" "$(runs "$(run 1 completed cancelled "$C1")")"
expect_push "父提交的运行被跳过"                           "$FULL" "$C1" "$M1" "$(runs "$(run 1 completed skipped "$C1")")"
expect_push "父提交的运行仍在进行"                         "$FULL" "$C3" "$M3" "$(runs "$(run 1 in_progress null "$C3")")"
expect_push "父提交的运行在排队"                           "$FULL" "$C3" "$M3" "$(runs "$(run 1 queued null "$C3")")"
expect_push "父提交没有任何运行"                           "$FULL" "$C1" "$M1" "$(runs)"
expect_push "父提交只有别的提交的运行"                     "$FULL" "$C1" "$M1" "$(runs "$(run 1 completed success "$OTHER_SHA")")"
expect_push "父提交只有 pull_request 事件的运行"           "$FULL" "$C1" "$M1" "$(runs "$(run 1 completed success "$C1" pull_request)")"
expect_push "父提交只有非 main 分支的运行"                 "$FULL" "$C1" "$M1" "$(runs "$(run 1 completed success "$C1" push feature)")"
expect_push "父提交较新的运行失败、较旧的成功：不信任"     "$FULL" "$C1" "$M1" "$(runs "$(run 1 completed success "$C1")" "$(run 2 completed failure "$C1")")"
expect_push "查询出错：接口返回错误对象"                   "$FULL" "$C1" "$M1" '{"message":"Not Found"}'
expect_push "查询出错：输出为空"                           "$FULL" "$C3" "$M3" ''
expect_push "查询出错：输出不是 JSON"                      "$FULL" "$C1" "$M1" 'gh: HTTP 502'
# before 本身不成立时，原因优先报告 before 的问题，而不是父提交未验证。
expect_push "before 全零时不看父提交"                      "true true true false false full-no-before" "$ZERO" "$M1" '{"message":"Not Found"}'
# 运行文件根本不存在，也按未验证处理。
out="$(cd "$ptmp" && "$changes" --push "$C1" "$M1" "$ptmp/no-such-file.json" 2>/dev/null | sed 's/^[a-z_]*=//' | tr '\n' ' ' | sed 's/ $//')"
if [ "$out" = "$FULL" ]; then pass "--push 父提交运行文件不存在"; else fail "--push 父提交运行文件不存在：$out"; fi

# 参数个数与选项式参数。
status=0; (cd "$ptmp" && "$changes" --push "$M1" >/dev/null 2>&1) || status=$?
if [ "$status" = 2 ]; then pass "--push 缺少参数时退出码 2"; else fail "--push 缺少参数：退出码 $status"; fi
status=0; (cd "$ptmp" && "$changes" --push "$C1" "$M1" >/dev/null 2>&1) || status=$?
if [ "$status" = 2 ]; then pass "--push 缺少父提交运行文件时退出码 2"; else fail "--push 缺少父提交运行文件：退出码 $status"; fi
status=0; (cd "$ptmp" && "$changes" --push "--all" "$M1" "$pfile" >/dev/null 2>&1) || status=$?
if [ "$status" = 2 ]; then pass "--push 拒绝选项式参数"; else fail "--push 拒绝选项式参数：退出码 $status"; fi

# gate：只有“需要且成功”或“无需且被跳过”才通过。
expect_gate() {
  local name="$1" expected="$2" status=0
  shift 2
  "$gate" "$@" >/dev/null 2>&1 || status=$?
  if [ "$status" = "$expected" ]; then pass "gate：$name"; else fail "gate：${name}：期望退出码 ${expected}，实际 $status"; fi
}

expect_gate "全部需要且成功"                    0 changes=true:success frontend=true:success backend=true:success integration=true:success images=true:success
expect_gate "纯文档：其余全部跳过"              0 changes=true:success frontend=false:skipped backend=false:skipped integration=false:skipped images=false:skipped
expect_gate "仅前端：后端与集成测试跳过"        0 changes=true:success frontend=true:success backend=false:skipped integration=false:skipped images=false:skipped
expect_gate "需要的任务失败"                    1 changes=true:success frontend=true:failure backend=false:skipped
expect_gate "需要的任务被取消"                  1 changes=true:success frontend=true:cancelled backend=false:skipped
expect_gate "不需要的任务失败也不放过"          1 changes=true:success frontend=false:failure
expect_gate "需要的任务被连带跳过"              1 changes=true:success frontend=true:skipped
expect_gate "已由 PR 验证：全部跳过（--push 的结果）" 0 changes=true:success frontend=false:skipped backend=false:skipped integration=false:skipped images=false:skipped
expect_gate "changes 失败，即使输出显示全部无需运行" 1 changes=true:failure frontend=false:skipped backend=false:skipped integration=false:skipped images=false:skipped
expect_gate "changes 失败"                      1 changes=true:failure frontend=:skipped backend=:skipped
expect_gate "changes 被取消"                    1 changes=true:cancelled frontend=:skipped
expect_gate "输出为空视为失败"                  1 changes=true:success frontend=:success
expect_gate "没有参数"                          2

# ci-reuse-check.sh：发版复用与父提交校验。夹带的工作流运行 JSON 夹具定义在文件前部（--push 的测试也要用）。
# expect_reuse <名称> <期望退出码> <期望标准输出> <JSON>
expect_reuse() {
  local name="$1" expected="$2" want_out="$3" input="$4" status=0 out
  out="$(printf '%s' "$input" | "$reuse" "$SHA" 2>/dev/null)" || status=$?
  if [ "$status" = "$expected" ] && [ "$out" = "$want_out" ]; then
    pass "复用：$name"
  else
    fail "复用：${name}：期望退出码 $expected 输出 [$want_out]，实际 $status [$out]"
  fi
}

URL1=https://github.com/o/r/actions/runs/1
URL2=https://github.com/o/r/actions/runs/2
expect_reuse "main 上这个提交的 ci 运行成功"          0 "$URL1" "$(runs "$(run 1 completed success)")"
expect_reuse "没有任何运行"                            1 ""      "$(runs)"
expect_reuse "运行仍在进行"                            1 ""      "$(runs "$(run 1 in_progress null)")"
expect_reuse "排队中"                                  1 ""      "$(runs "$(run 1 queued null)")"
expect_reuse "重跑中：状态未完成，即使结论字段仍是 success" 1 ""   "$(runs "$(run 1 in_progress success)")"
expect_reuse "运行失败"                                1 ""      "$(runs "$(run 1 completed failure)")"
expect_reuse "运行被取消"                              1 ""      "$(runs "$(run 1 completed cancelled)")"
expect_reuse "运行被跳过"                              1 ""      "$(runs "$(run 1 completed skipped)")"
expect_reuse "已完成但结论为空"                        1 ""      "$(runs "$(run 1 completed null)")"
expect_reuse "提交号不同"                              1 ""      "$(runs "$(run 1 completed success "$OTHER_SHA")")"
expect_reuse "pull_request 事件的运行不算"             1 ""      "$(runs "$(run 1 completed success "$SHA" pull_request)")"
expect_reuse "非 main 分支的运行不算"                  1 ""      "$(runs "$(run 1 completed success "$SHA" push feature)")"
expect_reuse "其他工作流（release.yml）不算"           1 ""      "$(runs "$(run 1 completed success "$SHA" push main .github/workflows/release.yml)")"
expect_reuse "较新的运行失败，较旧的成功也不复用"       1 ""      "$(runs "$(run 1 completed success)" "$(run 2 completed failure)")"
expect_reuse "较新的运行仍在进行，较旧的成功也不复用"   1 ""      "$(runs "$(run 2 in_progress null)" "$(run 1 completed success)")"
expect_reuse "较旧的失败、较新的成功：复用较新的"       0 "$URL2" "$(runs "$(run 2 completed success)" "$(run 1 completed failure)")"
expect_reuse "混杂其他提交与事件的运行：只认匹配的"     0 "$URL2" "$(runs "$(run 3 completed failure "$OTHER_SHA")" "$(run 4 completed failure "$SHA" pull_request)" "$(run 2 completed success)")"
expect_reuse "缺少 workflow_runs 字段"                 2 ""      '{"message":"Not Found"}'
expect_reuse "输入不是 JSON"                           2 ""      'not json'
expect_reuse "空输入"                                  2 ""      ''

status=0; echo '{"workflow_runs":[]}' | "$reuse" >/dev/null 2>&1 || status=$?
if [ "$status" = 2 ]; then pass "复用：缺少提交号"; else fail "复用：缺少提交号：退出码 $status"; fi
status=0; echo '{"workflow_runs":[]}' | "$reuse" main >/dev/null 2>&1 || status=$?
if [ "$status" = 2 ]; then pass "复用：提交号必须是 40 位十六进制"; else fail "复用：提交号必须是 40 位十六进制：退出码 $status"; fi

if [ "$failures" -ne 0 ]; then
  echo "$failures 项失败。" >&2
  exit 1
fi
echo "全部通过。"
