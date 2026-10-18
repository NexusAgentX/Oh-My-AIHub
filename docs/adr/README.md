# 架构决策记录

ADR 记录难以撤销、会长期约束系统的技术决定及其取舍：系统边界、存储与数据模型、协议与契约、身份与安全模型、部署方式。`ARCHITECTURE.md` 描述当前结构，ADR 解释为什么。

不写 ADR 的内容：可轻易撤销的实现细节、产品功能增删（写入 `PRODUCT.md` 与 `CHANGELOG.md`）、开发流程（写入 `AGENTS.md`）、任务计划与进度（写在 Issue）。

## 流程

1. 复制 [0000-template.md](0000-template.md)，使用下一个四位编号（含归档在内不重复；并行任务开工前再查一次）。
2. 加入下方“有效”索引。
3. 已通过的 ADR 不改写结论；决定改变时新建 ADR 取代它：
   - 整体取代：旧记录状态改为“已取代（由 ADR-xxxx 取代）”，移入 `archive/`，并修正指向它的链接。
   - 部分取代：旧记录留在有效目录，在其“替代关系”一节和下方索引中注明哪部分由哪篇取代、哪部分仍有效。

已通过的 ADR 可以原地修改的只有：状态、“替代关系”一节、失效链接、错别字，以及不改变含义的表述澄清。ADR 记录的是当时的理由，后来的重构改了函数名或表名不必追改；当前结构以 `ARCHITECTURE.md` 为准，必要时在“替代关系”中指向后续 ADR。

状态：`提议中`、`已通过`、`已拒绝`、`已取代`、`已废弃`。

## 有效

- [ADR-0005：采用中心化零和复式账本作为积分清算核心](0005-adopt-centralized-zero-sum-ledger.md)（冻结与自有渠道记账已由 ADR-0025、ADR-0035 取代）
- [ADR-0006：采用 PostgreSQL、Goose 与九位定点金额](0006-adopt-postgresql-goose-and-fixed-point-amounts.md)
- [ADR-0007：采用受邀身份与服务器端 Cookie 会话](0007-adopt-invited-identity-and-server-sessions.md)（初始管理员的创建方式已由 ADR-0037 取代）
- [ADR-0012：采用模型层多档价格与计价公式 v2](0012-adopt-tiered-model-pricing-and-pricing-formula-v2.md)（快照表、公式版本与预授权上界已由 ADR-0025、ADR-0035 取代；选档规则仍有效）
- [ADR-0013：采用管理员代发的账户密码重置](0013-adopt-admin-initiated-password-reset.md)
- [ADR-0017：采用 sqlc 与按领域分包的持久化分层](0017-adopt-sqlc-domain-persistence-layering.md)（迁移过渡安排已结束）
- [ADR-0019：前端采用 TanStack Query 与统一设计 token / 组件约定](0019-frontend-query-and-design-conventions.md)（视觉约定已由 ADR-0033 取代）
- [ADR-0020：跨领域原子提交由调用方持有事务](0020-adopt-caller-owned-transactions-across-persistence-domains.md)（账本接口已由 ADR-0025 的 `ledgerpg.Post` 取代；调用方持有事务的原则仍有效）
- [ADR-0021：以 OpenAPI 作为前后端唯一契约来源](0021-openapi-as-single-api-contract.md)
- [ADR-0025：账本去冻结与 C2C 托管账户](0025-remove-ledger-holds-and-adopt-c2c-escrow-account.md)
- [ADR-0028：平台 API Key 可逆加密保存](0028-store-platform-api-keys-reversibly-encrypted.md)
- [ADR-0029：观测现算不存快照，指标走内网端口](0029-compute-observability-on-demand-with-internal-metrics.md)
- [ADR-0030：以可靠 token 细分与实际响应事实定价](0030-price-reliable-token-breakdowns.md)
- [ADR-0031：论坛附件采用受限 PostgreSQL 存储并继承内容权限](0031-store-forum-attachments-in-postgresql.md)
- [ADR-0032：论坛采用 Vditor 源码编辑与统一 Markdown 渲染](0032-use-vditor-for-forum-markdown.md)
- [ADR-0033：前端采用贴纸风视觉、双主题与自托管字体](0033-adopt-sticker-visual-language-and-dual-themes.md)
- [ADR-0034：渠道自定义模型与转为官方后待设倍率](0034-adopt-channel-custom-models.md)
- [ADR-0035：网关透明转发、事后记账与按用户按模型路由（第二版）](0035-adopt-transparent-gateway-v2.md)（规则 4、6 的“上游错误原样返回”已由 ADR-0041 部分取代：出现的渠道上游 Key 会被抹掉）
- [ADR-0036：凭据加密与启动全量校验、钉住地址的出站连接池](0036-adopt-credential-startup-check-and-pinned-egress-pool.md)（供应商域名永久禁用规则由 ADR-0042 取代）
- [ADR-0037：首个管理员通过网页初始化](0037-initialize-first-admin-via-web.md)
- [ADR-0038：美元计价模型按平台汇率换算积分](0038-price-usd-models-with-platform-exchange-rate.md)
- [ADR-0039：按单实例部署，运行时状态放在进程内](0039-deploy-as-single-instance.md)
- [ADR-0040：网关热路径的只读数据来自进程内快照，写后同步失效](0040-serve-gateway-reads-from-process-snapshots.md)
- [ADR-0041：上游错误体返回前抹掉渠道 Key](0041-redact-channel-key-in-upstream-errors.md)

