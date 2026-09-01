# ADR-0004：原样采用 Airtable 设计分析规范

- 状态：已通过
- 日期：2026-09-01
- 决策者：项目维护者
- 关联内容：Issue #12、`DESIGN.md`、ADR-0001、ADR-0003

## 背景

项目此前采用 Binance 风格。维护者随后尝试过“Clay 品牌气质 + Airtable 产品结构”的混合方案，并要求全产品使用温暖底色；在审阅实际设计后，维护者明确否决该方案，要求完全使用 Airtable 设计体系，不再加入 Clay，也不由项目自行改造色板、组件或视觉规则。

维护者参考的是 [VoltAgent/awesome-design-md 中的 Airtable DESIGN.md](https://github.com/VoltAgent/awesome-design-md/blob/e06a96660396d741d0c106c8972172254dafbdc2/design-md/airtable/DESIGN.md)。该文件是对公开界面的第三方设计分析，不是 Airtable 官方设计规范；上游仓库使用 MIT License。

产品发现目前暂停，具体业务能力仍未确定。本决策只确定设计体系和资产治理，不通过示例画布发明产品功能。

## 决策目标

- 为全部品牌和产品界面提供唯一、固定的 Airtable 设计参考。
- 不再维护 Clay 分层、全暖色覆盖或项目自定义视觉 Token。
- 让 Agent 和实现直接使用上游已定义的颜色、排版、间距、圆角、组件与响应式规则。
- 将采用的上游版本、许可和必要的品牌边界保存到仓库并由 Git 追踪。
- 继续通过 OpenPencil MCP 维护可编辑设计源文件和同名评审预览。

## 候选方案

### 方案一：继续使用 Binance 风格

无需调整现有文档，但与维护者当前选择冲突。

### 方案二：混合 Clay 品牌层与 Airtable 产品层

可以同时追求温暖品牌感和结构化产品界面，但实际预览没有通过维护者审阅，而且会形成一套项目自行解释和维护的混合系统。

### 方案三：原样采用一个固定版本的 Airtable 设计分析

`DESIGN.md` 直接保存选定上游提交中的原文，不改写 Token、不翻译规则、不追加本地视觉覆盖层。后续只有维护者明确选择更新上游版本时才替换该文件。

## 决定

采用方案三。

- 仓库根目录的 `DESIGN.md` 与上游提交 `e06a96660396d741d0c106c8972172254dafbdc2` 中的 `design-md/airtable/DESIGN.md` 保持逐字节一致，作为全部界面的唯一视觉规则。
- 不再使用 Clay、项目自定义暖色底、额外品牌层或其他混合设计规则。
- OpenPencil 设计必须直接使用该文件定义的 Token、组件、节奏和响应式规则。上游明确允许在 Haas 字体不可用时使用 Inter Display 或系统字体，因此设计资产使用该回退不视为本地改造。
- 为兼容 OpenPencil 0.8.4 重新载入后的变量解析，`.op` 保留单值 `appearance: Default` 技术主题轴；该轴不定义第二套视觉方案，也不包含额外 Token。
- 上游 MIT License 保存在 `design/references/airtable-design-analysis/LICENSE`。
- Oh-My-AIHub 继续使用自己的名称、Logo、图标、内容和产品信息。不得把 Airtable wordmark、Logo、专有字体文件、真实产品截图、商标性文案或其他第三方资产提交为本项目资产；这属于身份和授权边界，不构成视觉体系改造。
- 该设计分析中尚未覆盖的交互状态保持“待确认”，不能由 Agent 自行补一套新视觉规则。

## 后果

### 正面影响

- 设计事实来源单一，不再因 Agent 解释不同而漂移。
- Token、组件和页面节奏可以直接对照固定上游版本检查。
- PR 可以通过文件哈希或逐字节比较证明 `DESIGN.md` 没有本地改写。
- 用户否决的 Clay 和全暖色混合方案不会进入 `main`。

### 负面影响与成本

- 上游分析以 Airtable 公开营销界面为主，产品内部状态和复杂业务组件可能存在已知空白。
- `DESIGN.md` 为英文原文，与仓库其他中文文档语言不同。
- Haas 是许可字体，未获得授权时只能使用上游列出的回退字体。
- 白色画布、深色签名卡和 Airtable 原有节奏会完整保留，不能再通过本地色板调整满足其他审美偏好。
- 更新规范需要重新选择上游提交、替换快照并同步 OpenPencil 资产，而不能直接局部编辑 `DESIGN.md`。

### 风险与缓解措施

- 将第三方分析误认为官方规范：在本 ADR 中明确来源和非官方性质。
- 品牌混淆或侵权：只采用设计规则，所有身份、内容和图形资产保持项目自有或许可清楚。
- 上游变化造成不可复现：固定提交 SHA，并将许可文件保存到仓库。
- 规范空白导致 Agent 自行发挥：未知状态标记为待确认，并通过后续人类设计检查点决定。
- 示例被误认为产品定义：设计资产继续明确标记为流程与工具链样例，产品范围仍以 `PRODUCT.md` 为准。

## 验证方式

- 将 `DESIGN.md` 与固定上游原文逐字节比较，结果一致。
- `DESIGN.md` 和 OpenPencil 设计资产中不存在 Clay Token、全暖色覆盖层或 `Warm` 主题。
- ADR-0001 标记为已由本 ADR 取代，ADR 索引指向当前文件。
- OpenPencil 样例使用 Airtable Token 和组件规则，可由 MCP 读取、lint 和导出。
- `mise run check-design` 与 `git diff --check` 通过。

## 替代关系

本 ADR 取代 [ADR-0001：采用 Binance 风格设计语言](0001-adopt-binance-inspired-design.md)。
