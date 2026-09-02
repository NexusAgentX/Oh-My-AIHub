# 架构决策记录

架构决策记录用于保存重要技术选择的背景、取舍和后果。`ARCHITECTURE.md` 描述当前架构，ADR 解释为什么做出会长期影响系统的决定。

## 何时创建 ADR

出现以下情况之一时，应考虑创建 ADR：

- 改变系统或组件边界。
- 选择或替换关键框架、存储、协议、部署方式或外部服务。
- 确定身份、授权、租户、数据生命周期或安全模型。
- 做出难以撤销、迁移成本较高或需要跨团队理解的取舍。
- 有多个合理方案，需要保留选择依据。

普通实现细节、容易撤销的局部调整和纯格式变更通常不需要 ADR。

## 状态

- `提议中`：正在讨论，尚未成为约束。
- `已通过`：已经采用，是当前有效决策。
- `已拒绝`：讨论后未采用，保留原因供参考。
- `已取代`：被更新的 ADR 替代。
- `已废弃`：不再适用，且没有直接替代项。

## 编号与流程

1. 复制 `0000-template.md`。
2. 使用下一个四位编号命名文件，例如 `0001-api-boundary.md`。
3. 填写背景、方案、决定、后果和验证方式。
4. 将记录加入下方索引。
5. 决策改变时新建 ADR，并在旧记录中注明替代关系。

## 索引

- [ADR-0001：采用 Binance 风格设计语言](0001-adopt-binance-inspired-design.md) — 已取代（由 ADR-0004 取代）
- [ADR-0002：采用人类定向、AI 执行的持续产品研发模型](0002-adopt-ai-native-product-workflow.md) — 已通过
- [ADR-0003：将 OpenPencil 可编辑设计源文件作为 Git 一等资产](0003-version-openpencil-design-assets-in-git.md) — 已通过
- [ADR-0004：原样采用 Airtable 设计分析规范](0004-adopt-airtable-design-analysis.md) — 已通过
- [ADR-0005：采用中心化零和复式账本作为积分清算核心](0005-adopt-centralized-zero-sum-ledger.md) — 已通过
- [ADR-0006：采用 PostgreSQL、Goose 与九位定点金额](0006-adopt-postgresql-goose-and-fixed-point-amounts.md) — 已通过
- [ADR-0007：采用受邀身份与服务器端 Cookie 会话](0007-adopt-invited-identity-and-server-sessions.md) — 已通过
