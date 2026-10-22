# Oh-My-AIHub

Oh-My-AIHub 是面向受邀小圈子的 API 资源共享与内部积分清算平台：成员共享自己已充值的 API 中转渠道，消费者持平台 API Key 经透明网关聚合调用、按优先级故障转移，成功调用由中心化零和账本清算积分，另含 C2C 卖单市场、成员论坛与私密工单。

## 文档导航

| 文档 | 内容 |
| --- | --- |
| [产品说明](PRODUCT.md) | 产品定位、范围与规则 |
| [产品路线图](ROADMAP.md) | 接下来交付的结果与顺序（由维护者填写） |
| [架构说明](ARCHITECTURE.md) | 系统结构、模块边界、不变量与安全边界 |
| [设计规范](DESIGN.md) | 视觉风格、主题、组件与品牌规则 |
| [Stitch 设计交接](docs/stitch/README.md) | 核心业务、独立双端原型设计简报与导出方式 |
| [安全策略](SECURITY.md) | 漏洞报告方式与处理原则 |
| [变更日志](CHANGELOG.md) | 各版本的重要变化 |
| [Agent 开发指南](AGENTS.md) | 任务流程、分支、验证、迁移与发版规则 |
| [架构决策记录](docs/adr/README.md) | 长期技术决策及其原因 |
| [运维手册](docs/runbooks/) | 部署、发布、备份恢复、观测与故障处理 |

向本仓库提交改动前，先读 [AGENTS.md](AGENTS.md)。

## 技术栈

- 前端：React 19、TypeScript、Vite、React Router、TanStack Query。
- 后端：Go `net/http`、pgx、sqlc；无第三方 Web 框架。
- 数据库：PostgreSQL 18，Goose 嵌入式编号迁移。
- 契约：`backend/api/openapi.yaml`（OpenAPI 3.1）是前后端唯一契约，前端类型由它生成。
- 工具与运行：mise 固定工具版本；Docker Compose 编排数据库、迁移、后端与 Nginx 前端。

## 环境要求

- [mise](https://mise.jdx.dev/)
- 支持 Compose 的 Docker 环境

## 快速开始

```bash
mise install          # 工具链（Go、Node、sqlc）
mise run install      # 前端依赖
mise run dev-database # 启动 PostgreSQL 并执行迁移

export UPSTREAM_CREDENTIAL_KEYRING='v1=<32 字节密钥的 Base64>'
export UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID='v1'
export C2C_PRIVATE_DATA_KEYRING='v1=<另一把 32 字节密钥的 Base64>'
export C2C_PRIVATE_DATA_ACTIVE_KEY_ID='v1'

mise run dev-backend   # 终端 A：后端 :8080，指标 :9090
mise run dev-frontend  # 终端 B：前端 :5173
```

浏览器访问 <http://localhost:5173/initialize> 创建首个管理员。

一条命令的替代（仍需先导出上面四个密钥环变量）：`mise run up-dev` 用 Docker Compose 整栈启动，入口 <http://localhost:3000>（仅绑定回环地址）。

本地模拟上游可显式设置 `UPSTREAM_ALLOW_LOCALHOST=true`，并通过 `UPSTREAM_ALLOWED_PORTS` 放行模拟服务端口，例如 `UPSTREAM_ALLOWED_PORTS=18081` 后使用 `http://localhost:18081`。开关默认关闭，仅支持主机名 `localhost`（固定连接 `127.0.0.1`），不支持 IP 字面量或其他内网主机；HTTPS 仍校验证书。容器内的 localhost 指向容器自身，模拟上游运行在宿主机时使用上面的本地后端启动方式。完整边界见 [系统结构](ARCHITECTURE.md#安全边界)。

两组密钥环各加密一类数据（渠道上游 Key / 平台 API Key 与 C2C 收款方式），必须互不相同、与数据库备份配套保存、保留全部历史 key id；服务启动时会逐条解密全部存量密文，缺 key id 或密文损坏即拒绝启动。细节见[部署手册](docs/runbooks/deployment.md)。

## 常用命令

```bash
mise run lint                 # 前端 oxlint + 后端 staticcheck
mise run test                 # 前后端测试与前端构建
mise run generate             # 改 OpenAPI 契约或 SQL 后重新生成代码
mise run dev-database         # 只启动数据库
mise run down                 # 停止 Compose 栈
```

全部任务见 `mise.toml`；验证矩阵与 CI 门禁说明见 [AGENTS.md](AGENTS.md)。

## 目录结构

```text
.
├── backend/         Go 后端（cmd/ 多进程：server、migrate、detection-sdk、cutover-check；internal/ 按领域分包）
├── frontend/        React 单页应用（src/ 按业务域分目录）
├── scripts/         任务开工收尾、备份恢复、迁移与 CI 守卫脚本
├── docs/adr/        架构决策记录
├── docs/runbooks/   运维手册
├── compose.yaml     容器编排
└── mise.toml        工具版本与任务定义
```

## 许可

第三方字体与组件的来源和许可见 [DESIGN.md](DESIGN.md) 与前端 `/licenses/` 页面。项目本身的许可证待维护者补充。
