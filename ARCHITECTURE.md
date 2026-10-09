# 架构说明

> 状态：v0.7.0。产品已按 [Epic #170](https://github.com/NexusAgentX/Oh-My-AIHub/issues/170) 原地重写为「API 市场 + 积分 C2C 市场」：新数据库基线、OpenAPI 契约、身份与会话、零和账本、模型目录、透明网关与渠道、API Key 与路由、C2C 卖单市场、调用与积分可观测性、用户界面、管理后台与落地页。

本文档描述仓库当前真实存在的系统结构，再单独列出已确认但尚未实现的目标约束。不得把目标约束当作当前代码能力。

## 当前结构

```text
浏览器 / 官方 SDK / 兼容客户端
  │
  ├── 开发环境：http://localhost:5173
  │                 │ /api、/v1、/v1beta 代理
  │                 ▼
  │              Go 后端：http://localhost:8080
  │
  └── Compose 环境：http://localhost:3000
                    │
                    ▼
                  Nginx
                    │ /api、/v1、/v1beta 代理
                    ▼
                  Go 后端：backend:8080
                    │
                    ▼
                PostgreSQL 18
```

## 组件

| 组件 | 位置 | 当前职责 |
| --- | --- | --- |
| 前端 | `frontend/` | React 单页应用（TanStack Query、`src/styles/tokens.css` 设计 token、`src/ui/` 基础组件、`src/ui/Icon.tsx` 图标集，见 ADR-0019）。外壳为 `src/layouts/`：桌面左侧分组侧栏（使用 API / 共享 / 积分，底部账户菜单与余额），<768px 为顶部条 + 底部 5 个 Tab（首页、模型、渠道、积分、我的）。用户页面按领域分目录，查询在各自 `queries.ts`，类型来自 `schema.gen.ts`：`home/`（`/home`）、`models/`（`/models`、`/models/:model`，含账号级与 Key 级共用的路由编辑器）、`keys/`（`/keys` 与抽屉）、`usage/`（`/usage`）、`channels/`（`/channels`、`/channels/new` 三步向导、`/channels/:id`）、`points/`（`/points` 各 Tab、`/points/trades/:id`、买卖抽屉）、`account/`（`/account`、移动端 `/me`）。`src/calls/` 是调用观测的共享组件（列表、筛选、汇总、SSE 实时 hook、调用详情抽屉与尝试时间线），按接口路径参数化，供用户用量、渠道编辑页与管理后台复用；列表的渠道列在尝试次数大于 1 时显示「换了 N 次」，汇总显示首字 p50/p95，请求 ID 是列表的精确筛选（`request_id`，设置后忽略时间范围）。积分账单的对账条按调用支出、渠道收入、C2C 买入、C2C 卖出（含退回）、调账与核销拆分，按所选月份以 `from`/`to` 查询 `GET /api/points`；按天/按 Key 汇总传 `group`。渠道编辑页统计显示 24h/7d 成功率、首字与速度 p50/p95、按小时或按天趋势、今日收入上限进度、失败按状态码分布与最近失败（含上游原始错误）。接口出错的区块显示可重试的错误态。实例初始化、登录与首次改密沿用原流程。公开落地页在 `src/welcome/`（已登录访问 `/` 跳到 `/home`）。管理后台在 `src/admin/`（Feature E）：自带外壳 `AdminFrame`（复用 `layout.css` 的侧栏与底部 Tab 样式，移动端前 4 项进 Tab、其余进「更多」）与 `RequireAdmin` 门禁，`/admin` 下有概览、调用、积分、用户、模型、渠道、申诉、设置 8 页；查询与写操作集中在 `admin/api.ts` 与 `admin/queries.ts`（写成功后失效 `['admin']` 前缀），调用页复用 `src/calls/`，需要原因的操作统一用两步确认对话框，一次性密码关闭即丢弃。概览的「需要处理」按 `AttentionItem.kind` 给出专属文案并跳转（核对与集中度类定位到积分页 `#checks`、`#risks`）；积分页走势时间窗走后端 `days` 参数，五项核对逐项展示并对漏记调用逐条补记或作废，交易浏览可按关联对象（`related_type`/`related_id`）筛选，交易抽屉显示变动前后余额、价格快照与相关用户最近的人工操作；申诉详情的相关账本按交易与所属卖单的关联对象查询 |
| 后端 | `backend/` | Go `net/http` 服务。`cmd/server` 组装服务并在启动时校验 `UPSTREAM_CREDENTIAL_*`、`UPSTREAM_*` 出站配置与 `C2C_PRIVATE_DATA_*` 密钥环；`cmd/migrate` 执行迁移 |
| API 契约 | `backend/api/openapi.yaml`、`backend/internal/api/` | OpenAPI 3.1 是唯一契约（ADR-0021），定义约 70 个 JSON 接口与 6 个外部模型 API 入口；`x-access` 声明门禁，`x-feature` 标明负责实现的 Feature。`internal/api` 的 `router` 按 access 包裹会话、首次改密与管理员门禁；路由经 `handle`（A）或 `implement`（B 起）注册。契约测试逐项对照路由表、门禁与 Feature，并用规范 schema 校验每个真实响应；PostgreSQL 集成测试同样用规范校验每个 `/api` 响应。前端类型由它生成为已提交的 `frontend/src/api/schema.gen.ts` |
| 数据库 | PostgreSQL 18 | 18 张表，见下文“数据与状态” |
| 持久化分层 | `backend/internal/postgres/`、`backend/sqlc.yaml` | 每个领域一个 `<domain>pg/`（`queries.sql` + sqlc 生成代码 + 领域 Store，ADR-0017）：`identitypg`、`catalogpg`、`settingspg`、`auditpg`、`ledgerpg`、`channelpg`（渠道与健康事件）、`keypg`（API Key 与路由偏好）、`gatewaypg`（网关热路径：Key 查找、候选渠道、调用记录与记账、首页读取）、`observepg`（Feature G 的只读观测查询与调用补记）；共享事务辅助在 `pgkit`。组合根 `postgres.Store` 以字段持有各领域 Store。跨领域原子提交由调用方持有 `pgx.Tx`（ADR-0020）：创建账户在同一事务内写身份行、用户账本账户与审计；账本过账 `ledgerpg.Post(ctx, tx, …)` 总在调用方事务内执行 |
| 数据库迁移 | `backend/internal/database/migrations/`、`backend/cmd/migrate/` | 只有一份原地重写的基线 `0001_baseline.sql`（ADR-0024、ADR-0026） |
| 开发任务 | `mise.toml` | 固定工具版本并提供安装、开发、测试、生成与构建命令 |
| 容器编排 | `compose.yaml` | 运行 PostgreSQL、一次性迁移、后端与前端；后端的 Prometheus 端口只 `expose` 在 Compose 网络内，不发布、不经 Nginx |
| Web 入口 | `frontend/nginx.conf` | 提供前端静态资源，将 `/api/` 与外部模型 API 路径代理至后端 |

## 后端领域包

| 包 | 职责 |
| --- | --- |
| `internal/identity` | 受邀账户、Argon2id 密码、服务器端会话、首次改密、管理员创建/修改/重置账户；不能停用、降级或重置自己，不能移除最后一个启用管理员 |
| `internal/ledger` | 账本领域：交易类型、系统账户、交易校验（至少两条、非零、账户不重复、合计为 0）、积分概况与账单读取、管理员调账与坏账核销；计价公式 v2（`CalculatePriceV2` 与条件价格档选择） |
| `internal/catalog` | 模型目录与条件价格档的规范化、校验与部分更新（`ModelPatch`，价格档整组替换） |
| `internal/settings` | 单行平台设置的校验与更新 |
| `internal/audit` | 审计日志读取；写入由各领域在自己的事务内调用 `auditpg.Record` |
| `internal/money` | 九位定点纳积分 `Amount` 与十进制字符串解析/格式化 |
| `internal/channel` | 渠道领域：渠道与模型的校验（倍率 0～1000、四种格式、请求头规则不得触碰鉴权与分帧头）、创建/修改/软删除、上游 Key 加密（不回显）、`discover`（读取上游模型列表并按精确、去日期后缀、大小写不敏感匹配目录，按模型族预选格式）、`test`（四种格式最小请求，写回 `format_tests` 与可选的 `formats`）、管理员强制下架与恢复；以及上游凭据版本化密钥环（ADR-0009）与固定出站策略（HTTPS、DNS/IP 校验、端口白名单、禁用主机 + `settings.extra_blocked_hosts`、禁止重定向）。`Outbound` 接口让测试替换出站边界 |
| `internal/apikey` | 平台 API Key：`sk-aih-` + 32 字节随机数，SHA-256 查找、凭据密钥环可逆加密（ADR-0028）；每人最多 20 把、首次访问首页惰性创建“默认 Key”、预算/可用模型/别名/过期时间校验、再次复制写审计 |
| `internal/routing` | 每个用户每个模型的路由偏好（便宜/稳定/快速/手动、取消勾选、最大尝试次数、首字超时），账号级与 Key 级覆盖 |
| `internal/gateway` | 透明网关（ADR-0027）：`bodyscan` 对顶层 JSON 做词法扫描与字节拼接（替换 `model`、补 `include_usage`）；`usage` 旁路读取四种格式的用量与流式指标；`rank` 按四种模式排序候选并处理粘性；`runtime` 保存进程内并发/RPM/冷却/连续失败/当日收入与 Key 花费缓存；`engine` 实现认证、模型解析、余额与预算检查、回退、原样回写、事后记账与 `slog` 日志；`events` 为非阻塞进程内事件总线；`models` 回答 `GET /v1/models` 与 `/v1beta/models` 并运行超时调用清理 |
| `internal/observe` | 观测领域（Feature G）：调用列表与汇总、调用详情与按查看者的可见性、用量聚合、渠道统计、用户积分走势与期间对账、账单汇总与导出、管理员积分全局、五项实时核对、风险、概览「需要处理」、交易浏览、调用补记、原始错误清理；`Feed` 订阅网关事件总线，向 SSE 订阅者与 `Observer`（指标）扇出 |
| `internal/metrics` | Prometheus 注册表与处理器（`client_golang`）；实现 `observe.Observer`，指标不带用户或 Key 标签 |
| `internal/localtime` | 记账日历：Asia/Shanghai 的自然日与自然月（预算窗口与“今日”统计） |
| `internal/c2c` | C2C 卖单市场领域：词汇与错误（`types.go`）、纯状态机（`machine.go`：数量恒等式、各转换的前置检查与幂等判断、应付金额向上取整）、`Service`（输入校验、收款方式加解密与可见性、游标分页、超时任务入口）、C2C 私密数据密钥环 |
| `internal/secretguard` | 凭据泄露检测辅助 |

## 可观测性（Feature G）

- **读取口径**：一切观测都现算自 `calls`、`ledger_*` 与 `c2c_*`，不建快照、历史或巡检表。余额类汇总一律由分录求和得到（核对 ② 单独检验 `balance_nano` 与分录一致），所以用户的「期初 + 各类变动 = 期末」在构造上成立；管理员走势按账户逐日累加分录净额，不依赖 `created_at` 与分录 id 的相对顺序。需要同一状态的查询（余额结构、五项核对、走势、概览）在一个 `REPEATABLE READ` 只读事务内完成，避免并发过账造成假警报。
- **成功与实际扣除**：成功 = `succeeded` 或 `succeeded_unbilled`；成功率 = 成功 /（成功 + 已结束的失败），`in_progress` 不计。所有「费用/花费/收入」只计已有账本交易的调用（`ledger_tx_id` 非空）；自己的渠道调用不产生账本交易，列表里 `cost` 仍显示、`charged` 为 0，也不计入花费或渠道收入。
- **调用查询**：`ListCalls`/`CallStats` 共用一组可选筛选（时间、Key、模型、格式、渠道、结果、耗时/tokens/费用区间、标签、请求 ID、账户），游标为 `(created_at, id)` 键集。渠道所有者的范围是「最终渠道或任一次尝试为该渠道」，用 `attempts` 上的 GIN 索引（`jsonb_path_ops`）查找，只返回该渠道自己的尝试，不含调用者、Key、标签。调用详情对调用者、渠道所有者（受限视图）与管理员开放，其他人得到 404。
- **渠道统计**以「到达该渠道的尝试」为单位（排除客户端取消与请求自身错误）：24h/7d 窗口、按小时与按天、首字与输出速度 p50/p95（`percentile_cont`）、失败按状态码分布、最近 20 次失败含上游原始错误、收入、今日收入与每日上限进度。
- **用量聚合**按 Asia/Shanghai 自然日；`view=revenue` 给共享者的渠道收入视角（按渠道、模型、天）。
- **实时流**：`Feed` 订阅网关事件总线（缓冲 4096），对 `call_started`/`call_finished` 读出完整调用并推给订阅者；订阅者缓冲 64，满了丢弃而不阻塞。SSE 每 15 秒一行注释心跳，每次写入重设写超时，响应带 `X-Accel-Buffering: no`；渠道流只推 `call.finished`（结束前无法知道会触达哪些渠道）。事件总线在网关缓冲满时同样丢弃，所以指标与实时流是「尽力而为」，对账以数据库为准。
- **Prometheus**：`METRICS_ADDR`（默认 `:9090`）上的独立 `http.Server`，只提供 `GET /metrics`，Compose 不映射端口、Nginx 不代理。调用类指标来自 `Feed` 读出的调用；积分与核对类指标由每分钟一次的刷新（五项核对 + 余额结构 + 各交易类型累计）以常量指标在抓取时输出；`aihub_channel_up` 在抓取时结合进程内冷却状态。`model` 标签只取目录中存在的模型，其余为 `other`。
- **五项核对**（`Snapshot` 内现算）：① 全部账户余额合计为 0；② 每个账户 `balance_nano` 等于分录合计；③ `c2c_escrow` 余额等于所有卖单 `available + in_trade`；④ 每个应计费的成功调用都有 `ledger_tx_id`（`succeeded`、`interrupted`、`client_disconnected` 且费用大于 0，排除渠道所有者即调用者的调用，部分索引 `calls_unbilled_idx`）；⑤ 每笔 `released`/`resolved_to_buyer` 交易都有 `ledger_tx_id`。
- **补记**：`POST /api/admin/ledger/repair-call/{id}` 在一个事务内锁定调用行，`charge` 以正常记账同一幂等键 `call:<id>` 过账（重复补记无效，已记账返回 409），`void` 把调用标为不收费的 `interrupted`；两者写审计 `ledger.repair_call`。
- **原始错误清理**：`cmd/server` 启动时与此后每 24 小时把 30 天前调用的 `attempts[].error_message` 置空（保留状态码与错误码），每批 1000 条。

## 当前请求链路

1. 浏览器加载 React 应用；`/`、`/welcome` 与未知路径显示公开落地页（`/` 与未知路径在已登录时跳到 `/home`）；实例尚无管理员时前端引导到 `/initialize`；`/admin/**` 只对管理员开放，其他用户回到 `/home`。
2. 开发环境由 Vite、Compose 环境由 Nginx 把 `/api`、`/v1`、`/v1beta` 代理到后端。
3. 后端中间件链为“写超时 → 安全头 → 同源校验 → mux”；只对可信内部代理采信转发头，对非安全方法校验同源 `Origin`（`/v1`、`/v1beta` 外部入口除外）；路由层执行会话、首次改密与管理员门禁。
4. 错误统一为 `{"error": "<code>", "message": "<中文>"}`；列表统一游标分页（`cursor`、`limit` → `items`、`next_cursor`）；响应带 `Cache-Control: no-store`。
5. 外部模型 API 入口（`POST /v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1beta/models/{model}:generateContent|:streamGenerateContent`，`GET /v1/models`、`/v1beta/models`）由 `internal/gateway` 处理，凭平台 API Key 认证（`Authorization: Bearer`、`x-api-key`、`x-goog-api-key` 或 `?key=`），不走会话与同源校验；其他 `/v1`、`/v1beta` 路径返回 404“不支持的接口”。

## 网关一次请求

1. **认证**：按 Key 的 SHA-256 查找；不存在/停用/过期/已删除返回 401，账号停用返回 403。认出 Key 之后每次调用都有一行 `calls`（先 `in_progress`，结束时更新），响应头 `X-AIHub-Request-Id`，平台错误正文含 `request_id`。
2. **格式与模型**：路径决定格式；顶层 `model` 经 Key 别名与可用范围，再对照启用的目录模型（Gemini 的模型取自路径）。
3. **余额与预算**：`余额 > −信用额度`，且 Key 的每日/每月/总额预算（窗口为 Asia/Shanghai 自然日与月，已用额从 `calls` 汇总后缓存并在记账后增量更新）未用完，否则 402；不预扣、不冻结。
4. **排渠道**：候选是该模型 `listed`、格式匹配、所有者账号启用的渠道模型；去掉取消勾选的、冷却中、达到并发/RPM/当日收入上限的；按路由模式排序（自己的渠道视为 0 价最前；样本少于 20 次的成功率按平台中位数处理；手动模式未出现在列表中的渠道视为取消勾选）；Responses 的 `previous_response_id` 把上一轮渠道提到最前；取前 `max_attempts` 个。
5. **转发**：URL = 渠道 Base URL + 原路径与查询（Gemini 路径换上游模型名，`key` 参数换成渠道 Key）；请求头去掉 hop-by-hop、`Host`、`Content-Length`、`Cookie`、`Accept-Encoding`、平台 Key 头、`X-AIHub-Tag` 与 `X-Forwarded-*`，应用渠道请求头规则与 UA，再把上游 Key 写回客户端原来的位置；请求体只替换顶层 `model`（OpenAI Chat 流式再补 `include_usage`）。
6. **失败就换**：尚未写出任何字节前，连接失败、首字超时、5xx/429/401/403/404/405 换下一个候选；其他 4xx 原样返回且不收费；全部失败返回最后一次上游的状态码与正文（`upstream_failed`）。连续失败达到阈值的渠道进入冷却并写 `channel_events`。
7. **回写与旁路**：响应头（去掉 hop-by-hop 与 `Set-Cookie`）与正文逐块原样写回并 flush，旁路解析器读取用量与首字/速度/token 间隔，解析失败不影响返回。
8. **记账**：结束时在一个事务内关闭 `calls` 并过账（见 ADR-0027）；读不到用量为 `succeeded_unbilled`；客户端断开或上游输出后中断按已读到的用量记账。后台每分钟把超过一小时零一分钟仍为 `in_progress` 的调用标为 `interrupted`。

## 数据与状态

金额一律为 `bigint` 纳积分（1 积分 = 1e9）；人民币为整数分；费率为纳比率（1e9 = 100%）；时间为 `timestamptz`。

| 领域 | 表 | 说明 |
| --- | --- | --- |
| 身份 | `accounts`、`sessions` | 账户（用户名、显示名、Argon2id 哈希、密码版本、管理员标记、状态、首次改密、信用额度 ≥ 0、默认 Key 创建标记）；会话只存令牌 SHA-256 摘要，密码版本变化、账户停用或删除行即失效 |
| 审计 | `audit_log` | 操作者、动作、对象、原因、`detail jsonb`、时间；追加写入 |
| 设置与目录 | `settings`、`models`、`model_price_tiers` | 单行平台设置（seed 默认值）；模型名即主键，四个基准价与可选展示信息；条件价格档最多 16 档 |
| 渠道（B） | `channels`、`channel_models`、`channel_events` | 上游 Key 密文与 key id、状态（listed/unlisted/suspended）、请求头规则与可空高级项；每个模型的上游名称、倍率、格式与格式测试结果；健康事件 |
| Key 与路由（B） | `api_keys`、`route_prefs`、`route_pref_channels` | Key 摘要（网关查找）与可逆密文、预算、可用模型、别名；账号级或 Key 级路由（`UNIQUE NULLS NOT DISTINCT`）与手动顺序、取消勾选 |
| 调用（B/G） | `calls` | 一次调用一行，`id` 即请求 ID；尝试时间线 `jsonb`、四类 token、价格快照、费用与手续费、流式指标、结果分类；不保存正文 |
| 账本 | `ledger_accounts`、`ledger_transactions`、`ledger_entries` | 用户账本账户（每人一个）与三个系统账户 `platform_revenue`、`c2c_escrow`、`bad_debt`；交易幂等键唯一；分录非零并记录变动后余额 |
| C2C（C） | `c2c_orders`、`c2c_trades` | 卖单数量恒等式 `total = available + in_trade + sold + closed`、加密收款方式；交易状态与各状态时间 |

账本不变量（ADR-0025）：

- 唯一的 `DEFERRABLE INITIALLY DEFERRED` 约束触发器在提交时要求每笔有分录的交易至少两条分录且合计为 0；`ledger_entries` 与 `ledger_transactions` 由触发器拒绝 UPDATE/DELETE。
- `ledgerpg.Post` 是唯一过账路径：插入交易（幂等键重复返回已有交易，不重复记账；同键不同类型或关联对象返回冲突）→ 按账本账户 id 升序 `FOR UPDATE` 锁定 → 更新余额 → 写分录与 `balance_after`。余额规则（API 调用“余额 > −信用额度”、C2C“只能卖正余额”）由调用方在同一事务内用 `ledgerpg.Balance`、`ledgerpg.CreditLimit` 检查。
- 管理员调账以 `platform_revenue` 为对手方；坏账核销把用户全部负余额转入 `bad_debt`。两者均写审计，接受 `Idempotency-Key`。

## 已实现边界

- 账户只能由管理员创建（首个管理员经 `POST /api/instance/initialize`，advisory lock 防并发，已初始化返回 409）；初始密码与重置密码只返回一次；首次登录只能访问 `/api/me`、`/api/me/password` 与退出。
- 停用账户在同一事务内删除其全部会话；重置密码提升密码版本并删除全部会话（ADR-0013）。移除管理员身份的修改在事务级 advisory lock 下检查剩余启用管理员数。
- 模型目录写入在 `FOR UPDATE` 锁内合并部分更新并整组替换价格档；创建、修改模型与平台设置均写审计（含修改前后值）。
- 模型删除在行锁事务内清理软删除渠道配置及路由偏好，再删除目录（价格档级联删除）；存活渠道的外键阻止删除并返回 409，整个事务回滚。历史调用、账单及 Key 名称引用保持不变。
- 用户只能读取本人积分与账单；账单按分录 id 倒序游标分页，可按交易类型、Key、时间筛选，`group=day|key` 返回汇总，`format=csv` 导出全部匹配记录（UTF-8 BOM，最多 50000 行，文本单元格防公式注入）。
- 上游凭据密钥环与出站策略在启动时校验配置，供渠道与网关使用；C2C 私密数据密钥环在启动时校验并用于加密卖单收款方式。
- 渠道公开信息（模型详情里的渠道列表）只含渠道名、所有者显示名、格式、倍率、现价、成功率、首字与状态，不含 Base URL、Key 与请求头规则；上游 Key 在任何读取接口都不回显。
- 渠道的并发、每分钟请求数、冷却、连续失败计数保存在进程内（单实例）；每日收入与 Key 预算已用额从数据库汇总并缓存。
- 自己的渠道调用手续费为 0，消费者与共享者是同一个账本账户，账本不允许一笔交易中同一账户出现两次，因此这类调用不产生账本交易（`calls.ledger_tx_id` 为空，费用仍记录）；Feature G 的记账核对与补记排除它们。

## C2C 卖单市场（Feature C）

积分的托管全部落在系统账户 `c2c_escrow`（ADR-0025）。`internal/postgres/c2cpg` 实现 `c2c.Store`：每个状态转换在一个数据库事务内完成加锁、状态检查、数量更新和 `ledgerpg.Post` 过账；纯规则在 `internal/c2c/machine.go`，持久化只负责加锁、读取、写入。

| 动作 | 过账（幂等键） | 数量变化 |
| --- | --- | --- |
| 挂单 | `c2c_list`：卖家 −总量，托管 +总量（`c2c:order:<id>:list`） | available = total |
| 买入 | 无 | available −= 数量，in_trade += 数量 |
| 放行 / 判给买家 | `c2c_release`：托管 −数量，买家 +数量（`c2c:trade:<id>:release`） | in_trade −= 数量，sold += 数量；开放且 available、in_trade 均为 0 时订单为 filled |
| 取消 / 超时 / 退回卖家（订单开放） | 无 | in_trade −= 数量，available += 数量 |
| 取消 / 超时 / 退回卖家（订单已关闭） | `c2c_return`：托管 −数量，卖家 +数量（`c2c:trade:<id>:return`） | in_trade −= 数量，closed += 数量 |
| 关闭卖单 | `c2c_return`：托管 −可买量，卖家 +可买量（`c2c:order:<id>:close`） | closed += available，available = 0，状态 closed |

- **恒等式**：`total = available + in_trade + sold + closed` 由数据库 CHECK 保证；`c2c_escrow` 余额始终等于所有订单 `available + in_trade` 之和（Feature G 做实时核对，集成测试在每条路径后断言）。
- **加锁顺序**：订单行 → 交易行 → 账本账户行（`Post` 内按 id 升序）。挂单先锁卖家账本账户再检查「余额 ≥ 总量」，并发挂单不会重复花同一笔余额；并发买入在订单行锁上串行，不会超卖，同一买家同一卖单的未完成交易唯一性在同一锁内检查。
- **幂等**：状态转换重复请求返回当前状态、不重复记账（放行已放行的交易、取消已取消的交易等）；挂单与买入带 `Idempotency-Key` 时，订单、交易 ID 由卖家/买家与该键派生，重复请求返回同一对象。
- **收款方式**：JSON 经 `C2C_PRIVATE_DATA_KEYRING` 以订单 ID 为附加数据加密保存；列表只解密出渠道名称，账号只在交易详情中按可见性规则返回（买家仅在交易未结束时）。
- **超时任务**：`cmd/server` 启动后每分钟调用 `Service.ExpireDue`，取消 `awaiting_payment` 且已过 `payment_deadline` 的交易（每批 100 笔，逐笔独立事务，锁内复查状态）；`paid`、`disputed` 不受影响。进程重启后下一轮继续处理。
- **仲裁**：管理员判给买家复用放行路径（`resolved_to_buyer`），退回卖家复用取消路径（`resolved_to_seller`）；原因必填并写入审计 `c2c.resolve`。

## 已确认但未实现的目标边界

当前没有。

## 架构原则

- 以当前源码和有效配置为事实依据，文档不得超前描述尚未实现的能力。
- 影响系统边界、数据模型、部署方式、安全模型或长期维护成本的重要决定，写入 `docs/adr/`。
- 架构变化必须同步更新本文档；只记录历史背景的内容保留在 ADR 中。

## 尚待确定

- 最后一名启用管理员遗忘密码时的凭据恢复流程（ADR-0013）。
- 生产数据库连接池容量与高可用策略；备份、恢复、容量、延迟、可用性和告警的数值目标。

### token 细分计价（ADR-0030）

四桶汇总仍用于统计；细分 token 是其互斥子集。ledger 扣除可靠细分，再按细分价或命中组的通用价计算，分项合并后统一舍入。catalog 的 token_prices 以纳积分 JSONB 存储，API 返回十进制字符串；未配置和显式零不同。条件档记录带协议前缀的 service_tier 与适用百炼模型的实际思考模式。Anthropic 实际 usage.speed=fast 优先映射为 anthropic:fast 计价条件；采集器分别保留真实服务档位和速度，缺失速度的后续帧保留前值，显式非 fast 速度恢复服务档位。请求速度不参与计价。

调用 price_snapshot 持久化当次细分价、实际档位、细分用量、通用余量说明和工具提示观察值；历史不查现价重算。只记录思考标记，不保留思考正文。余额与预算依旧只检查、不预留。当前未覆盖收费维度见 PRODUCT.md。
