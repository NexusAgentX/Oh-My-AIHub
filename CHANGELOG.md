# 变更日志

本文档记录对使用者、开发者和部署环境有意义的变化。项目尚未发布，当前变化统一记录在“未发布”章节。

## 未发布

### 新增

- 建立 React、TypeScript 与 Vite 前端工程骨架。
- 建立 Go HTTP 后端及 `/api/health` 健康检查。
- 建立 Docker Compose 本地运行方式。
- 建立 mise 工具版本和常用开发任务。
- 建立产品、架构、安全、Agent 协作和架构决策文档体系。
- 建立 Feature/Epic Issue 模板和 Issue 驱动的 Agent 开发工作流。
- 建立 Binance 风格的中文设计系统，并将其设为后续 UI 工作的规范性来源。
- 记录采用 Binance 风格设计语言的 ADR-0001。
- 建立人类定向、AI 执行的持续产品研发模型，将发现、专业工具设计、交付和上线学习纳入统一治理。
- 为 Feature/Epic Issue 模板补充用户问题、设计、结果验证和 Ready/Done/Validated 证据要求。
- 记录 AI 原生持续产品研发模型的 ADR-0002。
- 安装并验证 OpenPencil 桌面端、`op` CLI、Codex Skill 与 MCP 接入。
- 建立 `design/` 一等资产目录，提交 OpenPencil 可编辑源文件和同名 Git 评审预览。
- 新增设计资产命名、同步、安全与可移植性规则，以及 `mise run check-design` 聚焦检查。
- 规定 Agent 的 OpenPencil 设计操作只允许使用 MCP；MCP 不可用时暂停，不降级到 CLI。
- 记录将 OpenPencil 可编辑设计源文件纳入 Git 的 ADR-0003。

### 变更

- 将 mise 项目配置统一命名为 `mise.toml`。

### 修复

暂无。

### 移除

暂无。
