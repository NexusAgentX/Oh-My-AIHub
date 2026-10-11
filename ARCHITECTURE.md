# 架构说明

本文档描述当前系统结构、模块边界与必须保持的不变量。参数级细节（限额、超时、统计口径）以源码和测试为准；安全边界见 [SECURITY.md](SECURITY.md)，产品规则见 [PRODUCT.md](PRODUCT.md)，决策原因见 [docs/adr/](docs/adr/README.md)。

## 运行拓扑

```text
浏览器 / 官方 SDK / 兼容客户端
  ├── 开发：Vite :5173 ──┐
  └── Compose：Nginx :3000 ┤ 代理 /api、/v1、/v1beta
                           ▼
                     Go 后端 :8080 ──► PostgreSQL 18
                     （Prometheus 指标 :9090，仅内网）
```

| 组成 | 位置 | 职责 |
| --- | --- | --- |
| 前端 | `frontend/` | React 单页应用；`nginx.conf` 提供静态资源并反向代理 |
| 后端 | `backend/cmd/server` | 组装依赖、启动时校验密钥环与出站配置并解密全部存量密文、加载网关快照、运行后台任务 |
| 迁移 | `backend/cmd/migrate`、`backend/internal/database/migrations/` | Goose 嵌入式编号迁移，已发布迁移只追加 |
| API 契约 | `backend/api/openapi.yaml` | OpenAPI 3.1，前后端唯一契约（ADR-0021） |
| 编排与工具 | `compose.yaml`、`mise.toml` | 数据库、一次性迁移、后端、前端；工具版本与常用任务 |
| 运维 | `scripts/`、`docs/runbooks/` | 加密备份与恢复演练；部署、发布、事故处理 |

后台任务均在 `cmd/server` 内运行：网关超时调用清理、网关快照全量重建与余额缓存重新加载（每 60 秒，ADR-0040）、C2C 付款超时与美元汇率自动同步检查（每分钟，到点每天同步一次）、指标刷新（每分钟）、论坛未绑定附件清理（每小时）、上游原始错误文本清理（每天）。系统按单实例部署，运行时状态放在进程内（ADR-0039）。

## 后端分层

```text
internal/api（HTTP 适配、门禁、错误与分页约定）
   ↓
internal/<domain>（校验、状态机、服务；不依赖数据库）
   ↓
internal/postgres/<domain>pg（queries.sql + sqlc 生成代码 + Store，ADR-0017）
```

- 组合根 `postgres.Store` 持有各领域 Store；事务辅助在 `pgkit`。
- 跨领域原子提交由调用方持有 `pgx.Tx`（ADR-0020）；`ledgerpg.Post` 总在调用方事务内执行，审计 `auditpg.Record` 与业务写入同事务。会过账的存储（账本、网关、C2C、观测）用 `ledgerpg.Books.InTx` 开启事务，`Post` 只接受这种事务：事务提交、回滚或提交结果不明之后，进程内的观察者（网关余额缓存）被告知这笔过账对哪些用户账户做了什么，所以没有不被看见的过账路径。

| 包 | 职责 |
| --- | --- |
| `identity` | 受邀账户、Argon2id 密码、服务器端会话、首次改密、管理员管理账户（ADR-0007、ADR-0013、ADR-0037） |
| `ledger` | 交易类型与校验、系统账户、积分与账单读取、调账与坏账核销、计价公式（ADR-0012、ADR-0030） |
| `catalog`、`settings` | 模型目录（含计价币种）与条件价格档；单行平台设置与美元汇率同步（ADR-0038） |
| `channel` | 渠道与渠道模型、上游 Key 加密、模型发现与格式测试、上游凭据密钥环与固定出站策略（ADR-0036） |
| `apikey`、`routing` | 平台 API Key（ADR-0028）；按用户按模型的路由偏好，账号级与 Key 级 |
| `gateway` | 透明网关（ADR-0035）：请求体扫描与 `model` 替换、旁路用量解析、候选排序、进程内渠道状态、事件总线 |
| `snapshot` | 网关热路径数据的进程内缓存与写路径包装：Key、目录模型、渠道候选、路由偏好的快照，写事务提交后同步重建，另每 60 秒全量重建；账号的余额与信用额度（ADR-0040） |
| `observe`、`metrics` | 现算观测、对账核对与补记、SSE 推送；Prometheus 指标（ADR-0029） |
| `c2c` | 卖单市场纯状态机与服务、C2C 私密数据密钥环 |
| `keyringcheck` | 启动时逐条解密仍可能被读取的密文（未删除渠道与平台 API Key、全部 C2C 卖单），缺失或损坏的密钥拒绝启动；读取接口由各 `<domain>pg` 实现 |
| `forum` | 板块、主题、回复、工单与附件校验（ADR-0031） |
| `audit`、`money`、`localtime`、`ids`、`secretguard` | 审计读取；九位定点金额；Asia/Shanghai 记账日历；UUID；凭据检测与抹除 |

