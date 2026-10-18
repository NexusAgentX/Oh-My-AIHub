# 上游能力检测

任务和报告只存必要执行元数据；不保存请求、响应、密文或 history/notes 正文，也不产生平台积分过账。真实检测须使用明确提供的测试 Key，维护者自行在上游控制限额。创建执行当前范围内的适用套件；缺 SDK/MCP 配置或固定 Codex 制品时相应项目显示 `suite_pending`，不能以此批准真实创建或宣称供应商通过。

Codex 原生适配器现执行 RL01/02/03/08/12 的标准/Lite 声明与工具往返、RL05 多轮 reasoning context、RL06 指令、RL09 加密推理续用、RL13 上游认证、RL15 原生目录、RL14 zstd/未压缩推理、RL20/21 完整压缩窗口续用和 RL24 各启用服务档位。相同真实请求复用，不把目录中的上下文、工具或套餐声明当成实测能力；独立操作、协议形态、压缩前后及跨形态回放各保留实际证据。均访问对应 Codex 原生路径，不借用公共 `/v1/responses` 的成功。请求档位、实际返档与可靠用量分别记录，降档不证明请求档位通过。

上述场景也已接入独立的 `gateway_proxy` 层；RL13 验证本地平台身份与凭据分离，RL15 验证权限过滤与未证明元数据删除，其余场景经同一个正式处理器执行。私有桥使用临时合成分组、模型、平台 Key 和零价测试报价，实际测试 Key 只在绑定的父进程出站中使用；测试报价不代表供应商价格，不写业务账本或用量画像。固定出站仍由实际配置执行，保护会话头之后不再重复应用分组 Header 覆盖。检测桥只开放本轮固定 Codex 路径，没有通用转发或外部资源授权入口。

官方 CLI 已注册 21 个适用业务场景（RL01–09、12–19、21/22/24/25）；固定客户端不支持的 V1 压缩只要求上游与平台代理两层，RL10 仍汇总全部适用压缩证据。RL23/26 属于平台跨渠道与隔离回归，不参与贡献对象的上游全测，不把合成平台结果算作供应商通过。临时 HTTPS 回环桥使用一次性 CA；消费者身份由正式处理器返回，实际测试 Key 不交给 CLI。`notes`/`image` profile 使用固定本地模型描述激活客户端场景；其中的 32768 是客户端测试配置，不是容量检测结论，不进入模型目录或路由快照。`catalog` profile 只使用实际目录，核验 `model/list` 返回目标别名，不调用推理；不会用本地目录冒充原生目录通过。启动检查制品版本/摘要，缺制品保持 pending。运行时可指定绝对路径 `AIHUB_DETECTION_CODEX_PROBE`、`AIHUB_CODEX_BINARY`，默认查找 server 同目录。

渠道 Key 的最小可调用性探针与能力报告分开。只有当前有效的原生报告证明 Codex 入口支持、公共主入口缺少支持证据时，才以固定 Codex responses 或 images 入口探测；报告未知/失效不猜入口，也不额外补一次付费探测。`endpoint_id` 保留实际入口供所有者诊断，原生成功不能充当公共 `/v1/responses` 或 `/v1/images/generations` 支持。渠道所选地址仍按顺序回退，完整 SSE 终止或真实图片产物才证明可调用。

## 固定 Codex 客户端接入

以下仅对应本轮固定的 0.163.0；消费者填写本平台 Key，模型使用有权限且已有相应有效证据的实际模型 ID。使用独立的 `CODEX_HOME`，其 `auth.json` 为 `{"personal_access_token":"<PLATFORM_API_KEY>"}`；不要填写渠道 Key 或复制个人 ChatGPT 登录。

`config.toml`：

```toml
model = "<MODEL_ID>"
model_provider = "openai"
openai_base_url = "https://gateway.example/backend-api/codex"
chatgpt_base_url = "https://gateway.example/backend-api/"

[features.token_budget]
enabled = true
use_history_notes_extension = true
```

