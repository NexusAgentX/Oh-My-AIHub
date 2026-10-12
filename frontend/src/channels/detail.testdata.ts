import type { ChannelDetail, ChannelStats, ChannelCall } from '../api/types'

const time = '2026-10-12T12:00:00Z'
const prices = { input: '1', output: '4', cache_write: '1', cache_read: '0.1' }
export const detail: ChannelDetail = {
  channel: {
    id: 'demo', summary: '', description: '', name: '示例共享渠道', base_url: 'https://upstream.example/v1', status: 'listed',
    suspended_reason: null, cooldown_until: null, created_at: time, updated_at: time,
    today: { revenue: '14.8', calls: 1377, success_rate: '0.993' },
    advanced: { user_agent: null, header_rules: { set: [], remove: [] }, concurrency_limit: null, rpm_limit: null,
      daily_revenue_cap: null, ttft_timeout_ms: null, idle_timeout_ms: null, request_timeout_ms: null, cooldown_failures: null, cooldown_seconds: null },
    models: Array.from({ length: 7 }, (_, i) => ({ model_id: `model-${i + 1}`, display_name: `model-${i + 1}`, upstream_model: `model-${i + 1}`,
      price_currency: 'USD', multiplier: '0.13', fx_discount: true, effective_multiplier: '0.02', formats: ['openai_chat', 'openai_responses', 'anthropic'],
      format_tests: { openai_chat: { ok: true, status_code: 200, error: null, duration_ms: 50, tested_at: time } },
      enabled: true, current_prices: prices, custom_prices: null, pending_multiplier: false })),
  },
  events: Array.from({ length: 33 }, (_, i) => ({ id: `event-${i}`, kind: i % 2 ? 'cooldown_started' : 'cooldown_ended', reason: i % 2 ? `首字超时 ${i}` : '', created_at: time })),
}
const windowStats = { calls: 1867, succeeded: 1853, success_rate: '0.993', revenue: '14.8' }
export const stats: ChannelStats = {
  from: time, to: time, ...windowStats, last_24h: windowStats, last_7d: windowStats,
  ttft_p50_ms: 3150, ttft_p95_ms: 9840, output_tokens_per_second_p50: 46.9, output_tokens_per_second_p95: 91.6,
  today: { revenue: '14.8', daily_cap: null, progress: null },
  hourly: Array.from({ length: 24 }, (_, i) => ({ hour: `2026-10-12T${String(i).padStart(2, '0')}:00:00Z`, calls: i * 10, succeeded: i * 10 })),
  daily: Array.from({ length: 7 }, (_, i) => ({ date: `2026-10-${String(i + 6).padStart(2, '0')}`, calls: 100 + i * 120, succeeded: 99 + i * 119, revenue: '2' })),
  by_model: detail.channel.models.map((m, i) => ({ model_id: m.model_id, calls: 30 + i * 25, succeeded: 30 + i * 24, revenue: String(1 + i) })),
  status_codes: [{ status_code: null, count: 117 }, { status_code: 429, count: 1 }],
  recent_failures: Array.from({ length: 20 }, (_, i) => ({ call_id: `failure-${i}`, model_id: `failed-model-${i}`, created_at: time,
    status_code: null, error_code: 'timeout_ttft', error_message: '首字超时', end_reason: 'timeout_ttft' })),
  events: detail.events,
}
export const calls: ChannelCall[] = Array.from({ length: 45 }, (_, i) => ({
  id: `call-${i}`, created_at: time, model_id: `call-model-${i}`, format: 'openai_responses', stream: true,
  outcome: i % 5 === 0 ? 'upstream_failed' : 'succeeded', usage: { input_tokens: 1200, output_tokens: 80, cache_read_tokens: 0, cache_write_tokens: 0 },
  revenue: '0.01', served: true, ttft_ms: 500, duration_ms: 900, error: null,
}))

export function fixtureResponse(url: URL) {
  const path = url.pathname
  if (path.endsWith('/stats')) return stats
  if (path.endsWith('/calls')) {
    const rows = url.searchParams.get('outcome') ? calls.filter((c) => c.outcome === url.searchParams.get('outcome')) : calls
    const start = Number(url.searchParams.get('cursor') || 0)
    const limit = Number(url.searchParams.get('limit') || 50)
    return { items: rows.slice(start, start + limit), next_cursor: start + limit < rows.length ? String(start + limit) : null }
  }
  if (path === '/api/channels/demo') return detail
  if (path === '/api/models') return { items: detail.channel.models.map((m) => ({ id: m.model_id, price_currency: 'USD', usd_cny_rate: '6.7', custom: false })) }
  if (path.startsWith('/api/models/')) return { model: { base_prices: prices, current_tier: null }, price_tiers: [] }
  if (path === '/api/form-defaults') return { default_ttft_timeout_ms: 60000, default_idle_timeout_ms: 60000, default_request_timeout_ms: 600000, default_cooldown_failures: 3, default_cooldown_seconds: 300 }
  return {}
}
