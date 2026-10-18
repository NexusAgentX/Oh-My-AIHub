import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { RoutingMetrics } from './RoutingMetrics'
import { routingModeLabels } from '../calls'
import { CallResponseMetrics } from '../calls/CallDetail'
import type { CallDetail } from '../api/types'

describe('model-scoped routing measurements', () => {
  it('keeps historical fastest as latency and adds an independent speed mode', () => {
    expect(routingModeLabels.fastest).toBe('延迟优先')
    expect(routingModeLabels.speed).toBe('速率优先')
    expect(Object.keys(routingModeLabels)).toHaveLength(5)
  })
  it('does not show missing or thin data as measured zero or mature evidence', () => {
    expect(renderToStaticMarkup(<RoutingMetrics rows={[]} />)).toContain('未知')
    const html = renderToStaticMarkup(<RoutingMetrics rows={[{ format: 'openai_responses', tier: '', stream: true, attempts: 3, successes: 3, excluded: 2, success_rate: 1, first_output_p50_ms: 0, first_output_samples: 3, output_tps_p50: null, output_samples: 0, min_samples: 20 }]} />)
    expect(html).toContain('0 ms')
    expect(html).toContain('未知')
    expect(html).toContain('样本不足')
    expect(html).toContain('暂不用于排序')
    expect(html).not.toContain('0.0 tokens/s')
  })
  it('labels new measured TPS separately from historical whole-call averages', () => {
    const call = { attempts: [{ metrics_version: 2 }], stream: true, output_tokens_per_second: null, inter_token_p50_ms: null, inter_token_p95_ms: null } as CallDetail
    const html = renderToStaticMarkup(<CallResponseMetrics call={call} />)
    expect(html).toContain('有效输出速率')
    expect(html).toContain('未知')
    expect(html).not.toContain('历史口径')
  })
})
