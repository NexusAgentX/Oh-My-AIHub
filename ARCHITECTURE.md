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
| 后端 | `backend/cmd/server` | 组装依赖、启动时校验密钥环与出站配置、运行后台任务 |
| 迁移 | `backend/cmd/migrate`、`backend/internal/database/migrations/` | Goose 嵌入式编号迁移，已发布迁移只追加 |
| API 契约 | `backend/api/openapi.yaml` | OpenAPI 3.1，前后端唯一契约（ADR-0021） |
| 编排与工具 | `compose.yaml`、`mise.toml` | 数据库、一次性迁移、后端、前端；工具版本与常用任务 |
| 运维 | `scripts/`、`docs/runbooks/` | 加密备份与恢复演练；部署、发布、事故处理 |

后台任务均在 `cmd/server` 内运行：网关超时调用清理与 C2C 付款超时（每分钟）、指标刷新（每分钟）、论坛未绑定附件清理（每小时）、上游原始错误文本清理（每天）。

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
| `identity` | 受邀账户、Argon2id 密码、服务器端会话、首次改密、管理员管理账户（ADR-0007、ADR-0013） |
| `ledger` | 交易类型与校验、系统账户、积分与账单读取、调账与坏账核销、计价公式（ADR-0012、ADR-0030） |
| `catalog`、`settings` | 模型目录与条件价格档；单行平台设置 |
| `channel` | 渠道与渠道模型、上游 Key 加密、模型发现与格式测试、上游凭据密钥环与固定出站策略（ADR-0009） |
| `apikey`、`routing` | 平台 API Key（ADR-0028）；按用户按模型的路由偏好，账号级与 Key 级 |
| `gateway` | 透明网关（ADR-0027）：请求体扫描与 `model` 替换、旁路用量解析、候选排序、进程内渠道状态、事件总线 |
| `observe`、`metrics` | 现算观测、对账核对与补记、SSE 推送；Prometheus 指标（ADR-0029） |
| `c2c` | 卖单市场纯状态机与服务、C2C 私密数据密钥环 |
| `forum` | 板块、主题、回复、工单与附件校验（ADR-0031） |
| `audit`、`money`、`localtime`、`ids`、`secretguard` | 审计读取；九位定点金额；Asia/Shanghai 记账日历；UUID；凭据泄露检测 |

## API 约定

- 中间件链：写超时 → 安全头 → 同源校验 → 路由；只对配置的可信代理采信转发头；路由按 `x-access` 套上会话、首次改密与管理员门禁。
- 契约测试对照路由表与 OpenAPI，并用规范 schema 校验处理器与集成测试中的每个响应；`internal/api` 的覆盖门禁要求每个接口至少有一个经过校验的成功响应（外部模型 API 透传除外）；前端类型生成为 `frontend/src/api/schema.gen.ts`，CI 检查漂移。
- 错误统一为 `{"error": "<code>", "message": "<中文>"}`；列表统一游标分页（`cursor`、`limit` → `items`、`next_cursor`）；响应带 `Cache-Control: no-store`；记账类写请求接受 `Idempotency-Key`。
- 外部模型 API（`/v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1beta/models/...` 与模型列表）凭平台 API Key 认证，不走会话与同源校验。

## 网关一次请求

1. **认证与开调用**：按 Key 摘要查找；认出 Key 后每次请求写一行 `calls`（先 `in_progress`），响应带 `X-AIHub-Request-Id`。
2. **模型与额度**：别名 → Key 可用范围 → 启用的目录模型；余额与 Key 预算只检查、不预留、不冻结。
3. **选渠道**：候选为格式匹配的上架渠道，剔除取消勾选、冷却中和超过并发/RPM/当日收入上限的，再按路由模式（便宜/稳定/快速/手动）排序。
4. **透传与回退**：只替换鉴权与顶层 `model`，按需补 `include_usage`，应用渠道请求头规则与 UA，不做格式转换；尚未向客户端写出字节前，连接失败、首字超时与可重试状态码换下一个候选。
5. **回写与记账**：响应原样逐块回写，旁路读取用量；结束时在一个事务内关闭调用并过账，价格快照随调用保存，历史不按现价重算。

并发、RPM、冷却、限流和 Argon2 工作槽是进程内状态，当前按单实例部署设计。

## 数据与不变量

- **单位**：金额为 `bigint` 纳积分（1 积分 = 1e9），人民币为整数分，费率为纳比率，时间为 `timestamptz`（ADR-0006）。
- **账本**（ADR-0005、ADR-0025）：`ledgerpg.Post` 是唯一过账路径；幂等键唯一，重复提交返回原交易；按账本账户 id 升序加行锁；提交时由延迟约束触发器要求每笔交易至少两条分录且合计为 0；交易与分录不可修改、删除。系统账户为 `platform_revenue`、`c2c_escrow`、`bad_debt`。调用自己的渠道不产生账本交易（同一账户不能在一笔交易中出现两次），费用仍记在调用上，核对与补记排除这类调用。
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
- 五项核对：全部余额合计为 0；每个账户余额等于分录合计；托管余额等于卖单 `available + in_trade`；每个应计费的成功调用都已记账；每笔放行交易都已记账。管理员可对漏记调用补记或作废，与正常记账使用同一幂等键。
- `observe.Feed` 订阅网关事件总线，向 SSE 订阅者与 Prometheus 扇出；慢订阅者丢消息而不阻塞网关。指标不带用户或 Key 标签，端口只在内网。

## 论坛与工单

`forum` 与 `forumpg` 实现板块、主题、回复与私密工单；工单只对提交者和管理员可见，主题类型不可变。附件存 PostgreSQL `bytea` 并继承内容权限（ADR-0031）。Markdown 由 Vditor 编辑，阅读与预览共用 Lute 与 DOMPurify 白名单（ADR-0032）。限额与权限细节见 SECURITY.md。

## 前端

- TanStack Query 管理服务端状态，类型来自生成的 `schema.gen.ts`；`src/ui/` 基础组件与 `src/styles/tokens.css` 设计 token（ADR-0019）。
- 按领域分目录：`home`、`models`、`keys`、`usage`、`channels`、`points`、`account`、`forum`、`welcome`（公开落地页）；`src/calls/` 为用户、渠道与后台共用的调用观测组件；`src/markdown/` 封装编辑与渲染。
- `src/admin/` 是独立外壳的管理后台；管理后台与论坛按路由懒加载。账号变化时清空查询缓存。
- 外壳：桌面左侧分组侧栏；窄屏为顶部条加底部 Tab。

## 待定事项

- 最后一名启用管理员遗忘密码时的恢复流程（ADR-0013）。
- 数据库连接池容量与高可用；备份恢复、延迟、可用性与告警的数值目标。
