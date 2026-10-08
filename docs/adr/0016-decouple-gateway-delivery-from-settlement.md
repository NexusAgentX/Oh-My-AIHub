# ADR-0016：网关交付与结算解耦

- 状态：已取代（由 [ADR-0027](0027-adopt-transparent-gateway-with-post-hoc-billing.md) 取代）
- 日期：2026-09-23
- 决策者：仓库维护者
- 关联内容：[ADR-0014](0014-adopt-native-passthrough-gateway.md)、[ADR-0010](0010-adopt-snapshot-gateway-and-idempotent-settlement.md)、[Feature #90](https://github.com/NexusAgentX/Oh-My-AIHub/issues/90)

## 背景

ADR-0014 删除了请求侧的封闭字段白名单，但“形状校验”仍然留在响应与 SSE 侧，并继续在**交付之前**执行，使客户端拿不到上游已经产生的合法响应：

- 非流式 `RewriteNonStreamingResponse` 在写出响应体之前校验响应形状，并因为用量不完整返回 `ErrNoUsage`：一个省略 `usage` 或缺少 `finish_reason` 的 200 响应会让客户端收到平台错误，并把该请求的所有候选逐个烧完。
- 流式 `AnalyzeSSEFrame` 因事件名与 `data.type` 不一致、非 JSON 的 data 载荷、未知 SSE 字段、重复 `event:` 字段直接返回 `ErrInvalidInput`，整条流转失败。
- 终端阶段只放行 Anthropic 的 `ping`，其余帧触发 `unexpected_event_after_terminal`；`missing_success_terminal`、`invalid_choice_index`、`missing_terminal_usage` 同样在交付前拦截。
- 出站只透传 `anthropic-beta`，其余客户端头（`OpenAI-Beta`、`x-stainless-*` 等）全部丢弃；任意查询串被拒；`Content-Type` 必须恰好一个且为 JSON 家族；任何非 identity 的 `Content-Encoding` 直接判失败。
- 用量抽取无法区分“上游没有给 usage”与“上游给了无法计价的 usage”，两者都表现为 `ErrNoUsage`，因此任何改用累计快照的尝试都会让非法的终帧用量被前帧的合法值掩盖。

这些行为与 ADR-0014 的“原生协议透传、计费约束只在结算层执行”自相矛盾，也与产品合同冲突：平台只应理解路由与结算必需的最小集合。

## 决策目标

- 网关真正透明：上游支持什么就转发什么；未知字段、未知事件、未知 SSE 字段、厂商扩展头与查询串一律原样通过。
- 交付与结算解耦：无法计价的调用仍然把上游响应交付客户端，然后按 ADR-0014 的“不成功扣款”路径零收费终结，而不是隐藏响应并回退。
- 计费完整性不降级：成功结算仍要求完整、非负、互相一致且在预授权上界内的四类 token 用量。
- 安全边界不降级：凭据擦除、认证头隔离、正文不落盘、资源上限与超时全部保留。

不解决：跨协议转换、按请求参数的动态预授权、额外计费维度的目录化计价。

## 候选方案

### 方案一：保留响应形状门禁，逐条按需放开

继续用形状校验挡住“假成功”，但每支持一种上游行为都要改平台代码。实测代价是真实客户端拿到平台错误而非上游响应，且回退会重复消耗候选；这与 ADR-0014 已经否定的入口白名单是同一个错误。

### 方案二：只保留错误识别，其余形状判断降级为观察，交付先于结算

错误判定收敛为“payload 是否明确表示失败”；事件名、字段集合、choice 数量、工具索引、内容类型、查询串都不再参与拒绝。用量抽取改为三态（缺席 / 合法 / 非法），非法状态污染整次调用。无法计价时先交付上游响应，再以 `CallIncomplete` 零收费终结。

## 决定

采用方案二。

- 删除响应形状校验：`validateSuccessfulResponse` 与流式错误识别都只保留上游**显式失败**（非空 `error` 对象或 Responses 的 `status: failed`，含嵌套在 `response` 里的失败）；Responses 的 `incomplete` / `cancelled` 是带用量的正常终态，按普通成功响应交付并结算，不再是错误。
- 删除交付前的其余门禁：`Content-Type` 不再校验；`invalid_choice_index`、`missing_success_terminal`、`unexpected_event_after_terminal`、`missing_terminal_usage` 全部移除；`tool_calls[].index` 不再设上限。
- SSE 解析回归规范：未知字段忽略，重复 `event:` 取最后一个，事件名与 `data.type` 不一致只记录为观察，非 JSON 的 data 与无 data 的帧原样转发且不参与语义/终止/用量判定；`[DONE]` 只在 Chat 协议作为终止帧，其他协议原样下行。
- 终端区放行：终止事件之后的帧进入终端缓冲并照常下行，帧数上限由 3 提高，终端字节上限保留。所有协议均继续读取至 EOF 后结算，协议终止标记只冻结用量快照；读取期间仍执行原有超时、安全和资源限制。
- 正常 EOF 也算流结束：上游直接关闭连接（没有终止帧、没有 `[DONE]`）时，已到达的任何帧（包括未知事件、非 JSON data 与无 data 帧）先交付客户端再结算（有合法用量就成功结算，否则零收费不完整），不再当作截断回退；只有“什么都没产生”的关闭才按失败回退。
- 帧保真度明确到边界（已知偏差）：无 data、非 JSON、非 Chat 协议的 `[DONE]` 三种帧**原样逐字下行**，因此其中的 `id:`、`retry:` 与注释行会到达客户端；其余 JSON 帧会重新序列化为紧凑的 `event:` + `data:` 帧，`id:`、`retry:` 与注释行被丢弃，未知 **SSE 字段行**（`foo:`）不保留，未知 **JSON 字段**保留但键序与空白会规范化。逐字字节保真需要后续用 `sjson` 做外科改写；响应头仍只回传 request-id 系列（上游的缓存/算术类头不下行）。
- 用量三态：`usageObservation` 返回缺席 / 合法 / 非法；键存在但类型或数值非法、出现公式无法计价的维度（非零音频/图片 token、1h 缓存写入、非零 `server_tool_use`、Gemini 非 TEXT 模态）、或数值互相矛盾一律为非法；首个终止事件之前的合法记录以最后一帧为准，首个终止事件（含该帧）之后的所有帧——包括重复的终止帧——只下行、不进累计也不覆盖快照；非法状态始终污染整次调用。
- 结算路径：`settleable` 为假时先交付已缓冲帧，再以 `missing_settlement_usage` / `unpriceable_usage` + `AttemptIncomplete` + `CallIncomplete` 终结，释放预授权且不收费；非流式同样先交付响应体再终结。只有 `usage` 而没有内容的响应按用量结算（与 ADR-0014 已接受的取舍一致，非流式与流式同口径）。
- 出站头改为黑名单：只剥离凭据、账户作用域（`OpenAI-Organization`、`OpenAI-Project`）、客户端身份与链路（`Forwarded`、`X-Forwarded-*`、`Traceparent`、`Baggage`、`Idempotency-Key`）、hop-by-hop（含 `Connection` 在各条取值中列出的头）与 `Accept-Encoding`；其余客户端头原样转发。不再强制 `Accept-Encoding: identity`，由 Go transport 协商并解压 gzip；只有在解压后仍残留 `Content-Encoding`（网关解不了的编码）时才拒绝。
- 查询串透传：只拒绝畸形查询串与 `key` 参数（凭据泄漏），其余参数合并进供应商 endpoint；Gemini 流式保留强制 `alt=sse`。
- 出站凭据检查扩展到实际转发的头名称、头值与解码后的查询串，并在注入上游认证之前执行，避免同 Key 多候选被误判。

## 后果

### 正面影响

- 客户端可以收到上游实际返回的任何形状；上游协议演进（新事件、新字段、新头部）零维护。
- 平台错误只出现在真正失败的地方：上游显式错误、凭据泄漏、资源/超时上限、无法解码的压缩编码。
- 结算失败不再让客户端丢响应，也不再因为形状问题反复消耗候选。

### 负面影响与成本

- 上游返回异常形状（例如只有 usage 的 200）会被转发并按用量**成功扣款**（流式与非流式同口径）；这是“只做用量统计”的直接代价，需要靠运营指标观察异常比例。
- 无法计价的调用即使内容已交付也不收费，存在免费用量敞口；缓解：该路径复用 ADR-0010 的“提交后无法结算”状态机，调用事实与错误码全部持久化，可按错误码下钻。
- 终端区帧数上限提高后，异常的上游长尾帧会占用更多交付缓冲；字节上限保持不变。

## 验证方式

- 适配器单测：事件名不匹配、非 JSON data、未知 SSE 字段、重复 `event:` 字段、非 Chat 协议的 `[DONE]` 均透传且不标记语义/终止；事件名不匹配但 payload 明确失败仍判失败；用量三态区分缺席与非法（含类型错误、矛盾数值、不可计价维度、Gemini metadata 类型错误）；非 JSON 的非流式 body 原样返回。
- 代理单测：缺用量、非法用量、终端区非法用量、无终止帧（含只有 usage 帧的 Chat 流与缺少 `status` 的 `response.completed`）四种流的响应均交付客户端；前三种与无终止帧的情况按用量决定是成功结算还是零收费 `CallIncomplete`，均可验证不回退；仅未知事件、非 JSON data 或无 data 帧的流在 EOF 时交付且零收费；四种协议终止标记后的合法尾帧交付且不改写计费快照，非法用量尾帧使整次调用零收费；真正什么都不产生的空流仍回退；厂商扩展头与查询串到达上游；平台凭据位于头名称、头值或百分号编码的查询值时拒绝且不发出上游请求；真实 gzip 上游响应被 transport 解压后正常结算并成功扣款。
- 既有计费、提交点、回退、凭据擦除、幂等结算与并发测试全部保持通过。
