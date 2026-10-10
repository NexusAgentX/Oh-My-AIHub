# Oh-My-AIHub

Oh-My-AIHub 是面向受邀小圈子的 API 资源共享与内部积分清算平台。用户可以共享自己已经充值的 API 中转渠道，消费者通过平台 API Key 聚合多个渠道并按优先级故障转移；成功调用使用中心化零和账本结算，共享者可以在 C2C 市场挂卖单出售所得积分。

当前提供受邀账户、模型目录、渠道共享、平台 API Key 与四种原生协议的透明网关、积分结算、C2C 卖单市场，以及成员论坛与私密工单。

## 文档导航

- [产品说明](PRODUCT.md)：产品定位、范围与规则。
- [产品路线图](ROADMAP.md)：接下来交付的结果与顺序。
- [架构说明](ARCHITECTURE.md)：系统结构、模块边界与不变量。
- [安全策略](SECURITY.md)：漏洞报告方式与安全边界。
- [变更日志](CHANGELOG.md)：各版本的重要变化。
- [Agent 开发指南](AGENTS.md)：任务流程、分支、验证、迁移与发版规则。
- [架构决策记录](docs/adr/README.md)：长期技术决策及其原因。
- [运维手册](docs/runbooks/)：部署、发布、备份恢复与故障处理。

## 技术栈

- 前端：React 19、TypeScript、Vite、React Router 与 TanStack Query。
- 后端：Go `net/http`、pgx 与 sqlc。
- 数据库：PostgreSQL 18，Goose 嵌入式编号迁移。
- 契约：`backend/api/openapi.yaml`（OpenAPI 3.1）是前后端唯一契约，前端类型由它生成。
- 工具与运行：mise 固定工具版本；Docker Compose 运行数据库、迁移、后端，以及由 Nginx 提供的前端。

现有界面沿用了第三方设计分析中的部分规则，其 MIT 许可见 [第三方许可](licenses/airtable-design-analysis-MIT.txt)。

## 环境要求