## API 约定

- 中间件链：写超时 → 安全头 → 同源校验 → 路由；只对配置的可信代理采信转发头；路由按 `x-access` 套上会话、首次改密与管理员门禁。
- 契约测试对照路由表与 OpenAPI，并用规范 schema 校验处理器与集成测试中的每个响应；`internal/api` 的覆盖门禁要求每个接口至少有一个经过校验的成功响应（外部模型 API 透传除外）；前端类型生成为 `frontend/src/api/schema.gen.ts`，CI 检查漂移。
- 响应用 `internal/api` 内手写的结构体表示，与处理器同文件，按 OpenAPI schema 命名为 `<schema>JSON`（`ApiKey` → `apiKeyJSON`），不用 `map[string]any` 拼响应；规范要求存在的字段（含 `null`）不加 `omitempty`，空列表初始化为 `[]`，金额为十进制字符串；领域类型不为响应添加 json 标签（只有持久化 jsonb 与请求解码用的类型保留）。网关存下的价格快照与审计 `detail` 是已存储的 jsonb，以 `json.RawMessage` 原样透传，是唯一不逐字段定义的响应内容；实时流（SSE）事件载荷与对应列表项使用同一组类型。
- 平台接口的错误统一为 `{"error": "<code>", "message": "<中文>"}`；列表统一游标分页（`cursor`、`limit` → `items`、`next_cursor`）；响应带 `Cache-Control: no-store`；记账类写请求接受 `Idempotency-Key`。
- 外部模型 API（`/v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1beta/models/...` 与模型列表）凭平台 API Key 认证，不走会话与同源校验。网关自己产生的错误按客户端协议返回官方 SDK 能解析的格式（`internal/gateway/errors.go`：`/v1beta/` 为 Gemini 的 AIP-193，`/v1/messages` 或带 `anthropic-version` 为 Anthropic，其余为 OpenAI），状态码不随协议变化，平台错误码保留在 OpenAI 的 `code`、Gemini 的 `ErrorInfo.reason` 里；上游错误透传，其中出现的渠道上游 Key 被抹掉（见「网关一次请求」第 4 步）。

## 网关一次请求

