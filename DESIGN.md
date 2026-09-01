---
version: alpha
name: Oh-My-AIHub Binance 风格设计系统
description: 以近黑画布、高能黄色主操作、紧凑金融信息和明确涨跌语义为核心的双主题设计系统。
colors:
  primary: "#fcd535"
  primary-active: "#f0b90b"
  primary-disabled: "#3a3a1f"
  canvas-dark: "#0b0e11"
  surface-card-dark: "#1e2329"
  surface-elevated-dark: "#2b3139"
  canvas-light: "#ffffff"
  surface-soft-light: "#fafafa"
  surface-strong-light: "#f5f5f5"
  ink: "#181a20"
  body-on-dark: "#eaecef"
  muted: "#707a8a"
  muted-strong: "#929aa5"
  hairline-on-light: "#eaecef"
  hairline-on-dark: "#2b3139"
  border-strong: "#cdd1d6"
  on-primary: "#181a20"
  on-dark: "#ffffff"
  trading-up: "#0ecb81"
  trading-down: "#f6465d"
  info: "#3b82f6"
  focus-ring: "#3b82f6"
typography:
  hero-display:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, sans-serif"
    fontSize: 64px
    fontWeight: 700
    lineHeight: 1.1
    letterSpacing: -1px
  display-lg:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, sans-serif"
    fontSize: 48px
    fontWeight: 700
    lineHeight: 1.1
    letterSpacing: -0.5px
  display-md:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, sans-serif"
    fontSize: 40px
    fontWeight: 600
    lineHeight: 1.15
    letterSpacing: -0.3px
  display-sm:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, sans-serif"
    fontSize: 32px
    fontWeight: 600
    lineHeight: 1.2
    letterSpacing: 0
  title-lg:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, sans-serif"
    fontSize: 24px
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: 0
  title-md:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, sans-serif"
    fontSize: 20px
    fontWeight: 600
    lineHeight: 1.35
    letterSpacing: 0
  title-sm:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, sans-serif"
    fontSize: 16px
    fontWeight: 600
    lineHeight: 1.4
    letterSpacing: 0
  number-display:
    fontFamily: "IBM Plex Sans, Inter, -apple-system, sans-serif"
    fontSize: 40px
    fontWeight: 700
    lineHeight: 1.1
    letterSpacing: -0.3px
  number-md:
    fontFamily: "IBM Plex Sans, Inter, -apple-system, sans-serif"
    fontSize: 16px
    fontWeight: 500
    lineHeight: 1.4
    letterSpacing: 0
  number-sm:
    fontFamily: "IBM Plex Sans, Inter, -apple-system, sans-serif"
    fontSize: 14px
    fontWeight: 500
    lineHeight: 1.4
    letterSpacing: 0
  body-md:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, sans-serif"
    fontSize: 14px
    fontWeight: 400
    lineHeight: 1.5
    letterSpacing: 0
  body-sm:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, sans-serif"
    fontSize: 13px
    fontWeight: 400
    lineHeight: 1.5
    letterSpacing: 0
  caption:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, sans-serif"
    fontSize: 12px
    fontWeight: 500
    lineHeight: 1.4
    letterSpacing: 0
  button:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, sans-serif"
    fontSize: 14px
    fontWeight: 600
    lineHeight: 1
    letterSpacing: 0
rounded:
  xs: 2px
  sm: 4px
  md: 6px
  lg: 8px
  xl: 12px
  pill: 9999px
  full: 9999px
spacing:
  xxs: 4px
  xs: 8px
  sm: 12px
  md: 16px
  lg: 24px
  xl: 32px
  xxl: 48px
  section: 80px
