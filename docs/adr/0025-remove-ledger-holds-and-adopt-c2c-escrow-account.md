# ADR-0025：账本去冻结与 C2C 托管账户

- 状态：已通过
- 日期：2026-10-08
- 决策者：项目维护者与 AI 产品团队
- 关联内容：[Epic #170](https://github.com/NexusAgentX/Oh-My-AIHub/issues/170)、[Feature #171](https://github.com/NexusAgentX/Oh-My-AIHub/issues/171)、[ADR-0005](0005-adopt-centralized-zero-sum-ledger.md)、[ADR-0008](archive/0008-adopt-immutable-ledger-holds-and-pricing-formula-v1.md)、[ADR-0011](archive/0011-adopt-c2c-order-trade-hold-state-machine.md)、[ADR-0012](0012-adopt-tiered-model-pricing-and-pricing-formula-v2.md)、[ADR-0020](0020-adopt-caller-owned-transactions-across-persistence-domains.md)

## 背景

v0.6.0 的账本在零和复式记账（ADR-0005）之上叠加了两类持有（API 消费授权与 C2C 资产冻结，ADR-0008、ADR-0011）、三列余额投影（已入账、资产冻结、消费授权）、命令幂等表、持有事件，以及在提交时复算这些投影的一组约束触发器。网关为此需要“请求前预授权 → 交付前结算 → 补偿”，C2C 需要“父持有 → 子持有分配 → capture/release”。2026-10-08 维护者判断这套机制让产品与代码都“太难用”，并确认 Epic #170 的决定：

- 网关不预扣费：请求前只检查“余额 > −信用额度”与 Key 预算，请求结束后按用量一次记账，允许单次请求轻微透支。
- C2C 只有卖单；挂卖单时积分转入一个“C2C 托管”系统账户，不再有冻结。
- 余额只存一列，与交易同事务更新；管理员可核销坏账。

## 决策目标

- 账本只保留“平衡交易 + 不可变分录 + 单列余额”三件事，所有业务（API 调用、C2C、调账、坏账）都表达为普通交易。
- 数据库仍强制零和与历史不可变，但只用一个零和约束触发器。
- 账本核心可被网关、C2C 与管理员接口在各自持有的事务中复用（ADR-0020）。

不解决：网关与 C2C 的业务规则本身（Feature B、C）；可观测的对账与指标（Feature G）。

## 候选方案

### 方案一：保留持有，只简化界面

界面不再展示“冻结”，但预授权、父子持有与投影触发器继续存在。代码与运维复杂度不降低，且与“不预扣费”的产品决定冲突。

### 方案二：去掉持有，C2C 用托管系统账户

挂单是一笔普通交易（卖家 → `c2c_escrow`），成交放行是另一笔（`c2c_escrow` → 买家），关闭剩余是退回（`c2c_escrow` → 卖家）。API 调用结束后一笔交易同时记调用方支出、渠道所有者收入与平台手续费。余额约束由调用方在同一事务内检查。

## 决定

采用方案二。

1. 账本表只有 `ledger_accounts`（用户各一个，系统账户 `platform_revenue`、`c2c_escrow`、`bad_debt`）、`ledger_transactions`（类型 `api_call`、`c2c_list`、`c2c_release`、`c2c_return`、`admin_adjust`、`bad_debt_writeoff`，幂等键唯一）与 `ledger_entries`（非零金额与变动后余额）。
2. 数据库只有一个 `DEFERRABLE INITIALLY DEFERRED` 约束触发器：提交时每笔有分录的交易至少两条分录且合计为 0；分录与交易行由普通触发器拒绝 UPDATE/DELETE。余额列与分录之和的一致性不由触发器复算，由 Feature G 的实时核对与集成测试保证。
3. 过账接口 `ledgerpg.Post(ctx, tx, ledger.Transaction)` 在调用方持有的事务中执行：插入交易（幂等键冲突时返回已有交易、不重复记账；同键不同类型或关联对象返回冲突）、按账本账户 id 升序 `FOR UPDATE` 锁定涉及的账户、更新余额并写分录与 `balance_after`。每个账户在一笔交易中至多出现一次，调用方自行合并金额。
4. 业务余额规则由调用方在同一事务内用 `ledgerpg.Balance`、`ledgerpg.CreditLimit` 检查：API 调用要求“余额 > −信用额度”，C2C 挂单要求卖家余额 ≥ 挂单量（只能卖正余额）。
5. 管理员调账以 `platform_revenue` 为对手方；坏账核销把用户全部负余额转入 `bad_debt`，用户余额归零。两者都写审计。
6. 计价保留公式 v2 内核（选档、加权和、成本与手续费分别向上取整，ADR-0012），删除预授权上界计算与“自有渠道免手续费”分支（是否收手续费由调用方传入费率决定）。
7. 账户信用冻结（`credit_frozen`）删除；需要阻止消费时管理员停用账户或把信用额度改为 0。

## 后果

### 正面影响

- 账本概念、表与触发器大幅减少；网关与 C2C 不再需要持有状态机与补偿流程。
- 所有积分变动都是可读的普通交易，用户账单与管理员交易浏览只需一种模型。

### 负面影响与成本

- 没有预扣，并发请求可能让账户透支超过信用额度若干次请求的费用（Epic 已接受，进入管理员“需要处理”）。
- 进程在流式响应中途崩溃会丢失这次计费（Epic 已接受，后台把超时未结束的调用标为中断且不收费）。
- 余额列不再由数据库复算，错误代码可能写出与分录不一致的余额；依赖 `Post` 作为唯一写入路径与核对发现。

### 风险与缓解措施

- 死锁：`Post` 按账本账户 id 升序加锁，集成测试覆盖并发过账。
- 重复记账：交易幂等键唯一，调用方以业务对象 ID 构造幂等键（例如 `call:<id>`）。
- 余额漂移：集成测试断言余额等于分录之和、`balance_after` 连续；Feature G 提供实时核对。

## 验证方式

- 单元测试：交易校验（至少两条、非零、账户不重复、合计为 0、溢出）、计价公式。
- PostgreSQL 集成测试：不平衡与单分录交易在提交时被拒绝、分录与交易不可改不可删、幂等过账、并发过账后余额与分录一致且合计为 0、坏账核销。

## 替代关系

- 取代 [ADR-0008](archive/0008-adopt-immutable-ledger-holds-and-pricing-formula-v1.md) 的持有、投影、命令幂等表与信用冻结部分；其不可变分录与计价取整内核仍有效（计价见 ADR-0012）。
- 取代 [ADR-0011](archive/0011-adopt-c2c-order-trade-hold-state-machine.md) 的父子持有部分；C2C 订单与交易状态机由 Feature C 按本 ADR 的托管账户重写。
- 补充 [ADR-0005](0005-adopt-centralized-zero-sum-ledger.md)。