- [mise](https://mise.jdx.dev/)
- 支持 Compose 的 Docker 环境

## 本地开发

每个任务使用独立的 Git worktree：`mise run task-start <slug>` 同步 `main`，并创建 `codex/<slug>` 分支与 `../Oh-My-AIHub-worktrees/<slug>`；PR 合并后用 `mise run task-finish <slug>` 清理（规则见 `AGENTS.md`）。

安装工具链和前端依赖：

```bash
mise install
mise run install
```

启动数据库并执行迁移：

```bash
mise run dev-database
```

迁移为 Goose 嵌入式编号脚本，已发布的迁移只追加（见 `AGENTS.md`）。升级既有环境的注意事项见[发布手册](docs/runbooks/release.md)，不要通过删除生产数据卷处理升级。

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
mise run lint # 前端 oxlint（React Hooks 与正确性规则）与后端 staticcheck；CI gates 同样执行
mise run check-sqlc # 修改 queries.sql 或迁移后，先 mise run generate 重新生成并提交
mise run check-api-types # 修改 backend/api/openapi.yaml 后，先 mise run generate 重新生成前端 API 类型并提交
mise run test-backend-integration # 需要 Docker；直接 go test 且未设置 TEST_DATABASE_URL 时这些测试会跳过
mise run check-migrations # 已发布迁移（最新正式 tag）只允许新增，不得修改、删除或重命名
mise run check-migration-upgrade # 最新正式 tag 建库后，用当前代码升级到最新迁移版本；需要 Docker
mise run test-ci-scripts # 修改 CI 改动范围判定（scripts/ci-changes.sh）或汇总判定（scripts/ci-gate.sh）后运行
docker compose config --quiet
mise run check-proxy-trust # 需要已按上文启动安全栈
```

CI（`.github/workflows/ci.yml`）先由 `changes` 任务按改动文件判定范围（规则写在 `scripts/ci-changes.sh` 开头），再按需并行运行 `frontend`、`backend`、`integration`、`images`，最后由 `gates` 汇总：纯文档改动不运行检查，只改前端不跑后端与集成测试，只改后端不跑前端；`backend/api/openapi.yaml`、`.github/`、`mise.toml`、`scripts/`、`compose.yaml` 等共享输入变化时全部运行；推送到 `main` 始终全量运行。`main` 规则集的必需检查是 `gates` 与 `integration`：被跳过的任务在分支保护中按成功处理，`gates` 则核对每个应当运行的任务都成功，失败、取消或被连带跳过都会使 `gates` 失败。`images` 在 Dockerfile、`.dockerignore`、`frontend/nginx.conf`、依赖清单与锁文件，或 CI 与 Compose 定义变化时（只改 Dockerfile 或 `.dockerignore` 时仅运行 `images`），构建前后端镜像（仅 `linux/amd64`，不推送），并用渲染后的 Nginx 模板执行 `nginx -T` 校验；发版时的多架构构建见 `release.yml`。

CI 的 `integration` 任务使用 `postgres:18-alpine` 服务，以 `-race` 运行 `./internal/postgres` 与 `./internal/database` 的数据库集成测试，并执行上述两项迁移检查。CI 设置了 `AIHUB_REQUIRE_TEST_DATABASE=1`：缺少 `TEST_DATABASE_URL` 时集成测试失败而不是跳过；本地不设置该变量时仍然跳过。迁移检查依赖完整 Git 历史与 tag（浅克隆请先 `git fetch --tags --unshallow`）。

## 目录结构

```text
.
├── backend/       Go 后端
├── frontend/      React 前端
├── scripts/       任务开工与收尾、数据库加密备份、隔离恢复演练与迁移检查脚本
├── docs/adr/      架构决策记录
├── docs/runbooks/ 部署、发布、备份恢复与故障处理操作手册
├── licenses/      第三方许可
├── compose.yaml   容器编排配置
└── mise.toml      工具版本与常用任务
```

## Markdown 组件

`frontend/src/markdown` 提供 `MarkdownEditor` 与 `MarkdownContent`。编辑器采用 [Vditor 4.0.0](https://github.com/Vanessa219/vditor)（MIT），默认源码＋预览分屏，窄屏上下排列。两种展示都复用 Vditor 的 [Lute 引擎](https://github.com/88250/lute)（Mulan PSL v2），使用 [DOMPurify 3.4.16](https://github.com/cure53/DOMPurify)（Apache-2.0 / MPL-2.0）净化、[highlight.js 11.11.1](https://github.com/highlightjs/highlight.js)（BSD-3-Clause）高亮常用代码语言。依赖版本锁定在 `frontend/package-lock.json`；`jsdom` 仅用于组件与渲染测试。许可副本随前端部署到 `/licenses/markdown.txt`。

`MarkdownEditor` 接收 `value`、`onChange(value)`、可选 `upload(file): Promise<{ url, name, isImage }>`、`onUploadingChange(boolean)`、`disabled`、`label` 与 `placeholder`；`MarkdownContent` 接收 `value` 和可选 `className`。上传函数负责调用业务 API，结果地址必须为 `/api/forum/attachments/{uuid}`。提交按钮应随上传状态禁用，页面切换帖子、工单或草稿时应以其 ID 作为编辑器的 React `key`，避免复用未完成上传与撤销历史。编辑器不使用本地草稿缓存，上传成功才插入 Markdown，文件名按 Markdown 转义。

Lute、中文资源和图标通过 Vite 的 `?url&no-inline` 生成本站静态文件，不调用外部 CDN、不生成 data URL 脚本。编辑区只接收 Markdown 文本和上传文件；阅读与预览禁用原始 HTML、外站图片、危险链接以及图表/数学/媒体的额外脚本。保留表格、任务列表与代码块等常用语法。页面接入应使用路由懒加载：Lute 静态脚本约 3.74 MB（未压缩），无需让其他页面提前下载。

聚焦验证：`npm --prefix frontend test -- src/markdown`，类型及集成构建：`npm --prefix frontend run build`。
