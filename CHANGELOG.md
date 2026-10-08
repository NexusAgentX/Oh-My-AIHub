# 变更日志

本文档记录对使用者、开发者和部署环境有意义的变化。当前最新版本为 v0.9.0；未发布变化记录在“未发布”章节。

## 未发布

暂无。

## v0.9.0 - 2026-10-08

- 自动同步 Bifrost 聊天/Responses 模型资料，支持固定美元换算率、手动触发和单模型退出/恢复同步。
- 新模型停用、缺价禁止启用；保留来源、实际应用汇率和未覆盖说明，删除后不复活，保护手工模型。
- 管理模型列表增加搜索/分页与同步状态，来源区独立显示最大输入/输出和未知能力。

- 生产从 v0.8.0 升级须先备份并新增两张同步表，保留现有业务数据；步骤见发布 Runbook。汇率初始留空。

## v0.8.0 - 2026-10-08

- 细分 token 定价：支持缓存写入 5m/1h、可靠模态用量，以及实际响应服务档位和百炼思考条件；调用与账单保存当次价格快照，不可靠明细明确显示未细分。工具、容器、存储等附加费用仍未覆盖（#190，ADR-0030）。
- 输入长度条件明确为「输入总量分档」，模型高级资料统一称为「模型备注」（#193、#195）。
- 生产从 v0.7.0 升级前须备份并补齐四个定价字段，保留原有数据；具体 SQL 见发布 Runbook。新实例使用当前数据库基线。
- 管理后台支持确认删除模型，保留历史账单并保护仍被渠道使用的模型。

## v0.7.0 - 2026-10-08

### 升级须知

- 本版本是产品原地重写（Epic #170），不能从 v0.6.0 及更早版本升级：数据库基线已原地重写（ADR-0026），已有数据库必须重建，开发数据库须 `docker compose down -v` 后重新启动。生产实例 `ai.isok.dev` 于本版本上线时重建，只保留账户与模型目录（含条件价格档）和手续费率，余额、渠道、平台 API Key、C2C 与调用记录清空；用户需要重新复制默认 Key，共享者需要重新添加渠道（Feature #177）。
- 新增可选环境变量 `METRICS_ADDR`（默认 `:9090`）：Prometheus 指标只在 Compose 内网暴露，不发布到宿主机、不经 Nginx 代理。其余环境变量不变。

### 概要

- 产品定位改为「API 市场 + 积分 C2C 市场」：积分由信用额度发行，同一账户可以用 API、卖 API、买卖积分；界面默认简单，高级能力折叠在「高级设置」里。
- 网关改为透明转发 + 事后记账（ADR-0027）：只换鉴权、可选换 UA 与请求头规则、字节级替换顶层 `model`；不预扣、不冻结、不做格式转换；按用户按模型路由（便宜、稳定、快速、手动，Key 级覆盖）；四种原生格式与 `/v1/models`。
- 账本去掉持有与冻结，C2C 改用托管系统账户（ADR-0025）；平台 API Key 可逆加密、可再次复制（ADR-0028）；可观测性现算不存快照，指标走内网端口（ADR-0029）。
- 用户界面（9 个页面）、管理后台（8 个页面）与落地页全部重写，保留左侧分组侧栏、移动端底部 Tab、原有配色与图标。

### 变更

