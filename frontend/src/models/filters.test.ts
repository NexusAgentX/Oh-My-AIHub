import { describe, expect, it } from 'vitest'
import type { CatalogModel } from '../api/types'
import { contextFilterError, defaultFilters, filterModels, priceFilterError } from './filters'

const model: CatalogModel = {
  id: 'gpt-demo', display_name: 'Demo Model', provider: 'OpenAI', context_window: 128000,
  input_modalities: [], output_modalities: [], supports_tools: true, supports_structured_output: true,
  supports_vision: false, parameter_info: '', current_tier: null,
  base_prices: { input: '1', output: '2', cache_read: '0', cache_write: '0' },
  lowest_prices: { input: '0.123456789', output: '2', cache_read: '0', cache_write: '0' },
  formats: ['openai_chat'], online_channels: 2,
}
const offline = { ...model, id: 'offline', online_channels: 0, lowest_prices: null, formats: [] }
const unknown = { ...model, id: 'unknown', context_window: null, supports_tools: false }
const items = [model, offline, unknown]

describe('model catalog filters', () => {
  it('preserves the catalog and order by default; online excludes zero channels and clearing restores all', () => {
    expect(filterModels(items, defaultFilters)).toEqual(items)
    expect(filterModels(items, { ...defaultFilters, onlineOnly: true })).toEqual([model, unknown])
    expect(filterModels(items, defaultFilters)).toEqual(items)
  })
  it('searches id, display name and provider without case or surrounding whitespace', () => {
    for (const query of [' GPT-DEMO ', 'DEMO MODEL', 'openai']) {
      expect(filterModels([model], { ...defaultFilters, query })).toEqual([model])
    }
    expect(filterModels(items, { ...defaultFilters, query: 'missing' })).toEqual([])
  })
  it('combines format, provider, capabilities, online, context and both price limits', () => {
    const filters = { ...defaultFilters, onlineOnly: true, format: 'openai_chat' as const,
      provider: 'OpenAI', supports_tools: true, supports_structured_output: true,
      minContext: '128000', maxInput: '0.123456789', maxOutput: '2' }
    expect(filterModels(items, filters)).toEqual([model])
    expect(filterModels(items, { ...filters, supports_vision: true })).toEqual([])
    expect(filterModels(items, { ...filters, provider: 'Other' })).toEqual([])
    expect(filterModels(items, { ...filters, format: 'gemini' })).toEqual([])
  })
  it('uses inclusive context limits and excludes unknown context even at zero', () => {
    expect(filterModels(items, { ...defaultFilters, minContext: '0' })).toEqual([model, offline])
    expect(filterModels(items, { ...defaultFilters, minContext: '128001' })).toEqual([])
  })
  it('compares exact nano prices and excludes missing prices, including zero limits', () => {
    expect(filterModels(items, { ...defaultFilters, maxInput: '0.123456788' })).toEqual([])
    expect(filterModels(items, { ...defaultFilters, maxOutput: '1.999999999' })).toEqual([])
    const free = { ...model, lowest_prices: { ...model.lowest_prices!, input: '0', output: '0' } }
    expect(filterModels([free, offline], { ...defaultFilters, maxInput: '0', maxOutput: '0' })).toEqual([free])
  })
  it('rejects invalid conditions explicitly rather than silently applying them', () => {
    for (const value of ['-1', 'NaN', '1e3', '0.1234567890', '1.']) expect(priceFilterError(value)).toBeTruthy()
    for (const value of ['-1', '1.5', 'Infinity', '9007199254740992']) expect(contextFilterError(value)).toBeTruthy()
    expect(priceFilterError(' 0.000000001 ')).toBeUndefined()
    expect(contextFilterError(' 128000 ')).toBeUndefined()
    expect(filterModels(items, { ...defaultFilters, maxInput: '-1' })).toEqual([])
    expect(filterModels(items, { ...defaultFilters, minContext: '1.5' })).toEqual([])
    expect(filterModels(items, { ...defaultFilters, maxInput: ' ', minContext: ' ' })).toEqual(items)
  })
})
