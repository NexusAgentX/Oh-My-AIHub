import type { ChannelStats } from '../api/types'

const time = '2026-10-12T12:00:00Z'
const windowStats = { calls: 1867, succeeded: 1853, success_rate: '0.993', revenue: '14.8' }
export const stats: ChannelStats = {
  from: time, to: time, ...windowStats, last_24h: windowStats, last_7d: windowStats,
  ttft_p50_ms: 3150, ttft_p95_ms: 9840, output_tokens_per_second_p50: 46.9, output_tokens_per_second_p95: 91.6,
  today: { revenue: '14.8' },
  hourly: Array.from({ length: 24 }, (_, i) => ({ hour: `2026-10-12T${String(i).padStart(2, '0')}:00:00Z`, calls: i * 10, succeeded: i * 10 })),
  daily: Array.from({ length: 7 }, (_, i) => ({ date: `2026-10-${String(i + 6).padStart(2, '0')}`, calls: 100 + i * 120, succeeded: 99 + i * 119, revenue: '2' })),
  by_model: Array.from({ length: 7 }, (_, i) => ({ model_id: `model-${i + 1}`, calls: 30 + i * 25, succeeded: 30 + i * 24, revenue: String(1 + i) })),
  status_codes: [{ status_code: null, count: 117 }, { status_code: 429, count: 1 }],
  recent_failures: Array.from({ length: 20 }, (_, i) => ({ call_id: `failure-${i}`, model_id: `failed-model-${i}`, created_at: time,
    status_code: null, error_code: 'timeout_ttft', error_message: '首字超时', end_reason: 'timeout_ttft' })),
  events: Array.from({ length: 33 }, (_, i) => ({ id: `event-${i}`, kind: i % 2 ? 'cooldown_started' : 'cooldown_ended', reason: i % 2 ? `首字超时 ${i}` : '', created_at: time })),
}
