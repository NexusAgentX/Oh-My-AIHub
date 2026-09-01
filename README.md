# Oh-My-AIHub

Oh-My-AIHub 目前处于产品定义前的工程初始化阶段。仓库已经建立 React 前端、Go 后端、Docker Compose 运行环境与 mise 开发任务；产品定位、目标用户和核心功能尚待补充。

## 文档导航

- [产品说明](PRODUCT.md)：产品目标、用户、范围与需求。
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

完整职责、检查点、拆分和证据规则见 `AGENTS.md`；稳定产品事实维护在 `PRODUCT.md`，视觉规则维护在 `DESIGN.md`。

## 设计工作流

项目使用 [OpenPencil](https://github.com/ZSeven-W/openpencil) 作为主要 UI/UX 设计工具。AI Agent 只通过 OpenPencil MCP 执行设计；MCP 不可用时暂停并由维护者修复或重载，不降级到 CLI。人类负责目标用户、产品方向和关键方案选择。可编辑 `.op` 源文件与同名 PNG 预览都提交到 `design/`，并与对应代码处于同一个 Issue、分支和 Pull Request。

安装桌面端和 `op` CLI 后执行：

```bash
op install --target codex
op --version
```

重载 Codex 会话后验证 OpenPencil MCP 已进入工具清单。目录、命名、安全、导出和评审规则见 `design/README.md`；任何视觉任务还必须遵守 `DESIGN.md`。

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
mise run check-design
mise run test
docker compose config --quiet
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
