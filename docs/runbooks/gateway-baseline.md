# 网关性能基线 Runbook

面向开发者与维护者。高性能网关 Epic（#360）的每个 Feature 合并前后，用同一套工具、同一批场景各测一遍，得到可比较的数字。结果依赖机器，不写入仓库；数字与性能目标的建议写在对应 Issue 或 PR 里。

## 它测什么

| 指标 | 含义 |
| --- | --- |
| 网关自增延迟 | 经过网关的首字时间减去直连模拟上游的首字时间，看 p50 与 p99。首字时间是客户端从发出请求到收到响应体第一个字节；非流式请求就是整个响应 |
| 最大可持续 QPS | 逐级提高请求速率，满足下列判据的最高一级 |
| CPU 与内存分配 | 每请求的进程 CPU 时间、分配字节数与次数，pprof 的 CPU 与分配热点 |
| 每请求数据库语句数 | 按 sqlc 查询名统计，含事务的 `begin` 与 `commit`；每条语句是一次数据库往返 |
| 结算锁持有时间 | 结算事务持有账本账户行锁（`platform_revenue` 是热行）的时间：从 `LockLedgerAccounts` 返回到提交返回，客户端口径；以及这段时间里每条语句的往返和语句之间客户端自己的时间 |
| 其他 | 数据库连接池等待、上游新建连接数、DNS 查询数、goroutine 与堆峰值、各语句的平均耗时 |

最大可持续 QPS 的判据（`gateway_perf_baseline_test.go` 中的常量）：同时满足

- 没有因达到在途上限而丢弃的到达；
- 失败（非 2xx、传输错误、流没有收完）不超过 0.1%；
- 至少 98% 的目标速率完成；
- 首字时间 p99 不超过上游自身首字时间加 250 ms。

## 组成

| 位置 | 作用 |
| --- | --- |
| `backend/internal/gatewaybench` | 四种格式的模拟上游（首字时间、块间隔、token 数、usage 帧可配）、压测程序（并发数与 QPS，开环或闭环）、测试 CA、数据库语句计数器与锁持有测量（`hold.go`）。有单元测试，随 `go test ./...` 运行 |
| `backend/internal/gateway/stream_bench_test.go`、`request_bench_test.go` | 不要网络和数据库的 Go 基准，用来单看一个函数的代价：`stream_bench_test.go` 把基线模拟上游的四种流（1000 token）依次送过旁路解析器、纯用量帧过滤器和整个 `streamBack` 写回路径；`request_bench_test.go` 比较请求体的读取、扫描与拼接和它们改进前的做法。运行：`go -C backend test ./internal/gateway -run '^$' -bench . -benchmem`；分配次数与字节数是确定的，耗时依赖机器 |
| `backend/internal/postgres/gateway_perf_*_test.go` | 带 build tag `perfbaseline` 的测试：同一进程里启动真实的网关引擎、API 处理器、PostgreSQL、模拟上游与压测程序，跑场景并输出报告。普通 `go test ./...` 与 CI 不编译它们（`mise run lint-backend` 会带上这个 tag 做静态检查，防止它悄悄编译不过） |
| `.github/workflows/perf-baseline.yml`、`scripts/perf-ab.sh` | 在 GitHub 托管的 Linux 机器上手动运行基线，让两份代码交替运行并出对比表，见下文“在 GitHub Actions 上运行”。不是必需检查，也不在 push 或 PR 上自动运行 |
| `backend/internal/gatewaybench/perfcompare` | 把 `results.json` 汇总成 Markdown 对比表（中位数、范围、相对变化）的命令，有单元测试，随 `go test ./...` 运行 |

### 出站限制没有被放宽

出站策略拒绝回环地址并要求 HTTPS，基线没有绕开它：

- 网关用的是生产的 `channel.OutboundPolicy`：同样的主机名规范化、DNS 校验与缓存、地址检查和钉住地址的连接池（G7 之后；之前是每次尝试新建传输层）。
- 解析器用 `NewOutboundPolicyWithResolver` 注入，对每个渠道主机名返回一个看起来是公网的地址。
- 策略的拨号函数与可信根证书（`dialContext`、`testRootCAs`，包内私有、本来只给 `channel` 包自己的测试用）由测试文件通过反射设置，把“钉住的公网地址”接到本机的模拟上游，并信任基线自己签发的测试 CA。字段改名时测试会明确报错。
- 基线不启用本地开发例外（见 ARCHITECTURE.md「安全边界」），仍验证生产默认的 HTTPS 与公网 DNS 路径。拨号函数替换与测试 CA 注入只存在于带 build tag 的测试文件，没有可跳过 TLS 校验或为任意域名放行受保护地址的运行时开关。

