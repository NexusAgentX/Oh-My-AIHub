import type { CatalogModel, Format } from '../api/types'
import { parseNanoPoints } from '../money/amount'

export const capabilities = [
  { key: 'supports_tools', label: '工具' },
  { key: 'supports_structured_output', label: '结构化输出' },
  { key: 'supports_vision', label: '视觉' },
] as const
export type ModelFilters = {
  query: string
  format: Format | 'all'
  onlineOnly: boolean
  provider: string
  supports_tools: boolean
  supports_structured_output: boolean
  supports_vision: boolean
  minContext: string
  maxInput: string
  maxOutput: string
}
export const defaultFilters: ModelFilters = {
  query: '', format: 'all', onlineOnly: true, provider: '',
  supports_tools: false, supports_structured_output: false, supports_vision: false,
  minContext: '', maxInput: '', maxOutput: '',
}

export function priceFilterError(value: string) {
  if (value.trim() === '') return undefined
  return /^\d+(?:\.\d{1,9})?$/.test(value.trim()) ? undefined : '请输入非负价格，最多 9 位小数'
}
export function contextFilterError(value: string) {
  if (value.trim() === '') return undefined
  return /^\d+$/.test(value.trim()) && Number.isSafeInteger(Number(value))
    ? undefined : '请输入非负整数 token 数'
}
export function filterModels(models: CatalogModel[], filters: ModelFilters) {
  if (contextFilterError(filters.minContext) || priceFilterError(filters.maxInput) || priceFilterError(filters.maxOutput)) return []
  const needle = filters.query.trim().toLowerCase()
  const minContext = filters.minContext.trim() === '' ? null : Number(filters.minContext)
  const maxInput = filters.maxInput.trim() === '' ? null : parseNanoPoints(filters.maxInput)
  const maxOutput = filters.maxOutput.trim() === '' ? null : parseNanoPoints(filters.maxOutput)
  return models.filter((model) =>
    (!filters.onlineOnly || model.online_channels > 0) &&
    (filters.format === 'all' || model.formats.includes(filters.format)) &&
    (!filters.provider || model.provider === filters.provider) &&
    capabilities.every(({ key }) => !filters[key] || model[key]) &&
    (minContext === null || (model.context_window !== null && model.context_window >= minContext)) &&
    (maxInput === null || (model.lowest_prices !== null && parseNanoPoints(model.lowest_prices.input) <= maxInput)) &&
    (maxOutput === null || (model.lowest_prices !== null && parseNanoPoints(model.lowest_prices.output) <= maxOutput)) &&
    (!needle || [model.id, model.display_name, model.provider].some((value) => value.toLowerCase().includes(needle))),
  )
}
