# Oh-My-AIHub

Oh-My-AIHub 是面向受邀小圈子的 API 资源共享与内部积分清算平台。用户可以共享自己已经充值的 API 中转渠道，消费者通过平台 API Key 聚合多个渠道并按优先级故障转移；成功调用使用中心化零和账本结算，共享者可以在双边 C2C 市场出售所得积分。

产品方向和首版边界已经确认。当前代码已经交付公开产品落地页、受邀账户、模型目录、中心化零和账本、渠道共享与市场，以及平台 API Key、四协议优先级代理和调用结算；C2C 仍按 Epic #16 继续实现。已确认需求见 `PRODUCT.md`，推进顺序见 `ROADMAP.md`，当前真实实现见 `ARCHITECTURE.md`。
产品方向和首版边界已经确认。当前代码已经交付公开产品落地页、受邀账户、模型目录、中心化零和账本、渠道共享、校验、公开 API 市场与 C2C 双边市场；平台 API Key 和代理调用结算仍按 Epic #16 继续实现。已确认需求见 `PRODUCT.md`，推进顺序见 `ROADMAP.md`，当前真实实现见 `ARCHITECTURE.md`。

## 文档导航

- [产品说明](PRODUCT.md)：产品目标、用户、范围与需求。
- [产品路线图](ROADMAP.md)：面向用户结果的优先级、证据视野与推进顺序。
- [设计系统](DESIGN.md)：视觉 Token、组件、交互、响应式与无障碍规则。
- [设计资产](design/README.md)：OpenPencil 源文件、预览与 Git 工作流。
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
- AI 负责研究、假设整理、驱动专业工具设计、工程实现、测试、文档、发布准备与反馈归纳。
- 工作按方向与结果、发现、设计、交付、发布与学习形成闭环；设计不是开发前的一次性附件。
- 任务区分 Ready、Done 和 Validated，代码合并或功能上线不自动等于用户结果已经成立。
- 高不确定产品能力先用发现或原型 Feature 降低风险，再进入小批量交付；纯技术或已知小改动保持单个 Feature 的轻量流程。

完整职责、检查点、拆分和证据规则见 `AGENTS.md`；稳定产品事实维护在 `PRODUCT.md`，结果优先级与推进顺序维护在 `ROADMAP.md`，视觉规则维护在 `DESIGN.md`。

## 设计工作流