以 `CODEX_AUTHAPI_BASE_URL=https://gateway.example` 启动这个隔离客户端。身份入口返回本平台账号和未知套餐 `aihub`；固定 CLI 将此 PAT 分支标为 ChatGPT，这不表示官方订阅或任何 Pro 权益。模型目录只来自当前 Key 可见的站内候选及实际能力证据。后台 V2 验证使用另一个客户端配置，关闭 TokenBudget 后启用 `[features.async_compaction] enabled = true`，不与上述 history/notes 配置混用；本地检测中的 1% 阈值和合成模型目录用于触发测试，不是消费者配置或供应商容量声明。

## 操作与故障恢复

- 从目标分组/模型入口发起任务；草稿入口由贡献功能接入。记录页在 `/detection`，管理员搜索设置在 `/admin/detection-search`。任务的排队/运行/取消/中断与报告完整性分开显示。
- 取消后先终止请求并有界清理，页面持续读取直至收尾；已经发生的请求、用量和未清理资源保留。启动会将遗留排队/运行任务标中断，不从数据库恢复明文凭据。
- 恢复需要重新提供测试 Key，并核对目标、能力摘要、范围/代码和固定参数。换 Key 会关联新任务、固定新凭据来源，复用有效引擎证据并重跑凭据相关项；不使用新 Key 清理旧凭据范围的资源。目标或版本不兼容时新建检测，历史证据保留。
- 参数修改只影响后续新任务；恢复保留原参数版本。二分/三分轮数与单次大小保护是算法/资源边界，不是全测累计费用、HTTP、token 或总时长预算。
- 审批必须使用不可变报告 ID，不能引用可继续变化的任务当前视图。历史报告只读，每轮复测保留新报告和原始来源。合成机制证据与真实上游证据不能混用。
- 客户端未启用、检测器错误、平台前置未就绪或取消/中断不满足创建完整性，不能用终态 `inconclusive` 冒充完整负向报告；上游实际拒绝和实际前置失败继续按真实证据记录。原生 Codex 服务档位只使用其自身上游层证据，公共 API 或平台代理层通过不授权该档位；新启用档位要求相关三层复测。

## 资源与凭据

默认 worker、队列、用户活跃任务、单次请求/响应/SSE 及内存产物限制在 `detection.DefaultLimits` 统一维护。检测元数据和后台读取使用独立两连接池，不占转发/结算池；队列满返回 `detection_busy`，无平台累计预算耗尽状态。请求和各轮结果分开追加，长任务不将所有请求/正文保留在内存。

受控 CLI 使用独立 HOME/CODEX_HOME/临时目录和显式环境，不继承宿主登录或代理。标准输出/错误各限 1 MiB；临时输入、运行目录和子进程组有有限资源保护。实际固定 CLI、网络接线、配置及三层证据由 Codex 专项核验；子进程日志不能直接写入报告。

笔记、历史和图片生命周期包含多次真实推理/工具/恢复操作，不能将单次 HTTP 时限套作整个 CLI 场景的时限。驱动的有界执行窗口由 `codexprobe.ProbeTimeout` 统一定义，父进程另留收尾时间；每次出站仍应用原 HTTP 超时，子进程和文件保护不放宽。本地驱动到时不是上游不支持证据，未完成的场景仍不能用于创建审核。

`mise run build-detection-codex` 构建固定 Go 驱动器并安装官方 `0.163.0` 原生制品；Linux/macOS 的 amd64/arm64 包 SHA-512 固定在安装脚本中，每次均验证，只提取 CLI 可执行文件。源码固定为 `83086188ae0614bd882818ff1aa23f3194962012`，Apache-2.0 LICENSE 与原 NOTICE 位于 `backend/licenses/codex/`。Linux amd64 可执行文件约 283 MiB，下载包约 155 MiB；不安装 npm launcher、语音库或额外工具。制品供后台隔离检测使用，不进入普通网关请求流程。