components:
  page-dark:
    backgroundColor: "{colors.canvas-dark}"
    textColor: "{colors.body-on-dark}"
  panel-elevated-dark:
    backgroundColor: "{colors.surface-elevated-dark}"
    textColor: "{colors.on-dark}"
    rounded: "{rounded.lg}"
  footer-light:
    backgroundColor: "{colors.surface-soft-light}"
    textColor: "{colors.ink}"
    padding: 64px
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.on-primary}"
    typography: "{typography.button}"
    rounded: "{rounded.md}"
    padding: 12px 24px
    height: 40px
  button-primary-active:
    backgroundColor: "{colors.primary-active}"
    textColor: "{colors.on-primary}"
    rounded: "{rounded.md}"
  button-primary-disabled:
    backgroundColor: "{colors.primary-disabled}"
    textColor: "{colors.body-on-dark}"
    rounded: "{rounded.md}"
  button-secondary-dark:
    backgroundColor: "{colors.surface-card-dark}"
    textColor: "{colors.on-dark}"
    typography: "{typography.button}"
    rounded: "{rounded.md}"
    padding: 12px 24px
  button-secondary-light:
    backgroundColor: "{colors.canvas-light}"
    textColor: "{colors.ink}"
    typography: "{typography.button}"
    rounded: "{rounded.md}"
    padding: 12px 24px
  card-dark:
    backgroundColor: "{colors.surface-card-dark}"
    textColor: "{colors.on-dark}"
    rounded: "{rounded.xl}"
    padding: 24px
  data-row:
    backgroundColor: transparent
    textColor: "{colors.on-dark}"
    typography: "{typography.number-md}"
    padding: 12px 0
  price-up:
    backgroundColor: transparent
    textColor: "{colors.trading-up}"
    typography: "{typography.number-md}"
  price-down:
    backgroundColor: transparent
    textColor: "{colors.trading-down}"
    typography: "{typography.number-md}"
  input-dark:
    backgroundColor: "{colors.surface-card-dark}"
    textColor: "{colors.on-dark}"
    typography: "{typography.body-md}"
    rounded: "{rounded.lg}"
    padding: 10px 16px
    height: 40px
  input-light:
    backgroundColor: "{colors.surface-strong-light}"
    textColor: "{colors.ink}"
    typography: "{typography.body-md}"
    rounded: "{rounded.md}"
    padding: 10px 16px
    height: 40px
  input-disabled-light:
    backgroundColor: "{colors.surface-soft-light}"
    textColor: "{colors.ink}"
    typography: "{typography.body-md}"
    rounded: "{rounded.md}"
  info-text:
    backgroundColor: transparent
    textColor: "{colors.info}"
    typography: "{typography.body-sm}"
  focus-ring:
    backgroundColor: transparent
    textColor: "{colors.focus-ring}"
    rounded: "{rounded.md}"
  divider-dark:
    backgroundColor: "{colors.hairline-on-dark}"
    textColor: "{colors.muted-strong}"
    height: 1px
  divider-light:
    backgroundColor: "{colors.hairline-on-light}"
    textColor: "{colors.ink}"
    height: 1px
  divider-strong:
    backgroundColor: "{colors.border-strong}"
    textColor: "{colors.ink}"
    height: 1px
  caption-muted:
    backgroundColor: transparent
    textColor: "{colors.muted}"
    typography: "{typography.caption}"
---

# Oh-My-AIHub Binance 风格设计系统

> 状态：已采用。后续所有视觉、页面和组件工作必须先阅读并遵守本文档。

## Overview

本系统采用 Binance 风格的金融平台设计语言，但服务于 Oh-My-AIHub 自身品牌。整体感受应当专业、直接、紧凑、可信，并保留高信息密度下的清晰层级。

视觉核心是近黑画布 `{colors.canvas-dark}`、单一高能黄色 `{colors.primary}` 和克制的灰蓝中性色。黄色承担主操作和最高优先级强调；涨跌绿红只表达金融方向，不充当通用成功或错误颜色。

默认策略：

- 营销页、产品概览和数据密集面板使用深色画布。
- 表单、确认、购买、结算等事务性流程可以使用浅色画布。
- 同一流程内保持主题稳定，不因单张卡片随意切换明暗模式。
- 使用项目自己的名称、Logo、图标和文案，不复制币安商标或品牌口号。

## Colors

### 主色

