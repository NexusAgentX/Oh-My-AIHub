#!/usr/bin/env bash
# CI 汇总判定：ci.yml 的必需检查 gates 用它确认所有应当运行的任务都成功了。
#
# 用法：scripts/ci-gate.sh <任务>=<是否需要>:<needs.<任务>.result> ...
#   是否需要必须是 true 或 false（来自 changes 任务的判定输出）。
#   需要运行（true）  → 结果必须是 success。
#   无需运行（false） → 结果必须是 skipped（或 success）。
#   其他任何组合都失败：failure、cancelled、需要运行却被跳过（例如上游失败导致连带跳过）、
#   以及空值或无法识别的取值（例如 changes 失败时拿不到输出）。
# 被 if 跳过的任务在分支保护中按成功处理，所以不能只依赖各任务自己的状态：这里把“跳过”与“确实不需要”对上号。
set -euo pipefail

[ "$#" -gt 0 ] || { echo "用法：$0 <任务>=<是否需要>:<结果> ..." >&2; exit 2; }

failures=0
for arg in "$@"; do
  job="${arg%%=*}"
  rest="${arg#*=}"
  needed="${rest%%:*}"
  result="${rest#*:}"
  case "$needed:$result" in
    true:success | false:skipped | false:success)
      echo "ok    $job needed=$needed result=$result"
      ;;
    *)
      echo "::error::$job 未通过：needed=$needed result=$result"
      failures=$((failures + 1))
      ;;
  esac
done

if [ "$failures" -ne 0 ]; then
  echo "$failures 个任务未通过，gates 失败。" >&2
  exit 1
fi
echo "所有需要运行的任务均已成功。"
