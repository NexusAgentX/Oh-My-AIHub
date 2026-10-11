# 网关性能基线 Runbook

面向开发者与维护者。高性能网关 Epic（#360）的每个 Feature 合并前后，用同一套工具、同一批场景各测一遍，得到可比较的数字。结果依赖机器，不写入仓库；数字与性能目标的建议写在对应 Issue 或 PR 里。

## 它测什么

| 指标 | 含义 |
| --- | --- |
| 网关自增延迟 | 经过网关的首字时间减去直连模拟上游的首字时间，看 p50 与 p99。首字时间是客户端从发出请求到收到响应体第一个字节；非流式请求就是整个响应 |
| 最大可持续 QPS | 逐级提高请求速率，满足下列判据的最高一级 |
| CPU 与内存分配 | 每请求的进程 CPU 时间、分配字节数与次数，pprof 的 CPU 与分配热点 |
| 每请求数据库语句数 | 按 sqlc 查询名统计，含事务的 `begin` 与 `commit`；每条语句是一次数据库往返 |
| 其他 | 数据库连接池等待、上游新建连接数、DNS 查询数、goroutine 与堆峰值、各语句的平均耗时 |

最大可持续 QPS 的判据（`gateway_perf_baseline_test.go` 中的常量）：同时满足

- 没有因达到在途上限而丢弃的到达；
- 失败（非 2xx、传输错误、流没有收完）不超过 0.1%；
- 至少 98% 的目标速率完成；
- 首字时间 p99 不超过上游自身首字时间加 250 ms。

## 组成

| 位置 | 作用 |
| --- | --- |
| `backend/internal/gatewaybench` | 四种格式的模拟上游（首字时间、块间隔、token 数、usage 帧可配）、压测程序（并发数与 QPS，开环或闭环）、测试 CA、数据库语句计数器。有单元测试，随 `go test ./...` 运行 |
| `backend/internal/postgres/gateway_perf_*_test.go` | 带 build tag `perfbaseline` 的测试：同一进程里启动真实的网关引擎、API 处理器、PostgreSQL、模拟上游与压测程序，跑场景并输出报告。普通 `go test ./...` 与 CI 不编译它们（`mise run lint-backend` 会带上这个 tag 做静态检查，防止它悄悄编译不过） |

### 出站限制没有被放宽

出站策略拒绝回环地址并要求 HTTPS，基线没有绕开它：

- 网关用的是生产的 `channel.OutboundPolicy`：同样的主机名规范化、DNS 校验、地址检查和每次尝试的传输层。
- 解析器用 `NewOutboundPolicyWithResolver` 注入，对每个渠道主机名返回一个看起来是公网的地址。
- 策略的拨号函数与可信根证书（`dialContext`、`testRootCAs`，包内私有、本来只给 `channel` 包自己的测试用）由测试文件通过反射设置，把“钉住的公网地址”接到本机的模拟上游，并信任基线自己签发的测试 CA。字段改名时测试会明确报错。
- 产品里没有任何开关、设置或环境变量能做同样的事；这些代码只存在于带 build tag 的测试文件，不进入服务器二进制。

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
| `-perf.scenarios` | 全部 | `nonstream`、`stream`、`concurrent`、`budget`、`sticky`、`formats`、`sweep-nonstream`、`sweep-stream`，逗号分隔 |
| `-perf.duration` / `-perf.warmup` | 30s / 3s | 每次运行的测量窗口与预热（流式场景的预热至少是一条流时长的 1.5 倍） |
| `-perf.repeat` | 1 | 每个场景重复次数，报告给出中位数与范围。机器上有别的负载时建议 3 |
| `-perf.out` | 空 | 目录，写入 `report.md`、`results.json` 和 pprof 文件；设置后还会对 `nonstream` 与 `stream` 场景各做一次带 pprof 的运行（`-perf.profile`，默认 20s） |
| `-perf.consumers` / `-perf.channels` | 100 / 3 | 消费者账号数（各有两把 Key，其中一把带预算）与同价渠道数 |
| `-perf.qps` / `-perf.workers` | 场景自带 | 替换命名的速率驱动场景（`nonstream`、`stream`、`budget`、`sticky`）的参考速率，或并发场景的 500 个 worker。参考速率不是越高越好：机器空闲时请求稀疏，每个请求都要唤醒沉睡的线程，网关自增延迟和单请求 CPU 都明显偏高，需要看较高负载下的数字时用 `-perf.qps=200` |
| `-perf.dns` | 0 | 假 DNS 解析器的延迟，用来看每次尝试重新解析的代价 |
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

## 看结果

`report.md` 依次是：环境（含数据库的往返与提交延迟——这两个数差别很大时，两次结果不可比）、各场景的延迟对比、每请求成本、每请求数据库语句、扫描表、数据库表读取情况。`results.json` 是同样内容的机器可读形式。

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

- 网络是回环：到上游没有往返延迟，所以连接池与 DNS 缓存（G7）省下的往返时间在这里只表现为 CPU、握手次数和 DNS 查询次数；用 `-perf.dns` 可以看 DNS 延迟的线性影响。
- 数据库在本机 Docker 里，往返与提交落盘的速度和生产不同；结果适合同一台机器上做前后对比，绝对值只作数量级参考。
- 数据库表从空开始，每次运行前都会 `ANALYZE`（相当于有 autovacuum 的生产库），不模拟已有几百万条调用的大表。
- 压测程序、模拟上游与网关在同一进程、同一批核上，CPU 不是网关独占；用 pprof 的 `component` 标签区分。
- 只覆盖网关热路径：不含管理页面的查询、观测页、后台对账、指标刷新。
