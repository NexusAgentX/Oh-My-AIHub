import { useState, type FormEvent } from 'react'
import { rangeFrom, type RangePreset } from '../money/time'
import { Button, Icon, Segmented } from '../ui'
import { formatLabels, formats, outcomeLabels, outcomeOptions } from './labels'
import type { CallListParams } from './queries'

export type CallFilterState = {
  range: RangePreset
  model: string
  apiKeyId: string
  format: string
  outcome: string
  tag: string
  /** 请求 ID 精确匹配；设置后忽略时间范围 */
  requestId: string
}

export const emptyCallFilters: CallFilterState = {
  range: '7d',
  model: '',
  apiKeyId: '',
  format: '',
  outcome: '',
  tag: '',
  requestId: '',
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function isRequestId(value: string) {
  return uuidPattern.test(value)
}

export function filtersToParams(filters: CallFilterState, now = new Date()): CallListParams {
  return {
    from: filters.requestId ? undefined : rangeFrom(filters.range, now),
    request_id: filters.requestId || undefined,
    model: filters.model || undefined,
    api_key_id: filters.apiKeyId || undefined,
    format: filters.format || undefined,
    outcome: filters.outcome || undefined,
    tag: filters.tag || undefined,
  }
}

const rangeOptions: Array<{ key: RangePreset; label: string }> = [
  { key: 'today', label: '今天' },
  { key: '7d', label: '7 天' },
  { key: '30d', label: '30 天' },
  { key: 'all', label: '全部' },
]

function FilterSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  options: Array<{ value: string; label: string }>
}) {
  return (
    <label className="filter-select">
      <span className="visually-hidden">{label}</span>
      <select aria-label={label} className="input select-input" onChange={(event) => onChange(event.target.value)} value={value}>
        <option value="">{label}：全部</option>
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
    </label>
  )
}

/**
 * 调用筛选：时间范围、模型、Key、格式、结果、标签与请求 ID 精确查找。
 * models / keys 不传时隐藏对应筛选。
 */
export function CallFilterBar({
  value,
  onChange,
  models,
  keys,
}: {
  value: CallFilterState
  onChange: (value: CallFilterState) => void
  models?: string[]
  keys?: Array<{ id: string; name: string }>
}) {
  const [requestId, setRequestId] = useState(value.requestId)
  const [idError, setIdError] = useState('')
  const [tag, setTag] = useState(value.tag)
  const set = (patch: Partial<CallFilterState>) => onChange({ ...value, ...patch })
  const submitId = (event: FormEvent) => {
    event.preventDefault()
    const id = requestId.trim()
    if (id && !isRequestId(id)) {
      setIdError('请输入完整的请求 ID')
      return
    }
    setIdError('')
    set({ requestId: id })
  }
  const clearId = () => {
    setRequestId('')
    setIdError('')
    set({ requestId: '' })
  }
  return (
    <div className="call-filters">
      <Segmented label="时间范围" onChange={(range) => set({ range })} options={rangeOptions} value={value.range} />
      {models && (
        <FilterSelect
          label="模型"
          onChange={(model) => set({ model })}
          options={models.map((model) => ({ value: model, label: model }))}
          value={value.model}
        />
      )}
      {keys && (
        <FilterSelect
          label="Key"
          onChange={(apiKeyId) => set({ apiKeyId })}
          options={keys.map((key) => ({ value: key.id, label: key.name }))}
          value={value.apiKeyId}
        />
      )}
      <FilterSelect
        label="格式"
        onChange={(format) => set({ format })}
        options={formats.map((format) => ({ value: format, label: formatLabels[format] }))}
        value={value.format}
      />
      <FilterSelect
        label="结果"
        onChange={(outcome) => set({ outcome })}
        options={outcomeOptions.map((outcome) => ({ value: outcome, label: outcomeLabels[outcome] }))}
        value={value.outcome}
      />
      <form
        className="filter-inline-form"
        onSubmit={(event) => {
          event.preventDefault()
          set({ tag: tag.trim() })
        }}
      >
        <input
          aria-label="标签"
          className="input"
          onBlur={() => tag.trim() !== value.tag && set({ tag: tag.trim() })}
          onChange={(event) => setTag(event.target.value)}
          placeholder="标签"
          value={tag}
        />
      </form>
      <form className="filter-inline-form filter-id-form" onSubmit={submitId}>
        <input
          aria-describedby={idError ? 'call-request-id-error' : undefined}
          aria-invalid={idError ? true : undefined}
          aria-label="请求 ID"
          className="input mono"
          onChange={(event) => setRequestId(event.target.value)}
          placeholder="请求 ID"
          value={requestId}
        />
        <Button icon={<Icon name="search" />} type="submit" variant="secondary">
          查找
        </Button>
        {value.requestId && (
          <Button onClick={clearId} type="button" variant="quiet">
            清除
          </Button>
        )}
        {idError && (
          <span className="field-error" id="call-request-id-error" role="alert">
            {idError}
          </span>
        )}
      </form>
    </div>
  )
}