- 发版收尾（Feature #177）：管理员概览「记账或核对异常」的标题改为中文核对名（不再显示内部键），「渠道异常」每个渠道计 1 条（`AttentionItem.count` 写明含义）；管理员渠道详情页加入渠道统计面板；文档改为当前事实。
- 前端对齐观测与积分契约（Feature #183，Epic #170）：调用列表的渠道列显示「换了 N 次」，汇总显示首字 p50/p95 与总 tokens，请求 ID 改为列表精确筛选（不再直接打开详情）；模型详情中达到上限的渠道显示「今日额度剩 X%」；积分账单对账条按调用支出 / 渠道收入 / C2C 买入 / C2C 卖出 / 调账与核销拆分并跟随所选月份，按天 / 按 Key 汇总可用；渠道统计增加 24h/7d 成功率、p95、按小时趋势、今日收入上限进度、失败按状态码分布与最近失败（含上游原始错误），渠道调用标出「已换走」的尝试。管理后台：概览显示 24 小时调用（数量与成功率）与 24 小时 C2C 成交（笔数、数量与均价），「需要处理」全部类型有专属文案与跳转；积分页显示信用发行与总额度、托管卖单与交易中笔数、坏账已核销笔数，走势可切换流通与信用、收入与坏账、API 结算量与手续费、C2C 成交量、C2C 均价并按 7/30/90 天向后端取数，五项实时核对逐项展示并对漏记调用逐条补记或作废，风险表显示负余额天数、持有集中度取自后端，交易浏览可按关联对象筛选，交易详情显示变动前余额、价格快照与相关用户最近的人工操作；用户列表显示最近活跃；申诉详情的相关账本改为按关联对象查询。删除为 501 写的临时降级（如按账户列表估算集中度、前端截取走势时间窗）。
- 调用与积分可观测性后端（Feature #176，Epic #170）：实现 `GET /api/calls`（含筛选与汇总：请求数、成功率、首字 p50/p95、tokens、费用）、`/api/calls/{id}`（尝试时间线、价格快照、关联账本交易，渠道所有者只看到自己渠道的尝试）、`/api/usage`（按天/模型/Key/标签，`view=revenue` 为共享者收入视角）、`/api/channels/{id}/stats` 与 `/calls`、三个 SSE 实时流（心跳 15 秒，慢消费者丢弃）、内网 Prometheus `/metrics`（`prometheus/client_golang`，调用、渠道与积分指标，无用户或 Key 标签）、`/api/points` 的 30 天走势与期间对账、`/api/points/entries` 的 `group=day|key` 汇总与 `format=csv` 导出（UTF-8 BOM）、`/api/admin/points`（余额结构、五项实时核对、7/30/90 天走势、风险与持有集中度）、`/api/admin/overview`（需要处理与四个指标块）、账本交易浏览与详情、`POST /api/admin/ledger/repair-call/{id}` 补记（幂等、写审计）；后台每天清空 30 天前调用的上游原始错误文本。契约补齐 D/E 记录的缺口：`CallSummary.attempt_count`、`CallStats.ttft_p95_ms`/`total_tokens`、请求 ID/耗时/tokens/费用区间筛选、`ModelChannel.daily_cap_remaining`、`PointsPeriod` 按类别拆分、`ChannelStats` 的状态码分布/最近失败/24h·7d 窗口、`AdminOverview` 的 24 小时调用与 C2C 成交、`AttentionItem.kind` 新增类型、`AdminPoints` 的走势序列/核对/风险天数/持有集中度、交易浏览的关联对象筛选与价格快照、`AdminAccount.last_active_at`。`calls` 增加 `attempts` GIN 索引与漏记核对的部分索引。自己的渠道调用不产生账本交易，故其 `charged` 与渠道收入为 0，漏记核对与补记排除它们。
- C2C 卖单市场（Feature #173，Epic #170）：实现卖单挂出、市场列表、买入（部分成交、单笔范围、同买家同卖单唯一未完成交易、付款截止）、我已付款、卖家放行、取消、关闭卖单、文字申诉与管理员仲裁，以及每分钟一次的付款超时取消任务。挂单把积分转入 `c2c_escrow` 系统账户，只能卖正余额；放行、退回在同一事务内过账，订单数量恒等式由数据库 CHECK 保证，状态转换幂等。收款方式只存文字并用 `C2C_PRIVATE_DATA_KEYRING` 加密，只对交易双方（买家仅在交易未结束时）与管理员可见。契约调整：挂单请求的 `min_per_trade` 改为可省略（省略 = 不限），`GET /api/c2c/my/orders` 增加 `status` 筛选，`GET /api/c2c/my/trades` 增加 `pending`（待我处理）筛选，申诉陈述上限 1000 字；C2C 接口不再返回 501。新增 `c2cpg` 持久化包与 `ledgerpg.LockBalance`。
- 管理后台与落地页重写（Feature #175，Epic #170）：管理后台移到 `frontend/src/admin/`，独立外壳（侧栏 8 项，移动端前 4 项进底部 Tab），`/admin` 概览（需要处理、账本、信用占用、今日调用、C2C）、调用、积分（五个数与零和等式、7/30/90 天走势、实时核对与补记调用、风险与持有集中度、交易浏览与分录抽屉、操作记录）、用户（新建与一次性初始密码、信用额度、启停、管理员标记、重置密码、调账、坏账核销）、模型（基准价、高级设置、最多 16 档的条件价格档编辑器，支持跨午夜与上下移动）、渠道（列表、只读详情、强制下架与恢复）、申诉（列表、详情、仲裁）、设置（手续费率百分比、付款超时与高级设置）。调账、坏账核销、仲裁、强制下架与恢复需填写原因并二次确认，重置密码需二次确认。依赖尚未实现接口（501）的区块显示可重试的错误态。落地页改为「API 市场 + 积分市场」定位（用 API、卖 API、积分），只有登录入口；已登录访问 `/` 跳到 `/home`。走势图为自绘 SVG，未新增依赖。
- 地基重写（Feature #171，Epic #170）：`0001_baseline.sql` 原地重写为 18 张表（身份与审计、平台设置、模型目录与条件价格档、渠道、API Key 与路由、调用、账本、C2C），删除持有/冻结、账本命令、投影与对账触发器群、费率版本、渠道凭据与报价校验、路由池、调用候选/尝试/结算/补偿、C2C 命令/事件/支付方式/争议陈述与巡检表。账本只保留一个零和约束触发器与分录不可变触发器，余额只存一列；新增 `ledgerpg.Post`（调用方事务内过账、幂等键、按序加锁、`balance_after`）与系统账户 `platform_revenue`、`c2c_escrow`、`bad_debt`；删除账户信用冻结（ADR-0025）。`backend/api/openapi.yaml` 重写为完整契约（约 70 个 JSON 接口与 6 个外部入口），错误统一为 `{"error": "<code>", "message": "..."}`，列表统一游标分页；未实现接口返回 501，契约测试对照 `x-access`、`x-feature` 与实现状态。已实现：实例初始化、登录与退出、`GET /api/me`、`POST /api/me/password`、`GET /api/points`、`GET /api/points/entries`、管理员账户（创建、修改、重置密码、调账、坏账核销）、模型目录（`PATCH` 部分更新，价格档整组替换）、平台设置与审计查询。旧的 `/api/auth/session`、`/api/account*`、钱包、市场、渠道、网关、C2C、运营与手续费率接口删除。计价保留公式 v2 内核（`CalculatePriceV2`），删除预授权与自有渠道分支。前端删除依赖旧接口的全部页面，保留可构建外壳。恢复演练脚本改为直接核对账本余额合计为 0 且与分录一致。
- 用户界面重写（Feature #174，Epic #170）：新外壳（桌面分组侧栏与账户菜单、移动端底部 5 个 Tab 与「我的」页）与 9 个用户页面——首页（接口地址、默认 Key 与按格式的调用示例、余额、今天、最近调用、待处理交易横幅）、模型与模型详情（价格档、调用方式、渠道勾选与手动排序含键盘操作）、API Key 列表与抽屉（再次复制、高级设置五项含 Key 级路由、上限 20 把）、用量（分组花费图、筛选、汇总、实时、调用详情与尝试时间线）、我的渠道与三步上架向导及编辑页（读取上游模型、测试格式、上游名称、高级设置、统计、健康事件、调用）、积分（余额与走势、买卖抽屉、我的交易与交易详情、我的卖单、账单对账、汇总与导出）、账户设置。调用观测组件放在 `frontend/src/calls/` 供管理后台复用。B/C/G 接口未实现前相关区块显示可重试的 501 错误态。
- 透明网关、渠道、API Key 与路由（Feature #172，Epic #170）：`POST /v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1beta/models/{model}:generateContent|:streamGenerateContent` 与 `GET /v1/models`、`/v1beta/models` 上线；请求体除顶层 `model`（及 Chat 流式的 `include_usage`）外与客户端发送的字节一致，响应原样回写并旁路读取用量，失败自动换渠道，请求结束后一次记账（不预扣，ADR-0027）。新增渠道（添加、发现上游模型、格式测试、上下架、软删除）、API Key（最多 20 把、默认 Key、再次复制、预算、别名、可用模型，ADR-0028）、按用户按模型的路由（便宜/稳定/快速/手动，Key 级覆盖）、模型浏览与首页、管理员渠道治理；每次调用有请求 ID（`X-AIHub-Request-Id`）、尝试时间线、流式指标与 `X-AIHub-Tag`，进程内事件总线与结构化日志供 Feature G 订阅。ADR-0010、ADR-0014、ADR-0016 被取代，ADR-0008 的预授权部分不再适用。