- [ADR-0042：取消供应商域名永久禁用](0042-remove-permanent-provider-host-ban.md)

- [ADR-0043：持久跟踪后台 Responses，按可靠推理分量结算](0043-track-background-responses-and-component-billing.md)

- [ADR-0044：分组市场采用稳定对象、不可变报价与能力证据绑定](0044-group-market-contract.md)

## 归档

探索期（v0.7.0 原地重写之前）及已失效的记录，只供追溯，不构成当前约束。探索期记录的文件内状态保持原样；被新 ADR 整体取代的记录，文件内状态改为“已取代”。

| ADR | 归档原因 |
| --- | --- |
| [0001 Binance 风格设计语言](archive/0001-adopt-binance-inspired-design.md) | 已取代 |
| [0002 人类定向、AI 执行的研发模型](archive/0002-adopt-ai-native-product-workflow.md) | 流程类，现行规则见 `AGENTS.md` |
| [0003 OpenPencil 设计源文件](archive/0003-version-openpencil-design-assets-in-git.md) | 已取代 |
| [0004 Airtable 设计分析规范](archive/0004-adopt-airtable-design-analysis.md) | 已取代 |
| [0008 不可变账本、双持有投影与计价公式 v1](archive/0008-adopt-immutable-ledger-holds-and-pricing-formula-v1.md) | 由 ADR-0012、0025、0027 取代 |
| [0009 版本化凭据加密与固定出站网络边界](archive/0009-adopt-encrypted-upstream-credentials-and-pinned-egress.md) | 由 ADR-0036 取代 |
| [0010 快照化 API 网关与幂等终结状态机](archive/0010-adopt-snapshot-gateway-and-idempotent-settlement.md) | 由 ADR-0027、0028 取代 |
| [0011 C2C 父子持有与订单交易状态机](archive/0011-adopt-c2c-order-trade-hold-state-machine.md) | 由 ADR-0025 取代；当前 C2C 规则见 `ARCHITECTURE.md` |
| [0014 网关原生协议透传](archive/0014-adopt-native-passthrough-gateway.md) | 由 ADR-0027 取代 |
| [0015 MVP 完成后直接基于代码开发](archive/0015-develop-directly-from-implemented-mvp.md) | 流程类，现行规则见 `AGENTS.md` |
| [0016 网关交付与结算解耦](archive/0016-decouple-gateway-delivery-from-settlement.md) | 由 ADR-0027 取代 |
| [0018 移除 C2C 付款截图与证据图片](archive/0018-remove-c2c-evidence-images.md) | 产品删减，事实见 `PRODUCT.md` |
| [0022 取消用户验证环节](archive/0022-drop-user-validation-stage.md) | 流程类，现行规则见 `AGENTS.md` |
| [0023 删除 C2C 买单](archive/0023-remove-c2c-buy-orders.md) | 产品删减，事实见 `PRODUCT.md` |
| [0024 迁移压缩为单一基线](archive/0024-squash-migrations-to-single-baseline.md) | 阶段性决定已结束；迁移规则见 `AGENTS.md` |
| [0026 产品重写时原地重写基线](archive/0026-rewrite-baseline-migration-in-place.md) | 阶段性决定已结束；迁移规则见 `AGENTS.md` |
| [0027 网关透明转发、事后记账与按用户按模型路由](archive/0027-adopt-transparent-gateway-with-post-hoc-billing.md) | 由 ADR-0035 取代 |
