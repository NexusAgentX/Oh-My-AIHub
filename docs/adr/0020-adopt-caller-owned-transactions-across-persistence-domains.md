# ADR-0020：跨领域原子提交由调用方持有事务

- 状态：已通过
- 日期：2026-10-07
- 决策者：仓库维护者
- 关联内容：[Feature #122](https://github.com/NexusAgentX/Oh-My-AIHub/issues/122)、[Epic #109](https://github.com/NexusAgentX/Oh-My-AIHub/issues/109)、[ADR-0017](0017-adopt-sqlc-domain-persistence-layering.md)、[ADR-0005](0005-adopt-centralized-zero-sum-ledger.md)、[ADR-0011](0011-adopt-c2c-order-trade-hold-state-machine.md)、[ADR-0010](0010-adopt-snapshot-gateway-and-idempotent-settlement.md)

## 背景

C2C 与网关需要让账本记账、积分持有和各自的业务行在同一个数据库事务内提交：持有创建、释放、捕获与交易、调用状态必须同时成功或同时回滚，并由账本的延迟约束触发器在同一次提交时校验。ADR-0017 把每个领域放进独立的 `<domain>pg` 包，并只写明“由持有两个领域的调用方传入同一个 `pgx.Tx`，具体在对应领域迁移时设计”。账本迁移（Feature #122）需要先固定这条约定，后续 c2c、gateway、ops 迁移才能一致遵循。

## 决策目标

- 账本与业务行仍在一个事务内原子提交，锁顺序与延迟约束语义不变。
- 领域包之间不互相导入对方的 Store，不引入跨领域事务编排框架。
- 事务隔离级别（网关使用 RepeatableRead / Serializable）和提交时机始终由业务领域决定。

不解决：c2c、gateway、ops 自身的 sqlc 迁移。

## 候选方案

### 方案一：账本 Store 暴露 `WithTransaction(work)`，业务在回调内使用

账本拥有事务，业务只能在回调里借用。无法让业务领域选择隔离级别、保存点或在提交前后做自己的工作，网关的 `BeginTx` 选项与提交钩子无法表达。

### 方案二：调用方开启 `pgx.Tx`，各领域暴露接受 `DBTX` 的绑定类型

调用方（业务领域）开启事务并决定隔离级别与提交；账本通过 `ledgerpg.NewTx(db DBTX)` 得到绑定到该事务的 `*ledgerpg.Tx`，它实现完整的 `ledger.Store`，可直接传给 `ledger.NewService`。账本类型从不开启、提交或回滚事务。

### 方案三：统一的工作单元（Unit of Work）抽象

可以集中管理，但为目前仅有的两处调用方引入新的抽象层与注册机制，成本高于收益。

## 决定

采用方案二。

1. 账本包 `ledgerpg` 提供两个入口：`Store`（每个操作自己开启事务，读取直接走连接池，供服务层使用）和 `Tx`（`ledgerpg.NewTx(db DBTX)`，绑定到调用方持有的事务或保存点，实现 `ledger.Store`，另含仅限内部使用的 `ReverseSystem`）。`Store.WithTransaction` 是便捷封装：开启事务、执行回调、用 `ledgerpg.MapError` 映射提交错误。
2. 需要跨领域原子提交的业务领域在自己的事务内调用 `ledger.NewService(ledgerpg.NewTx(tx))`，随后写入自己的行并由自己提交。导入方向只允许业务领域包导入 `ledgerpg`，反向不允许。
3. 账本在调用方事务内的锁顺序不变：先按 `ledger_accounts.id` 升序加锁，再读取账户信用状态；账本命令的幂等行、分录、余额投影与持有事件都在同一事务内写入，延迟约束在调用方提交时统一校验。
4. 业务领域把 `Tx` 与自己的 pgx 事务一起使用时不得在账本调用之间提交；使用保存点（`tx.Begin`）的调用方对保存点同样可用 `NewTx(savepoint)`。
5. 过渡：尚未迁移的 `postgres` 包内手写代码继续使用 `postgres.LedgerTransaction`，它嵌入 `pgx.Tx` 与 `*ledgerpg.Tx`，由 `newLedgerTransaction(tx)` 构造；channel、c2c、gateway 迁移完成后删除它，迁移后的领域直接使用 `pgx.Tx` 与 `ledgerpg.NewTx`。

## 后果

### 正面影响

- 业务领域保留对隔离级别、保存点与提交时机的控制，账本逻辑只有一份实现。
- 账本包无需知道任何业务表，领域边界保持单向依赖。

### 负面影响与成本

- 调用方必须自己保证在同一个 `pgx.Tx` 上使用账本绑定类型；传入不同连接会破坏原子性，只能靠评审与集成测试发现。
- 迁移完成前存在 `postgres.LedgerTransaction` 过渡类型。

### 风险与缓解措施

- **跨事务误用**：既有 c2c 与网关集成测试覆盖账本与业务行同提交、同回滚。
- **锁顺序回归**：账本内部锁顺序集中在 `ledgerpg/posting.go` 的 `lockAccounts`，业务领域不得自行锁账户行后再调用账本以外的顺序。

## 验证方式

- 账本、C2C、网关、运营的 PostgreSQL 集成测试在不修改断言的情况下通过。
- `mise run check-sqlc` 无差异。

## 替代关系

补充 ADR-0017 第 3 条关于跨领域事务“再设计”的部分，不取代 ADR-0017。