## v0.6.0 - 2026-10-08

### 升级须知

- 本版本不能从 v0.5.0 及更早版本原地升级：迁移已压缩为单一基线（ADR-0024），已有数据库必须重建。生产实例 `ai.isok.dev` 于本版本上线时重建，只保留账户与模型目录，余额、渠道、平台 API Key、C2C 与调用记录清空（Feature #166）。

### 变更

- 数据库迁移压缩为单一基线（Feature #165）：`0001`～`0010` 合并为 `backend/internal/database/migrations/0001_baseline.sql`，结构等同原 v10，但删除只服务历史数据的遗留对象：`c2c_orders.side` 列及其 `NOT VALID` 约束、买单索引与买单触发器分支（`parent_hold_id` 改为 `NOT NULL`），以及从既有账户回填用户账本账户的种子；保留 `platform_incentive`、`platform_loss` 两个系统账本账户与默认手续费率。删除 `database.MigrateTo` 和按旧版本号迁移的测试。`goose down` 现在删除全部对象。已有开发数据库不再兼容，须执行 `docker compose down -v` 后重建；此后的结构变化从 `0002` 起追加。见 ADR-0024。
- 运营台精简、删除旧重定向与精简产品文档（Feature #161，Epic #158）：运营台只保留「总览」（含共享者收入）与「账本与费率」（含巡检历史）两个分区，`tab` 参数只接受这两个值；删除试用证据摘要与 `GET /api/admin/ops/trial-summary`（含 OpenAPI 路径与 schema、sqlc 查询、前端查询）；巡检逻辑、硬异常与收入口径不变。删除 7 条旧 URL 重定向：`/keys/new`、`/keys/:keyID/settings`、`/market/channels/:channelID/add`、`/wallet/insufficient`、`/c2c/orders/:orderID/take`、`/c2c/trades/:tradeID/dispute`、`/admin/providers`，旧地址不再重定向（落到公开落地页）。`PRODUCT.md` 删除「待验证假设」「待真实使用回答」，改为简短「已知风险」，`ROADMAP.md` 删除真实验证表述。无数据库迁移。
- 工作台待处理事项改由后端单一聚合接口提供（Feature #160，Epic #158）：新增 `GET /api/dashboard/pending-items`，服务端按当前用户一次计算待放行 / 待付款的 C2C 交易（只看交易买卖双方身份与状态）、校验失败与已暂停的渠道、含不可用渠道的路由和只有一个渠道的路由，不再受“最近 100 笔调用”分页限制；前端删除 `pendingDerive.ts` 的多接口拼接推导，只渲染接口返回的条目。
- C2C 只保留卖单（Feature #159，Epic #158）：删除买单（`side=buy`）。创建挂单不再接受 `side`；订单、交易与行情响应删除 `side`、`order_side`、`buy_orders`、`best_bid_fen`、`spread_fen`，运营指标的订单计数不再按方向分组、报价不含买一与价差；钱包恢复动作删除 `create_buy_order`。市场页直接列出卖单，挂单编辑器无方向切换，承接按钮统一为“购买”。数据库结构已并入 `0001` 基线（不再有 `side` 列）。见 ADR-0023、ADR-0024。

### 移除

- 删除渠道 1～5 分评分（Feature #160，Epic #158）：删除 `PUT /api/market/channels/{channelID}/rating`、市场 / 渠道 / 管理员响应中的 `average_rating`、`rating_count`、`current_user_rating` 字段和 `rating` 排序选项，以及市场列表、公开渠道详情、共享者与管理员渠道页的评分展示和 `MarketRating` 组件；`0001` 基线不再包含 `channel_ratings` 表。工作台不再有“待评分”事项。
- 删除 `GET /api/dashboard` 响应中不再使用的整数字段 `pending_items`（不健康报价计数），避免与新的 `GET /api/dashboard/pending-items` 混淆（Feature #160）。

## v0.5.0 - 2026-10-07

### 新增

- 模型价格支持缓存 TTL、可选模态单价、实际服务档位与百炼思考条件；账单保留细分价格和未细分原因。

- 管理后台改版（Feature #129，Epic #109）：运营总览、共享者收入与试用证据摘要合并为 `/admin/ops` 运营台（总览 / 共享者收入 / 试用与巡检 / 账本与费率分区，时间窗口、硬异常与固定下钻常驻，窗口与分区写入 URL），`/admin/providers` 重定向到 `/admin/ops?tab=providers`，管理员导航移除「共享者收入」。账户、模型目录、渠道治理、争议处理迁移到 TanStack Query 与 `src/ui` 组件；模型条件价格档改为紧凑列表（条件摘要、四价、上移/下移改变优先级）加抽屉编辑器，仅展开已开启的 Token 区间或星期/时间窗条件，并支持以 24:00 结束的时间窗。后端与 API 不变；争议裁决与限制账户新增确认弹窗。
- 删除 C2C 付款截图与争议证据图片（Feature #111，Epic #109）：买家声明付款与双方争议陈述只保留文字，卖家收款方式的收款码保持不变。删除付款截图和争议图片的上传、加密存储与下载；删除 `GET /api/c2c/evidence/{evidenceID}`，`POST /api/c2c/trades/{tradeID}/evidence` 改名为 `/statements`，`/paid` 与争议接口只接受 JSON，交易响应不再含 `evidence`。迁移 `0008` 删除 `c2c_evidence` 表及其触发器与函数；终态 180 天私密清理继续销毁付款参考与争议陈述。`C2C_PRIVATE_DATA_*` 密钥环仍保护收款资料与陈述，部署配置无需更改。
- 管理员配置全局 API 手续费率（Feature #97）：运营总览新增手续费率面板，可查看当前费率与历史版本并设置新费率（0%～100%，最多 7 位百分比小数，即九位定点比率）；新增 `GET/PUT /api/admin/fee-rate`，每次修改追加不可变版本、按期望版本乐观并发并写入带原因与前后值的审计。新费率只影响之后的新调用，历史调用保留原快照。
- C2C 争议裁决支持限制当事方账户（Feature #98）：管理员在争议详情页可带原因并二次确认后冻结买方或卖方信用（`POST /api/admin/c2c/trades/{tradeID}/resolve` 新增 `restrict_buyer` / `restrict_seller` 动作）。冻结复用账户信用冻结，交易状态和积分持有保持不变，可与延长核实并用；操作写入交易事件与审计，已冻结时幂等成功。信用冻结账户现在也不能发布 C2C 买单或接取卖单。管理员交易响应新增 `buyer_credit_frozen` / `seller_credit_frozen`。
- 信用冻结账户的 C2C 挂单禁止被承接（Feature #103）：所有者信用冻结期间他人接取其挂单会被拒绝，解冻后恢复；不自动取消挂单、不释放父持有，已有交易不受影响。此类挂单与停用或未改密所有者的挂单一并从公开市场列表和最优买卖价中隐藏（此前停用所有者的挂单仍会列出但接取必然失败），订单响应新增 `takeable`，接取页对不可接取订单显示提示而非表单。