1. **认证与开调用**：按 Key 摘要在 Key 快照里查找，查不到即无效，不回落数据库；认出 Key 后每次请求写一行 `calls`（先 `in_progress`），响应带 `X-AIHub-Request-Id`。
2. **模型与额度**：别名 → Key 可用范围 → 启用的目录模型（目录快照）；余额（`余额 > −信用额度`，来自进程内余额缓存）与 Key 预算只检查、不预留、不冻结。检查通过后账号占一个在途名额（`gateway.Inflight`，进程内按账号计数，上限取自平台设置 `account_max_inflight_calls`，0 为不限）；超限以 429 `too_many_inflight_calls` 拒绝并记 `rejected_inflight`，不访问上游、不收费。名额由 `defer` 在 `Serve` 返回时释放，错误与 panic 路径同样；客户端断开后的续读与记账都在 `Serve` 内同步完成，所以续读期间名额仍被占用，记账后才归还。以后若有在 `Serve` 返回之后才结束的工作，释放要跟着移到它结束的地方。
3. **选渠道**：候选来自候选快照，为格式匹配的上架渠道，路由偏好来自路由快照，剔除取消勾选、冷却中和超过并发/RPM/当日收入上限的，再按路由模式（便宜/稳定/快速/手动）排序；稳定与快速模式用的渠道统计来自进程内滚动窗口（见下），快速模式用的首字中位数只统计流式调用；在某个模型上 4xx 异常的渠道（`gateway.detectAnomalies`）排到最后但不排除。
4. **透传与回退**：只替换鉴权与顶层 `model`，按需补 `include_usage`（请求体只扫描一次：`ScanRequest` 同时取出 `model`、`stream`、`previous_response_id` 与计价成员 `enable_thinking`、`service_tier`，后者的结果与 `encoding/json` 整体解码一致；`UpstreamBody` 在这次扫描的基础上找出换 `model` 与补 `include_usage` 的全部改动、一次拼接，没有改动时直接用客户端的字节；它同时返回客户端原本是否已开启，没开启则记 `call.usageInjected`），应用渠道请求头规则与 UA，不做格式转换；尚未向客户端写出字节前，连接失败、首字超时与可重试状态码换下一个候选；首字超时只用于流式请求，非流式响应要等生成结束才有首字节，只受总超时约束。其余 4xx 默认视为请求本身的问题，原样返回、不计渠道失败；例外是响应体表明模型不存在或不可用（`gateway.ModelUnavailable`，规则见 PRODUCT.md「4xx 与回退」），按上游错误处理，错误码 `upstream_model_unavailable`。因此这类 4xx 的响应体（最多 1MB）先读进内存再判断，不命中时写出缓冲内容。
   写出前，所有上游非 2xx 应答（请求自身的 4xx 透传与最后一个失败应答）都经过 `newUpstreamFailure`：本次尝试所用渠道的 Key 在响应头值与响应体里被替换为 `[redacted]`（ADR-0041，匹配在 `secretguard.Redact`：原样、百分号编码与任意 JSON 转义写法；日志与存储的错误文本由同一逻辑换成 `***`）。Key 没有出现时状态码、响应头与字节与上游一致；出现时 `Content-Length` 改为实际长度，担保旧字节的摘要头去掉。读错误体时多读 Key 最长编码长度（`secretguard.MaxEncodedLen`）的余量，替换后再截到 1MB，跨过上限的 Key 不会留下开头；传输层没有解码的编码体（`Content-Encoding` 非 identity）无法检查，不写正文。2xx 响应（含流式）不处理，见 ADR-0041 决定 7。
   超时全部是请求 context 上的计时器，出站客户端自己不设 `Timeout`（ADR-0035 规则 5，出站连接因此可以跨请求复用）：非流式是整次请求的总超时（`timeout_total`，尚未写出字节，可回退）；流式在首字节前是首字超时，之后是相邻两次读到数据的间隔（空闲超时，每次读到数据重置）；整个调用另有从开始算起的上限 `callTimeLimit`（60 分钟，含全部尝试）。兜底任务的 61 分钟时限由它加 1 分钟余量得出，两处常量互相注释，改一处要同步另一处。
5. **回写与记账**：响应原样逐块回写（读缓冲区取自池，首块与后续读共用一块），旁路读取用量：旁路解析器先看每个事件的成员名（`scanFrameKeys`，不分配内存），没有用量、细分、服务档位或思考标记成员的事件不解码，所以绝大多数只带几个字的事件不产生分配；有这些成员的事件按原有规则解析，结果与逐帧完整解码一致（`legacy_observer_test.go` 保留原实现，对拍测试与模糊测试逐事件比较）；结束时在一个事务内关闭调用并过账，价格快照随调用保存，历史不按现价重算。首字节之后的中断不再回退：空闲超时、总超时与读取错误记尝试 `interrupted`（`error_code` 为 `timeout_idle`、`timeout_total`、`read_error`）并计渠道失败；调用上限到期记 `interrupted` / `call_deadline`，不计渠道失败；两者都按已读到的用量结算。调用上限在首字节之前到期时，网关自己返回 504 `call_deadline`，不再尝试后面的候选。
   读到 `io.EOF` 只有在这次尝试没被取消时才算回答正常结束（`answerEnded`）。取消（调用上限、空闲或续读超时）会让传输层关闭连接，而关闭 TLS 连接先发 close_notify，对端（例如一直等着被取消的上游）可以抢在套接字真正关闭前发完分块响应体的结尾，阻塞在读上的一方会拿到最后几个字节和干净的 EOF 而不是错误；这种 EOF 按取消原因处理，不当成完成。
   客户端中途断开：开始回写之前（等响应、回退中）断开，立即取消上游，不收费；开始回写之后断开，网关停止写客户端、继续读上游，读到的字节只喂给旁路解析器（`clientWatch`），直到上游结束、读满 `drainWindow`（10 秒）、触发空闲超时或调用上限，再取消上游并按读到的用量结算，调用 `client_disconnected`。续读期间渠道并发槽位照常占用，上游出错与客户端断开都不计渠道失败，`response_bytes` 只算写给客户端的字节。客户端收完最后一帧就挂断、上游随后正常结束的调用不算断开，仍是 `succeeded`。
   替客户端开启了 `include_usage`（`usageInjected`）的 OpenAI Chat 流式响应，经 `usageFrameFilter` 去掉上游多发的那一帧：按空行（`\n\n`、`\r\n\r\n` 或混用）切分事件，只有“单个 `data:` 行、JSON 对象、`choices` 为空数组、`usage` 为非空对象”的事件被丢弃，其余字节（注释行、`data: [DONE]`、`usage` 在非空 `choices` 帧里的响应）逐字原样写出；未完整的事件先暂存，到边界再处理，流结束时剩余字节原样写出；单个事件超过 64 KiB 不再暂存。旁路解析器读取的仍是上游的全部原始字节，所以计费不变；`response_bytes` 只算写给客户端的字节。客户端自己开了 `include_usage`、非 SSE 响应和其他三种格式都不过滤。