- `{colors.primary}`：主按钮、关键数字和最高优先级强调。禁止作为大面积背景或正文颜色。
- `{colors.primary-active}`：主操作悬停、按下状态。
- `{colors.primary-disabled}`：深色画布上的禁用主操作。

### 表面

- 深色画布使用 `{colors.canvas-dark}`，卡片使用 `{colors.surface-card-dark}`，嵌套或悬停表面使用 `{colors.surface-elevated-dark}`。
- 浅色画布使用 `{colors.canvas-light}`，弱区块使用 `{colors.surface-soft-light}` 或 `{colors.surface-strong-light}`。
- 深色与浅色层级主要依靠纯色表面和 1px 发丝线区分，不依靠重阴影。

### 文本与边框

- 深色正文使用 `{colors.body-on-dark}`，最高对比标题可用 `{colors.on-dark}`。
- 浅色标题和正文使用 `{colors.ink}`。
- 次要信息使用 `{colors.muted}`，需要略高强调时使用 `{colors.muted-strong}`。
- 边框使用 `{colors.hairline-on-dark}` 或 `{colors.hairline-on-light}`。

### 金融语义

- `{colors.trading-up}` 仅表示价格上涨、买入或做多方向。
- `{colors.trading-down}` 仅表示价格下跌、卖出或做空方向。
- 涨跌信息必须同时提供符号、箭头或文字，不能只靠颜色表达。

## Typography

项目不使用 BinanceNova 或 BinancePlex 等币安定制字体资产。

- 标题、正文、导航和按钮采用 Inter，并回退到系统无衬线字体。
- 价格、数量、百分比和统计数据采用 IBM Plex Sans，并启用等宽数字特性；缺少字体资源时回退到 Inter 和系统字体。
- 大标题依靠字号与 600–700 字重建立力量感，不使用装饰字体。
- 正文基准为 14px / 1.5；数据表可使用 13–14px，但不得牺牲可读性。
- 数字列右对齐，使用等宽数字；单位、币种和时间不得与数值产生含义歧义。

| 角色 | Token | 用途 |
| --- | --- | --- |
| 主视觉标题 | `{typography.hero-display}` | 页面唯一的最高层标题 |
| 大标题 | `{typography.display-lg}` | 核心价值或关键数字区块 |
| 中小标题 | `{typography.display-md}`、`{typography.display-sm}` | 页面章节与主要卡片组 |
| 卡片标题 | `{typography.title-lg}`、`{typography.title-md}`、`{typography.title-sm}` | 局部层级 |
| 金融数字 | `{typography.number-display}`、`{typography.number-md}`、`{typography.number-sm}` | 价格、数量、变化率和统计值 |
| 正文 | `{typography.body-md}`、`{typography.body-sm}` | 说明、帮助和表单信息 |

## Layout

- 以 4px 为基础间距单位，只使用 `spacing` 中定义的值。
- 营销和概览页面内容最大宽度约 1280px；高密度工作台可扩展到 1440px。
- 桌面端优先使用 12 列网格；主工作区与侧栏通常采用 8/4 分栏。
- 主要章节垂直间距使用 `{spacing.section}`；数据密集页面可缩小区块内部间距，但不能破坏全局节奏。
- 卡片默认内边距 `{spacing.lg}`，大型行动区使用 `{spacing.xl}` 或 `{spacing.xxl}`。
- 同类信息必须对齐。数字列、操作列和状态列在所有行保持稳定位置。
- 信息密度高不等于拥挤：优先用清晰列、发丝线、字号层级和一致间距组织内容。

## Elevation & Depth

本系统以扁平表面为主：

| 层级 | 处理方式 | 使用场景 |
| --- | --- | --- |
| 基础 | 纯画布、无阴影 | 页面背景、导航、主视觉区 |
| 分隔 | 1px 发丝线 | 表格行、输入框、折叠面板 |
| 卡片 | 相邻层级的纯色表面 | 内容卡片、数据面板、下拉菜单 |
| 浮层 | 克制阴影与明确边框 | 菜单、对话框、悬浮提示 |
| 焦点 | 2px `{colors.focus-ring}` | 键盘焦点状态 |

