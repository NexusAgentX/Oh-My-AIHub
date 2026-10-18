# 网关契约与隔离原型

本手册用于 F0 的可重复协议/权限机制验证。正式端点由 F7/F8 交付，供应商真实检测由 F4/F8 交付；原型不连接真实上游、不操作数据库，也不产生平台账本或供应商能力通过记录。目标端点与场景清单只维护在 `backend/api/openapi.yaml` 的 `x-aihub-gateway-contract`，架构边界见 [ARCHITECTURE](../../ARCHITECTURE.md#f0-发现阶段边界)。

## 契约与权限回归

在任务 worktree 中执行：

```sh
mise exec -- go -C backend test ./internal/gatewaycontract ./internal/api
```

契约检查覆盖原始四协议 279 个 ID 和 Codex RL01–RL26 的完整追溯（含移出项），并验证端点/来源/负责 Feature 的关联。ID 数量不是请求次数或当前完整性分母。原型通过本地 HTTP fixture 验证不透明内容与 SSE 字节保留、单次 multipart 图片、引用越权及跨渠道候选；这些结论只属于平台机制。

`pagination_test.go` 验证多页共享列表先过滤再返回、空页继续、原生游标加密认证及换账号/Key/凭据/父级/过滤条件的拒绝。`state_test.go` 验证同实际上游 Key 跨渠道碰撞的隔离；未知上下文首读不能自动获得归属。`transport_test.go` 验证同组按所选地址顺序回退、凭据轮换/渠道删除后的新资源请求不可改换归属、不确定写入不能重放。正式创建登记必须是原子持久操作，fixture 的元数据不是生产索引。

运行既有后台引擎的快照回归：

```sh
mise exec -- go -C backend test ./internal/gateway -run TestRegisteredBackgroundSurvivesRevocationWithOriginalCredentialAndQuote
```

它在普通渠道/目录存储缺席、平台 Key 撤销及密钥环轮换后，验证已登记任务依旧使用原凭据、原报价收敛结算，新读取/取消则被拒绝。它不是新分组结构已经接入的证明。

## 固定官方 CLI 接入原型

需要 Node、OpenSSL 及官方 `@openai/codex@0.163.0`。把该版本安装到任务专用临时目录，传入其可执行文件绝对路径：

```sh
AIHUB_CLI_LAB="$(mktemp -d)"
npm install --prefix "$AIHUB_CLI_LAB" --ignore-scripts --no-audit --no-fund --registry=https://registry.npmjs.org @openai/codex@0.163.0
node backend/internal/gatewaycontract/testdata/codex-bootstrap.mjs "$AIHUB_CLI_LAB/node_modules/.bin/codex"
rm -rf "$AIHUB_CLI_LAB"
```

脚本拒绝其他客户端版本。它为每组配置创建独立的子进程配置目录与合成工作区，使用临时 CA 和非 CA 叶证书建立回环 HTTPS 后端；TLS 校验保持开启。子进程仅获得必要环境，Git 的宿主配置/凭据辅助读取关闭，插件、应用和 shell 工具关闭，外部代理指向本地 fixture；不加载宿主登录。运行结束或取消时清理进程组和临时目录。输出仅包含版本、认证类别、方法/路径、布尔断言与输入类型，不保存完整 HTTP、认证内容、stdout/stderr、推理或笔记正文。

凭据对照及原生图片工具闭环：

| 配置 | 合成 auth.json 字段 | 预期机制结果 |
| --- | --- | --- |
| 普通 API Key | `OPENAI_API_KEY` | 推理完成，notes 扩展未启用 |
| 平台身份兼容原型 | `personal_access_token` | 本地 whoami/accounts 返回合成平台身份及未知套餐 `aihub`，CLI 分类为 ChatGPT；调用 notes thread_hint 后推理完成 |
| 平台身份兼容＋生图工具 | `personal_access_token` | 合成 SSE 下发 image_gen.imagegen，CLI 实际调用原生 images/generations，以图像模型请求并把工具结果回填推理 |

可以在命令末尾附加 `api-key`、`synthetic-platform-pat` 或 `synthetic-platform-imagegen`，只复现一个配置。生图用本地固定 PNG，不访问真实图片服务；它证明固定客户端与原生入口的闭环，不证明供应商生图支持。Codex JSON edits 字段与通用 multipart edits 是两个原生契约，分别验证，不相互转换。

共同设置是内置 `openai` provider、指向本地 HTTPS 的 `openai_base_url`（`/backend-api/codex`）/`chatgpt_base_url`（`/backend-api/`）、本地 `CODEX_AUTHAPI_BASE_URL`，并启用 token budget 与 history/notes。具体开关以可执行脚本为准。身份辅助接口按维护者决定由平台本地回答，正式实现不能将共享供应商 whoami/accounts 直接转给消费者。

这一步证明固定客户端的启用和合成生图工具闭环，不证明真实 notes 写读/历史恢复、跨供应商兼容、正式平台端点、模型目录、zstd 或真实上游生图已通过。fixture 会拒绝未实现路径，目录失败后使用客户端内置目录；它没有伪造一份已通过的供应商目录。客户端可能先尝试 WebSocket 再回退到 HTTP/SSE，不能把回退记录为 WebSocket 支持。`responses_websockets` 系列在该版本已经移除，不将这些无效开关作为禁用 WS 的证据。

合成后端返回空 thread_hint 只证明调用发生。它没有标准的“创建会话并证明所有者”语义，不能据此把客户端提交的未知 session 先登记给第一个调用者。正式 F8 必须证明实际原生协议下的首次归属；未证明时按既定安全边界失败关闭并标记平台前置未就绪，不写供应商 unsupported，不增加来源粘性。

后台 V2 压缩需要独立配置：固定版本要求启用 `features.async_compaction`、关闭 token budget，且 provider 的远程压缩能力为 V2；它与上述 notes 配置互斥。精确配置及十个 history/notes 操作的字段/必要头只维护在 OpenAPI 扩展，不能将此启用实验解释成后台压缩或全部辅助操作通过。

## 证据及后续验证

PR/Issue 分别记录真实直接上游、正式平台代理和固定客户端证据。此脚本的类别是 `official_cli_with_synthetic_backend`：它可以证明启用/传输机制，不能单独给真实供应商写 `official_cli` 能力通过。真实付费验证仅使用明确提供的测试 Key，由用户在上游限制消费；平台不设全测累计费用/次数/token/总时长门禁。

开发期间不运行端到端性能 A/B。本原型未接入 server、快照或账本，没有生产转发的新增 SQL、锁、连接或逐帧工作；后续 Feature 接入时分别核对热路径成本，最终集成候选在发版前按[性能基线手册](gateway-baseline.md)集中验证。