## 运行

先起数据库（项目名与端口按任务自取，避免与别的任务冲突）：

```sh
COMPOSE_PROJECT_NAME=gateway-baseline POSTGRES_PORT=55460 docker compose up -d --wait database
```

再运行基线。测试在库里建一个独立的 schema，结束后删除：

```sh
PGPASSWORD=oh-my-aihub-development \
TEST_DATABASE_URL="postgres://oh_my_aihub@127.0.0.1:55460/oh_my_aihub?sslmode=disable" \
go -C backend test -tags perfbaseline -run TestGatewayPerfBaseline -count=1 -timeout 90m -v \
  ./internal/postgres -args -perf.out=/tmp/gateway-baseline
```

结束后 `docker compose down -v`（同一个项目名）。

常用参数（都跟在 `-args` 后面）：

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `-perf.scenarios` | 全部 | `nonstream`、`stream`、`concurrent`、`saturate`、`budget`、`sticky`、`formats`、`sweep-nonstream`、`sweep-stream`，逗号分隔 |
| `-perf.duration` / `-perf.warmup` | 30s / 3s | 每次运行的测量窗口与预热（流式场景的预热至少是一条流时长的 1.5 倍） |
| `-perf.repeat` | 1 | 每个场景重复次数，报告给出中位数与范围。机器上有别的负载时建议 3 |
| `-perf.out` | 空 | 目录，写入 `report.md`、`results.json` 和 pprof 文件；设置后还会对 `nonstream` 与 `stream` 场景各做一次带 pprof 的运行（`-perf.profile`，默认 20s） |
| `-perf.consumers` / `-perf.channels` | 100 / 3 | 消费者账号数（各有两把 Key，其中一把带预算）与同价渠道数 |
| `-perf.qps` / `-perf.workers` | 场景自带 | 替换命名的速率驱动场景（`nonstream`、`stream`、`budget`、`sticky`）的参考速率，或闭环场景（`concurrent`、`saturate`）的 worker 数。参考速率不是越高越好：机器空闲时请求稀疏，每个请求都要唤醒沉睡的线程，网关自增延迟和单请求 CPU 都明显偏高，需要看较高负载下的数字时用 `-perf.qps=200` |
| `-perf.dns` | 0 | 假 DNS 解析器的延迟。G7 之前用来看每次尝试重新解析的代价；DNS 缓存之后每个主机每 60 秒才解析一次，它只影响刷新的那一次 |
| `-perf.synccommit` | on | 设为 `off` 时网关的数据库连接不等提交落盘，用来区分“存储慢”和“网关慢” |
| `-perf.sweepstep` | 10s | 扫描中每一级的测量窗口 |
| `-perf.quiet` | 0 | 大于 0 时，每次运行前等 1 分钟负载均值降到这个数以下（最多等 3 分钟）。机器上有别的任务时用，比如 `-perf.quiet=9` |

## 场景

模拟上游的首字时间与块间隔是确定的，所以延迟差只来自网关与数据库。

| 场景 | 内容 | 参考负载 |
| --- | --- | --- |
| `nonstream-small` | OpenAI Chat 非流式，上游 50 ms 后一次返回 64 个输出 token | 50 次/秒（开环） |
| `stream-1k` | OpenAI Chat 流式，首字 200 ms，每 5 ms 一块，共 1000 个 token（约 5 秒一条） | 20 次/秒 |
| `concurrent-500` | OpenAI Chat 流式，首字 100 ms，每 20 ms 一块，共 100 个 token | 500 个 worker 背靠背（闭环） |
| `nonstream-saturated` | 同 `nonstream-small`，但 worker 数多于数据库连接 | 64 个 worker 背靠背（闭环）：看吞吐上限本身，即结算在饱和时每秒能处理多少个调用；扫描找的是满足延迟判据的最高速率，对存储的偶发停顿很敏感（同一代码两轮可以差一倍），这个数字稳定得多 |
| `key-budget` | 同 `nonstream-small`，Key 带日、月、总预算 | 50 次/秒 |
| `responses-sticky` | OpenAI Responses 非流式，请求带上一轮的 `previous_response_id` | 50 次/秒 |
| `formats` | 四种格式的流式与非流式各一个场景 | 非流式 40 次/秒，流式 10 次/秒 |
| `sweep-*` | 对 `nonstream-small`、`stream-1k` 逐级提高速率，再在最后一个成功级与第一个失败级之间二分两次 | 见报告 |

