import { describe, expect, it } from 'vitest'
import {
  filtersFromParams,
  hasActiveFilters,
  paramsFromFilters,
} from './MarketPage'

describe('market filters', () => {
  it('reads model, protocol, owner and sort from the URL', () => {
    const filters = filtersFromParams(
      new URLSearchParams('model=openai%2Fgpt-5&protocol=openai_responses&owner=ann&sort=rating'),
    )
    expect(filters).toEqual({
      modelID: 'openai/gpt-5',
      protocol: 'openai_responses',
      owner: 'ann',
      sort: 'rating',
    })
    expect(hasActiveFilters(filters)).toBe(true)
  })

  it('ignores unknown protocol and sort values', () => {
    const filters = filtersFromParams(new URLSearchParams('protocol=grpc&sort=hype'))
    expect(filters).toEqual({ modelID: '', protocol: '', owner: '', sort: 'input_price' })
    expect(hasActiveFilters(filters)).toBe(false)
  })

  it('omits defaults when writing filters back so clearing yields a clean URL', () => {
    const base = { modelID: '', protocol: '' as const, owner: '', sort: 'input_price' as const }
    expect(paramsFromFilters(base).toString()).toBe('')
    expect(paramsFromFilters({ ...base, sort: 'tps' }).toString()).toBe('sort=tps')
    expect(hasActiveFilters({ ...base, sort: 'tps' })).toBe(false)
  })
})
