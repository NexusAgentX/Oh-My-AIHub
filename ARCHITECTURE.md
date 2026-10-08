# 架构说明

> 状态：产品重写中（[Epic #170](https://github.com/NexusAgentX/Oh-My-AIHub/issues/170)）。Feature A（#171）已交付新数据库基线、完整 OpenAPI 契约、身份与会话、账本核心、模型目录、平台设置与审计；网关、渠道、Key、C2C 与观测接口已在契约中定义并返回 501，由后续 Feature 实现。

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
| 前端 | `frontend/` | React 单页应用（TanStack Query、`src/styles/tokens.css` 设计 token、`src/ui/` 基础组件、`src/ui/Icon.tsx` 图标集，见 ADR-0019）。外壳为 `src/layouts/`：桌面左侧分组侧栏（使用 API / 共享 / 积分，底部账户菜单与余额），<768px 为顶部条 + 底部 5 个 Tab（首页、模型、渠道、积分、我的）。用户页面按领域分目录，查询在各自 `queries.ts`，类型来自 `schema.gen.ts`：`home/`（`/home`）、`models/`（`/models`、`/models/:model`，含账号级与 Key 级共用的路由编辑器）、`keys/`（`/keys` 与抽屉）、`usage/`（`/usage`）、`channels/`（`/channels`、`/channels/new` 三步向导、`/channels/:id`）、`points/`（`/points` 各 Tab、`/points/trades/:id`、买卖抽屉）、`account/`（`/account`、移动端 `/me`）。`src/calls/` 是调用观测的共享组件（列表、筛选、汇总、SSE 实时 hook、调用详情抽屉与尝试时间线），按接口路径参数化，供用户用量、渠道编辑页与管理后台复用。依赖尚未实现（501）接口的区块显示可重试的错误态。公开落地页、实例初始化、登录与首次改密沿用原流程；管理后台由 Feature E 重建 |
| 后端 | `backend/` | Go `net/http` 服务。`cmd/server` 组装服务并在启动时校验 `UPSTREAM_CREDENTIAL_*`、`UPSTREAM_*` 出站配置与 `C2C_PRIVATE_DATA_*` 密钥环；`cmd/migrate` 执行迁移 |
| API 契约 | `backend/api/openapi.yaml`、`backend/internal/api/` | OpenAPI 3.1 是唯一契约（ADR-0021），定义约 70 个 JSON 接口与 6 个外部模型 API 入口；`x-access` 声明门禁，`x-feature` 标明负责实现的 Feature。`internal/api` 的 `router` 按 access 包裹会话、首次改密与管理员门禁；未实现的路由经 `planned` 注册，保留门禁并返回 `501 {"error":"not_implemented"}`。契约测试逐项对照路由表、门禁、Feature 与实现状态，并用规范 schema 校验每个真实响应。前端类型由它生成为已提交的 `frontend/src/api/schema.gen.ts` |
| 数据库 | PostgreSQL 18 | 18 张表，见下文“数据与状态” |
| 持久化分层 | `backend/internal/postgres/`、`backend/sqlc.yaml` | 每个领域一个 `<domain>pg/`（`queries.sql` + sqlc 生成代码 + 领域 Store，ADR-0017）：`identitypg`、`catalogpg`、`settingspg`、`auditpg`、`ledgerpg`；共享事务辅助在 `pgkit`。组合根 `postgres.Store` 以字段持有各领域 Store。跨领域原子提交由调用方持有 `pgx.Tx`（ADR-0020）：创建账户在同一事务内写身份行、用户账本账户与审计；账本过账 `ledgerpg.Post(ctx, tx, …)` 总在调用方事务内执行 |
| 数据库迁移 | `backend/internal/database/migrations/`、`backend/cmd/migrate/` | 只有一份原地重写的基线 `0001_baseline.sql`（ADR-0024、ADR-0026） |
| 开发任务 | `mise.toml` | 固定工具版本并提供安装、开发、测试、生成与构建命令 |
| 容器编排 | `compose.yaml` | 运行 PostgreSQL、一次性迁移、后端与前端 |
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
| `internal/channel` | 目前只保留上游凭据版本化密钥环（ADR-0009）与固定出站策略（HTTPS、DNS/IP 校验、端口白名单、禁用主机、禁止重定向）；渠道领域由 Feature B 在其上重建 |
| `internal/c2c` | 目前只保留 C2C 私密数据密钥环；卖单与交易由 Feature C 重建 |
| `internal/secretguard` | 凭据泄露检测辅助 |

## 当前请求链路

1. 浏览器加载 React 应用；`/`、`/welcome` 与未知路径显示公开落地页；实例尚无管理员时前端引导到 `/initialize`。
2. 开发环境由 Vite、Compose 环境由 Nginx 把 `/api`、`/v1`、`/v1beta` 代理到后端。
3. 后端中间件链为“写超时 → 安全头 → 同源校验 → mux”；只对可信内部代理采信转发头，对非安全方法校验同源 `Origin`（`/v1`、`/v1beta` 外部入口除外）；路由层执行会话、首次改密与管理员门禁。
4. 错误统一为 `{"error": "<code>", "message": "<中文>"}`；列表统一游标分页（`cursor`、`limit` → `items`、`next_cursor`）；响应带 `Cache-Control: no-store`。
5. 外部模型 API 入口（`POST /v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1beta/models/{model}`，`GET /v1/models`、`/v1beta/models`）已登记路由，当前返回 501。

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
- 用户只能读取本人积分与账单；账单按分录 id 倒序游标分页，可按交易类型、Key、时间筛选，`format=csv` 由 Feature G 实现（当前 501）。
- 上游凭据密钥环与出站策略在启动时校验配置，供 Feature B 使用；C2C 私密数据密钥环同样在启动时校验。

## 已确认但未实现的目标边界

以下由后续 Feature 实现，契约已在 `openapi.yaml` 中定义：

- Feature B：透明网关（换鉴权、可选换 UA 与请求头规则、字节级替换顶层 `model`、OpenAI Chat 流式补 `include_usage`）、按格式选渠道、按用户按模型路由、无预扣的事后一次记账、渠道发现与格式测试、API Key 可逆加密与预算、首页、管理员渠道治理。
- Feature C：C2C 卖单、托管账户过账、部分成交、付款超时、申诉与仲裁。
- Feature G：调用与用量查询、SSE 实时流、渠道统计、Prometheus 指标、管理员概览与积分全局、账本交易浏览与调用修复、积分走势与 CSV 导出。

## 架构原则

- 以当前源码和有效配置为事实依据，文档不得超前描述尚未实现的能力。
- 影响系统边界、数据模型、部署方式、安全模型或长期维护成本的重要决定，写入 `docs/adr/`。
- 架构变化必须同步更新本文档；只记录历史背景的内容保留在 ADR 中。

## 尚待确定

- 最后一名启用管理员遗忘密码时的凭据恢复流程（ADR-0013）。
- 生产数据库连接池容量与高可用策略；备份、恢复、容量、延迟、可用性和告警的数值目标。