项目使用 [OpenPencil](https://github.com/ZSeven-W/openpencil) 作为主要 UI/UX 设计工具。AI Agent 只通过 OpenPencil MCP 执行设计；MCP 不可用时暂停并由维护者修复或重载，不降级到 CLI。人类负责目标用户、产品方向和关键方案选择。可编辑 `.op` 源文件与同名 PNG 预览都提交到 `design/`，并与对应代码处于同一个 Issue、分支和 Pull Request。

安装桌面端和 `op` CLI 后执行：

```bash
op install --target codex
op --version
```

重载 Codex 会话后验证 OpenPencil MCP 已进入工具清单。目录、命名、安全、导出和评审规则见 `design/README.md`；任何视觉任务还必须遵守 `DESIGN.md`。

## 当前工程组成

- 前端：React 19、TypeScript、Vite 与 React Router。
- 后端：Go HTTP 服务、受邀账户、模型目录、零和账本、渠道安全托管、市场、平台 API 网关、调用结算与管理员治理 API。
- 后端：Go HTTP 服务、受邀账户、模型目录、零和账本、渠道安全托管、API 市场、C2C 状态机与管理员治理 API。
- 数据库：PostgreSQL 18，使用 Goose 管理嵌入式 SQL 迁移。
- 本地工具链：mise。
- 容器运行：Docker Compose，前端由 Nginx 提供静态资源并代理 `/api` 与四类外部协议请求，迁移完成后再启动后端。

管理员运营总览提供 UTC 时间窗口的统一指标、硬异常下钻、跨模块巡检历史与试用证据摘要；发布准备包含 `mise run check-release` 门禁、CI、加密备份与隔离恢复演练，操作手册见 `docs/runbooks/`。当前可运行能力包括未认证的 `/welcome`、受邀登录与改密、账户和模型目录管理、真实钱包与管理员账本运营。已改密用户可以托管和校验自己的渠道，在公开市场按价格、评分、成功率、TTFT 或 TPS 选择报价；也可以创建或轮换只显示一次的多把平台 API Key，为每把 Key 配置模型协议池与固定优先级，通过 Chat Completions、Responses、Anthropic Messages 或 Gemini GenerateContent 原生入口调用。平台在调用前建立快照和预授权，提交点前顺序回退，成功后精确结算；总览、调用记录和渠道页使用真实调用指标。C2C 业务流程尚未交付。
当前可运行能力包括未认证的 `/welcome`、受邀登录与改密、账户和模型目录管理、真实钱包与管理员账本运营。已改密用户还可以创建自己的渠道，安全替换或撤销上游凭据，为模型配置原生协议和统一倍率，执行可能产生上游费用的显式校验，发布、暂停或逻辑删除渠道；公开 API 市场提供报价筛选、确定性游标分页、独立价格和评分，管理员可以查看非敏感配置、重验并带原因暂停或删除异常渠道。C2C 市场支持固定价格买卖单、部分成交、多种支付方式、可选付款截图、争议和管理员裁决；卖单积分由账本父持有担保，买单按成交冻结卖家的积分。平台 Key、真实请求代理与结算和运行质量聚合尚未交付。

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

首次运行时，在交互终端创建唯一的初始管理员：

```bash
mise run bootstrap-admin -- --username admin --display-name "管理员"
```

若数据库使用自定义密码，在运行命令时传入同一个 `POSTGRES_PASSWORD`；任务会通过 `PGPASSWORD` 传递密码，不会把密码拼入连接 URL。

启动后端：

```bash
export UPSTREAM_CREDENTIAL_KEYRING='v1=<32 字节密钥的 Base64>'
export UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID='v1'
export C2C_PRIVATE_DATA_KEYRING='v1=<另一把 32 字节密钥的 Base64>'
export C2C_PRIVATE_DATA_ACTIVE_KEY_ID='v1'
mise run dev-backend
```

`UPSTREAM_CREDENTIAL_KEYRING` 使用逗号分隔的 `key-id=base64-key`，每把密钥解码后必须正好 32 字节。已有密文引用的旧密钥必须在完成重加密前继续保留；密钥环和数据库备份必须配套保存，不能每次启动临时生成。多副本轮换时，先让全部副本同时持有完整的新旧密钥环并统一使用新的活动密钥 ID，确认没有仍以旧密钥写入的副本后，才能调用管理员重加密。管理员从同源已认证会话调用 `POST /api/admin/channel-credentials/reencrypt`，请求体为 `{"limit": 100}`；`limit` 必须为 1～1000。按批次重复调用，直到响应 `{"reencrypted": 0}`，再确认数据库库存不再引用旧 key ID 且全部副本都使用新配置，最后才移除旧密钥。无法保证这个顺序时应暂停凭据写入和重加密，而不是混合运行。默认只允许上游 HTTPS 443 端口；如确需其他端口可用 `UPSTREAM_ALLOWED_PORTS` 显式追加，额外禁用域名可用 `UPSTREAM_BLOCKED_HOSTS` 追加。`api.openai.com` 及其子域永久禁用，不能通过配置解除。

`C2C_PRIVATE_DATA_KEYRING` 采用相同的 `key-id=base64-key` 语法，但必须使用与上游凭据不同的密钥，保护收款方式详情、联系方式、付款说明、争议陈述和净化后的证据图片。服务启动会验证全部存活私密数据可由当前密钥环解密；新的活动密钥只影响后续写入，当前尚无批量重加密入口，因此任何仍被库存引用的旧密钥都必须保留。终态满 180 天后会清理私密正文和图片密文。该密钥环同样必须与数据库备份配套保存，不得每次启动临时生成。

在另一个终端启动前端：

```bash
mise run dev-frontend
```

前端开发服务器位于 <http://localhost:5173>，公开落地页位于 <http://localhost:5173/welcome>，并将 `/api`、`/v1/chat/completions`、`/v1/responses`、`/v1/messages` 和 `/v1beta/models/...` 请求代理到 <http://localhost:8080>。

平台代理入口只接受各协议规定的认证头：OpenAI 风格使用 `Authorization: Bearer <平台 Key>`，Anthropic 使用 `x-api-key`，Gemini 使用 `x-goog-api-key`。客户端必须提交模型目录中的 canonical model ID；平台不做跨协议转换。请求上限为 32 MiB，非流式调用最长 10 分钟，流式调用最长 30 分钟。

本地开发默认不信任客户端提供的转发头。Compose 通过 `BACKEND_TRUSTED_PROXY_CIDRS` 配置后端可采信的内部 Nginx 源网段；未配置时后端忽略全部转发头。外层代理到 Nginx 的信任边界使用 `TRUSTED_PROXY_CIDR` 单一网段配置。

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
mise run check-design
mise run test
mise run test-backend-integration
docker compose config --quiet
mise run check-proxy-trust # 需要已按上文启动安全栈
```

## 目录结构

```text
.
├── backend/       Go 后端
├── design/        OpenPencil 设计源文件与预览
├── frontend/      React 前端
├── scripts/       仓库聚焦检查脚本
├── docs/adr/      架构决策记录
├── compose.yaml   容器编排配置
└── mise.toml      工具版本与常用任务
```
