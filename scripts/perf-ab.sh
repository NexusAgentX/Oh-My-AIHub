#!/usr/bin/env bash
# 网关性能基线的 A/B 运行：在同一台机器、同一个数据库上，让 base 与 head 两份代码的基线工具交替运行。
# 由 .github/workflows/perf-baseline.yml 调用；方法与指标见 docs/runbooks/gateway-baseline.md。
#
# 用法：scripts/perf-ab.sh <base 代码树> <head 代码树> <输出目录>
#   代码树是仓库的检出（需含 backend/internal/postgres/gateway_perf_baseline_test.go，即 #362 之后的提交）。
#   需要 PATH 里有 go（CI 里用 mise exec -- 调用），并设置 TEST_DATABASE_URL。
#
# 环境变量：
#   PERF_SCENARIOS  逗号分隔，默认与 G8.0 一致。取值同基线工具的 -perf.scenarios：
#                   nonstream stream concurrent saturate budget sticky formats sweep-nonstream sweep-stream；
#                   另可写 <场景>@<速率>（场景限 nonstream stream budget sticky），以 -perf.qps=<速率> 单独运行，
#                   如 nonstream@200 就是 200 次/秒的非流式。
#   PERF_ROUNDS     轮数，默认 3。每轮把每个场景在 base 与 head 上各测一次；奇数轮 base 先、偶数轮 head 先，
#                   两边交替，抵消机器随时间的漂移。报告里的中位数与范围就是对这些轮取的。
#   PERF_TOOL_ARGS  传给基线工具的额外参数，只接受 -perf.<名称>=<值>，如 "-perf.duration=10s -perf.sweepstep=5s"。
#   PERF_DRY_RUN    非空时只打印计划，不编译也不运行。
#
# 运行分成几个阶段，每个阶段是基线工具的一次调用（各自建立数据，数据库里的 schema 用完即删）：
#   default   不带 @ 的场景（sweep-* 除外）
#   qps<N>    带 @N 的场景
#   sweep     sweep-* 场景（最大可持续 QPS 扫描，耗时长，单独成阶段）
# 一轮里先做完一个阶段的 base 与 head 两次运行，再进入下一阶段，所以同一场景的两边运行相隔不过十几分钟。
#
# 输出：<输出目录>/<side>/r<轮>/<阶段>/ 下是基线工具的 results.json、report.md、pprof 文件、
# 完整日志 test.log，以及 meta.txt（起止时间、退出码、运行期间整机 CPU 忙碌与被宿主机偷走的比例）。
# 某次运行失败不会中断后面的运行，最后以非零状态退出。汇总用 backend/internal/gatewaybench/perfcompare。
set -euo pipefail

die() {
  echo "perf-ab: $*" >&2
  exit 2
}