禁止玻璃拟态、霓虹光晕、无依据的渐变和大面积模糊阴影。产品活动页可以使用一次性的黄到深色渐变，但不能把它泛化为基础组件样式。

## Shapes

- 小型标签使用 `{rounded.sm}`。
- 标准按钮和输入框使用 `{rounded.md}` 或 `{rounded.lg}`。
- 卡片与大型容器使用 `{rounded.xl}`。
- 胶囊形只用于页面最重要的单一行动，不能让所有按钮都变成胶囊。
- 图标保持简洁、几何化，默认使用 16px、20px、24px 尺寸。
- 内容图片使用 12px 圆角；数据图表和表格不添加装饰性圆角。

## Components

### 导航

顶部导航高度以 64px 为基准。深色页面使用近黑背景和浅色文字，当前项与主操作用黄色强调。导航项数量过多时使用分组或“更多”，不能压缩到难以点击。

### 按钮

- 主按钮始终是黄底黑字，使用 `{components.button-primary}`；不要改成白字。
- 次按钮根据画布选择 `{components.button-secondary-dark}` 或 `{components.button-secondary-light}`。
- 文本按钮无背景，只用于低优先级行动。
- 桌面按钮可为 40px 高；触控环境的有效点击区域不得小于 44×44px。
- 涨跌色按钮只用于明确的买入、卖出、做多、做空行为，不能用于普通确认和取消。

### 卡片与数据表

- 深色卡片使用 `{components.card-dark}`，依靠表面色而非阴影抬升。
- 数据表的价格、数量、百分比采用数字字体并右对齐。
- 行高至少提供 44px 有效点击区域；行间使用发丝线。
- 表头清楚说明单位和时间范围；横向空间不足时允许表格水平滚动，不强行挤压关键列。
- 上涨与下跌使用 `{components.price-up}` 和 `{components.price-down}`，同时配合正负号或方向图标。

### 表单

- 深色表面使用 `{components.input-dark}`，浅色表面使用 `{components.input-light}`。
- 标签常驻显示，不用占位文字代替字段名称。
- 聚焦、无效、禁用、只读和加载状态必须可区分。
- 破坏性或资金相关操作必须展示明确对象、数量、单位和最终确认。

### 图表与状态

- 图表背景与所在表面一致，网格线使用发丝线颜色。
- 涨跌绿红只编码方向，并同时提供文本、符号或图例。
- 加载状态避免布局跳动；空状态说明原因与下一步；错误状态提供恢复路径。
- 大数字必须携带单位、精度规则和数据时间，不能只展示脱离上下文的数值。

## Motion & Interaction

- 普通反馈使用 120–180ms，面板进入或布局变化使用 180–240ms。
- 默认缓动采用 `cubic-bezier(0.2, 0, 0, 1)`，退出可以更快。
- 悬停只改变颜色、边框或轻微表面层级，不使用明显缩放和漂浮。
- 实时价格更新可以短暂闪烁方向色，但不得持续超过 600ms，也不得引起布局移动。
- 支持 `prefers-reduced-motion`；减少动效时取消位移、缩放和闪烁，仅保留即时状态变化。
- 所有悬停能力都必须有键盘焦点与触控等价行为。

## Responsive Behavior

| 断点 | 范围 | 行为 |
| --- | --- | --- |
| 移动端 | `< 768px` | 导航折叠；单列卡片；表格横向滚动；主标题缩小到约 36px |
| 平板 | `768–1023px` | 两列内容；次要导航收纳；主区域与侧栏按内容重新排序 |
| 桌面 | `1024–1440px` | 完整导航；多列数据；工作台可使用 8/4 分栏 |
| 宽屏 | `> 1440px` | 保持最大内容宽度，增加外侧留白，不无限拉宽表格 |

- 移动端优先保留主要数据、主要行动和风险信息，次要列可折叠到详情。
- 图标可以保持固定尺寸，文本和容器按断点调整。
- 关键数值避免在数字内部换行；必要时降低字号或调整格式。
- 底部操作在移动端可固定，但必须避开系统安全区域并保留内容滚动空间。

