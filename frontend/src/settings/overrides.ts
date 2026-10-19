import type { components } from '../api/schema.gen'

export const emptyOverrides: components['schemas']['MarketDefaults'] = { user_agent: null, header_rules: null, concurrency_limit: null, rpm_limit: null, ttft_timeout_ms: null, request_timeout_ms: null, idle_timeout_ms: null, cooldown_failures: null, cooldown_seconds: null }
