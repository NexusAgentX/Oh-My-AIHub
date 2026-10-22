# 网关契约与请求边界

本手册说明 `x-aihub-gateway-contract` 的作用、如何验证它，以及网关不保存服务端状态后各入口的请求边界。结构边界见 [ARCHITECTURE](../../ARCHITECTURE.md#无状态请求边界)。

## 契约包含什么

契约是 `backend/api/openapi.yaml` 根级的扩展字段，位于 `paths` 之外，不登记正式路由，只描述已交付入口的来源与策略：

- 端点只声明 8 条推理/图片入口（OpenAI Chat、Responses、Anthropic Messages、Gemini `generateContent`/`streamGenerateContent`、图片生成/编辑/变体）以及 OpenAI、Anthropic、Gemini 三种风格的模型列表与单模型元数据。`/v1`、`/v1beta`、`/backend-api` 上的其他路径回答 404 `unsupported_endpoint`。
- `sources` 只保留与保留入口相关的官方文档或 SDK 来源；`suites` 记录上游协议来源编号与断言分组；`exclusions` 记录移出范围的来源编号及原因（含已退役能力），表示“不验证”，不表示“上游不支持”。
- `adjustments` 记录来源编号的合并/拆分与请求边界结论。
- 来源编号是外部可追溯基线，不是请求次数或完整性分母。新增或删除入口必须同时修改契约与真实路由；契约里的条目不会自动变成可用入口。

## 验证

```sh
mise exec -- go -C backend test ./internal/gatewaycontract ./internal/api ./internal/gateway
mise run generate   # 重新生成 frontend/src/api/schema.gen.ts，提交前确认无残差
```

覆盖范围：

- `TestManifestTraceabilityAndScope` 要求四个协议的来源编号完整（Chat 51、Responses 80、Messages 68、GenerateContent 80），并拒绝文件、上传、向量库、容器类资源路径重新进入端点表。
- `TestManifestRejectsBrokenHandoffs` 要求缺失端点、重复 ID、缺失来源、错误 Feature、聚合环等破坏都无法通过校验。
- `TestGatewayF7EveryNativeEndpointUsesRealEntryAndScope` 逐个 F7 推理/图片端点走真实入口：平台 Key 换渠道上游 Key、图片 multipart 只替换模型字段、Gemini 路径解析 `{model}:{operation}`、未实现的 operation 回答 `unsupported_endpoint`。
- `TestRetiredGatewayEndpointsAnswerNotFound` 与 `TestGatewayForwardsWithoutServerSideState` 固定退役路径与请求边界；`TestBackgroundResponseIsRejected` 固定后台请求的拒绝。
- `internal/gateway` 的 `stateless` 用例固定 `store` 剥离、`previous_response_id`/`conversation`/`cachedContent` 识别与零复制路径。

契约失败时的排查顺序：先看 `paths` 与路由表是否一致，再看端点引用的 `source` 是否存在、`suite` 的 `endpoints` 是否都还在端点表里。删除入口时要把对应来源编号移入 `exclusions` 并写明原因，而不是留下悬空引用。

## 请求边界

| 请求内容 | 行为 | 不变量 |
| --- | --- | --- |
| Chat/Responses 顶层 `store` 为真 | 整组删除该成员（任一重复键为真则全部删；大小写、字符串与数字写法都算真） | 其余字节不动；全为假值或不携带时零复制 |
| `previous_response_id`、`conversation` | 转发前返回 400 `stateful_request_unsupported`，文案点名被拒字段 | 显式 `null` 或空串视为未请求状态，原样转发 |
| Gemini 顶层 `cachedContent` | 同上，400 `stateful_request_unsupported` | 显式缓存管理入口已退役，引用已失效缓存也无法兑现 |
| `background` 为真 | 开调用记录之前返回 400 `background_unsupported` | 不降级为同步调用，不占用账号/渠道在途名额 |
| 会话、响应、条目、缓存、后台任务的读取/列表/取消/删除 | 404 `unsupported_endpoint` | 网关没有服务端资源，也没有对应归属索引 |

## 证据边界

平台机制验证（本地 fixture、合成上游）只证明网关自身的转发、鉴权与边界，不能作为供应商真实支持证据，也不能代替真实付费验证。真实上游验证只使用明确提供的测试 Key，由用户在上游自行限制消费。

网关不保存请求或响应正文、也不保存凭据到日志；契约与用例都不得为此登记持久字段。开发期不运行端到端性能 A/B，发版前按[网关性能基线](gateway-baseline.md)集中覆盖请求扫描、`store` 剥离、旁路解析与结算。