## Accessibility

- 目标为 WCAG 2.2 AA。
- 正文、交互文字和数据必须满足相应对比度；黄色背景统一使用深色文字。
- 颜色不能成为唯一信息来源，尤其是涨跌、风险、成功和错误状态。
- 所有交互支持键盘操作，焦点顺序与视觉顺序一致，焦点环始终可见。
- 图标按钮提供可访问名称；图表提供摘要、图例或等价数据表。
- 触控目标原则上不小于 44×44px；高密度表格中的小操作必须通过行级点击区或间距补足。
- 动态数据更新使用克制的无障碍通知，避免屏幕阅读器被连续刷新打断。

## Do's and Don'ts

### 应当

- 稀缺地使用黄色，让它始终代表最高优先级。
- 在深色数据面板中用表面层级、发丝线和排版建立结构。
- 让价格、数量、百分比和单位在视觉上快速扫描且不会误读。
- 为所有组件定义默认、悬停、焦点、按下、禁用、加载和错误状态。
- 同时验证桌面、移动端、键盘操作和降低动效模式。
- 修改视觉规则或 Token 时同步更新本文档和受影响实现。

### 禁止

- 不使用币安名称、Logo、口号或定制字体来冒充官方产品。
- 不引入第二个高饱和品牌主色与黄色竞争。
- 不把黄色铺满大面积背景，也不用黄色写长正文。
- 不把涨跌绿红挪作普通成功、失败或装饰颜色。
- 不使用玻璃拟态、霓虹边缘、漂浮卡片、网格渐变和无意义动效。
- 不用巨大留白掩盖信息结构问题，也不把所有内容都包进卡片。
- 不在组件内直接发明新色值、间距、圆角或字体值。

## Implementation & Maintenance

- `DESIGN.md` 是视觉规则的事实来源；代码中的设计 Token 应由本文档映射，不能形成另一套相互冲突的值。
- 开始任何 UI Issue 前先阅读本文档，并在 Acceptance 中加入视觉一致性、响应式和无障碍验证。
- OpenPencil 是主要 UI/UX 设计工具。具体设计的权威源文件保存在 `design/sources/*.op`，同名 PNG 预览保存在 `design/previews/`；详细规则见 `design/README.md` 和 ADR-0003。
- 设计必须由 Agent 直接通过 OpenPencil MCP 创建、读取、修改、保存、lint 和导出，不能降级到 `op` CLI，也不能手写 `.op` JSON 冒充专业工具产物。MCP 不可用时应停止设计工作并请维护者修复或重载会话。
- 影响用户流程、交互或视觉结果时，设计源文件、预览和实现必须在同一个 Issue、分支和 Pull Request 中同步更新；实现反馈改变设计时也要回写源文件。
- 设计文件使用稳定名称，由 Git 表达历史版本；不得使用日期、`final`、`new`、`v2` 等后缀代替版本管理。
- 原始十六进制颜色只能出现在 Token 定义或必要的图形资源中，组件样式必须引用语义 Token。
- 产品需求由 `PRODUCT.md` 决定，视觉规则由本文档决定，系统实现边界由 `ARCHITECTURE.md` 和 ADR 决定。
- 若现有 UI 与本文档不一致，新工作默认向本文档收敛；大规模迁移应单独建立 Issue。
- 改变主色、字体体系、主题策略、间距基线或核心组件语言属于长期决策，应更新本文档并视影响新增 ADR。

## 来源与授权

本文档参考 [VoltAgent/awesome-design-md 的 Binance DESIGN.md](https://github.com/VoltAgent/awesome-design-md/blob/main/design-md/binance/DESIGN.md) 进行中文化和项目化改编。上游内容是对公开可观察设计模式的第三方分析，并非 Binance 官方设计规范，也未获得 Binance 认可。Binance 及其标识属于相应权利人。

本项目不复制 BinanceNova、BinancePlex、Logo 或其他品牌资产。上游仓库采用 MIT License；以下许可文本按要求保留英文原文：

```text
MIT License

Copyright (c) 2026 VoltAgent

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
