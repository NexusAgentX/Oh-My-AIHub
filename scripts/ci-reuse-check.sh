#!/usr/bin/env bash
# 发版复用判定：release.yml 的 gates 用它确认标签提交在 main 上已有成功的 ci 运行，成功则跳过重复的 check-release。
#
# 用法：
#   gh api "repos/<owner>/<repo>/actions/workflows/ci.yml/runs?head_sha=<sha>&event=push&branch=main&per_page=100" \
#     | scripts/ci-reuse-check.sh <sha>
#   标准输入是 GitHub “列出工作流运行”接口的 JSON；<sha> 是标签指向的完整提交号。
# 退出码与输出：
#   0  可以复用：标准输出是被复用的 ci 运行的网址。
#   1  不可复用（没有这次提交的运行、还在进行、失败、取消……）：原因写到标准错误，调用方应改为运行 check-release。
#   2  参数或输入有误（含缺少 jq）：同样应改为运行 check-release。
#
# 判定规则：只看 ci.yml 在 main 上、由 push 事件为这个提交触发的运行，取其中最新的一次，
# 只有它已完成且结论为 success 才复用。查询参数只是预过滤，这里再核对一遍，不依赖接口一定按参数过滤：
#   - 用工作流运行而不是检查运行（check-runs）：检查运行不带事件与分支，分辨不出这个提交上的 gates 是
#     main 上的 push 运行，还是 PR 头上对合并引用跑出来的结果（PR 未同步 main 时，后者不是这个提交的代码树）。
#   - 工作流运行 success 等价于其 gates 成功：gates 带 if: always() 并依赖所有任务，任何需要运行的任务
#     失败、取消或被连带跳过都会使 gates 失败，进而使整个运行不是 success。
#   - 取“最新一次”：较新的运行失败或仍在进行时，不能拿较旧的成功结果来复用。
# check-release 的各项（前端 lint、测试、构建，后端 vet、staticcheck、race 测试、Compose 配置）都包含在 ci.yml
# 的 frontend、backend、images 之内，所以 ci 成功的提交不需要再跑一遍。
# 本地验证：scripts/test-ci.sh
set -euo pipefail

usage() {
  echo "用法：<workflow runs 的 JSON> | $0 <完整提交号>" >&2
  exit 2
}

sha="${1:-}"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || usage
[ "$#" -eq 1 ] || usage
command -v jq >/dev/null 2>&1 || { echo "缺少 jq" >&2; exit 2; }

input="$(cat)"
if ! printf '%s' "$input" | jq -e '(.workflow_runs | type) == "array"' >/dev/null 2>&1; then
  echo "标准输入不是 workflow runs 响应" >&2
  exit 2
fi

latest="$(
  printf '%s' "$input" | jq -c --arg sha "$sha" '
    [.workflow_runs[]
      | select(.path == ".github/workflows/ci.yml"
               and .event == "push"
               and .head_branch == "main"
               and .head_sha == $sha)]
    | max_by(.id) // empty'
)"
if [ -z "$latest" ]; then
  echo "main 上没有这个提交的 ci 运行" >&2
  exit 1
fi

status="$(printf '%s' "$latest" | jq -r '.status // ""')"
conclusion="$(printf '%s' "$latest" | jq -r '.conclusion // ""')"
url="$(printf '%s' "$latest" | jq -r '.html_url // ""')"
if [ "$status" != completed ] || [ "$conclusion" != success ]; then
  echo "最新的 ci 运行不是成功状态：status=$status conclusion=$conclusion $url" >&2
  exit 1
fi
if [[ ! "$url" =~ ^https://[^[:space:]]+$ ]]; then
  echo "ci 运行缺少有效网址：$url" >&2
  exit 1
fi
echo "$url"
