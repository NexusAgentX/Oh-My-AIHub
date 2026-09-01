# Oh-My-AIHub

Oh-My-AIHub 目前处于产品定义前的工程初始化阶段。仓库已经建立 React 前端、Go 后端、Docker Compose 运行环境与 mise 开发任务；产品定位、目标用户和核心功能尚待补充。

## 文档导航

- [产品说明](PRODUCT.md)：产品目标、用户、范围与需求。
- [设计系统](DESIGN.md)：视觉 Token、组件、交互、响应式与无障碍规则。
- [架构说明](ARCHITECTURE.md)：当前系统结构、边界与技术决策。
- [变更日志](CHANGELOG.md)：面向版本与使用者的重要变化。
- [Agent 协作说明](AGENTS.md)：所有 Agent 在本仓库中的工作规则。
- [安全策略](SECURITY.md)：漏洞报告方式与安全基线。
- [架构决策记录](docs/adr/README.md)：需要长期保留的架构决策及其背景。

开始工作前，应先阅读 `AGENTS.md`，再根据任务阅读并维护相关文档。

## 任务管理

开发任务通过 GitHub Issue 管理：小任务使用单个 Feature Issue，大任务使用 Epic Issue 拆分多个 Feature Issue。每个 Issue 的最新 Spec、Plan、Tasks 和 Acceptance 都维护在 body 中。具体工作方式见 `AGENTS.md`。

## 当前工程组成

- 前端：React 19、TypeScript、Vite。
- 后端：Go HTTP 服务。
- 本地工具链：mise。
- 容器运行：Docker Compose，前端由 Nginx 提供静态资源并代理 `/api` 请求。

当前前后端仅实现 `/api/health` 健康检查链路，不代表产品功能已经确定。

## 环境要求

- [mise](https://mise.jdx.dev/)
- 支持 Compose 的 Docker 环境

## 本地开发

安装工具链和前端依赖：

```bash
mise install
mise run install
```

启动后端：

```bash
mise run dev-backend
```

在另一个终端启动前端：

```bash
mise run dev-frontend
```

前端开发服务器位于 <http://localhost:5173>，并将 `/api` 请求代理到 <http://localhost:8080>。

## Docker Compose 运行

```bash
mise run up
```

访问 <http://localhost:3000>。停止服务：

```bash
mise run down
```

## 验证

```bash
mise run test
docker compose config --quiet
```

## 目录结构

```text
.
├── backend/       Go 后端
├── frontend/      React 前端
├── docs/adr/      架构决策记录
├── compose.yaml   容器编排配置
└── mise.toml      工具版本与常用任务
```
