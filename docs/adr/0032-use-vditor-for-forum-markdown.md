# ADR-0032：论坛采用 Vditor 源码编辑与统一 Markdown 渲染

- 状态：已通过
- 日期：2026-10-09
- 决策者：维护者（调研后选择方案 A：Vditor）
- 关联内容：Epic #238、Feature #240、Feature #241

## 背景

成员论坛、公告板块及私密工单需要 Markdown 工具栏、源码编辑、预览、粘贴图片和附件上传。维护者要求选成熟开源组件，正文保存 Markdown，并沿用现有 React、TypeScript、Cookie 会话与 CSP。

## 决策目标

复用成熟编辑器，统一预览与阅读结果，附件走站内鉴权 API；不引入独立富文本存储格式或第三方上传服务。

## 候选方案

### 方案一：Vditor

[Vditor](https://github.com/Vanessa219/vditor) 提供源码工具栏及 Markdown 解析能力，中文体验完整；命令式组件需要 React 生命周期封装，Lute 运行时体积较大。

### 方案二：React Markdown 编辑器

[@uiw/react-md-editor](https://github.com/uiwjs/react-md-editor) 与 [md-editor-rt](https://github.com/imzbf/md-editor-rt) 可作为 React 原生替代；维护者在调研比较后选择 Vditor。

## 决定

采用固定版本 Vditor 4.0.0，默认源码编辑，封装 `MarkdownEditor` 与 `MarkdownContent`。预览和阅读共享 Vditor 的 Lute 解析、DOMPurify 白名单净化及代码高亮。关闭 Vditor 自带动态预览资源、自动媒体/图表渲染与本地草稿缓存，运行时、图标与语言包本地发布。

图片与附件通过本站 API 上传，Markdown 插入受保护的文件 URL，提交时显式绑定附件 ID。图片仅允许本站附件路径；剪贴板只接受纯文本与上传文件。按草稿上下文挂载独立编辑器，卸载后不插入异步上传结果。

## 后果

### 正面影响

- 工具栏与源码编辑复用开源实现，预览和阅读一致，私密附件权限由后端统一执行。
- 不依赖外部 CDN，现有 CSP 无需放宽。

### 负面影响与成本

- Lute 静态资源约 3.74 MB（未压缩）；论坛路由懒加载，列表不加载编辑器运行时。
- 维护命令式编辑器与 React 的生命周期桥接，升级需复验 CSP、粘贴与异步上传行为。
- 首版不提供公式、流程图、外部图片或嵌入媒体渲染。

### 风险与缓解措施

- 用户 Markdown 统一净化，HTML 和不允许的链接/图片不进入展示；附件遵循 ADR-0031。
- 依赖与静态资源保留完整许可，见 `frontend/public/licenses/markdown.txt`；升级同步评估维护、安全与体积成本。

## 验证方式

编辑器生命周期、剪贴板与渲染净化测试；真实后端上传/下载、发布后渲染及不同账号访问验证；桌面与 390px 界面验收，生产构建与现有 CSP 检查。

## 替代关系

无。