### 变更

- 取消用户验证环节（Feature #154，ADR-0022）：任务状态只保留 Ready 与 Done；`AGENTS.md`、`PRODUCT.md`、`ROADMAP.md`、`README.md` 与 Issue 模板移除 Validated 与真实用户验证要求，删除小圈子试用 Runbook，关闭 #22。
- 前端遗留清理与验收缺陷修复（Feature #148，Epic #109）：抽屉与对话框不再被 `.page-content` 的子元素间距推移（基础层改为排除 `dialog`，删除 `c2c.css` 局部补丁）；管理员争议详情在交易进入终态后禁用「放行给买家」「退还给卖家」「取消剩余挂单」（延长复核仅限 `disputed`，放行/退还/限制账户仅限 `paid`、`disputed`，与后端一致）；表单中聚焦的数字输入框滚动滚轮不再改变数值；删除旧 `.auth-*`、渠道旧编辑器/校验历史/管理员样式、C2C 已迁出的管理员样式、`ui/FormControls` 兼容出口与未引用的 `requestGate.ts`；渠道与网关页共用 `gateway/presentation` 中的 `protocolLabels`、`PricePair`、`formatDate`、`ratingText`。无后端与 API 变化。
- 前端 API 类型改由 OpenAPI 生成（Feature #146，ADR-0021，Epic #109）：新增开发依赖 `openapi-typescript`（隔离在 `frontend/tools/openapi-types/`），从 `backend/api/openapi.yaml` 生成已提交的 `frontend/src/api/schema.gen.ts`，删除手写 `contracts.ts`，`api/types.ts` 提供别名；`client.ts` 的请求体与响应按 operationId 校验。CI 新增生成一致性检查，`mise run generate` 同时生成前端类型。规范修正：价格档请求 `weekdays` 允许 `null`，市场 `sort` 与钱包 `recovery_actions.kind` 补充枚举。无运行时行为、URL 或字段变化。
- 后端重构遗留清理（Feature #147，Epic #109）：删除过渡类型 `postgres.LedgerTransaction` / `Store.WithLedgerTransaction`（测试改用 `pgx.Tx` + `ledgerpg.NewTx`，断言不变）；对既有未格式化的后端文件执行 `gofmt`，CI 新增 `gofmt -l backend` 检查；`POST /api/instance/initialize` 在请求体无法解析时改为返回 `400 invalid_json`（此前返回空的 200），OpenAPI 与契约测试同步。
- API 层按领域拆分路由并产出 OpenAPI 契约（Feature #124，ADR-0021）：`backend/internal/api` 的路由改为各领域文件内注册，中间件、限流状态与响应辅助各自独立成文件，`handler.go` 只保留组装；新增 `backend/api/openapi.yaml`（OpenAPI 3.1，覆盖全部 `/api/**` JSON 接口，外部协议入口只登记路径与认证）及契约测试（路由表与规范一一对应、响应按 schema 校验）。新增测试依赖 `jsonschema/v6` 与 `yaml/v3`。URL、方法、字段、状态码与错误文案均不变。
- 前端改版“使用 API”（Feature #126，Epic #109）：工作台新增快速开始（选择模型与原生协议、从市场勾选一个或多个渠道，创建 Key 与路由后展示带真实 Base URL 与 Key 的 curl / Python / Node.js 调用示例，完整 Key 仍只显示一次）和待处理事项（由现有接口在前端组合：待放行/待付款 C2C 交易、校验失败或暂停的渠道、单渠道路由、需更新的路由渠道、已使用未评分渠道）；API Key 创建与设置、加入路由改为抽屉，界面用语“模型协议池”改称“路由”、优先级改称“备用顺序”并保留版本冲突提示；`/keys/new`、`/keys/:keyID/settings`、`/market/channels/:channelID/add` 改为重定向到对应抽屉；API 市场筛选以 URL 参数保存并支持清除筛选，公开渠道详情保留 1～5 分评分；调用记录与详情迁移到 TanStack Query 与基础组件。无后端与接口变化。
- 前端改版：积分（Feature #128，Epic #109）：钱包页的风险提示条内含补足积分入口，原“余额不足”页并入钱包，`/wallet/insufficient` 重定向至 `/wallet`；账本分录改用统一数据表。C2C 全部页面迁移到 TanStack Query 与基础组件：市场页按卖单/买单分标签展示，承接挂单改为抽屉（`/c2c?take=<订单>`，旧 `/c2c/orders/:id/take` 重定向），取消挂单/交易、放行与争议陈述改为确认对话框与表单对话框（旧 `/c2c/trades/:id/dispute` 重定向至交易详情），我的挂单与交易以标签页展示并标出待你付款/待你放行，交易详情未结束时自动刷新，交易记录改为中文说明。买卖双向挂单、部分成交、卖单收款码上传与展示、文字付款声明与争议均保持不变；不改后端与 API。
- 删除交互式命令 `cmd/bootstrap-admin`（Feature #125，Epic #109）：实例初始化只保留网页 `/initialize`（`POST /api/instance/initialize`，行为不变）；同时移除 `mise run bootstrap-admin` 任务、后端镜像中的 `bootstrap-admin` 二进制与 `golang.org/x/term` 依赖。部署手册首次部署步骤改为访问 `/initialize`。
- 前端改版公开页与身份（Feature #130，Epic #109）：落地页精简为一屏（产品定位、消费者与共享者价值、备用顺序、隐私与积分边界、受邀登录入口），移除示意市场表与演示数据，`WelcomePage` 由 634 行 TSX + 1799 行 CSS 降至 126 行 + 134 行；登录、实例初始化、首次改密与账户设置统一为同一套外壳、基础组件与错误展示（登录限流以提示条显示），首次改密与账户设置共用 `PasswordChangeForm`，提交改用 TanStack Query 写操作（`auth/queries.ts`）。路由守卫、权限跳转与后端接口不变。
- 前端基础改版（Feature #114，ADR-0019）：新增设计 token（`styles/tokens.css`）与按组件拆分的样式，基础组件库 `src/ui/`（Button、Card、Metric、Badge、DataTable、Toolbar、EmptyState、Dialog、Drawer、Tabs/Segmented、表单控件与独立图标集）；外壳改为用户与管理员两个 react-router layout route，页面不再各自包裹 `AppShell`；用户导航分为“使用 API / 共享渠道 / 积分”三组，顶栏常驻可用积分与钱包入口，760px 以下改为底部 Tab 栏与“更多”抽屉；引入 TanStack Query，工作台与钱包迁移为样板并以 `useWallet()` 取代 `WalletProvider`。路由、权限跳转与后端接口不变；删除未使用的 `UpcomingC2CPage`。
- 账本持久化迁移到 sqlc（Feature #122，Epic #109，ADR-0020）：`internal/postgres/ledger.go` 由 `internal/postgres/ledgerpg` 取代，查询集中在 `queries.sql`，金额列登记为 `money.Amount`；`ledgerpg.Store` 供服务层使用，`ledgerpg.NewTx(db)` 返回绑定到调用方 `pgx.Tx` 的账本 Store，C2C 与网关仍在同一事务内原子提交账本与业务记录。API、行为与数据库结构不变。
- 共享渠道界面改版（Feature #127，Epic #109）：「我的渠道」「上架/编辑渠道」「渠道详情」迁移到 TanStack Query 与 `src/ui/` 基础组件。列表呈现渠道状态、可用报价与累计收入；详情分「报价」（价格、校验状态、启用/停用/删除）与「调用与收入」（调用、成功率、TTFT、TPS、收入）两个标签，校验记录在抽屉中查看，发布、暂停、撤销凭据与删除渠道保留确认对话框，报价校验仍需先确认可能产生上游费用；编辑页丢弃未保存修改改用对话框确认，上游凭据始终不回显。不单设「渠道收入」导航（现有接口没有跨渠道收入明细，收入见列表与详情）。不改后端与 API。
- 持久化引入 sqlc 并按领域分包（Feature #113，ADR-0017）：新增 `backend/sqlc.yaml`、`mise run generate` 与 `mise run check-sqlc`，CI 增加生成物一致性检查；身份、模型目录与 API 手续费率迁移到 `internal/postgres/{identity,catalog,feerate}pg`，共享审计与事务辅助拆为 `auditpg`、`pgkit`。服务层接口、API 与数据库结构不变，其余领域仍在 `postgres` 包内待后续迁移。
- 网关持久化迁移到 sqlc（Feature #138，Epic #109，ADR-0017、ADR-0020）：平台 Key、模型池、调用快照、尝试、心跳租约、终结与结算、送达确认与补偿、孤儿恢复及仪表盘的 SQL 移入 `internal/postgres/gatewaypg/queries.sql` 并生成扫描代码，删除手写的 `postgres/gateway_{calls,keys,queries}.go` 与过渡桥接 `channel_routing.go`；网关调用金额列登记为 `money.Amount`，`interval` 参数映射为 `time.Duration`。`REPEATABLE READ` 快照、保存点、`SERIALIZABLE` 终结、与账本 capture/release 的原子结算、幂等终结与恢复语义不变；池成员指标由逐行 `LATERAL` 改为按池内报价分组的 CTE。API、行为与数据库结构不变。
- C2C 持久化迁移到 sqlc（Feature #137，Epic #109，ADR-0017、ADR-0020）：订单、交易、收款方式、争议陈述、事件、幂等命令与 180 天私密数据清理的 SQL 移入 `internal/postgres/c2cpg/queries.sql` 并生成扫描代码，删除手写的 `postgres/c2c.go`；每条写命令自己开启 `pgx.Tx` 并用 `ledgerpg.NewTx` 记账，不再经 `postgres.LedgerTransaction`；订单与成交金额列登记为 `money.Amount`。公开行情的最优买价/卖价改为取自已排序盘口首行（结果不变）。API、行为、收款资料加密与数据库结构不变。
- 运营指标持久化迁移到 sqlc（Feature #140，Epic #109，ADR-0017）：指标、供应商收入、异常、巡检与试运行汇总的 SQL 移入 `internal/postgres/opspg/queries.sql` 并生成扫描代码，删除手写的 `postgres/ops.go`；硬异常口径由异常检查与巡检共用同一条查询；集中度指标的嵌套 CTE 展开为同级 CTE，可空聚合以哨兵值加 `has_*` 标志返回并还原空值语义。空样本空值、UTC 窗口、数据库时钟闲置天数、硬异常定义与巡检落库保持不变，API、行为与数据库结构不变。
- 渠道持久化迁移到 sqlc（Feature #123，Epic #109，ADR-0017）：渠道、报价、凭据、验证记录、市场与评分的 SQL 移入 `internal/postgres/channelpg/queries.sql` 并生成扫描代码，删除手写的 `postgres/channel.go`；市场列表的八种排序与键集游标改为一条固定文本的 sqlc 查询（原为按排序拼接 SQL）；网关指标由逐报价 `LATERAL` 改为按报价分组的 CTE 连接，结果不变。`channelpg` 导出 `ResolveRoutingTargets` 与 `RoutingEligibility` 供网关持久化使用。API、行为、凭据加密与数据库结构不变。
- 网关交付与结算解耦（Feature #90，ADR-0016）：删除响应侧残留的形状门禁。事件名与 `data.type` 不一致、未知 SSE 字段、重复或空 `event:` 字段、非 JSON 的 data 帧、非 Chat 协议的 `[DONE]`、终止事件之后的意外帧、超出请求 `n` 或重复的 choice 索引、过大的 `tool_calls` 索引不再拒绝或中断流；仅未知事件、非 JSON 或无 data 帧的流在 EOF 时也照常交付；四种协议均在终止标记后继续读取至 EOF，尾帧保持安全检查且不覆盖已冻结用量；Responses 的 `incomplete` / `cancelled` 不再是错误（只有非空 `error` 对象或 `status: failed` 才算失败），非流式的 `Content-Type` 不再校验；请求头默认全量透传（只剥离凭据、`OpenAI-Organization`/`OpenAI-Project` 等账户作用域头、`Forwarded`/`X-Forwarded-*`/`Traceparent`/`Baggage`/`Idempotency-Key`、hop-by-hop 与 `Accept-Encoding`），查询串原样合并进供应商 endpoint（只拒绝畸形串与 `key`），压缩改由 transport 协商并解压。上游已返回的响应不再因为形状或用量的原因被平台错误替换。
- 用量抽取改为三态并用不影响交付的结算路径（Feature #90，ADR-0016）：区分“上游没给 usage”与“上游给了无法计价的 usage”，首个终止事件之前的合法用量以最后一帧为准、之后（含重复终止帧）不再覆盖，非法的用量（键存在但类型或数值非法、数值互相矛盾、非零 `server_tool_use`、音频/图片 token、1h 缓存写入、Gemini 非 TEXT 模态）污染整次调用。无法计价时先把上游响应交付客户端，再以 `missing_settlement_usage` / `unpriceable_usage` 零收费终结（`CallIncomplete`），释放预授权且不再触发回退；只有“什么都没产生”的截断流才按失败回退。出站凭据检查扩展到实际转发的头名称、头值与解码后的查询串，并在注入上游认证之前执行。
- 网关协议适配器按协议拆分（Feature #112，Epic #109）：`backend/internal/gateway` 引入 `protocolAdapter` 接口，四种原生协议各一个 `adapter_*.go`，`proxy.go` 由 1317 行降至约 500 行，SSE、用量、头与错误处理提取为独立文件；纯结构重构，API、协议行为与数据库交互不变，并新增四协议的成功、回退与用量污染矩阵测试。
- MVP 完成后取消独立设计阶段与 OpenPencil 前置要求，移除设计源文件、预览、DESIGN.md 和资产校验门禁；后续直接基于需求与现有代码开发，通过实际界面与测试验收。历史决策由 ADR-0015 标注取代，第三方 MIT 许可移至 `licenses/`。
- 发布工作流的生产环境链接和部署摘要更新为 `https://ai.isok.dev`（#92）。

