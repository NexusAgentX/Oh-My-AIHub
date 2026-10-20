import { useEffect, useMemo, useState } from 'react'
import { usePageVisible } from '../api/visibility'
import { shanghaiDate } from '../money/time'
import { enumField, stringField, useUrlState } from '../ui/urlState'
import { emptyCallFilters, filtersToParams, type CallFilterState } from './CallFilterBar'

const aliases = { apiKeyId: 'api_key_id', requestId: 'request_id' } as const
const optional = ['stream', 'min_duration_ms', 'max_duration_ms', 'min_tokens', 'max_tokens', 'min_cost', 'max_cost',
  'min_attempts', 'max_attempts', 'status_code', 'error_code', 'end_reason', 'delivery', 'usage_status', 'billed',
  'from', 'to', 'channel_id', 'attempt_channel_id'] as const
export const callFilterSchema = {
  ...Object.fromEntries(optional.map(key => [key, stringField()])),
  range: enumField(['today', '7d', '30d', 'all'] as const, '7d'),
  model: stringField(), api_key_id: stringField(), request_id: stringField(),
  format: stringField(), outcome: stringField(), tag: stringField(), account_id: stringField(),
  dimension: stringField(), dimension_value: stringField(), second_dimension: stringField(), second_value: stringField(),
}

export function readCallFilters(search: URLSearchParams): CallFilterState {
  const value = { ...emptyCallFilters }
  for (const key of optional) if (search.has(key)) value[key] = search.get(key) ?? ''
  for (const key of ['model', 'format', 'outcome', 'tag'] as const) value[key] = search.get(key) ?? ''
  for (const [key, name] of Object.entries(aliases)) value[key as keyof typeof aliases] = search.get(name) ?? ''
  const range = search.get('range')
  if (range === 'today' || range === '30d' || range === 'all') value.range = range
  return value
}

export function useCallFilters() {
  const [url, update] = useUrlState(callFilterSchema)
  const visible = usePageVisible()
  const [day, setDay] = useState(() => shanghaiDate(new Date()))
  useEffect(() => {
    if (!visible) return
    const refresh = () => setDay(shanghaiDate(new Date()))
    refresh()
    const timer = window.setInterval(refresh, 60_000)
    window.addEventListener('focus', refresh)
    return () => { window.clearInterval(timer); window.removeEventListener('focus', refresh) }
  }, [visible])
  const filters = useMemo(() => readCallFilters(new URLSearchParams(Object.entries(url))), [url])
  const setFilters = (next: CallFilterState) => {
    const patch: Record<string, string> = {}
    for (const [key, value] of Object.entries(next)) patch[aliases[key as keyof typeof aliases] ?? key] = value ?? ''
    update(patch)
  }
  const clear = () => update(Object.fromEntries(Object.entries(callFilterSchema).map(([key, field]) => [key, field.defaultValue])))
  // 相对范围不向列表发送固定结束时刻；补拉与实时条目始终包含新调用。
  const params = useMemo(() => ({ ...filtersToParams(filters, new Date(`${day}T12:00:00+08:00`)),
    to: filters.requestId ? undefined : filters.to || undefined, account_id: url.account_id || undefined,
    dimension: url.dimension || undefined, dimension_value: url.dimension ? url.dimension_value : undefined,
    second_dimension: url.second_dimension || undefined, second_value: url.second_dimension ? url.second_value : undefined,
  }), [filters, url, day])
  return { filters, setFilters, params, url, update, clear }
}
