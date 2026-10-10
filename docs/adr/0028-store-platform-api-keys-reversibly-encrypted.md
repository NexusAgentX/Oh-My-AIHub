# ADR-0028：平台 API Key 可逆加密保存

- 状态：已通过
- 日期：2026-10-08
- 决策者：项目维护者与 AI 产品团队
- 关联内容：[Epic #170](https://github.com/NexusAgentX/Oh-My-AIHub/issues/170)、[Feature #172](https://github.com/NexusAgentX/Oh-My-AIHub/issues/172)、[ADR-0009](0009-adopt-encrypted-upstream-credentials-and-pinned-egress.md)、[ADR-0027](0027-adopt-transparent-gateway-with-post-hoc-billing.md)

## 背景

v0.6.0 的平台 API Key 只保存哈希，完整 Key 仅在创建时显示一次，丢失只能重建。重写后的产品原则是“小白第一次登录就能复制配置开始调用”，并允许随时再复制 Key（Epic #170 决定 7）。这改变了安全边界：数据库不再只保存不可逆摘要。

## 决策目标

- 用户可以随时在已登录的会话中再次复制完整 Key。
- 网关仍能用 Key 的摘要快速查找。
- 不新增密钥环境变量，不让数据库泄露即等于全部 Key 泄露。
- 不解决：Key 轮换界面、按用途限定 Key 的权限范围。

## 候选方案

### 方案一：只存哈希

最安全，但无法再次复制，与产品目标冲突。

### 方案二：哈希用于查找，另存可逆加密密文

沿用 ADR-0009 的版本化 AEAD 凭据密钥环；密文与记录 ID、版本、密钥 ID 绑定。

## 决定

采用方案二。

1. `api_keys.key_hash` 保存完整 Key 的 SHA-256，网关据此查找；`key_ciphertext`、`key_nonce`、`key_key_id` 用上游凭据密钥环（`UPSTREAM_CREDENTIAL_KEYRING`）以 AES-256-GCM 加密，AAD 绑定 Key ID、版本与密钥 ID，不新增环境变量。
2. 完整 Key 只通过 `GET /api/keys/{id}/secret` 与创建响应返回，且仅返回给 Key 的所有者；列表与详情只含前缀。每次读取明文写入审计 `api_key.reveal`。
3. Key 带 `sk-aih-` 前缀，由 32 字节随机数生成；日志只记录展示前缀，从不记录完整 Key。
4. 软删除的 Key 立即失效；停用账号的 Key 以 403 拒绝。

## 后果

### 正面影响

- 用户随时可复制 Key，首页直接给出可用配置。
- 与上游 Key 使用同一套密钥环与轮换流程，运维不增加新密钥。

### 负面影响与成本

- 持有密钥环与数据库的人可以解出全部平台 Key；与上游 Key 的威胁模型一致。
- 会话被盗后攻击者可再次读取 Key；以审计和停用/删除 Key 兜底。

### 风险与缓解措施

- 密钥环丢失会使所有 Key 无法再复制（仍可按哈希认证）；备份要求与上游凭据相同。

## 验证方式

- 集成测试：默认 Key 惰性创建且只创建一次；再次复制与审计；列表不含明文；他人的 Key 返回 404；日志不含 Key。

## 替代关系

补充 [ADR-0009](0009-adopt-encrypted-upstream-credentials-and-pinned-egress.md) 与 [ADR-0007](0007-adopt-invited-identity-and-server-sessions.md) 的安全边界；取代 [ADR-0010](archive/0010-adopt-snapshot-gateway-and-idempotent-settlement.md) 中“平台 API Key 不保存可恢复明文”的约定。