并发、RPM、冷却、限流、账号在途计数、渠道统计和 Argon2 工作槽是进程内状态，当前按单实例部署设计。

Key、目录模型、渠道候选索引（含各格式下在线的模型和自定义模型是否在线）、路由偏好来自进程内快照（`internal/snapshot`，ADR-0040）；请求在访问上游之前只剩开调用记录（带预算时还有已缓存的 Key 已用额）。快照与余额缓存是 `gateway.Store`、`routing.Store` 与 `gateway.Models` 之后的缓存层，引擎不感知它。

- 失效：会写这些数据的服务（Key、渠道、目录、身份、路由）必须建立在 `postgres.Cached` 提供的包装 Store 上。包装在事务提交成功之后重建受影响的快照，重建完成才返回，所以后台操作一返回，下一个请求就按新数据处理；停用或删除 Key、停用账号、下架或删除渠道（含管理员强制下架）也走这条路，没有例外。快照记录自己已计入的变更数，比最近一次变更旧的快照不被使用，重建失败则请求失败（失败关闭）。
- 兜底：启动加载一次（读不到就拒绝启动），之后每 60 秒全量重建；不经后台的改动（手工 SQL、迁移）最长 60 秒后可见。
- 新增会写这些表的 Store 方法，要在 `internal/snapshot/wrap.go` 的清单里归类并接上钩子；守护测试 `TestEveryStoreMethodIsClassified` 在接口有方法而清单没有时失败。
- 查不到的 Key 直接拒绝，不查库。快照里的值被所有请求共享、只读；`Candidates` 返回副本，因为引擎要给候选设置汇率。
- Responses 粘性用小 LRU（最近的 response_id → 渠道），由调用结算写入，未命中再查库。美元汇率不在快照里，请求时从设置快照取（5 秒内）。
- 绑定单实例（ADR-0039）：多副本时一个副本的写入无法使另一个副本的快照失效。

余额与信用额度（`snapshot/balances.go`，沿用 ADR-0040 的原则：提交后失效、失败关闭、定期全量刷新）：事前检查读进程内的账号缓存，缓存只用于这次检查，记账、账本余额与核对仍以数据库为准，允许余额短暂陈旧（只检查、不冻结，ADR-0035）。账号在第一次被请求时惰性加载，不在启动时预加载。

