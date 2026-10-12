# ADR-0042：取消供应商域名永久禁用

- 状态：已通过
- 日期：2026-10-12
- 决策者：维护者

## 背景

ADR-0036 继承了对 `api.openai.com` 及其子域的永久禁用，导致管理员无法将 OpenAI 官方 API 地址加入供应商目录，用户也无法配置该官方渠道。维护者确认取消这项供应商特例。

## 决策

出站策略不再内置供应商域名禁用项。OpenAI 官方域名与其他公网域名遵循相同校验；部署配置 `UPSTREAM_BLOCKED_HOSTS` 和平台设置 `settings.extra_blocked_hosts` 仍可显式禁用域名及其子域。

HTTPS、端口白名单、受保护地址拒绝、DNS 地址校验与钉住拨号、TLS 域名验证、禁止环境代理和重定向的边界保持有效。允许保存官方 API 地址不保证账号权限或上游服务可用。

## 替代关系

部分取代 [ADR-0036](0036-adopt-credential-startup-check-and-pinned-egress-pool.md) 中的 OpenAI 官方端点永久禁用要求；其余决定不变。

## 验证

URL 规范化和供应商识别测试覆盖 OpenAI 官方地址；显式域名禁用及现有出站安全测试继续覆盖配置和网络边界。
