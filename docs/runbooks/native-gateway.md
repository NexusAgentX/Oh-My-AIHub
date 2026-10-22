# 四类协议转发与原生图片

平台入口以 OpenAPI 的实际 `paths` 为准；`x-aihub-gateway-contract` 的端点表只声明同一批入口，不额外开放能力。请求使用平台 API Key，网关换用渠道的上游 Key；不使用宿主登录或官方订阅账号，也不做协议转换。

当前只代理无状态推理与图片：OpenAI Chat、OpenAI Responses、Anthropic Messages、Gemini `generateContent`/`streamGenerateContent`，以及图片生成/编辑/变体；另有 OpenAI、Anthropic、Gemini 三种风格的模型列表与单模型元数据。会话、响应资源、条目、显式缓存、后台响应与 token 计数入口已退役，对应路径回答 404 `unsupported_endpoint`；请求边界（`store` 剥离、续接引用 400）见[网关契约手册](gateway-contracts.md#请求边界)。

## 各入口的转发差异

| 入口 | 网关注入或替换 | 边界 |
| --- | --- | --- |
| Chat Completions | 顶层 `model` 换成渠道上游名；流式请求补 `stream_options.include_usage=true`，客户端原本未开启时把上游多发的纯用量帧滤掉，用量仍照常计费 | 顶层 `store` 为真时删除；`previous_response_id`、`conversation` 返回 400 |
| Responses | 同上，仅替换 `model` 并剥离 `store` | 同上；`background` 为真返回 400 `background_unsupported`，读取/取消/条目入口为 404 |
| Messages | 同上，保留 `anthropic-version` 等协议头，鉴权换到 `x-api-key` 位置 | 无服务端资源可引用；计数与压缩入口已退役 |
| Gemini | 模型与流式来自路径 `{model}:{operation}`，请求体只替换 `model` | `cachedContent` 返回 400；`countTokens` 等未保留 operation 回答 404 |
| 图片生成/编辑/变体 | JSON 或单次 multipart；multipart 只替换 `model` 字段的字节，文件、边界与其他参数原样转发 | 不保存图片、不代理上游返回的 URL，也没有独立 Files/容器/工具资源 |

模型列表与单模型元数据只按当前平台 Key 权限回答本站生效模型及别名，不读取上游共享账号的目录；可选上下文/输出元数据只有所有候选都能证明时才给出保守下界，否则保持未知。

## 请求传输与压缩

请求编码只接受 identity 与 zstd 单一编码。压缩前后都限制为 32 MiB，解码器另有 64 MiB 内存保护与 32 MiB 窗口上限，编解码窗口 2 秒；每进程最多两个编解码操作同时进行，不排队。

饱和返回 429 `request_codec_busy`，取消或超时返回 408 `request_codec_timeout`，编码不受支持返回 415 `unsupported_content_encoding`，超限或损坏返回 413/400。这些本地失败不换渠道、不计渠道健康，也不重试上游。无压缩请求沿用同一份读取与扫描结果，不额外复制正文或请求头。

## 图片与计费

账单只根据可靠用量及实际成功产物计算：`n` 是请求参数，不是已成功的产物数；SSE 的 partial 图片不按成功图片收费；没有实际 token 依据就不显示图片 token/s。图片按分组报价与实际可靠事实同步结算，未定价的部分保留未计价原因，不猜测费用。

## 运行上限与故障

- 非流式请求按单次尝试的请求超时计时；流式在首字节前用首字超时，之后用空闲超时；一次调用另有 60 分钟总上限。向客户端写出字节之后不再回退渠道，按已读到的用量结算；写出前可换下一个候选。
- 出站只连已校验并钉住的地址，复用连接池；本地编解码或读取失败不污染渠道健康。
- 网关不保存请求或响应正文，日志只有请求 ID、账号、Key 前缀、模型、格式、渠道、结果、状态码、耗时、token 与费用。

开发期不运行端到端性能 A/B；发版前按[网关性能基线](gateway-baseline.md)集中覆盖请求扫描、`store` 剥离、旁路解析、图片 JSON/multipart/SSE 与同步结算。
