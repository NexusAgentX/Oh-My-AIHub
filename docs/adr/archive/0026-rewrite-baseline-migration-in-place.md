# ADR-0026：产品重写时原地重写基线迁移

- 状态：已通过
- 日期：2026-10-08
- 决策者：项目维护者与 AI 产品团队
- 关联内容：[Epic #170](https://github.com/NexusAgentX/Oh-My-AIHub/issues/170)、[Feature #171](https://github.com/NexusAgentX/Oh-My-AIHub/issues/171)、[ADR-0006](../0006-adopt-postgresql-goose-and-fixed-point-amounts.md)、[ADR-0024](0024-squash-migrations-to-single-baseline.md)

## 背景

ADR-0024 把迁移压缩为单一基线 `0001_baseline.sql`，并约定此后的结构变化从 `0002` 起追加。2026-10-08 维护者决定把产品原地重写为“API 市场 + 积分 C2C 市场”（Epic #170）：v0.6.0 的 34 张表中绝大多数被删除或重新设计（持有、命令、投影、路由池、报价校验、调用候选与结算、C2C 命令与事件、巡检），发版后生产数据库重建，只保留账号与模型目录。

若按 ADR-0024 追加 `0002`，迁移会先建出旧结构再全部删除，读者与 sqlc 都要理解一份已被抛弃的模型。

## 决策目标

- 迁移目录继续只有一份描述当前产品事实的基线。
- 明确这是未发布中间态的一次性例外，不改变 ADR-0024 之后“追加式迁移”的一般规则。

## 候选方案

### 方案一：追加 `0002` 删除旧表并建新表

保持迁移历史线性，但基线描述的是已删除的产品，`0002` 是一次几乎全量的重建。

### 方案二：原地重写 `0001_baseline.sql`

基线直接描述重写后的结构；已有数据库须重建。

## 决定

采用方案二。

1. Feature #171 原地重写 `backend/internal/database/migrations/0001_baseline.sql`：18 张表，按“身份与审计、平台设置与模型目录、渠道、API Key 与路由、调用、账本、C2C”分节，seed 三个系统账本账户与 `settings` 默认行；`Down` 删除全部对象。
2. 新表一次性包含 Feature B、C、G 所需的全部列，使后续 Feature 在重写期间只改查询与代码，不再改迁移；确需改表时仍在本次重写周期（Epic #170 发版前）内原地修改基线，并在对应 Feature 中说明。
3. Epic #170 发版（Feature F）后恢复 ADR-0024 的规则：结构变化从 `0002` 起追加。
4. 已有开发数据库须 `docker compose down -v` 后重建；生产由 Feature F 重建，导入保留的账号与模型目录。

## 后果

### 正面影响

- 基线只描述新产品；sqlc 与读者不必理解被删除的模型。

### 负面影响与成本

- 任何基于旧基线的数据库都不能升级，只能重建。

### 风险与缓解措施

- 重写期间多个 Feature 并行修改基线可能冲突：Feature A 一次定下全部表，后续 Feature 原则上不改表；冲突由后合并者解决。
- 生产重建遗漏数据：由 Feature F 的发布清单单独验证账号与模型目录导入。

## 验证方式

- 空库上 `goose up`、`down`、`up` 均成功，表数量为 18（集成测试断言）。
- `mise run check-sqlc` 与 PostgreSQL 集成测试通过。

## 替代关系

补充 [ADR-0024](0024-squash-migrations-to-single-baseline.md)：在 Epic #170 发版前作为一次性例外原地重写基线，发版后 ADR-0024 的追加规则继续有效。
