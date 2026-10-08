import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { CallStats, CallSummary } from '../api/types'
import { matchesFilters } from './CallExplorer'
import { emptyCallFilters, filtersToParams, isRequestId } from './CallFilterBar'
import { CallStatsBar } from './CallStatsBar'
import { CallTable, summaryColumns, switchedLabel } from './CallTable'

const id = '0b6f3d1e-2f4a-4c2b-9d7e-1a2b3c4d5e6f'

const call = (attempts: number): CallSummary => ({
  id,
  created_at: '2026-10-08T08:00:00Z',
  completed_at: '2026-10-08T08:00:01Z',
  model_id: 'gpt-x',
  requested_model: 'gpt-x',
  format: 'openai_chat',
  stream: true,
  tag: null,
  api_key: null,
  outcome: 'succeeded',
  channel: { id: 'c1', name: '老陈的中转' },
  attempt_count: attempts,
  usage: { input_tokens: 10, output_tokens: 20, cache_read_tokens: 0, cache_write_tokens: 0 },
  cost: '0.3',
  fee: '0.0003',
  charged: '0.3003',
  ttft_ms: 300,
  duration_ms: 900,
})

describe('request ID filter', () => {
  it('passes request_id and drops the time range so the lookup is exact', () => {
    const params = filtersToParams({ ...emptyCallFilters, requestId: id })
    expect(params.request_id).toBe(id)
    expect(params.from).toBeUndefined()
    expect(filtersToParams(emptyCallFilters).request_id).toBeUndefined()
  })

  it('accepts only full UUIDs', () => {
    expect(isRequestId(id)).toBe(true)
    expect(isRequestId('0b6f3d1e')).toBe(false)
  })

  it('keeps only the matching call from the live stream', () => {
    const row = call(1) as unknown as Parameters<typeof matchesFilters>[0]
    expect(matchesFilters(row, { request_id: id })).toBe(true)
    expect(matchesFilters(row, { request_id: '11111111-2222-3333-4444-555555555555' })).toBe(false)
  })
})

describe('attempt count', () => {
  it('labels switched channels', () => {
    expect(switchedLabel(1)).toBeNull()
    expect(switchedLabel(0)).toBeNull()
    expect(switchedLabel(3)).toBe('换了 2 次')
  })

  it('shows 换了 N 次 next to the channel in call lists', () => {
    const markup = renderToStaticMarkup(<CallTable caption="调用" columns={summaryColumns()} rows={[call(2)]} />)
    expect(markup).toContain('老陈的中转')
    expect(markup).toContain('换了 1 次')
    expect(renderToStaticMarkup(<CallTable caption="调用" columns={summaryColumns()} rows={[call(1)]} />)).not.toContain('换了')
  })
})

describe('call stats bar', () => {
  it('shows first-token p50 / p95 and total tokens', () => {
    const stats: CallStats = {
      calls: 12,
      succeeded: 11,
      failed: 1,
      success_rate: '0.9166',
      charged: '1.5',
      input_tokens: 1000,
      output_tokens: 500,
      total_tokens: 1700,
      ttft_p50_ms: 320,
      ttft_p95_ms: 1250,
    }
    const markup = renderToStaticMarkup(<CallStatsBar stats={stats} />)
    expect(markup).toContain('首字 p50 / p95')
    expect(markup).toContain('320 ms')
    expect(markup).toContain('1.25 s')
    expect(markup).toContain('1,700')
  })
})
