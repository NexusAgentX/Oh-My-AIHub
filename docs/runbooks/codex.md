# Codex HTTP 接入

Codex 模式复用平台 Key、模型权限、路由、额度及账本。身份响应只为客户端兼容，不代表官方套餐权益。协议研究基于官方 CLI 0.163.0；升级客户端后应重新验证目录和扩展协议。

## 平台配置

管理员在设置的「Codex 兼容响应」粘贴三份 JSON：PAT 身份、账号检查、模型目录。每份最多 512 KiB；留空关闭对应接口。保存保留未知字段及文档内容，接口验证平台 Key 后直接返回，不根据权限或渠道裁剪。

所有有效 Key 看到同一份固定身份。移除真实个人信息及任何凭据；whoami 的 `chatgpt_account_id` 必须在账号检查的 `accounts` 映射中存在。响应账号 ID 不参与实际鉴权、计费或资源归属，不作为上游账号头转发；真实上游账号头由渠道主通过已有请求头规则设置。

目录的 `models` 是数组，`slug` 唯一。目录中的模型名应对应平台模型或 Key 别名；目录列出模型不会授予调用权限。关闭 `prefer_websockets` 等 WebSocket 声明；本轮不支持 WebSocket 和 Codex history/notes，也不提供 `thread_hint` 初始化。模型指令中若包含这些工具的描述，需结合客户端配置检查实际工具声明，不能只依据目录标记认定已关闭。

保存失败维持原配置。保存成功后随既有设置快照刷新生效（通常 5 秒内），每次读取只使用同一份完整快照。审计仅记录三份文档组合的摘要，更新时间和操作者沿用平台设置审计，不复制文档正文。

## 渠道配置

推荐 Base URL 填供应商根地址，例如 `https://api.example.com`。Codex 请求保留 `/backend-api/codex/...` 路径；若 Base URL 已以 `/backend-api/codex` 结尾，同一前缀只拼接一次。标准 `/v1` 请求仍按原规则拼接，不能把 Codex 专用 Base URL 当成标准入口的自动转换器。

渠道主表单维护 Responses/Codex 兼容矩阵：HTTP 操作与流式/非流式支持属于渠道；工具、推理、Lite、V1/V2 压缩与 Fast/Ultrafast 属于渠道模型。Codex 独立搜索、图片生成与编辑各有渠道单元；独立 `/v1/images/*` 已停用。可探测填写，也可手动声明；未验证项目保留原值，探测样本不保证所有参数组合兼容。

请求在满足全部能力的渠道内按用户手动顺序执行排除、限额、冷却和故障转移。上游推理、搜索、生图、压缩结果均透明转发；不转换搜索/压缩实现或悄悄降档。完整压缩内容及加密推理不因其自身绑定渠道；`previous_response_id`、`conversation` 和真正的条目 ID 引用继续遵守原资源归属与凭据范围。跨供应商密文兼容性由实际服务决定，网关不承诺所有供应商互通。

## 客户端

使用独立 CODEX_HOME 或容器，勿覆盖已有官方登录。在私有配置目录写入 `auth.json`，PAT 和 API Key 都使用平台 Key。不要把该文件加入版本控制。

```json
{"personal_access_token":"<平台 Key>","OPENAI_API_KEY":"<平台 Key>"}
```

`config.toml` 的模型名替换为平台可调用的模型：

```toml
model = "<平台模型或 Key 别名>"
model_provider = "openai"
openai_base_url = "https://<平台域名>/backend-api/codex"
chatgpt_base_url = "https://<平台域名>/backend-api/"
web_search = "live"

[features]
context_management = false
enable_request_compression = true

[features.token_budget]
use_history_notes_extension = false
```

启动前设置 `CODEX_AUTHAPI_BASE_URL=https://<平台域名>`。保留官方 openai provider，以便客户端按其原生协议发送扩展字段。WebSocket 以目录及目标版本的客户端配置关闭；不要把 HTTP/SSE 的成功当作 WS 已验证。

V1 是独立 `/responses/compact`；V2 是 Responses 中的 `compaction_trigger`。后台压缩是客户端并行发起的 V2 请求，非 `background:true`。0.163.0 的后台压缩要求相应功能开启、支持 V2 且关闭 TokenBudget，具体配置和回退必须用该版本验收。本轮不自动修改用户客户端配置。

## 观察与排障

调用列表保留格式，另显示操作分类并支持筛选；详情展示所需能力、资源绑定及被排除渠道。历史记录不猜测补分类。后台执行没有可靠标记时显示 V2 压缩，不根据耗时或 auto/post_turn 推测。

`routing_not_configured` 表示尚未手动选择渠道，应到模型详情或当前 API Key 配置；升级会将已有选择全部清空。`capability_unavailable` 表示当前模型和协议没有满足全部声明能力的候选；查看详情区分能力、用户排除和限额原因。`codex_not_configured` 表示固定响应留空。完整压缩内容不再因未登记 ID 而返回 404，但仅带 ID 的资源引用仍必须属于当前 Key。

zstd 请求在字段扫描前解压，压缩体和解压体均受 32 MiB 限制，解码窗口最多 32 MiB、解码器内存上限 64 MiB，并以单线程解码。解压发生在推理准入前，因此全实例最多同时运行 2 个解码器；饱和立即返回 429 `request_decoders_busy` 和 `Retry-After: 1`，不排队、不联系上游。普通 JSON 不创建解码器，也不受该解压限制影响。转发解压 JSON 时移除压缩及旧长度头，仍替换顶层模型并执行权限校验。

沿用上游可靠用量结算；没有可靠用量时保持未知/未计费语义。搜索不新增次数费，压缩未被客户端采用也不抹去上游已经产生的可靠消耗。
