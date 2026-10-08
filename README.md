# Oh-My-AIHub

Oh-My-AIHub 是面向受邀小圈子的 API 资源共享与内部积分清算平台。用户可以共享自己已经充值的 API 中转渠道，消费者通过平台 API Key 聚合多个渠道并按优先级故障转移；成功调用使用中心化零和账本结算，共享者可以在 C2C 市场挂卖单出售所得积分。

MVP 已完成，涵盖公开产品入口、受邀账户、模型目录、API 渠道共享、平台 Key 与四协议代理、积分结算和 C2C 卖单市场。后续直接基于需求和现有代码迭代。已确认需求见 `PRODUCT.md`，推进顺序见 `ROADMAP.md`，当前系统结构见 `ARCHITECTURE.md`。

## 文档导航

- [产品说明](PRODUCT.md)：产品目标、用户、范围与需求。
- [产品路线图](ROADMAP.md)：面向用户结果的优先级、证据视野与推进顺序。
- [架构说明](ARCHITECTURE.md)：当前系统结构、边界与技术决策。
- [变更日志](CHANGELOG.md)：面向版本与使用者的重要变化。
- [Agent 协作说明](AGENTS.md)：所有 Agent 在本仓库中的工作规则。
- [安全策略](SECURITY.md)：漏洞报告方式与安全基线。
- [架构决策记录](docs/adr/README.md)：需要长期保留的架构决策及其背景。

开始工作前，应先阅读 `AGENTS.md`，再根据任务阅读并维护相关文档。

## 任务管理

开发任务通过 GitHub Issue 管理：小任务使用单个 Feature Issue，大任务使用 Epic Issue 拆分多个 Feature Issue。每个 Issue 的最新 Spec、Plan、Tasks 和 Acceptance 都维护在 body 中。具体工作方式见 `AGENTS.md`。

## 产品研发方式

项目采用“人类定向、AI 执行”的持续产品研发模型：

- 人类从用户视角负责目标用户、核心问题、产品方向、关键取舍和发布判断。
- AI 负责研究、假设整理、工程实现、测试、文档、发布准备与反馈归纳。
- 工作按方向与结果、发现、直接开发、验收、发布形成闭环。
- 任务区分 Ready 与 Done，不设用户验证环节（见 ADR-0022）。
- 高不确定产品能力先用发现或原型 Feature 降低风险，再进入小批量交付；纯技术或已知小改动保持单个 Feature 的轻量流程。

完整职责、检查点、拆分和证据规则见 `AGENTS.md`；稳定产品事实维护在 `PRODUCT.md`，结果优先级与推进顺序维护在 `ROADMAP.md`，界面以现有组件和样式为基础持续迭代。

## 直接开发

MVP 已完成，后续需求明确后直接基于现有代码实施，无需独立设计阶段、OpenPencil、设计源文件或预览。保留 Issue、独立 worktree、聚焦验证与 PR 验收流程；界面变化通过实际运行结果验收。

现有界面沿用了第三方设计分析中的部分规则，其 MIT 许可保存在 [第三方许可](licenses/airtable-design-analysis-MIT.txt)。该许可仅用于来源归属，不构成开发前置设计规范。

## 当前工程组成

- 前端：React 19、TypeScript、Vite、React Router 与 TanStack Query。
- 后端：Go HTTP 服务；当前实现受邀账户与会话、零和账本核心、模型目录与条件价格档、平台设置、审计与用户积分接口。
- 数据库：PostgreSQL 18，使用 Goose 管理嵌入式 SQL 迁移。
- 本地工具链：mise。
- 容器运行：Docker Compose，前端由 Nginx 提供静态资源并代理 `/api` 与外部模型 API 请求，迁移完成后再启动后端。