官方 CLI→正式网关→合成上游回归以 `AIHUB_TEST_CODEX_BINARY` 和 `AIHUB_TEST_CODEX_PROBE` 显式指定上述两个制品，运行 `go -C backend test ./internal/api -run TestOfficialCodexCLIUsesProductionHandlers`。CI 会运行普通推理、平台 PAT 身份与 notes 启用、独立图片模型和工具结果回填；图片另校验独立报价且仅结算一次。驱动器只接受回环桥、固定 profile 和受控临时目录，禁止审批/任意客户端命令；官方 CLI 原始输出只在内存解析，返回固定阶段和布尔结论，取消后清理整个进程组及目录。机制验收不构成真实供应商通过，也不消除仍未注册套件的 pending。

固定版本的 [RemoteCompactionSupport](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/model-provider-info/src/capabilities.rs) 只有 Unsupported/V2；V1 standalone 仍执行上游与平台代理的整个返回窗口续用，不编造 CLI V1 配置。V2 客户端仅安装返回的 compaction 项，保留消息由原请求按客户端规则筛选，不能用合成的 V1 多消息返回窗口冒充其实际安装规则。三层适用断言及汇总依赖由 OpenAPI manifest 声明，不跨层借用结果。

`async_compact` 使用独立配置：TokenBudget 关闭，AsyncCompaction 开启，阈值 1%，固定合成上下文；验证真实前后台请求重叠、结果采用及后来消息保留。后台完成后要在后续前台检查点采用，不能等采用事件后才启动该轮。关闭阶段先等待实际 HTTP 操作返回、桥进入交付暂缓状态，再关闭客户端线程；仅观察请求开始会让客户端取消抢在响应头之前，不能验证可靠结果的读取与结算。取消用例在私有桥暂缓交付已取得的上游结果，官方 CLI 关闭独立合成线程后释放；这是 CLI/网关功能检测，不是上游故障诱发，也不声称上游撤销了请求。网关仍按断开后继续读取的既有规则收尾，按每个调用的可靠结果核验单次结算。私有同步入口仅在该执行的回环桥上注册，校验临时平台 Key，不增加生产接口；等待真实事件有执行期限，不做 TTL 或到期测试。

`encrypted` 配置实际进行两轮推理并核验上游密文原样回放；`search` 显式启用 StandaloneWebSearch，核验实际独立 alpha/search、有效结果 URL 结构和固定 CLI 带回的 input_text 输出。固定版本即使同时收到 encrypted_output 也使用 SearchResponse.output；协议直接检测与客户端实际行为分别记录。所有中间内容仍只留在私有执行内存中。

`image_lifecycle` 让 CLI 实际生成图片，再以刚收到的图片编辑并继续推理。图像模型单独保持权限与按产物的测试报价；主对话使用目录中另一个文本候选并实际验证可调用，不将目录声明当作支持。缺少伴随文本模型时保留实际目录证据并标前置无法核验。检测用零价报价不参与真实业务或供应商估价。私有执行仅允许此次内存图片、固定 imagegen 工具和合成输入，不接收模型要求读取宿主路径或执行其他客户端命令；该检测保护不改变生产透明转发。

RL17 复用 notes 生命周期的实际 CLI 请求。固定客户端会缓存初始 thread_hint，不能假设本地恢复线程就会重新获取。检测在首次私有会话的实际 CLI hint 请求前，用实际模型生成的加密参数向同一隔离上下文写入合成标记；该准备调用单独留上游证据与计量，不能代替 CLI hint。本人 hint 须观察到标记，另一会话不得包含本人任一合成标记；只有空 hint 时仍是无法核验。RL18 的恢复阶段仍要求实际新读回执与返回结果续用，不能借旧读取或 hint 满足。

RL07 分别运行真实 Namespace 动态回调与内置 freeform apply_patch 的空补丁闭环；后者校验实际无操作诊断、参数分片和 call/result，不宣称文件写入能力。私有桥只交付规范空补丁，任何改变文件的内容或其他客户端工具在执行前拒绝。RL12 先复用标准/Lite 独立工具证据，再在同一个隔离官方客户端中用两个本地协议配置别名指向同一实际上游模型，分别把工具历史续用到另一格式。CLI 自身生成对应原生声明，网关不转换；两个方向均须识别到同一实际 call/result 的格式切换，不以两次独立工具成功代替兼容回放。

