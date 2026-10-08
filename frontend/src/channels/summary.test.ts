import { describe, expect, it } from 'vitest'
import type { ChannelOffer } from '../api/types'
import { sumIncome, summarizeChannel } from './summary'

function offer(partial: Partial<ChannelOffer>): ChannelOffer {
  return {
    id: 'o',
    model_id: 'm',
    model_name: 'M',
    model_provider: 'P',
    protocol: 'openai_chat_completions',
    multiplier: '1',
    status: 'active',
    validation_version: 1,
    eligible: false,
    ineligible_reason: '',
    upstream_model_id: 'upstream',
    version: 1,
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    price_tiers: [],
    call_success_rate: null,
    ttft_milliseconds: null,
    tokens_per_second: null,
    call_count: null,
    provider_income: null,
    latest_validation: null,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...partial,
  }
}

describe('summarizeChannel', () => {
  it('counts live offers and weights the success rate by calls', () => {
    const summary = summarizeChannel({
      offers: [
        offer({ id: 'a', eligible: true, call_count: 3, call_success_rate: '1', provider_income: '0.000000001' }),
        offer({ id: 'b', status: 'disabled', call_count: 1, call_success_rate: '0', provider_income: '2.5' }),
        offer({ id: 'c', status: 'deleted', provider_income: '10' }),
      ],
    })
    expect(summary.offers).toHaveLength(2)
    expect(summary.enabledCount).toBe(1)
    expect(summary.eligibleCount).toBe(1)
    expect(summary.calls).toBe(4)
    expect(summary.successRate).toBe('0.7500')
    expect(summary.income).toBe('12.500000001')
    expect(summary.incomeIncludesDeleted).toBe(true)
  })

  it('reports no metrics when nothing was called or earned', () => {
    const summary = summarizeChannel({ offers: [offer({})] })
    expect(summary.calls).toBe(0)
    expect(summary.successRate).toBeNull()
    expect(summary.income).toBeNull()
  })
})

describe('sumIncome', () => {
  it('adds fixed-point values and ignores channels without income', () => {
    expect(sumIncome(['1.5', null, '0.25'])).toBe('1.75')
    expect(sumIncome([null])).toBeNull()
  })
})