### 修复

- 运营指标负余额风险的 `inactive_days` 改用数据库时钟计算并下限为 0（Feature #107）：此前用后端时钟减去数据库写入的分录时间，数据库时钟稍快时会得到 `-1`。

## v0.4.0 - 2026-09-04

### 变更

- API 网关改为原生协议透传，计费约束移到结算层（Feature #87，ADR-0014）：删除 Feature #20 引入的请求/响应封闭字段白名单，`previous_response_id`、`store`、`n`、`candidateCount`、多模态、服务端工具、`anthropic-beta` 等原生字段原样转发并由上游决定；不再强制改写 `service_tier` 或 `include_obfuscation`；未知响应字段与未知 SSE 事件透传，不再把上游 200 判成 502“所有候选渠道均失败”。成功结算仍要求完整四类 token 用量，出现计价公式无法表示的额外计费维（非零 `server_tool_use`、音频/图片 token、1h 缓存写入、Gemini 非 TEXT 模态）时该次调用不成功扣款并按失败回退。修复真实客户端的 Responses 流式与会话续接被网关误拦的问题。

## v0.3.4 - 2026-09-04

### 变更

- 镜像构建层缓存改用 GHCR registry cache（Feature #80 后续）：buildx 缓存从 `type=gha`（索引按触发 ref 作用域隔离，tag 发版之间互相不可见，v0.3.3 实测零层命中）改为每镜像独立的 `ghcr.io/…:buildcache`，跨 tag/分支持久共享；首个发版仍冷构建并写入缓存，其后发版 `npm ci`/`go mod download` 等层可命中。