- 跟随账本：网关自己的结算（`api_call`，补记漏记的调用同属此类）在事务提交后，把分录金额累加到已加载的消费者与渠道所有者条目上；其他过账（C2C 挂单、放行、退回，管理员调账，坏账核销）在提交后让相关账号的条目失效，下一个请求重新读库；回滚不改缓存，提交结果不明则失效。信用额度由身份写路径（修改账号）在提交后失效。以上都在操作返回之前完成，所以调账、核销、C2C 与信用额度修改之后的下一个网关请求立即按新值检查。
- 累加要求没有读库的请求跨过提交，否则同一笔结算会被计两次或漏计，所以每个条目带一个纪元与“进行中结算数”：从结算的过账完成到它的事务结束，相关账号视为忙；读库的结果若是在忙时写入、或读取期间纪元变过，就不写入缓存，只供该次请求使用。账号忙时条目若已失效，请求自己读库，和没有缓存时一样。
- 兜底：每 60 秒把上一轮用过的条目一次读库刷新，没用过的忘掉；绕过后台直接改库（手工 SQL）最长 60 秒后可见。读库失败时保留旧条目，已失效的条目没有旧值可用，请求报错（失败关闭）。
- 新增会过账的路径必须用 `Books.InTx`（`Post` 的参数类型保证）；新增改信用额度的写路径要在 `snapshot/wrap.go` 的身份包装里失效对应账号。

渠道上游 Key 的明文缓存（ADR-0036 决定 8）：每次尝试通过 `Keyring.OpenCredential` 取 Key，同一份密文只解密一次，明文留在进程内存里。缓存归密钥环所有，每个渠道一条，只对解出它的那份密文（版本、key id、nonce、密文都相同）作答，所以渠道换 Key 或换用新密钥后旧条目自然失效；渠道服务在更新 Key、删除渠道写库成功后立即调用 `ForgetCredential` 清掉该渠道的条目。大小受渠道数约束，不需要淘汰；解密失败不缓存。平台 API Key、渠道探测和启动校验仍直接用 `Decrypt`，不进缓存。

渠道统计（`gateway.StatsBook`，ADR-0040 的延伸，绑定单实例）：每个渠道一个 288 格的环形窗口，每格 5 分钟，记尝试数、成功数和首字时间对数直方图（约 9% 宽的区间，读出时取区间中点，误差不超过 4.6%）；每个“渠道 × 模型”另有一个窗口，记尝试数与 4xx（`client_error`）数。每次尝试结束时在 `Serve` 循环（没有可回写的响应时）或 `streamBack`（有响应时，在结局确定之后）累加一次；整格过期，窗口读出时顺便清掉过期的格并丢掉空窗口。累加不查库，`client_disconnected` 不计，`client_error` 只进“渠道 × 模型”窗口，首字只收流式调用的 `completed` 尝试。网关读到的是不可变快照（`statsSnapshot`，原子指针读取），`RunRefresh` 每 5 秒从窗口重新发布，同时判定 4xx 异常（阈值是 `stats.go` 里的常量），新出现的异常经 `Runtime.Announce` 发 `anomalous_client_errors` 渠道事件。启动时 `Engine.LoadStats` 读一次数据库（`AttemptHistory`，按渠道、模型、5 分钟、首字区间聚合，分箱边界由 `gateway.TTFTBinEdges` 传给 SQL，内存与数据库按同一张表分箱）作为窗口的起点，读不到则带着空窗口启动，已有的异常视为已通知；此后统计不再查库。重启时仍在途的调用，其尝试不在窗口里。

冷却由连续失败阈值触发；上游 429 带 `Retry-After` 或 `retry-after-ms` 时不等阈值，`gateway.RateLimitCooldown` 解析并限定时长后由 `Runtime.CooldownFor` 直接冷却整个渠道，事件原因为 `rate_limited`（规则见 PRODUCT.md「自动冷却」）。

## 数据与不变量

- **单位**：金额为 `bigint` 纳积分（1 积分 = 1e9），人民币为整数分，费率为纳比率，时间为 `timestamptz`（ADR-0006）。美元模型的价格以纳美元存储，汇率 `settings.usd_cny_rate_nano` 为纳单位；计费在用量总额上一次性乘倍率与换算率（`ledger.ChannelRate`：开汇率优惠为 1，否则为汇率）并向上取整，便宜优先排序比较「倍率 × 换算率」。
- **账本**（ADR-0005、ADR-0025）：`ledgerpg.Post` 是唯一过账路径，且只在 `Books.InTx` 开启的事务里执行；幂等键唯一，重复提交返回原交易；按账本账户 id 升序加行锁；提交时由延迟约束触发器要求每笔交易至少两条分录且合计为 0；交易与分录不可修改、删除。系统账户为 `platform_revenue`、`c2c_escrow`、`bad_debt`；`platform_revenue` 既收手续费，也是管理员调账的对手方，所以它的余额不等于平台收入（口径见 `PRODUCT.md`）。调用自己的渠道不产生账本交易（同一账户不能在一笔交易中出现两次），费用仍记在调用上，核对与补记排除这类调用。
- **C2C 托管**：卖单满足 `total = available + in_trade + sold + closed`（数据库 CHECK），`c2c_escrow` 余额恒等于全部卖单 `available + in_trade` 之和；加锁顺序为订单 → 交易 → 账本账户；状态转换幂等。

