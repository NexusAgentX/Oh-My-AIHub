# ADR-0017：采用 sqlc 与按领域分包的持久化分层

- 状态：已通过
- 日期：2026-10-07
- 决策者：仓库维护者
- 关联内容：[Feature #113](https://github.com/NexusAgentX/Oh-My-AIHub/issues/113)、[Epic #109](https://github.com/NexusAgentX/Oh-My-AIHub/issues/109)、[ADR-0006](0006-adopt-postgresql-goose-and-fixed-point-amounts.md)

## 背景

所有领域的 SQL 都手写在单个 `internal/postgres` 包中（`channel.go`、`c2c.go` 等都超过一千行）。每个查询都重复列清单与 `Scan` 代码，改列时没有编译期保护，领域之间共用包级私有辅助函数，边界模糊，也无法让多个 Feature 并行迁移而不互相冲突。

## 决策目标

- 查询与表结构不一致时在生成阶段或编译阶段失败，而不是在运行时才发现。
- 每个领域的 SQL、生成代码和实现自成一个目录，可由不同 Feature 并行迁移。
- 保持 ADR-0006 的约束：Goose 迁移是唯一的结构事实，金额是 `BIGINT` 纳积分并在 Go 中使用 `money.Amount`。
- 迁移期间服务层不改，集成测试断言不改。

不解决：ledger、channel、gateway、c2c、ops 的迁移（第二波 Feature）；跨领域事务编排的统一抽象；schema 变更。

## 候选方案

### 方案一：继续手写 SQL，只做文件拆分

零依赖，但扫描样板与列漂移风险不变，只是挪了位置。

### 方案二：ORM 或查询构建器

隐藏 SQL，难以审阅行锁、`FOR UPDATE`、`LATERAL`、咨询锁等本项目依赖的 PostgreSQL 特性，也与 ADR-0006 “迁移显式、约束在数据库”的取向相左。

### 方案三：sqlc 生成 + 按领域分包

SQL 仍是手写且可审阅的事实来源，sqlc 对照迁移后的 schema 做类型检查并生成扫描代码；生成物提交入库，运行时无新增依赖。

## 决定

采用方案三，版本由 `mise.toml` 固定（当前 1.31.1），配置为 `backend/sqlc.yaml`，驱动 `pgx/v5`，schema 直接指向 Goose 迁移目录（`internal/database/migrations`，sqlc 忽略 `-- +goose Down` 段，后续迁移自动纳入）。

### 目录约定

```text
backend/internal/postgres/
├── store.go         组合根：把各领域 Store 以别名嵌入 postgres.Store；尚未迁移领域的手写 SQL 暂留此包
├── <domain>pg/      每个已迁移领域一个目录，包名同目录名
│   ├── queries.sql  该领域的全部 SQL（sqlc 输入，手写）
│   ├── db.go / models.go / queries.sql.go   sqlc 生成，禁止手改
│   └── store.go     领域 Store：实现 <domain>.Store 接口，负责事务边界、领域映射与错误映射
├── auditpg/         共享：审计事件写入（sqlc + Record 辅助）
└── pgkit/           共享：无 SQL 的事务辅助 InTx
```

规则：

1. **一个领域一个目录，一个 `sql` 条目。** 在 `sqlc.yaml` 复制现有条目，仅改 `queries`、`package`、`out`。目录与包名用 `<domain>pg`，避免与同名领域包 `internal/<domain>` 冲突。
2. **领域服务依赖自己定义的接口**（`identity.Store` 等，保持不变）。领域 Store 在包内以 `var _ <domain>.Store = (*Store)(nil)` 断言实现。领域包之间不互相导入对方的 Store；确需读取别人的数据时，通过对方导出的只读函数并传入事务（参见 `catalogpg.PriceTiersByModel`），或直接在自己的 `queries.sql` 里 JOIN 表。
3. **事务边界在领域 Store 方法内**：用 `pgkit.InTx(ctx, pool, func(tx pgx.Tx) error {...})` 开启，`q := s.q.WithTx(tx)` 执行查询，`auditpg.Record(ctx, tx, ...)` 在同一事务写审计。需要跨领域原子提交（例如账本 + 业务记录）时，由持有两个领域的调用方传入同一个 `pgx.Tx`，各领域暴露接受 `DBTX` 的函数；这在对应领域迁移时再设计。
4. **映射在领域 Store，不在 SQL 之外的地方**：生成的行结构只在 `<domain>pg` 内可见，`toXxx` 函数转为领域类型。账户与余额等成对读取使用 `sqlc.embed(...)`，让所有查询共用一个映射函数。
5. **金额与标识符的类型覆盖集中在 `sqlc.yaml`**：uuid 一律生成 `string`（可空为 `*string`），`timestamptz` 生成 `time.Time`，可空标量生成指针而非 `pgtype.*`。每个纳积分列必须在 `overrides` 中按 `表.列` 登记为 `money.Amount`，未登记会生成裸 `int64`——新增金额列或新领域的金额列时必须同步登记。
6. **共享辅助放在公共位置**：无 SQL 的放 `pgkit`；有 SQL 且被多领域使用的（审计）自成一个小领域目录。不得在领域目录之间复制粘贴辅助函数。
7. **生成物提交入库，CI 校验一致**：`mise run generate` 重新生成，`mise run check-sqlc`（`sqlc diff`）在 CI 中运行，生成结果与 SQL、迁移不一致即失败。
8. **组合根**：`postgres.Store` 以类型别名嵌入各领域 Store（每个领域包的类型都叫 `Store`，需要别名来区分嵌入字段名），方法被提升，`cmd/server` 与集成测试仍使用 `postgres.New(pool)`。迁移一个领域 = 新建 `<domain>pg`，在 `store.go` 增加别名与构造，并删除旧的手写实现。

### 已知限制与应对

- sqlc 生成的 `New(db DBTX)` 返回 `*Queries`，因此领域 Store 的构造函数命名为 `NewStore`。
- 每个领域包都有自己的 `DBTX` 接口类型；它们结构相同，`*pgxpool.Pool` 与 `pgx.Tx` 均满足。`omit_unused_structs` 避免每个包都复制全部表结构。
- 旧手写代码仍把窄接口（只有 `Query`）传给 `loadModelPriceTiers`；它通过 `postgres` 包内的 `readOnlyDB` 适配到 `catalogpg.DBTX`，待 channel 与 gateway 迁移后删除。
- sqlc 不能推断表达式列的覆盖类型，需要时在 SQL 中加显式转换（如 `::bigint`、`::text`、`::timestamptz`）。

## 后果

### 正面影响

- 扫描代码由工具生成，列与类型漂移在 `sqlc generate` 或编译时暴露。
- 领域边界成为目录边界，第二波可并行迁移且互不冲突。
- 无运行时新依赖，生成物可审阅。

### 负面影响与成本

- 开发环境与 CI 需要 sqlc 二进制（由 `mise` 固定）；改 SQL 后必须重新生成并提交。
- 动态 SQL（运行时拼接条件）不适合 sqlc，需要用 `sqlc.narg` + `COALESCE` 等可空参数表达，或在对应领域保留少量手写查询并说明理由。
- 迁移期间存在两种风格并存。

### 风险与缓解措施

- **金额漏登记 override**：生成物中出现 `int64` 的 `*_nano*` 字段；评审生成 diff 时检查。
- **sqlc 版本漂移**：版本固定在 `mise.toml`，CI 使用同一版本。
- **事务语义回归**：保持原有锁顺序与事务边界，由既有 PostgreSQL 集成测试覆盖。

## 验证方式

- `mise run check-sqlc` 在 CI 通过；`go -C backend vet ./...` 与 `go -C backend test ./...` 通过。
- 既有 PostgreSQL 集成测试在不修改断言的情况下全部通过。
- 迁移后 identity、catalog、feerate 不再有手写 `Scan` 代码。

## 替代关系

无。