## v0.3.3 - 2026-09-04

### 变更

- CI 门禁 npm 安装提速（Feature #80 后续）：CI 与 `mise run check-release` 的 `npm ci` 改用 `--prefer-offline --no-audit --no-fund --fetch-timeout=60000`。v0.3.2 实测 `~/.npm` 缓存已 2 秒命中，但安装仍静默卡满 npm 默认 fetch-timeout 的 300 秒（单次 registry 请求挂起，audit 上报失败不报错）后瞬间完成；关闭审计上报并收紧超时后命中缓存即装完。版本正确性仍由 package-lock 保证，本地 `mise run install` 不受影响。

## v0.3.2 - 2026-09-04

### 变更

- CI/CD 提速（Feature #80）：backend 镜像改在原生架构上以 `GOARCH=$TARGETARCH` 交叉编译、frontend 构建阶段固定 `--platform=$BUILDPLATFORM` 只构建一次静态产物，多架构镜像不再经 QEMU 模拟编译；CI 与 release 门禁新增 npm/Go 依赖与构建缓存。产物语义不变，digest 固定部署链路不受影响。

## v0.3.1 - 2026-09-04

### 修复

- 渠道上游 Key 取消最短 16 字符限制（Feature #77）：创建渠道与更换凭据不再要求凭据长度不少于 16 字节，任意非空 Key 均可提交；空值、超过 8192 字节与含控制字符的凭据仍被拒绝，首尾空格原样保留的语义不变。

## v0.3.0 - 2026-09-04

### 新增

- 管理员可在「管理账户」模态框为其他账户重置密码（Feature #74）：后端新增 `POST /api/admin/accounts/{accountID}/password-reset`，生成仅展示一次的新随机初始密码，单一事务内提升密码版本、标记强制改密、撤销目标账户全部会话并写入审计；重置复用改密限流与 Argon2 工作槽，禁止重置发起者自己；前端在管理账户模态框新增两步确认重置与一次性新初始密码展示（含复制）；OpenPencil 原型新增「57 · 管理账户」「58 · 重置密码」画板（ADR-0013）。

## v0.2.0 - 2026-09-03

### 新增

- 多档定价前端（Epic #68 前端部分）：管理端模型表单新增条件档位编辑器（Token 区间、带时区时间窗、星期谓词与四价，校验与后端一致）；API 市场、公开渠道详情与 Key 池展示默认价 + 条件档徽标及各档条件价；调用详情显示命中的计费档位；OpenPencil 原型新增「56 · 模型目录 · 条件档位」画板并重新导出评审预览。
- 模型目录支持条件价格档（Epic #68 后端部分）：管理员可为模型配置最多 16 条条件档，谓词为单次请求输入侧 token 区间与带 IANA 时区的每周时间窗（支持跨午夜），多谓词 AND、首个命中生效、整单按档、默认档兜底；结算引入计价公式 v2（无档位模型结果与 v1 一致），调用开始时快照全部条件档并在结算时按最终用量与请求开始时刻选档、记录命中档位，预授权上界覆盖全档；市场、渠道、Key 池与调用详情 API 暴露各档有效价与命中档位（ADR-0012）。

## v0.1.0 - 2026-09-03

### 新增

