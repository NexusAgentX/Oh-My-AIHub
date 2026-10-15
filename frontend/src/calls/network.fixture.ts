import type { CallDetail } from '../api/types'
export function networkFixture(): CallDetail {
  return {
    id: '30000000-0000-4000-8000-000000000001', created_at: '2026-10-15T08:00:00Z', completed_at: '2026-10-15T08:00:04Z',
    model_id: 'demo-chat', requested_model: 'demo-chat', format: 'openai_chat', stream: true, tag: '', api_key: { id: 'key', name: '默认 Key' },
    outcome: 'succeeded', channel: { id: 'channel', name: '示例渠道' }, attempt_count: 2, usage: { input_tokens: 200, output_tokens: 100, cache_read_tokens: 0, cache_write_tokens: 0 },
    cost: '0.01', fee: '0.00001', charged: '0.01001', ttft_ms: 1000, duration_ms: 4000, routing_mode: 'cheapest', routing_source: 'account', client_user_agent: 'Demo SDK', price_snapshot: null,
    output_tokens_per_second: 33.3, inter_token_p50_ms: 30, inter_token_p95_ms: 50, response_bytes: 12000, ledger_transaction_id: null, upstream_response_id: null,
    attempts: [
      { upstream_model: 'demo', timeouts: null, offset_ms: 10, reused_connection: false, fallback_reason: '', channel: { id: 'first', name: '示例备用渠道' }, status_code: 502, error_code: 'bad_gateway', error_message: '上游暂不可用', connect_ms: 50, ttft_ms: null, duration_ms: 200, response_bytes: 100, end_reason: 'upstream_error' },
      { upstream_model: 'demo', timeouts: null, offset_ms: 250, reused_connection: true, fallback_reason: '', channel: { id: 'channel', name: '示例渠道' }, status_code: 200, error_code: null, error_message: null, connect_ms: 2, ttft_ms: 750, duration_ms: 3700, response_bytes: 12000, end_reason: 'completed' },
    ],
    diagnostics: {
      stages: [{ stage: 'authentication', offset_ms: 0, duration_ms: 2 }, { stage: 'request', offset_ms: 2, duration_ms: 5 }, { stage: 'routing', offset_ms: null, duration_ms: 3 }],
      routing: [], stop_reason: 'stop', usage_status: 'reported', delivery: 'complete', frames: 100, max_frame_gap_ms: 100, upstream_bytes: 12000, max_attempts: 3, call_timeout_ms: 600000, drain_timeout_ms: 30000,
      network: {
        client: { ip: '198.51.100.20', location: { label: '示例 · 东京地区', latitude: 35.7, longitude: 139.7 } },
        gateway: { ip: '', location: { label: '示例 · 香港部署', latitude: 22.3, longitude: 114.2 } },
        attempts: [
          { node: { ip: '', location: null }, phases: [{ stage: 'connection', offset_ms: 0, duration_ms: 50 }] },
          { node: { ip: '203.0.113.20', location: { label: '示例 · 新加坡入口', latitude: 1.3, longitude: 103.8 } }, phases: [
            { stage: 'prepare', offset_ms: 0, duration_ms: 1 }, { stage: 'connection', offset_ms: 1, duration_ms: 1 }, { stage: 'send', offset_ms: 2, duration_ms: 3 }, { stage: 'headers', offset_ms: 5, duration_ms: 700 },
          ] },
        ], location_source: 'ipwho.is',
      },
    },
  }
}
