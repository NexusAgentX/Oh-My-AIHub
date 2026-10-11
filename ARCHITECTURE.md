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
| 后端 | `backend/cmd/server` | 组装依赖、启动时校验密钥环与出站配置并解密全部存量密文、运行后台任务 |
| 迁移 | `backend/cmd/migrate`、`backend/internal/database/migrations/` | Goose 嵌入式编号迁移，已发布迁移只追加 |
| API 契约 | `backend/api/openapi.yaml` | OpenAPI 3.1，前后端唯一契约（ADR-0021） |
| 编排与工具 | `compose.yaml`、`mise.toml` | 数据库、一次性迁移、后端、前端；工具版本与常用任务 |
| 运维 | `scripts/`、`docs/runbooks/` | 加密备份与恢复演练；部署、发布、事故处理 |

后台任务均在 `cmd/server` 内运行：网关超时调用清理、C2C 付款超时与美元汇率自动同步检查（每分钟，到点每天同步一次）、指标刷新（每分钟）、论坛未绑定附件清理（每小时）、上游原始错误文本清理（每天）。系统按单实例部署，运行时状态放在进程内（ADR-0039）。

## 后端分层

```text
internal/api（HTTP 适配、门禁、错误与分页约定）
   ↓
internal/<domain>（校验、状态机、服务；不依赖数据库）
   ↓
internal/postgres/<domain>pg（queries.sql + sqlc 生成代码 + Store，ADR-0017）
```

- 组合根 `postgres.Store` 持有各领域 Store；事务辅助在 `pgkit`。
- 跨领域原子提交由调用方持有 `pgx.Tx`（ADR-0020）；`ledgerpg.Post` 总在调用方事务内执行，审计 `auditpg.Record` 与业务写入同事务。

| 包 | 职责 |
| --- | --- |
| `identity` | 受邀账户、Argon2id 密码、服务器端会话、首次改密、管理员管理账户（ADR-0007、ADR-0013、ADR-0037） |
| `ledger` | 交易类型与校验、系统账户、积分与账单读取、调账与坏账核销、计价公式（ADR-0012、ADR-0030） |
| `catalog`、`settings` | 模型目录（含计价币种）与条件价格档；单行平台设置与美元汇率同步（ADR-0038） |
| `channel` | 渠道与渠道模型、上游 Key 加密、模型发现与格式测试、上游凭据密钥环与固定出站策略（ADR-0036） |
| `apikey`、`routing` | 平台 API Key（ADR-0028）；按用户按模型的路由偏好，账号级与 Key 级 |
| `gateway` | 透明网关（ADR-0035）：请求体扫描与 `model` 替换、旁路用量解析、候选排序、进程内渠道状态、事件总线 |
| `observe`、`metrics` | 现算观测、对账核对与补记、SSE 推送；Prometheus 指标（ADR-0029） |
| `c2c` | 卖单市场纯状态机与服务、C2C 私密数据密钥环 |
| `keyringcheck` | 启动时逐条解密仍可能被读取的密文（未删除渠道与平台 API Key、全部 C2C 卖单），缺失或损坏的密钥拒绝启动；读取接口由各 `<domain>pg` 实现 |
| `forum` | 板块、主题、回复、工单与附件校验（ADR-0031） |
| `audit`、`money`、`localtime`、`ids`、`secretguard` | 审计读取；九位定点金额；Asia/Shanghai 记账日历；UUID；凭据泄露检测 |

## API 约定

