import { describe, expect, it } from 'vitest'
import { advancedChangedCount, advancedInput, applyTestResults, customNameFrom, emptyAdvanced, modelsInput, rowsFromChannel, rowsFromDiscover, validateRows } from './channelForm'
import type { Channel } from '../api/types'

describe('channel wizard', () => {
  const rows = rowsFromDiscover([
    { id: 'gpt-5', matched_model_id: 'gpt-5', suggested_formats: ['openai_chat', 'openai_responses'] },
    { id: 'vendor/claude', matched_model_id: 'claude-sonnet-4-5', suggested_formats: ['anthropic'] },
    { id: 'unknown-x', matched_model_id: null, suggested_formats: [] },
  ])

  it('preselects catalog matches and keeps the upstream name', () => {
    expect(rows.map((row) => [row.sell, row.modelId, row.upstream])).toEqual([
      [true, 'gpt-5', 'gpt-5'],
      [true, 'claude-sonnet-4-5', 'vendor/claude'],
      [false, '', 'unknown-x'],
    ])
    expect(validateRows(rows)).toBe('')
    expect(modelsInput(rows)).toEqual([
      { compatibility: { responses: [], codex: [] }, model_id: 'gpt-5', upstream_model: 'gpt-5', multiplier: '1', fx_discount: false, enabled: true },
      { compatibility: { responses: [], codex: [] }, model_id: 'claude-sonnet-4-5', upstream_model: 'vendor/claude', multiplier: '1', fx_discount: false, enabled: true },
    ])
    expect(modelsInput([{ ...rows[0], multiplier: '0.13', fxDiscount: true }])[0]).toMatchObject({ multiplier: '0.13', fx_discount: true })
  })

  it('applies verified model cells and keeps skipped declarations', () => {
    const tested = applyTestResults([{ ...rows[0], capabilities: ['codex/vision'] }], [
      { model_id: 'gpt-5', format: 'openai_responses', protocol: 'responses', capability: 'function_tools', scope: 'model', skipped: false, ok: true, status_code: 200, error: null, duration_ms: 10 },
      { model_id: 'gpt-5', format: 'codex', protocol: 'codex', capability: 'vision', scope: 'model', skipped: true, ok: false, status_code: null, error: 'manual', duration_ms: 0 },
      { model_id: 'gpt-5', format: 'codex', protocol: 'codex', capability: 'inference', scope: 'channel', skipped: false, ok: true, status_code: 200, error: null, duration_ms: 10 },
    ])
    expect(tested[0].capabilities).toEqual(['codex/vision', 'responses/function_tools'])
    expect(tested[0].passed).toEqual({ 'responses/function_tools': true })
    expect(tested[0].errors).toEqual({})
  })

  it('rejects selling a model without a platform match or format', () => {
    expect(validateRows([{ ...rows[2], sell: true }])).toBe('勾选的模型需要选择平台模型或设为自定义模型')
    expect(validateRows([{ ...rows[0], formats: [] }])).toBe('')
  })

  it('sends custom models with their prices and no multiplier', () => {
    const custom = { ...rows[2], sell: true, custom: true, formats: ['openai_chat' as const], modelId: customNameFrom('vendor/unknown x'),
      customPrices: { input: '1', output: '9', cacheWrite: '0', cacheRead: '0.1' } }
    expect(custom.modelId).toBe('unknown-x')
    expect(validateRows([custom])).toBe('')
    expect(modelsInput([custom])).toEqual([{
      compatibility: { responses: [], codex: [] }, model_id: 'unknown-x', upstream_model: 'unknown-x', enabled: true,
      custom_prices: { input: '1', output: '9', cache_write: '0', cache_read: '0.1' },
    }])
    expect(validateRows([{ ...custom, customPrices: { ...custom.customPrices, cacheRead: '' } }])).toContain('四个价格')
    expect(validateRows([{ ...custom, customPrices: { ...custom.customPrices, input: '100001' } }])).toContain('四个价格')
    expect(validateRows([{ ...custom, modelId: '-bad' }])).toContain('自定义模型名')
  })

  it('asks for a multiplier before a pending model sells again', () => {
    const channel = { models: [{
      model_id: 'foo-1', upstream_model: 'foo-1', multiplier: '1', fx_discount: false, formats: ['openai_chat'],
      format_tests: {}, enabled: false, custom_prices: null, pending_multiplier: true,
    }] } as unknown as Channel
    const [pending] = rowsFromChannel(channel)
    expect([pending.pending, pending.multiplier, pending.custom, pending.sell]).toEqual([true, '', false, false])
    expect(validateRows([{ ...pending, sell: true }])).toBe('foo-1 已加入模型目录，请先设置倍率')
    expect(validateRows([{ ...pending, sell: true, multiplier: '1.5' }])).toBe('')
  })

  it('leaves advanced settings at platform defaults when blank', () => {
    expect(advancedChangedCount(emptyAdvanced)).toBe(0)
    expect(advancedInput(emptyAdvanced)).toMatchObject({ user_agent: null, concurrency_limit: null, cooldown_seconds: null })
    const form = { ...emptyAdvanced, rpm: '60', cooldownFailures: '3', cooldownMinutes: '5' }
    expect(advancedChangedCount(form)).toBe(2)
    expect(advancedInput(form)).toMatchObject({ rpm_limit: 60, cooldown_failures: 3, cooldown_seconds: 300 })
  })
})

it('counts an idle-only override and keeps all three timeouts independent', () => {
  expect(advancedChangedCount({ ...emptyAdvanced, idleTimeout: '2000' })).toBe(1)
  expect(advancedInput({ ...emptyAdvanced, ttftTimeout: '60000', idleTimeout: '2000', requestTimeout: '10000' })).toMatchObject({
    ttft_timeout_ms: 60000, idle_timeout_ms: 2000, request_timeout_ms: 10000,
  })
  expect(advancedInput(emptyAdvanced)).toMatchObject({ ttft_timeout_ms: null, idle_timeout_ms: null, request_timeout_ms: null })
})