每个场景先跑直连模拟上游（压测程序到上游，TLS，连接复用），再跑经过网关（压测程序到网关，明文 HTTP，连接复用）；两次的负载与参数相同。随后做一次数据库语句审计：用同形状的短回答跑几百个请求，缓存已预热，按查询名统计每请求语句数。

测试自己照顾的几件事，免得它们变成测量噪音：

- 建好数据后先经网关写入 2000 个调用再 `ANALYZE`，之后每次运行前再 `ANALYZE`，相当于有 autovacuum 的生产库；否则刚建的空表会让账本触发器走全表扫描。
- 每次运行前等所有渠道离开冷却。网关过载时本地超时会被记成渠道失败而冷却渠道，下一次运行会遇到“没有可用渠道”的 503，测到的是冷却而不是负载。
- 闭环负载（并发场景）的 worker 在一条回答的时间内错开启动；一起启动、回答又一样长的 worker 会成批到达，成批结算，量到的是人为的排队。

模拟上游贴着真实 API 的形状：OpenAI Chat 流式只在请求带 `stream_options.include_usage` 时才发纯用量帧（网关替客户端打开它，所以会经过纯用量帧过滤）、每块带 `"usage":null`；Anthropic 的 `message_start` 带输入、`message_delta` 带输出；Gemini 每块重复累计用量；Responses 的最后一个事件重复整段文本。

## 在 GitHub Actions 上运行

维护者的笔记本不安静（别的开发任务、内存换页、热压力、休眠、换网络），本机测出的延迟、CPU 与 QPS 可信度低。公开仓库用 GitHub 托管的标准 Linux x64 机器（`ubuntu-24.04`，4 vCPU、16 GB，与生产 VPS 同架构同核数同内存）不限时长，所以延迟、CPU 与 QPS 的前后对比放到那里测。工作流是 `.github/workflows/perf-baseline.yml`，只手动触发。

### 日常 PR 选择最小相关场景

完整矩阵用于较低频的全路径检查与大范围网关改动，不作为每次迭代的默认动作。普通 PR 先用相关回归测试或微基准守住 SQL、分配和持锁尾部成本，再为受影响的端到端路径选择最小场景集，仍保留 3 轮相对比较。已有完整矩阵发现特定退化时，优化后的复测先只选该场景；结果显示其他问题或改动扩大时才扩展场景。对外部网络、后台任务与高负载未覆盖的部分，在 PR 明确说明。

例如只复测 Responses 粘性路由，可使用：

```sh
gh workflow run perf-baseline.yml \
  -f base_ref=<基线提交> -f head_ref=<任务分支> \
  -f scenarios=sticky -f repeat=3 -f jobs=1 \
  -f 'tool_args=-perf.duration=10s -perf.warmup=1s -perf.profile=1s -perf.audit=50'
```

短 A/B 以约 3–5 分钟反馈为目标，排队、工具链编译与机器负载可能延长；它不能替代持续吞吐扫描或完整的容量结论。`perf.duration` 只是单次采样窗口：每个场景还会分别跑直连与经过网关、预热、等待在途请求结束与 SQL 审计，部分场景另外执行 profiling，再乘 base/head 与轮数。流式预热还有按单条回答时长计算的下限，因此仅缩短 duration 或 profile 不能让完整矩阵同等缩短。需要长流、并发或容量验证时显式选择对应场景，不为节省时间缩成无法覆盖改动的样本。

### 触发

```sh
gh workflow run perf-baseline.yml -f base_ref=9d551df -f head_ref=main
gh run watch    # 选择刚触发的运行；默认参数一个作业要数小时（作业上限 300 分钟）
```

工作流文件必须先在默认分支上才能手动触发。输入：