- 中间件链：写超时 → 安全头 → 同源校验 → 路由；只对配置的可信代理采信转发头；路由按 `x-access` 套上会话、首次改密与管理员门禁。
- 契约测试对照路由表与 OpenAPI，并用规范 schema 校验处理器与集成测试中的每个响应；`internal/api` 的覆盖门禁要求每个接口至少有一个经过校验的成功响应（外部模型 API 透传除外）；前端类型生成为 `frontend/src/api/schema.gen.ts`，CI 检查漂移。
- 响应用 `internal/api` 内手写的结构体表示，与处理器同文件，按 OpenAPI schema 命名为 `<schema>JSON`（`ApiKey` → `apiKeyJSON`），不用 `map[string]any` 拼响应；规范要求存在的字段（含 `null`）不加 `omitempty`，空列表初始化为 `[]`，金额为十进制字符串；领域类型不为响应添加 json 标签（只有持久化 jsonb 与请求解码用的类型保留）。网关存下的价格快照与审计 `detail` 是已存储的 jsonb，以 `json.RawMessage` 原样透传，是唯一不逐字段定义的响应内容；实时流（SSE）事件载荷与对应列表项使用同一组类型。
- 平台接口的错误统一为 `{"error": "<code>", "message": "<中文>"}`；列表统一游标分页（`cursor`、`limit` → `items`、`next_cursor`）；响应带 `Cache-Control: no-store`；记账类写请求接受 `Idempotency-Key`。
- 外部模型 API（`/v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1beta/models/...` 与模型列表）凭平台 API Key 认证，不走会话与同源校验。网关自己产生的错误按客户端协议返回官方 SDK 能解析的格式（`internal/gateway/errors.go`：`/v1beta/` 为 Gemini 的 AIP-193，`/v1/messages` 或带 `anthropic-version` 为 Anthropic，其余为 OpenAI），状态码不随协议变化，平台错误码保留在 OpenAI 的 `code`、Gemini 的 `ErrorInfo.reason` 里；上游错误原样透传。

## 网关一次请求

1. **认证与开调用**：按 Key 摘要查找；认出 Key 后每次请求写一行 `calls`（先 `in_progress`），响应带 `X-AIHub-Request-Id`。
2. **模型与额度**：别名 → Key 可用范围 → 启用的目录模型；余额与 Key 预算只检查、不预留、不冻结。检查通过后账号占一个在途名额（`gateway.Inflight`，进程内按账号计数，上限取自平台设置 `account_max_inflight_calls`，0 为不限）；超限以 429 `too_many_inflight_calls` 拒绝并记 `rejected_inflight`，不访问上游、不收费。名额由 `defer` 在 `Serve` 返回时释放，错误与 panic 路径同样；客户端断开后的续读与记账都在 `Serve` 内同步完成，所以续读期间名额仍被占用，记账后才归还。以后若有在 `Serve` 返回之后才结束的工作，释放要跟着移到它结束的地方。
3. **选渠道**：候选为格式匹配的上架渠道，剔除取消勾选、冷却中和超过并发/RPM/当日收入上限的，再按路由模式（便宜/稳定/快速/手动）排序；快速模式用的首字中位数只统计流式调用。
4. **透传与回退**：只替换鉴权与顶层 `model`，按需补 `include_usage`（`EnsureIncludeUsage` 同时返回客户端原本是否已开启，没开启则记 `call.usageInjected`），应用渠道请求头规则与 UA，不做格式转换；尚未向客户端写出字节前，连接失败、首字超时与可重试状态码换下一个候选；首字超时只用于流式请求，非流式响应要等生成结束才有首字节，只受总超时约束。其余 4xx 默认视为请求本身的问题，原样返回、不计渠道失败；例外是响应体表明模型不存在或不可用（`gateway.ModelUnavailable`，规则见 PRODUCT.md「4xx 与回退」），按上游错误处理，错误码 `upstream_model_unavailable`。因此这类 4xx 的响应体（最多 1MB）先读进内存再判断，不命中时写出缓冲内容，状态码、响应头与字节与上游一致。
   超时全部是请求 context 上的计时器，出站客户端自己不设 `Timeout`（ADR-0035 规则 5，出站连接因此可以跨请求复用）：非流式是整次请求的总超时（`timeout_total`，尚未写出字节，可回退）；流式在首字节前是首字超时，之后是相邻两次读到数据的间隔（空闲超时，每次读到数据重置）；整个调用另有从开始算起的上限 `callTimeLimit`（60 分钟，含全部尝试）。兜底任务的 61 分钟时限由它加 1 分钟余量得出，两处常量互相注释，改一处要同步另一处。