RL19 使用 TokenBudget/history-notes 配置进行实际 history ingest，随后由 CLI 完成 list_windows/list_items/read_item/search_contents、另开会话的隔离及恢复后的新读取；只跟随本次写入元数据中的逻辑窗口或 context-window 标识，实际选择的 opaque ID 原样用于后续操作。固定客户端的 [传输窗口生成](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/core/src/session/mod.rs) 与 [上下文窗口展示](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/core/src/context/token_budget_context.rs) 使用不同标识，不能强行把所有窗口当 UUID。查不到刚写入条目时不猜 item ID、不等索引延迟，记录未观测；外国上下文的真实 403/404 拒绝与空结果分别观测，拒绝不能被算成对应操作的支持证据。

原生 notes/history 准备请求同样使用固定客户端的完整 turn 初始化与对应请求头；只有自报 session ID 时，上游可能尚未建立上下文，不能据此判断读写不支持。逻辑窗口为线程 ID 加序号，历史读取使用本任务生成的独立 context-window ID。后续加密参数生成和结果解释不重新 ingest 辅助提示，避免替换已知历史；隔离验证先初始化另一合成会话，再尝试读取本人的已验证记录。初始化元数据仅留有界 worker 内存，不建立来源渠道映射。

官方 CLI 的 notes/history 验证以实际上下文中的合成待办任务完成情况核验恢复，不要求客户端引用或复述其内置工具规则要求保密的正文。上游工具输出仍须有真实 HTTP 回执、结果哈希及后续推理中的对应回填，不能仅靠最终文本成功。请求/响应、私有笔记/历史和密文不进入报告。

官方 SDK 检测使用独立的 `aihub-detection-sdk` 制品，版本固定在 manifest 来源与 `RegisterSDK` 中。容器构建同时生成并安装它；本地 `mise run dev-backend` 自动先构建，单独运行 server 时可用 `AIHUB_DETECTION_SDK_BINARY` 指定绝对路径，默认查找 server 同目录的制品。缺少制品时对应套件保持待实现，不降低创建全测要求；制品版本或 SHA 改变会改变套件版本。不要指向个人 CLI、宿主登录脚本或任意下载的可执行文件。

SDK 子进程只获得合成提示、模型 ID、临时回环地址/令牌和占位 Key，实际测试 Key 由父进程的固定出站持有。桥仅允许当前模型的一个基础请求；SDK 重试和自动续用关闭，短答响应不超过 256 KiB，子进程用 `GOMEMLIMIT=128MiB`、单 Go 执行核及既有进程保护。原始 stdout/stderr 不入报告。SDK 类型中包含的 Files/容器/WS 等资源不构成代理范围。机制回归：先 `mise run build-detection-sdk`，再以构建制品的绝对路径设置 `AIHUB_TEST_SDK_BINARY` 运行 `go -C backend test ./internal/detection -run 'TestOfficialSDKBridge|TestMissingSDKArtifact'`；它只使用合成上游。真实供应商验收仍需要明确测试 Key。

Linux 上 SDK 与 Codex 的 Go 检测驱动使用 2 GiB 虚拟地址空间上限，其他受控子进程默认仍为 1 GiB。实测 1 GiB 会使固定 SDK 在版本校验阶段偶发崩溃，Codex 驱动也出现相同的启动故障，2 GiB 对照消除了该复现；这给 Go 的虚拟地址预留留空间，不代表允许 2 GiB 的受管堆，两个驱动仍用 128 MiB 的 Go 内存目标。并发槽位、单执行核、文件/输出/FD 和时间上限继续保留，调用方只能选择预定义的两个地址空间档，不能传入任意或无限限制；不靠自动重跑掩盖启动失败。虚拟地址空间的硬限制按 Linux 验证，macOS 本地执行不宣称具有相同的 OS 限额保证。