[ "$#" -eq 3 ] || die "用法：$0 <base 代码树> <head 代码树> <输出目录>"
base_tree="$1"
head_tree="$2"
out_dir="$3"
# 基线工具在包目录里运行，输出目录必须是绝对路径。
case "$out_dir" in /*) ;; *) out_dir="$PWD/$out_dir" ;; esac

scenarios="${PERF_SCENARIOS:-nonstream,nonstream@200,stream,concurrent,budget,sticky,sweep-nonstream,sweep-stream}"
rounds="${PERF_ROUNDS:-3}"
tool_args="${PERF_TOOL_ARGS:-}"
dry_run="${PERF_DRY_RUN:-}"

[[ "$rounds" =~ ^[1-9][0-9]?$ ]] || die "PERF_ROUNDS 必须是 1 到 99 的整数：$rounds"
extra=()
for word in $tool_args; do
  [[ "$word" =~ ^-perf\.[a-z]+=[A-Za-z0-9.]+$ ]] || die "PERF_TOOL_ARGS 只接受 -perf.<名称>=<值>：$word"
  extra+=("$word")
done

# ---- 把场景分成阶段 ----
IFS=',' read -r -a tokens <<<"$scenarios"
plain=()
sweeps=()
rates=()
for token in "${tokens[@]}"; do
  case "$token" in
    sweep-nonstream | sweep-stream) sweeps+=("$token") ;;
    nonstream | stream | concurrent | saturate | budget | sticky | formats) plain+=("$token") ;;
    nonstream@* | stream@* | budget@* | sticky@*)
      rate="${token#*@}"
      [[ "$rate" =~ ^[1-9][0-9]*$ ]] || die "速率必须是正整数：$token"
      case " ${rates[*]-} " in
        *" $rate "*) ;;
        *) rates+=("$rate") ;;
      esac
      ;;
    *) die "未知的场景：$token" ;;
  esac
done

phase_name=()
phase_list=()
phase_rate=()
join() {
  local IFS=,
  echo "$*"
}
add_phase() {
  phase_name+=("$1")
  phase_list+=("$2")
  phase_rate+=("$3")
}
if [ "${#plain[@]}" -gt 0 ]; then
  add_phase default "$(join "${plain[@]}")" ""
fi
if [ "${#rates[@]}" -gt 0 ]; then
  for rate in "${rates[@]}"; do
    list=()
    for token in "${tokens[@]}"; do
      case "$token" in
        *@"$rate") list+=("${token%@*}") ;;
      esac
    done
    add_phase "qps$rate" "$(join "${list[@]}")" "$rate"
  done
fi
if [ "${#sweeps[@]}" -gt 0 ]; then
  add_phase sweep "$(join "${sweeps[@]}")" ""
fi
[ "${#phase_name[@]}" -gt 0 ] || die "没有要运行的场景"

group() { if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::group::$*"; else echo "== $*"; fi; }
endgroup() { if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::endgroup::"; fi; }

echo "场景：$scenarios；$rounds 轮；阶段："
for i in "${!phase_name[@]}"; do
  echo "  ${phase_name[$i]}: ${phase_list[$i]}${phase_rate[$i]:+ (-perf.qps=${phase_rate[$i]})}"
done
echo "轮次顺序：奇数轮 base→head，偶数轮 head→base；每个阶段内两边相邻运行"

# ---- 准备 ----
bin_dir=""
if [ -z "$dry_run" ]; then
  : "${TEST_DATABASE_URL:?TEST_DATABASE_URL is required}"
  for tree in "$base_tree" "$head_tree"; do
    [ -f "$tree/backend/internal/postgres/gateway_perf_baseline_test.go" ] \
      || die "$tree 里没有基线工具（需要 #362 之后的提交）"
  done
  bin_dir="$(mktemp -d)"
  trap 'rm -rf "$bin_dir"' EXIT
  mkdir -p "$out_dir"
  for side in base head; do
    tree="$base_tree"
    [ "$side" = base ] || tree="$head_tree"
    group "编译 $side 的基线工具"
    go -C "$tree/backend" test -c -tags perfbaseline -o "$bin_dir/$side.test" ./internal/postgres
    endgroup
  done
fi

# 整机 CPU 计数：总量、空闲（含 iowait）、被宿主机偷走的时间。只有 Linux 有 /proc/stat。
cpu_sample() {
  [ -r /proc/stat ] || return 0
  awk '/^cpu /{ total = 0; for (i = 2; i <= 9; i++) total += $i; print total, $5 + $6, $9; exit }' /proc/stat
}

cpu_percent() {
  [ -n "$1" ] && [ -n "$2" ] || return 0
  awk -v a="$1" -v b="$2" 'BEGIN {
    split(a, x, " "); split(b, y, " "); total = y[1] - x[1]
    if (total <= 0) exit
    printf "cpu_busy_percent=%.2f\ncpu_steal_percent=%.2f\n", 100 * (1 - (y[2] - x[2]) / total), 100 * (y[3] - x[3]) / total
  }'
}

failed=0

# run_pass <side> <轮> <阶段序号>：基线工具的一次调用。
run_pass() {
  local side="$1" round="$2" index="$3"
  local name="${phase_name[$index]}" list="${phase_list[$index]}" rate="${phase_rate[$index]}"
  local tree="$base_tree"
  [ "$side" = base ] || tree="$head_tree"
  local dir="$out_dir/$side/r$round/$name"

  if [ -n "$dry_run" ]; then
    echo "[dry run] $side r$round $name: -perf.scenarios=$list${rate:+ -perf.qps=$rate} ${extra[*]-}"
    return 0
  fi

  mkdir -p "$dir"
  local args=(${extra[@]+"${extra[@]}"} -perf.scenarios="$list" -perf.repeat=1 -perf.out="$dir")
  if [ -n "$rate" ]; then
    args+=(-perf.qps="$rate")
  fi
  local before after started code=0
  started="$(date +%s)"
  before="$(cpu_sample)"
  group "$side，第 $round 轮，阶段 $name：$list${rate:+ @ $rate/s}"
  # 测试在包目录里运行，与 go test 一致。
  (cd "$tree/backend/internal/postgres" \
    && "$bin_dir/$side.test" -test.run '^TestGatewayPerfBaseline$' -test.count=1 -test.timeout=90m -test.v "${args[@]}") \
    2>&1 | tee "$dir/test.log" || code=$?
  endgroup
  after="$(cpu_sample)"
  if [ "$code" -eq 0 ] && [ ! -s "$dir/results.json" ]; then
    echo "perf-ab: $side 第 $round 轮阶段 $name 没有留下 results.json" >&2
    code=1
  fi
  {
    echo "started=$started"
    echo "seconds=$(($(date +%s) - started))"
    echo "exit=$code"
    cpu_percent "$before" "$after"
  } >"$dir/meta.txt"
  if [ "$code" -ne 0 ]; then
    echo "perf-ab: $side 第 $round 轮阶段 $name 失败（退出码 $code）；继续后面的运行" >&2
    failed=1
  fi
}

for ((round = 1; round <= rounds; round++)); do
  if ((round % 2 == 1)); then order="base head"; else order="head base"; fi
  for i in "${!phase_name[@]}"; do
    for side in $order; do
      run_pass "$side" "$round" "$i"
    done
  done
done

exit "$failed"
