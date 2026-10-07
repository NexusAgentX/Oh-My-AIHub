# ADR-0019：前端采用 TanStack Query 与统一设计 token / 组件约定

- 状态：已通过
- 日期：2026-10-07
- 决策者：仓库维护者
- 关联内容：[Feature #114](https://github.com/NexusAgentX/Oh-My-AIHub/issues/114)、[Epic #109](https://github.com/NexusAgentX/Oh-My-AIHub/issues/109)

## 背景

MVP 的 34 个页面各自用 `useState`/`useEffect` 手写加载、错误与重试，各自包裹 `AppShell`；约 3600 行全局 CSS 中散落着颜色、圆角与间距取值，导航图标重复，页面之间的状态展示不一致。Epic #109 将由多个子代理并行改版各领域页面，需要先建立可共同依赖的数据层、视觉 token 与组件约定。维护者已确认视觉方向为“现有风格精修”（浅色、墨色主色、芥末黄点缀），并同意引入 TanStack Query。

## 决策目标

- 页面不再各自管理请求状态，加载 / 错误 / 空 / 内容四态一致；
- 视觉取值只有一个来源，页面与组件不写散落的数值；
- 外壳由路由层提供，页面只关心自身内容；
- 并行改版的各领域只修改自己的文件。

不在本决策范围：切换 OpenAPI 生成类型（第三波）、暗色主题、国际化。

## 候选方案

### 方案一：继续手写 hook 与 Context

不新增依赖，但每个页面重复缓存、去重、重试与失效逻辑，WalletProvider 式的 Context 会越来越多。

### 方案二：TanStack Query（选定）

成熟的服务端状态库，提供缓存、请求去重、重试、失效与分页；约 40 KB（gzip 约 13 KB）。

### 方案三：SWR / RTK Query

能力相近，但变更（mutation）与无限分页的语义不如 TanStack Query 完整，也与计划中的 OpenAPI 生成客户端集成度较低。

## 决定

1. **数据层**：采用 `@tanstack/react-query`。
   - `src/api/query.ts` 提供 `createQueryClient`（`staleTime` 30 秒、4xx 不重试、其余最多重试 2 次、关闭窗口聚焦刷新）、`errorMessage`（`ApiError` → 后端中文文案，其余用兜底）。
   - 每个领域在 `<domain>/queries.ts` 导出 key 工厂与 `useXxx` hook；页面不直接写 key，也不在页面里调用 `api.*` 取数。
   - key 形如 `[领域, 资源, ...参数]`（`as const`）；写操作使用 `useMutation`，成功后以领域前缀 `invalidateQueries`。
   - 页面使用 `QueryBoundary` 渲染四态；会话切换时 `SessionQueryReset` 整体清空缓存。
   - 401 等会话变更仍由 `api/client.ts` 的 `authFailureHandler` 处理，与 Query 无关。
   - 旧 `WalletProvider` 删除，改为 `wallet/queries.ts` 的 `useWallet()`（形状不变）。其余页面在第二波改版时迁移。
2. **设计 token**：`src/styles/tokens.css` 是颜色、字号、间距（4px 栅格）、圆角（卡片 12 / 按钮与输入 9）、阴影与布局尺寸的唯一来源。状态色为“柔和底色 + 深色文字”，芥末黄只用于导航选中色条、强调徽标与进度条，数字使用 `tabular-nums`，首要按钮为墨色实底。新代码只引用 `var(--token)`，不写十六进制颜色与裸 px 圆角。
3. **组件与样式拆分**：基础组件位于 `src/ui/`，每个组件文件配同名 `.css`，页面一律从 `../ui` 引入；`src/styles/index.css` 按固定顺序导入 token、基础、UI、外壳与各领域样式。领域样式放在 `<domain>/<domain>.css`，只写本领域私有布局，通用外观必须用 UI 组件。旧 `ui/FormControls` 仅作兼容出口，待全部页面迁移后删除。
4. **外壳**：用户与管理员各一个 react-router layout route（`ProductLayout` / `AdminLayout`），页面不再包裹 `AppShell`。用户导航分“使用 API / 共享渠道 / 积分”，配置集中在 `layouts/navigation.ts`，每项必须使用唯一图标（有单测）；760px 以下改为底部 Tab 栏加“更多”抽屉。
5. **无障碍**：Dialog / Drawer 基于原生 `<dialog>.showModal()`（焦点陷阱、Esc、焦点归还），路由切换后焦点移至主内容，提供“跳到主要内容”链接，所有图标按钮必须有 `aria-label`。

## 后果

### 正面影响

- 页面代码显著变短，并行改版时只改自己的领域目录与样式文件；
- 请求去重与缓存使顶栏余额、工作台、钱包共享同一份数据；
- 组件、token 与导航有测试与文档约束，风格不会随页面漂移。

### 负面影响与成本

- 新增一个运行时依赖，包体增加约 13 KB（gzip）；
- 迁移期间新旧模式并存（未迁移页面仍手写请求状态）。

### 风险与缓解措施

- 缓存导致数据陈旧：默认 `staleTime` 较短，写操作后按领域前缀失效；
- 跨账号数据泄漏：会话变化时清空整个缓存；
- 样式回退：旧类名在领域 CSS 中暂时保留，随页面改版逐步删除。

## 验证方式

`npm --prefix frontend test` 覆盖错误映射、重试策略、导航唯一图标与路由匹配；浏览器在 1440 / 900 / 390 像素检查外壳、导航与工作台、钱包页。

## 替代关系

无。