| 输入 | 默认 | 说明 |
| --- | --- | --- |
| `base_ref` / `head_ref` | `main` / `main` | A 侧与 B 侧：分支、tag 或提交（短 SHA 可用）。工作流先把它们解析成提交，所有并行作业测同一对提交 |
| `scenarios` | `nonstream,nonstream@200,stream,concurrent,budget,sticky,sweep-nonstream,sweep-stream` | 语法同 `-perf.scenarios`；`<场景>@<速率>` 表示以 `-perf.qps=<速率>` 单独运行（`nonstream@200` 是 200 次/秒的非流式），场景限 `nonstream`、`stream`、`budget`、`sticky` |
| `repeat` | 3 | 轮数：每轮把每个场景在两侧各测一次 |
| `jobs` | 1 | 并行作业数，每个作业在自己的机器上完整跑一遍；用来量同一对提交在不同机器上的波动，不用来加速 |
| `tool_args` | 空 | 传给基线工具的额外参数，只接受 `-perf.<名称>=<值>`，如 `-perf.duration=10s -perf.sweepstep=5s` |

### 一个作业里做什么

- 检出 `base_ref` 与 `head_ref` 两份代码，各自编译带 `-tags perfbaseline` 的测试二进制；PostgreSQL 18 是服务容器，参数同 `ci.yml` 的 `integration` 作业。两边用同一个数据库实例，基线工具每次运行都在库里新建 schema、建数据、结束后删除。
- 运行分阶段：`default`（不带 `@` 的场景）、`qps<N>`（带 `@N` 的场景）、`sweep`（QPS 扫描，耗时最长，单独成阶段）。每一轮里按阶段依次进行，每个阶段先后跑 base 与 head 各一次；奇数轮 base 先，偶数轮 head 先。同一场景的两侧运行相隔不过十几分钟，机器随时间的漂移被抵消，三轮得到三个样本。细节与参数见 `scripts/perf-ab.sh` 开头的注释。
- 记录环境：`lscpu`（CPU 型号）、`nproc`、内存、内核、PostgreSQL 版本与关键设置；每次运行的数据库往返与提交延迟（工具报告里，开始与结束各一次）、开始时的负载均值，以及运行期间整机 CPU 忙碌与被宿主机偷走（steal）的比例。
- 某次运行失败不会中断后面的运行，作业最后失败，摘要里仍列出已有的结果。

### 读结果

- 作业摘要（运行页面的 Summary）：先是环境，再是“Base against head”表，每个场景的每个指标给出 base 与 head 的中位数 [最小–最大]、相对变化，以及两侧范围是否重叠；重叠表示变化在这台机器的噪声之内。“direct ttft p99”是不经网关、直连模拟上游的首字时间，反映当时机器有多吵。“lock queue”是等账本行锁的时间（`LockLedgerAccounts` 语句的均值）；“lock hold”是结算持有这把锁的时间，只有带锁持有测量的基线工具（G8.7 之后）才写出，另一侧是旧工具时显示“–”。之后是“Runs”表：每次运行的数据库往返与提交延迟、负载和 CPU steal，据此判断哪几次运行不可信。
- `jobs` 大于 1 时多一个“Spread between jobs”摘要：各作业的 base / head 中位数并排，并给出各指标在作业之间的离散度（最高与最低的差除以中位数），以及每个作业的 CPU 型号。
- artifact `perf-baseline-job-<n>`（保留 30 天）：`<side>/r<轮>/<阶段>/` 下是每次运行的 `report.md`、`results.json`、pprof（`nonstream` 与 `stream` 各有一次带 pprof 的运行）、完整日志 `test.log` 与 `meta.txt`；`environment.txt`、`cpu.txt`、`refs.txt` 与 `comparison.md`（摘要原文）在根目录。pprof 的看法同下文。

### 可信度

- 同一作业内的 A/B 对比可靠：两侧在同一台虚拟机、同一个数据库、交替先后上运行。仍要看范围是否重叠：中位数相差而范围重叠的变化不能当作改进。
- 不同作业（不同机器、不同时间）的绝对值有波动，CPU 型号不同时更大；不要拿两次工作流运行的绝对值直接比较，要比较就用 `jobs` 大于 1 的运行，或看“同一提交在两次运行里的差”来估计这个波动。
- 虚拟机的宿主机上可能有别的租户：“Runs”表里 CPU steal 明显高于其余运行的那几次应当剔除，或重跑。
- 计数类指标（每请求数据库语句、分配次数、新建连接与 DNS 查询）不受机器影响，仍可直接比较。
- 服务容器里的 PostgreSQL 用默认设置，数据目录在运行器的磁盘上；提交延迟与笔记本、生产 VPS 都不同，绝对值只作数量级参考，这与“局限”里的说明一致。