- 建立 GitHub Actions 发版与生产部署流水线：`v*` tag 触发 `mise run check-release` 门禁、backend/frontend 双镜像多架构构建推送 GHCR（digest 固定）并创建 GitHub Release，`production-hub` Environment 人工审批后经 SSH forced-command 受限脚本部署到 hub.isok.dev，失败自动回滚；`workflow_dispatch` 支持按既有 tag 重跑或回滚。

- 按 `product-experience.op` 收口产品与管理信息架构：导航和工作台对齐原型，公开渠道通过独立页加入模型协议池，管理员新增按共享者拆开的近 30 天收入表。
- 实例初始化一步到位：创建首个管理员后自动建立会话直达控制台（不再要求重新登录），且自设密码不再触发首登强制改密（该规则仅适用于管理员代发生成密码的受邀账户）；CLI bootstrap-admin 创建的账户同样免强制改密。
- 认证类表单补充输入规则提示与提交前校验：实例初始化页用户名/密码规则提示与具体错误（不再只有笼统的「请检查提交内容」）、首次改密与账户设置新密码提示、登录页凭据提示、管理员建号用户名规则提示与前校验；共享校验工具与后端规则保持一致（11 项单测）。
- 公开落地页移除顶部全局导航栏（品牌/锚点/登录按钮整行）；登录入口保留在页面多处 CTA。
- 公开 SaaS 落地页设为站点首页（`/` 与未匹配路径直达），登录入口保留。
- 新增实例初始化流程：实例不存在任何管理员时全站重定向到 `/initialize`，网页创建首个管理员（`GET /api/instance`、`POST /api/instance/initialize`，已初始化返回 409）；初始化窗口期的防抢注由站长自行负责（已确认取舍）。OpenPencil 新增「55 · 实例初始化」画板。
- 修复首轮全站视觉排查发现的问题：运营总览异常列表内边距与集中度数值精度（HHI/占比截断至 6 位小数）、发布挂单与渠道编辑吸底操作条不透明并贴底、模型目录空态垂直居中、运营总览巡检按钮补齐按钮基类；按 DESIGN.md 落地全局链接样式（link #1b61c9 / link-active #1a3866，默认无下划线、悬停下划线），模型目录为空时 Key 编辑器模型下拉显示占位提示。
- 交付统一管理员运营指标：按 UTC 时间窗口（24 小时/7 天/30 天）提供账本零和与投影、信用、API 调用漏斗（成功率分母固定为到达上游且已终态）、消费与共享者收入、C2C 挂单/交易与买卖盘、负余额风险（不预设逾期）和积分集中度（Top1/Top5、HHI）；空样本保持空值，不伪造为零或成功。
- 交付硬异常与固定下钻：零和/投影差异、成功调用无结算、结算无账本、C2C 数量与持有不一致列为硬异常，信用风险、失败率与争议仅作关注项；跨模块巡检在启动、每小时与手动触发时持久化历史。
- 交付试用证据摘要入口：仅聚合真实计数、时间与状态，不宣称参与者为真人或人民币已到账。
- 交付发布准备：`mise run check-release` 统一门禁与 PR/main CI；AES-256-CBC+PBKDF2 加密数据库备份、清单哈希与密钥环引用、防误提交保护；隔离恢复演练自动完成清单校验、恢复、旧会话清除、迁移、凭据可解密自检与跨模块巡检；新增部署、备份恢复、故障处理与小圈子试用 Runbook。
- 建立 React、TypeScript 与 Vite 前端工程骨架。
- 建立 Go HTTP 后端及 `/api/health` 健康检查。
- 建立 Docker Compose 本地运行方式。
- 建立 mise 工具版本和常用开发任务。
- 建立产品、架构、安全、Agent 协作和架构决策文档体系。
- 建立 Feature/Epic Issue 模板和 Issue 驱动的 Agent 开发工作流。
- 原样引入固定上游版本的 Airtable 设计分析，作为全部界面的唯一视觉规范。
- 记录原样采用 Airtable 设计分析的 ADR-0004，并将原 Binance 风格 ADR-0001 标记为已取代。
- 建立人类定向、AI 执行的持续产品研发模型，将发现、专业工具设计、交付和上线学习纳入统一治理。
- 为 Feature/Epic Issue 模板补充用户问题、设计、结果验证和 Ready/Done/Validated 证据要求。
- 记录 AI 原生持续产品研发模型的 ADR-0002。
- 安装并验证 OpenPencil 桌面端、`op` CLI、Codex Skill 与 MCP 接入。
- 建立 `design/` 一等资产目录，提交 OpenPencil 可编辑源文件和同名 Git 评审预览。
- 新增设计资产命名、同步、安全与可移植性规则，以及 `mise run check-design` 聚焦检查。
- 规定 Agent 的 OpenPencil 设计操作只允许使用 MCP；MCP 不可用时暂停，不降级到 CLI。
- 记录将 OpenPencil 可编辑设计源文件纳入 Git 的 ADR-0003。
- 建立结果导向的 `ROADMAP.md`，以 Now / Next / Later 区分产品结果的当前、后续和远期证据视野。
- 完成首轮产品发现，明确受邀小圈子的 API 渠道共享、优先级路由、中心化零和积分清算与 C2C 双边市场方向。
- 记录目标用户、模型目录、渠道倍率、平台 API Key、故障转移、计量结算、部分成交、数据边界和真实用户验证标准。
- 建立首版交付 Epic #16，并拆分专业工具设计、账户与模型目录、账本、渠道安全、API 路由结算、C2C 和闭环验证 Feature。
- 记录采用中心化零和复式账本的 ADR-0005。
- 新增并确认首版核心工作流 OpenPencil 原型与同名 Git 预览，覆盖 25 个可点击桌面页面、四角色流程、关键状态和桌面、平板、移动响应式规则，作为后续实现基线。
- 扩展核心工作流原型至 28 个可点击页面，新增 API 市场列表、公开渠道详情和加入模型池流程，并将市场发现入口补入导航、流程图、评审总览与响应式规则。
- 扩展 API 市场原型至 32 个可点击页面，新增用户评分、无匹配、渠道暂停以及重复或协议不兼容状态，并统一市场价格单位、排序语义、交互目标和评审预览。
- 收口首版专业工具原型为 48 个可点击页面：重构密集总览、平台 Key 与渠道统一配置器、模型池数据表、余额不足钱包、C2C 行情和支付流程；补齐资源编辑/删除、管理员创建账户模态框、渠道详情内评分、支付方式、收款码、联系方式和可选付款截图，并移除独立状态索引、独立评分页与冗余渠道模型页。
- 在核心工作流 OpenPencil 设计资产中新增第五页 SaaS 落地页，包含桌面与独立重排的移动布局，并在同名评审总览中补充公开入口；集中呈现 API 聚合、渠道共享、顺序回退、透明指标、数据边界、积分清算和受邀登录入口。
- 交付受邀账户与模型目录基础：新增 PostgreSQL/Goose 持久化、交互式初始管理员、用户名登录、首次强制改密、服务器端会话、管理员账户与信用额度管理、公开模型元数据和九位精度四类基准价；账户与模型写入使用版本化并发控制，并补齐对应 Airtable 响应式界面、审计、容器和集成测试。
- 记录 PostgreSQL、Goose、九位定点纳积分和受邀身份/服务器端 Cookie 会话的 ADR-0006 与 ADR-0007。
- 实现未认证可访问的 `/welcome` SaaS 落地页，按 OpenPencil 基线交付桌面、平板和移动响应式布局、语义化结构、键盘焦点样式与指向 `/login` 的受邀登录入口。
- 交付中心化零和账本基础：新增身份、激励与损失账户，不可变平衡分录、精确冲正、双持有及部分 capture/release、信用冻结与超限语义、原始结果幂等回放、管理员调整和坏账转移；提供用户钱包、可追溯分录、余额不足恢复入口、管理员账户账本与全局一致性指标，并固定支持共享者收入/平台手续费拆分的计价公式 v1。
- 记录不可变账本、双持有投影与计价公式 v1 的 ADR-0008。
- 交付渠道共享与公开 API 市场：实现加密凭据、保存和访问双重 SSRF 防护、四种原生协议报价、校验门禁、生命周期、评分、确定性游标分页、共享者配置台和管理员治理；公开、所有者与管理员投影保持敏感字段隔离。
- 记录版本化上游凭据加密与固定出站网络边界的 ADR-0009。
- 交付平台 API Key、模型协议池与调用总览：Key 只存不可逆摘要并支持一次性创建/轮换、启停、删除墓碑和 CAS；消费者可以配置稳定报价的固定优先级池，从公开渠道详情直接加入报价并查看调用与结算详情。
- 交付 Chat Completions、Responses、Anthropic Messages 和 Gemini GenerateContent 四类原生非流式/流式代理：实现认证头隔离、逐候选模型映射、提交点前顺序回退、成功元数据还原、协议错误 envelope、资源上限和客户端取消传播。
- 交付调用快照、预授权和精确结算：同一事务取得 Key、池、资格、凭据版本、价格和费率，非自有调用捕获实际费用并释放余量，自有渠道免手续费并按名义费用记录净零双分录；幂等 finalizer 与 orphan 恢复避免重复结算或误释放。
- 以真实调用事实为 API 市场和共享者页面提供成功率、TTFT、TPS、调用量及渠道收入，并增加成功率、TTFT 和 TPS 的确定性游标排序。
- 配置 Vite 与 Nginx 代理全部外部协议路径，关闭协议请求/响应落盘缓冲并支持受控长流；记录快照化 API 网关与幂等终结状态机的 ADR-0010。
- 交付 C2C 双边市场：支持固定价格买卖单、部分成交、多种支付方式、可选付款截图、精确超时、取消、放行、争议和管理员裁决；卖单使用父持有担保库存，买单按交易冻结卖家积分，业务状态与零和账本在同一事务结算。
- 使用独立版本化密钥环加密 C2C 收款资料、付款说明、争议陈述与证据，强制 JPEG/PNG 解码重编码、最小授权下载和终态 180 天清理；新增真实 PostgreSQL 并发矩阵、Go race、API 隐私边界及桌面、平板、移动前端验收覆盖。
- 记录 C2C 父子持有、订单交易状态机、幂等锁序和私密证据生命周期的 ADR-0011。

