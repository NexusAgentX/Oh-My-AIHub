import { describe, expect, it } from 'vitest'
import { advancedChangedCount, advancedInput, applyTestResults, emptyAdvanced, modelsInput, rowsFromDiscover, validateRows } from './channelForm'

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
      { model_id: 'gpt-5', upstream_model: 'gpt-5', multiplier: '1', formats: ['openai_chat', 'openai_responses'], enabled: true },
      { model_id: 'claude-sonnet-4-5', upstream_model: 'vendor/claude', multiplier: '1', formats: ['anthropic'], enabled: true },
    ])
  })

  it('corrects formats from test results and marks passes', () => {
    const tested = applyTestResults(rows, [
      { model_id: 'gpt-5', format: 'openai_chat', ok: true, status_code: 200, error: null, duration_ms: 10 },
      { model_id: 'gpt-5', format: 'openai_responses', ok: false, status_code: 404, error: 'no', duration_ms: 10 },
      { model_id: 'gpt-5', format: 'anthropic', ok: true, status_code: 200, error: null, duration_ms: 10 },
    ])
    expect(tested[0].formats).toEqual(['openai_chat', 'anthropic'])
    expect(tested[0].passed).toEqual({ openai_chat: true, openai_responses: false, anthropic: true })
    expect(tested[1]).toBe(rows[1])
  })

  it('rejects selling a model without a platform match or format', () => {
    expect(validateRows([{ ...rows[2], sell: true }])).toBe('勾选的模型需要选择平台模型')
    expect(validateRows([{ ...rows[0], formats: [] }])).toContain('格式')
  })

  it('leaves advanced settings at platform defaults when blank', () => {
    expect(advancedChangedCount(emptyAdvanced)).toBe(0)
    expect(advancedInput(emptyAdvanced)).toMatchObject({ user_agent: null, concurrency_limit: null, cooldown_seconds: null })
    const form = { ...emptyAdvanced, rpm: '60', cooldownFailures: '3', cooldownMinutes: '5' }
    expect(advancedChangedCount(form)).toBe(2)
    expect(advancedInput(form)).toMatchObject({ rpm_limit: 60, cooldown_failures: 3, cooldown_seconds: 300 })
  })
})