| 动作 | 过账 | 数量变化 |
| --- | --- | --- |
| 挂单 | 卖家 → 托管 | available = total |
| 买入 | 无 | available → in_trade |
| 放行 / 判给买家 | 托管 → 买家 | in_trade → sold |
| 取消、超时、退回卖家（卖单开放） | 无 | in_trade → available |
| 取消、超时、退回卖家（卖单已关闭） | 托管 → 卖家 | in_trade → closed |
| 关闭卖单 | 托管 → 卖家（可买量） | available → closed |

- **表**（22 张）：身份与审计 `accounts`、`sessions`、`audit_log`；设置与目录 `settings`、`models`、`model_price_tiers`；渠道 `channels`、`channel_models`、`channel_events`；Key 与路由 `api_keys`、`route_prefs`、`route_pref_channels`；调用 `calls`（不保存请求与响应正文）；账本 `ledger_accounts`、`ledger_transactions`、`ledger_entries`；C2C `c2c_orders`、`c2c_trades`；论坛 `forum_boards`、`forum_topics`、`forum_replies`、`forum_attachments`。

## 可观测性

- 一切观测现算自 `calls`、`ledger_*`、`c2c_*`，不存快照或历史表；需要一致状态的查询在同一 `REPEATABLE READ` 只读事务内完成。
- 平台收入与调账净额也现算自分录：前者是 `api_call` 交易记入 `platform_revenue` 的分录之和，后者是 `admin_adjust` 交易对用户账户的净额；管理员积分页与指标 `aihub_points_platform_revenue`、`aihub_points_admin_adjustments` 用这两个口径，账户余额只用于天平等式。
- 五项核对：全部余额合计为 0；每个账户余额等于分录合计；托管余额等于卖单 `available + in_trade`；每个应计费的成功调用都已记账；每笔放行交易都已记账。管理员可对漏记调用补记或作废，与正常记账使用同一幂等键。
- `observe.Feed` 订阅网关事件总线，向 SSE 订阅者与 Prometheus 扇出；慢订阅者丢消息而不阻塞网关。指标不带用户或 Key 标签，端口只在内网。

## 论坛与工单

`forum` 与 `forumpg` 实现板块、主题、回复与私密工单；工单只对提交者和管理员可见，主题类型不可变。附件存 PostgreSQL `bytea` 并继承内容权限（ADR-0031）。Markdown 由 Vditor 编辑，阅读与预览共用 Lute 与 DOMPurify 白名单（ADR-0032）。限额与权限细节见 SECURITY.md。

## 前端

- TanStack Query 管理服务端状态，类型来自生成的 `schema.gen.ts`；`src/ui/` 基础组件与 `src/styles/tokens.css` 设计 token（ADR-0019）。
- 主题只由 `<html data-theme>` 切换：`public/theme-init.js` 在首屏前按本机偏好设置，`src/theme/` 负责切换与跟随系统；token 为浅色与深色各给一套值，视觉规则见 `DESIGN.md`（ADR-0033）。
- 按领域分目录：`home`、`models`、`keys`、`usage`、`channels`、`points`、`account`、`forum`、`welcome`（公开落地页）；`src/calls/` 为用户、渠道与后台共用的调用观测组件；`src/markdown/` 封装编辑与渲染。
- `src/admin/` 是独立外壳的管理后台；管理后台与论坛按路由懒加载。账号变化时清空查询缓存。
- 外壳：桌面左侧分组侧栏；窄屏为顶部条加底部 Tab。

## 待定事项

- 最后一名启用管理员遗忘密码时的恢复流程（ADR-0013）。
- 数据库连接池容量与高可用；备份恢复、延迟、可用性与告警的数值目标。