### 变更

- 调用记录与 API 市场桌面列表去掉行尾「详情 / 查看」按钮，改为整行进入对应详情，避免默认视口必须横向滚动才能发现入口。
- 产品侧栏改回原型顺序与标签（API Keys、API 市场），账户设置改由侧栏账户区进入；管理侧栏补回共享者收入，争议入口改回「争议处理」。
- 总览改回工作台：展示可用余额、剩余信用、今日消费与不含自有调用的今日渠道收入，并并排放「浏览 API 市场」「创建 API Key」。
- 渠道报价校验探测的输出上限由 1 token 调整为 64 token，四种原生协议仍使用非流式 `ping` 与 15 秒超时，使推理模型可以完成最小成功响应后再判定校验结果。
- 对齐首版原型与实现契约：统一用户名登录和普通账户能力，明确同一模型的多格式协议池、渠道生命周期、固定失败不收费与明确市场排序；补齐 C2C 发布买单状态，将原型扩展至 49 个可点击页面，并为公开 SaaS 落地页建立独立实现任务。
- 将 mise 项目配置统一命名为 `mise.toml`。
- 将统一产品设计资产由 `core-workflows` 更名为 `product-experience`，使文件名同时覆盖交互原型、角色流程、响应式规则与公开落地页。
- 所有仓库改动统一从 `origin/main` 创建独立分支和 worktree，主工作区固定保持同步的 `main`；补充并行 Feature、PR 主动合并、Issue 关闭及分支/worktree 清理闭环。
- 将默认视觉方向从 Binance 风格调整为固定版本的 Airtable 设计分析；移除未通过审阅的 Clay 混合方案和项目自定义视觉覆盖。
- 精简首版核心工作流原型文案，移除页面副标题、重复流程叙述和说明卡片，仅保留操作所需的标签、数据、状态、错误与风险确认。

### 修复

- 网关接受 xAI/中转站成功响应中的推理元数据与用量附加字段：Responses 允许 `presence_penalty`/`frequency_penalty`，Chat 允许 `reasoning_content`/`native_finish_reason`；忽略美元成本等无关 usage 字段，并在 Chat `total_tokens` 把 reasoning 单独加总时将其计入输出用量，避免上游 200 被误判为不可结算。
- 设计资产检查器兼容 OpenPencil MCP 保存的多页面 `.op` 结构，不再把有效多页设计误报为缺少可编辑节点。
- 会话查询或撤销遇到数据库故障时不再错误清除本地会话；Compose 安全入口不再关闭 Secure Cookie，只向宿主机回环地址发布明文前端端口，并要求显式配置内外两层可信代理网段后通过全链路自检。

### 移除

暂无。