### 本机试运行计划

`PERF_DRY_RUN=1 PERF_ROUNDS=2 scripts/perf-ab.sh a b out` 只打印运行计划，不编译也不运行；汇总命令：`go -C backend run ./internal/gatewaybench/perfcompare <作业目录>`（多个作业目录并排）。

## 看结果

`report.md` 依次是：环境（含数据库的往返与提交延迟——这两个数差别很大时，两次结果不可比）、各场景的延迟对比、每请求成本、每请求数据库语句、结算锁持有（均值、p50、p99，以及一次持有由哪些语句的往返、语句之间客户端的时间和提交组成）、扫描表、数据库表读取情况。扫描表里的“lock hold ms”是每一级的锁持有均值，与同一行的 `LockLedgerAccounts ms`（排队等锁）对照看：持有越长，后来者排得越久。`results.json` 是同样内容的机器可读形式。

pprof 文件在 `-perf.out` 目录：

```sh
# CPU：按组件标签拆分（gateway / upstream / client），再看网关那部分
go -C backend tool pprof -tags /tmp/gateway-baseline/nonstream-small.cpu.pprof
go -C backend tool pprof -top -cum -nodecount=40 -tagfocus=component=gateway /tmp/gateway-baseline/nonstream-small.cpu.pprof

# 内存分配：用运行前的快照作基线，只看这次运行
go -C backend tool pprof -sample_index=alloc_space -top -cum -nodecount=40 \
  -base /tmp/gateway-baseline/nonstream-small.allocs.before.pprof \
  -focus='internal/(api|gateway)\.' /tmp/gateway-baseline/nonstream-small.allocs.after.pprof
```

模拟上游与压测程序跑在同一进程里，所以 CPU 剖析用 `component` 标签区分；GC 等运行时 goroutine 没有标签，算在“未标记”里。分配剖析没有标签，用 `-focus` 限定到网关的函数。

## 前后对比

- 同一台机器、同一份代码之外的东西不变；跑之前看一眼负载，关掉别的重负载。报告的延迟表里有每次运行开始时的 1 分钟负载均值，别的任务突然占满机器的那几次一眼能看出来；用 `-perf.quiet` 可以让测试自己等。
- 每个 Feature 合并前（基于合并前的 `main`）和合并后各跑一遍，参数一致，用 `-perf.repeat 3`，比较中位数与范围。
- 报告里数据库的往返与提交延迟差别大（比如换了机器、Docker 状态不同）时，不要直接比。
- 要和上一轮基线比较时，场景、参数、`-perf.consumers`、`-perf.channels` 都沿用，场景参数改动要在 PR 里写明。

## 局限

- 网络是回环：到上游没有往返延迟，所以连接池与 DNS 缓存（G7）省下的往返时间在这里只表现为 CPU、握手次数和 DNS 查询次数。DNS 缓存生效后每个主机每 60 秒才解析一次，`-perf.dns` 只影响刷新的那一次；要看每次尝试重新解析的代价，对照 G7 之前的 `main`。模拟上游同时提供 HTTP/2 与 HTTP/1.1，G7 之后网关与它之间走 HTTP/2。
- 数据库在本机 Docker 里，往返与提交落盘的速度和生产不同；结果适合同一台机器上做前后对比，绝对值只作数量级参考。
- 数据库表从空开始，每次运行前都会 `ANALYZE`（相当于有 autovacuum 的生产库），不模拟已有几百万条调用的大表。
- 压测程序、模拟上游与网关在同一进程、同一批核上，CPU 不是网关独占；用 pprof 的 `component` 标签区分。
- 只覆盖网关热路径：不含管理页面的查询、观测页、后台对账、指标刷新。
- 网关像服务器一样从进程内快照读取 Key、目录、渠道候选与路由偏好，从进程内缓存读取账号的余额与信用额度（ADR-0040）：搭建数据时每次后台操作都会重建快照、使余额条目失效，测量期间只有每 60 秒的兜底重建与重新加载，它们的查询按请求之外的工作计数；每个账号的余额在第一个请求时读一次库，预热阶段已经做完。
