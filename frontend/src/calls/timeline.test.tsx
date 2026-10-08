import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { CallAttempt, CallDetail } from '../api/types'
import { AttemptTimeline } from './AttemptTimeline'
import { attemptSegments, timelineScale } from './timeline'
import { mergeStreamItem } from './useCallStream'
import { matchesFilters } from './CallExplorer'

const failed: CallAttempt = {
  channel: { id: 'c1', name: '老陈的中转' },
  status_code: 429,
  error_code: 'upstream_error',
  error_message: '{"error":"rate limited"}',
  connect_ms: 100,
  ttft_ms: null,
  duration_ms: 400,
  response_bytes: 30,
  end_reason: 'upstream_error',
}

const succeeded: CallAttempt = {
  channel: { id: 'c2', name: '小王的渠道' },
  status_code: 200,
  error_code: null,
  error_message: null,
  connect_ms: 50,
  ttft_ms: 300,
  duration_ms: 800,
  response_bytes: 2048,
  end_reason: 'completed',
}

describe('attempt timeline segments', () => {
  it('splits a successful attempt into connect, wait and output on a shared scale', () => {
    const scale = timelineScale([failed, succeeded])
    expect(scale).toBe(800)
    expect(attemptSegments(succeeded, scale)).toEqual([
      { kind: 'connect', ms: 50, percent: 6.25 },
      { kind: 'wait', ms: 250, percent: 31.25 },
      { kind: 'output', ms: 500, percent: 62.5 },
    ])
  })

  it('marks the part after connecting as failed when no first token arrived', () => {
    expect(attemptSegments(failed, 800).map((segment) => [segment.kind, segment.ms])).toEqual([
      ['connect', 100],
      ['failed', 300],
    ])
  })

  it('marks output after the first token as failed when the attempt was interrupted', () => {
    const interrupted = { ...succeeded, end_reason: 'timeout_total' }
    expect(attemptSegments(interrupted, 800).at(-1)?.kind).toBe('failed')
  })

  it('renders status code, raw upstream error and streaming metrics', () => {
    const call = {
      ttft_ms: 300,
      output_tokens_per_second: 42.5,
      inter_token_p50_ms: 20,
      inter_token_p95_ms: 80,
      usage: { input_tokens: 1200, output_tokens: 300, cache_read_tokens: 0, cache_write_tokens: 0 },
    } as CallDetail
    const markup = renderToStaticMarkup(<AttemptTimeline attempts={[failed, succeeded]} call={call} />)
    expect(markup).toContain('老陈的中转')
    expect(markup).toContain('429 · 上游错误')
    expect(markup).toContain('{&quot;error&quot;:&quot;rate limited&quot;}')
    expect(markup).toContain('42.5 tokens/s')
    expect(markup).toContain('20 ms / 80 ms')
    expect(markup).toContain('attempt-segment-failed')
  })
})

describe('live stream merge', () => {
  it('inserts new calls on top and replaces updated ones in place', () => {
    const a = { id: 'a', outcome: 'in_progress' }
    const b = { id: 'b', outcome: 'succeeded' }
    expect(mergeStreamItem([a], b).map((item) => item.id)).toEqual(['b', 'a'])
    expect(mergeStreamItem([b, a], { id: 'a', outcome: 'succeeded' })[1].outcome).toBe('succeeded')
  })

  it('applies the current filters to live rows', () => {
    const row = {
      id: 'x',
      created_at: '',
      model_id: 'gpt-5',
      format: 'openai_chat',
      stream: true,
      outcome: 'succeeded',
      usage: { input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0 },
      ttft_ms: null,
      duration_ms: null,
      api_key: { id: 'k1', name: '默认' },
      tag: null,
    } as const
    expect(matchesFilters({ ...row }, { model: 'gpt-5', api_key_id: 'k1' })).toBe(true)
    expect(matchesFilters({ ...row }, { api_key_id: 'k2' })).toBe(false)
    expect(matchesFilters({ ...row }, { outcome: 'upstream_failed' })).toBe(false)
  })
})
