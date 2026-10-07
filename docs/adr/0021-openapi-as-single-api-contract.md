# ADR-0021：以 OpenAPI 作为前后端唯一契约来源

- 状态：已通过
- 日期：2026-10-07
- 决策者：仓库维护者
- 关联内容：[Feature #124](https://github.com/NexusAgentX/Oh-My-AIHub/issues/124)、[Epic #109](https://github.com/NexusAgentX/Oh-My-AIHub/issues/109)

## 背景

前端 `contracts.ts` 手写后端 JSON 形状，字段变化没有任何校验；`handler.go` 同时承担 93 条路由、限流状态与中间件，难以回答“某接口需要什么门禁、返回什么”。第三波将让前端改用生成类型，需要先有一份机器可读、与实现强绑定的契约。

## 决策目标

- 一份文档回答每个 `/api/**` 接口的方法、门禁、请求、响应与错误；
- 契约与实现漂移时测试立即失败；
- 不改变任何既有 URL、方法、字段、状态码与错误文案。

## 决定

1. **契约来源**：`backend/api/openapi.yaml`（OpenAPI 3.1）是前后端 JSON 契约的唯一来源。字段、状态码或门禁变化必须同时修改该文件；前端类型日后从它生成，不再手写。
2. **范围**：覆盖全部 `/api/**` JSON 接口，描述请求、成功响应、错误信封与认证。外部协议入口 `/v1/**`、`/v1beta/**` 只登记路径与网关 Key 认证，不描述透传正文。
3. **门禁声明**：每个操作带扩展字段 `x-access`（`public`、`session`、`ready`、`admin`、`gateway_key`），与后端路由注册时声明的 access 一一对应。
4. **契约测试**（`backend/internal/api/contract_test.go`）：路由表与规范的路径、方法、`x-access` 逐项一致；规范内部自洽（operationId 唯一、路径参数已声明、全部 schema 可编译）；用规范 schema（响应对象 `additionalProperties: false`）校验真实响应构造器与处理器的输出，字段多或少都会失败。
5. **依赖**：仅测试使用 `github.com/santhosh-tekuri/jsonschema/v6`（JSON Schema 2020-12，OpenAPI 3.1 的方言；随带 `regexp2`）与 `go.yaml.in/yaml/v3`（此前已是间接依赖）。均不进入服务二进制。
6. **路由组织**：路由按领域在各自文件内通过 `router` 注册，由 `router` 按声明的 access 包裹会话、首次改密与管理员门禁；全局中间件链固定为“写超时 → 安全头 → 同源校验 → mux”。限流依赖请求体与口令槽位，保持在 handler 内，状态集中于 `rateLimits`。

## 后果

### 正面影响

- 前端、后端与评审者共用一份可验证契约，第三波可直接生成类型；
- 新增路由若忘记登记规范，或规范与实现不符，测试即失败。

### 负面影响与成本

- 改接口需同步维护 YAML；
- 新增两个测试依赖（体积小，均只在测试中编译）。

### 风险与缓解措施

- 只校验被测试覆盖的响应：每个响应构造器都有填充与空值两类夹具，新增构造器须补测试；
- 少数响应的可空性依据代码推断，以夹具覆盖；发现偏差时以实现为准修正规范。

## 验证方式

`go -C backend test ./internal/api` 执行全部契约测试。

## 替代关系

无。