5. **回写与记账**：响应原样逐块回写，旁路读取用量；结束时在一个事务内关闭调用并过账，价格快照随调用保存，历史不按现价重算。首字节之后的中断不再回退：空闲超时、总超时与读取错误记尝试 `interrupted`（`error_code` 为 `timeout_idle`、`timeout_total`、`read_error`）并计渠道失败；调用上限到期记 `interrupted` / `call_deadline`，不计渠道失败；两者都按已读到的用量结算。调用上限在首字节之前到期时，网关自己返回 504 `call_deadline`，不再尝试后面的候选。
   读到 `io.EOF` 只有在这次尝试没被取消时才算回答正常结束（`answerEnded`）。取消（调用上限、空闲或续读超时）会让传输层关闭连接，而关闭 TLS 连接先发 close_notify，对端（例如一直等着被取消的上游）可以抢在套接字真正关闭前发完分块响应体的结尾，阻塞在读上的一方会拿到最后几个字节和干净的 EOF 而不是错误；这种 EOF 按取消原因处理，不当成完成。
   客户端中途断开：开始回写之前（等响应、回退中）断开，立即取消上游，不收费；开始回写之后断开，网关停止写客户端、继续读上游，读到的字节只喂给旁路解析器（`clientWatch`），直到上游结束、读满 `drainWindow`（10 秒）、触发空闲超时或调用上限，再取消上游并按读到的用量结算，调用 `client_disconnected`。续读期间渠道并发槽位照常占用，上游出错与客户端断开都不计渠道失败，`response_bytes` 只算写给客户端的字节。客户端收完最后一帧就挂断、上游随后正常结束的调用不算断开，仍是 `succeeded`。
   替客户端开启了 `include_usage`（`usageInjected`）的 OpenAI Chat 流式响应，经 `usageFrameFilter` 去掉上游多发的那一帧：按空行（`\n\n`、`\r\n\r\n` 或混用）切分事件，只有“单个 `data:` 行、JSON 对象、`choices` 为空数组、`usage` 为非空对象”的事件被丢弃，其余字节（注释行、`data: [DONE]`、`usage` 在非空 `choices` 帧里的响应）逐字原样写出；未完整的事件先暂存，到边界再处理，流结束时剩余字节原样写出；单个事件超过 64 KiB 不再暂存。旁路解析器读取的仍是上游的全部原始字节，所以计费不变；`response_bytes` 只算写给客户端的字节。客户端自己开了 `include_usage`、非 SSE 响应和其他三种格式都不过滤。

并发、RPM、冷却、限流、账号在途计数和 Argon2 工作槽是进程内状态，当前按单实例部署设计。

渠道上游 Key 的明文缓存（ADR-0036 决定 8）：每次尝试通过 `Keyring.OpenCredential` 取 Key，同一份密文只解密一次，明文留在进程内存里。缓存归密钥环所有，每个渠道一条，只对解出它的那份密文（版本、key id、nonce、密文都相同）作答，所以渠道换 Key 或换用新密钥后旧条目自然失效；渠道服务在更新 Key、删除渠道写库成功后立即调用 `ForgetCredential` 清掉该渠道的条目。大小受渠道数约束，不需要淘汰；解密失败不缓存。平台 API Key、渠道探测和启动校验仍直接用 `Decrypt`，不进缓存。

冷却由连续失败阈值触发；上游 429 带 `Retry-After` 或 `retry-after-ms` 时不等阈值，`gateway.RateLimitCooldown` 解析并限定时长后由 `Runtime.CooldownFor` 直接冷却整个渠道，事件原因为 `rate_limited`（规则见 PRODUCT.md「自动冷却」）。

## 数据与不变量

- **单位**：金额为 `bigint` 纳积分（1 积分 = 1e9），人民币为整数分，费率为纳比率，时间为 `timestamptz`（ADR-0006）。美元模型的价格以纳美元存储，汇率 `settings.usd_cny_rate_nano` 为纳单位；计费在用量总额上一次性乘倍率与换算率（`ledger.ChannelRate`：开汇率优惠为 1，否则为汇率）并向上取整，便宜优先排序比较「倍率 × 换算率」。
- **账本**（ADR-0005、ADR-0025）：`ledgerpg.Post` 是唯一过账路径；幂等键唯一，重复提交返回原交易；按账本账户 id 升序加行锁；提交时由延迟约束触发器要求每笔交易至少两条分录且合计为 0；交易与分录不可修改、删除。系统账户为 `platform_revenue`、`c2c_escrow`、`bad_debt`；`platform_revenue` 既收手续费，也是管理员调账的对手方，所以它的余额不等于平台收入（口径见 `PRODUCT.md`）。调用自己的渠道不产生账本交易（同一账户不能在一笔交易中出现两次），费用仍记在调用上，核对与补记排除这类调用。
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
