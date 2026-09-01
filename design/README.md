# 设计资产

本目录保存与代码同等重要、由 Git 版本管理的产品设计资产。具体视觉语言由仓库根目录的 `DESIGN.md` 规定；具体页面、流程和组件的可编辑状态保存在 OpenPencil `.op` 文件中。

## 目录与权威性

```text
design/
├── sources/    OpenPencil 可编辑源文件，设计事实来源
└── previews/   从同名源文件导出的 PNG，供 GitHub 和代码评审查看
```

- `sources/<name>.op` 是权威设计源文件。截图、云端链接和口头描述不能替代它。
- `previews/<name>.png` 是派生预览，必须与同名 `.op` 同步更新。
- `DESIGN.md` 决定视觉 Token 和通用规则，`.op` 决定某个具体设计的结构和状态，产品范围仍由 `PRODUCT.md` 决定。
- 当前的 `ai-native-product-loop` 仅验证工具链和研发流程，不是产品页面或产品功能定义。

## 工具

主要设计工具是 [OpenPencil](https://github.com/ZSeven-W/openpencil)。当前资产由 OpenPencil 0.8.4 创建；更新工具后，应先确认现有 `.op` 能正常打开、保存和导出。

本机完成桌面端和 `op` CLI 安装后，将 OpenPencil 接入 Codex：

```bash
op install --target codex
op --version
```

随后重载 Codex 会话，使 OpenPencil Skill 和 MCP 工具进入新的工具清单。Agent 的设计读取、创建、修改、保存、lint 和导出只能直接调用 OpenPencil MCP，不得降级到 `op` CLI。若 MCP 工具未加载或服务不可用，Agent 必须停止设计工作并请维护者修复或重载会话。

`op` CLI 作为本机接入组件保留，用于初次执行官方安装命令和人工确认版本；除非维护者明确要求诊断安装本身，Agent 不把它作为设计操作或自动化后备通道。

## 工作流

任何影响用户流程、交互或视觉结果的任务都应在同一个 Feature Issue、分支和 Pull Request 中维护：

1. 开工前读取 `PRODUCT.md`、`DESIGN.md`、本文件和相关 ADR，确认任务已经 Ready。
2. 由 Agent 直接通过 OpenPencil MCP 生成或修改稳定名称的 `.op` 源文件；不得用 CLI 降级、手写 JSON 或直接写前端冒充专业工具设计。
3. 由 OpenPencil 从同一源文件导出同名 PNG 预览。
4. 实现前由人类在需要的方向检查点确认设计；实现过程中保持设计、代码和验收证据同步。
5. 提交前运行 `mise run check-design`，并在 PR 中同时评审源文件、预览和对应实现。
6. 若实现反馈改变了设计，必须在同一个任务中回写 `.op` 和预览，不能只改代码。

设计尚未确认或只用于探索时，也应通过独立的发现或原型 Feature 管理，不提前提交依赖其结论的业务实现。

## 命名与 Git 规则

- 文件名使用小写 kebab-case，源文件和预览保持同名，例如 `workspace-overview.op` 与 `workspace-overview.png`。
- 文件名保持稳定，不添加日期、`final`、`new`、`v2` 等人工版本后缀；历史版本由 Git 保存。
- 一个 `.op` 可以包含同一用户流程的多个页面和关键状态。内容过大或评审边界不同再拆文件。
- `.op` 以 UTF-8/LF 文本提交，便于 Git 追踪；PNG 作为二进制预览提交。
- 当前不使用 Git LFS。只有实际资产体积或仓库性能证明必要时，才通过独立 ADR 引入。
- 不提交 OpenPencil 缓存、用户偏好、本机 Codex 配置或临时导出文件。

## 安全与可移植性

设计源文件和预览不得包含：

- 凭据、令牌、私钥或可用的密钥示例。
- 真实用户数据、未脱敏日志或私人内容。
- `/Users/...`、`/home/...`、Windows 用户目录等仅在单台机器有效的绝对路径。
- 需要个人登录态才能解析、且没有仓库内替代物的关键资源。

需要图片、字体或图标时，优先使用仓库内可授权、可追溯的资产；外部来源及许可必须能够被评审。

## 验证

```bash
mise run check-design
git diff --check
```

`check-design` 会验证 `.op` 是有效 JSON、包含可编辑节点、使用稳定文件名、没有常见凭据或本机绝对路径，并检查每个源文件都有有效的同名 PNG 预览且不存在孤立预览。真实用户数据、视觉一致性和源预览是否对应仍需在评审中确认。