产品已按 [Epic #170](https://github.com/NexusAgentX/Oh-My-AIHub/issues/170) 原地重写为「API 市场 + 积分 C2C 市场」（v0.7.0）。`backend/api/openapi.yaml` 是前后端唯一契约；产品事实见 `PRODUCT.md`，系统结构见 `ARCHITECTURE.md`。

模型目录四类基准价每项允许 `0～100000` 积分/百万 token，最多九位小数；渠道倍率允许 `0～1000` 倍。

## 环境要求

- [mise](https://mise.jdx.dev/)
- 支持 Compose 的 Docker 环境

## 本地开发

安装工具链和前端依赖：

```bash
mise install
mise run install
```

启动数据库并执行迁移：

```bash
mise run dev-database
```

迁移只有一份基线 `0001_baseline.sql`，产品重写时已原地重写（ADR-0024、ADR-0026）。模型细分价格（#190）也在该基线中新增列。更早创建的开发数据库与它不兼容，须先用 `docker compose down -v` 删除数据卷再重新启动。

首次运行时，启动后端与前端后访问 `/initialize`，在网页上创建唯一的初始管理员。

启动后端：

```bash
export UPSTREAM_CREDENTIAL_KEYRING='v1=<32 字节密钥的 Base64>'
export UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID='v1'
export C2C_PRIVATE_DATA_KEYRING='v1=<另一把 32 字节密钥的 Base64>'
export C2C_PRIVATE_DATA_ACTIVE_KEY_ID='v1'
mise run dev-backend
```

`UPSTREAM_CREDENTIAL_KEYRING` 使用逗号分隔的 `key-id=base64-key`，每把密钥解码后必须正好 32 字节。已有密文引用的旧密钥必须继续保留；密钥环和数据库备份必须配套保存，不能每次启动临时生成。服务启动时校验密钥环格式；渠道的上游 Key 与平台 API Key 使用它加密。默认只允许上游 HTTPS 443 端口；如确需其他端口可用 `UPSTREAM_ALLOWED_PORTS` 显式追加，额外禁用域名可用 `UPSTREAM_BLOCKED_HOSTS` 追加。`api.openai.com` 及其子域永久禁用，不能通过配置解除。

`C2C_PRIVATE_DATA_KEYRING` 采用相同的 `key-id=base64-key` 语法，但必须使用与上游凭据不同的密钥，用于加密 C2C 收款方式文字（Feature C）。服务启动时校验密钥环格式；任何仍被库存引用的旧密钥都必须保留。该密钥环同样必须与数据库备份配套保存，不得每次启动临时生成。

在另一个终端启动前端：

```bash
mise run dev-frontend
```

前端开发服务器位于 <http://localhost:5173>，公开落地页位于 <http://localhost:5173/>（`/welcome` 同样可达），并将 `/api`、`/v1/chat/completions`、`/v1/responses`、`/v1/messages` 和 `/v1beta/models/...` 请求代理到 <http://localhost:8080>。后端改用其他端口（`PORT`）时，可用 `AIHUB_BACKEND_ORIGIN=http://127.0.0.1:<端口>` 覆盖 Vite 的代理目标。

外部模型 API 入口（OpenAI Chat、OpenAI Responses、Anthropic Messages、Gemini 与两个模型列表）已实现：用首页给出的平台 API Key 指向后端地址即可调用，请求按原生格式透传，不做格式转换。

本地开发默认不信任客户端提供的转发头。Compose 通过 `BACKEND_TRUSTED_PROXY_CIDRS` 配置后端可采信的内部 Nginx 源网段；未配置时后端忽略全部转发头。外层代理到 Nginx 的信任边界使用 `TRUSTED_PROXY_CIDR` 单一网段配置。

## 查看指标

后端在独立的内网端口提供 Prometheus 指标（`METRICS_ADDR`，默认 `:9090`，只有 `GET /metrics`）。Compose 只在容器网络内 `expose` 该端口，不发布到宿主机，Nginx 也不代理；同一 Compose 网络内的 Prometheus 可以抓取 `backend:9090`。本地开发时直接访问：

```bash
curl http://127.0.0.1:9090/metrics | grep '^aihub_'
```

指标不带用户或 Key 标签：`aihub_requests_total`、`aihub_active_requests`、`aihub_upstream_attempts_total`、`aihub_upstream_latency_seconds`、`aihub_first_token_latency_seconds`、`aihub_inter_token_latency_seconds`、`aihub_tokens_total`、`aihub_cost_points_total`、`aihub_channel_up`、`aihub_channel_cooldowns_total`，以及积分与核对类的 `aihub_points_*`、`aihub_ledger_imbalance`（应恒为 0）、`aihub_ledger_transactions_total`、`aihub_ledger_amount_total`、`aihub_settlement_failures_total`、`aihub_reconciliation_failed{check}`。积分类指标每分钟计算一次。

## Docker Compose 运行

本机 HTTP 开发使用显式开发入口：

```bash
mise run up-dev
```

访问 <http://localhost:3000>。`mise run up-dev` 和 `mise run up` 都要求显式提供两套互不复用的 `UPSTREAM_CREDENTIAL_*` 与 `C2C_PRIVATE_DATA_*` 密钥环变量。面向 HTTPS 环境使用 `mise run up` 时，该入口不会关闭 Secure Cookie，并额外强制要求独立的 `POSTGRES_PASSWORD`、`TRUSTED_PROXY_CIDR` 与 `BACKEND_TRUSTED_PROXY_CIDRS`。Compose 只把前端绑定到宿主机回环地址 `127.0.0.1:${FRONTEND_PORT:-3000}`；唯一公网入口应是同机 TLS 反向代理，不要另行暴露该明文端口。

`TRUSTED_PROXY_CIDR` 必须填写前端容器实际观察到的 TLS 代理源地址，而不是想当然地使用 `127.0.0.1/32`；Docker NAT 后该地址会因运行环境而异。可以先在受限环境以 `mise run up-dev` 启动，让同机代理发起一次请求，再从 `docker compose logs frontend` 的访问日志首列取得源 IP，并以最窄的 CIDR（通常为单地址 `/32` 或 `/128`）配置安全入口。

`BACKEND_TRUSTED_PROXY_CIDRS` 应填写当前 Compose 项目网络的实际子网。可先运行 `docker compose create` 只创建容器与网络，再通过 `docker inspect` 取得前端容器所连接的 Network ID，并用 `docker network inspect` 读取其 IPAM 子网；不要硬编码假定的 `172.16.0.0/12`。安全栈启动后运行以下自检；返回 `proxy trust check passed` 才说明“宿主 TLS 代理 → Nginx → Go”整条 HTTPS `Origin` 写入链路可用：

```bash
mise run check-proxy-trust
```

数据库密码通过 `PGPASSWORD` 传给客户端，可以包含 URL 保留字符，无需手动 URI 编码；仓库默认值只用于本机开发。停止服务：

```bash
mise run down
```

## 验证

```bash
mise run test
mise run check-sqlc # 修改 queries.sql 或迁移后，先 mise run generate 重新生成并提交
mise run check-api-types # 修改 backend/api/openapi.yaml 后，先 mise run generate 重新生成前端 API 类型并提交
mise run test-backend-integration
docker compose config --quiet
mise run check-proxy-trust # 需要已按上文启动安全栈
```

## 目录结构

```text
.
├── backend/       Go 后端
├── frontend/      React 前端
├── scripts/       数据库加密备份与隔离恢复演练脚本
├── docs/adr/      架构决策记录
├── docs/runbooks/ 部署、发布、备份恢复与故障处理操作手册
├── licenses/      第三方许可
├── compose.yaml   容器编排配置
└── mise.toml      工具版本与常用任务
```