用途根密钥通过现有上游凭据密钥环封装，无新增环境密钥。启动先验证全部已存根密钥；加载的根密钥可在保持明文字节不变的情况下重新封装到活动 Key。先保留旧包装 Key 完成验证与重新封装，再按既有密钥环检查处理历史秘密；删除旧 Key 后打不开任一根密钥必须阻止启动，不能生成新根密钥替代它。会话命名空间身份与包装 Key ID 无关，相关元数据应纳入备份，禁止通过降级迁移丢弃。

远端 MCP 检测需要 `AIHUB_DETECTION_PUBLIC_ORIGIN=https://网关域名`，值只能为本应用的公网 HTTPS origin，不含路径、认证或查询。反向代理须允许上游访问 `POST /api/detection/mcp`；该固定回调凭检测器随机临时令牌认证，不使用站内 Cookie 或平台 Key，也不要求浏览器 Origin。没有配置时两项 MCP 套件保持 pending。回调只提供一个空参数、只读、无副作用的合成工具，结果为没有写入提示的随机标记；须同时收到真实授权回调和匹配的原生 MCP 工具结果才通过。每探针请求/工具调用与活动探针有界，任务完成/取消即撤销，不存请求/响应/令牌。它不是通用 MCP 托管、文件或工具资源服务。Responses 使用明确审批往返，Messages 固定既定 beta 与精确工具 allowlist，不访问宿主 MCP 配置。

分组余额/配额诊断只识别固定的原生剩余量响应头；缺失记未观测，负数/非整数记失败，不等待重置。当前原生推理 Key 契约没有统一金额余额接口，不尝试组织管理员 API 或中转站任意私有路径，不把 token 剩余量称为余额。这些凭据相关数值仅供所有者/管理员查看，不进入公开分组画像或路由支持证据。新增私有金额余额适配前须先固定接口与认证契约。

## 开发验证

机制单测：`go -C backend test ./internal/detection ./internal/api`。PostgreSQL、迁移升级与 `-race` 交 CI。可显式启用 `uiqa` 构建标签的合成浏览器 HTTP harness；它不使用数据库、宿主登录、付费 Key 或真实上游，不构成供应商验收。开发不运行端到端性能 A/B，相关影响路径随版本交接至发版前集中验证。

RL23/26 是平台回归矩阵，不是对任意供应商两两组合的创建检测，也不开放 `gateway` 贡献目标。CI 使用合成批准对象和固定 CLI，覆盖两渠道的实际加密推理/V2 压缩续用、notes A 写/B 读、history 读取恢复及凭据轮换；同组/跨组、thinking 签名、真正远端引用、碰撞/伪造/其他账号与 Key、撤销、固定出站与错误抹除走正式 HTTP 处理器。非法标识攻击是 HTTP 权限证据，不声称固定 CLI 自身提供制造这些请求的功能。测试只保留断言、计数和版本；合成上游正文只留测试内存，不写供应商报告。贡献对象的全部适用三层项目由 `TestAllApplicableCodexBusinessSuitesRegisterWithFixedArtifacts` 守住，缺制品仍保持待就绪。

平台矩阵按固定客户端的实际请求模型分层：HTTP `ResponsesApiRequest` 不含 `previous_response_id`（同文件中的该字段属于已排除的 WS 请求）；Messages thinking 签名、伪造/碰撞 ID、任意出站与秘密错误注入在正式 HTTP 层验证。CLI 层验证其实际生成的推理/压缩/上下文及正常跨渠道流程、实际会话的账号/Key 隔离、凭据轮换与撤销。原功能断言均保留，分层由 manifest 版本固定；`TestCodexPlatformManifestHasConcreteRegressionOwners` 防止新增断言失去具体回归归属。CLI 可能在本地 PAT 配置下继续尝试推理，撤销的正确边界是每个正式入口拒绝该 Key、没有上游调用，不依赖 CLI 的本地认证标签。
