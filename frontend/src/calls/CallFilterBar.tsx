import { useState, type FormEvent } from 'react'
import { rangeFrom, type RangePreset } from '../money/time'
import { Button, Icon, Segmented } from '../ui'
import { formatLabels, formats, outcomeLabels, outcomeOptions } from './labels'
import type { CallListParams } from './queries'

export type CallFilterState = {
  stream?: string
  min_duration_ms?: string
  max_duration_ms?: string
  min_tokens?: string
  max_tokens?: string
  min_cost?: string
  max_cost?: string
  min_attempts?: string
  max_attempts?: string
  status_code?: string
  error_code?: string
  end_reason?: string
  delivery?: string
  usage_status?: string
  billed?: string
  from?: string
  to?: string
  channel_id?: string
  attempt_channel_id?: string

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

function toLocalInput(iso?: string) {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function isRequestId(value: string) {
  return uuidPattern.test(value)
}

export function filtersToParams(filters: CallFilterState, now = new Date()): CallListParams {
  return {
    from: filters.requestId ? undefined : filters.from || rangeFrom(filters.range, now),
    to: filters.requestId ? undefined : filters.to || (filters.range === 'all' ? undefined : now.toISOString()),
    stream: filters.stream === undefined || filters.stream === '' ? undefined : filters.stream === 'true',
    min_duration_ms: filters.min_duration_ms ? Number(filters.min_duration_ms) : undefined,
    max_duration_ms: filters.max_duration_ms ? Number(filters.max_duration_ms) : undefined,
    min_tokens: filters.min_tokens ? Number(filters.min_tokens) : undefined,
    max_tokens: filters.max_tokens ? Number(filters.max_tokens) : undefined,
    min_cost: filters.min_cost || undefined,
    max_cost: filters.max_cost || undefined,
    min_attempts: filters.min_attempts ? Number(filters.min_attempts) : undefined,
    max_attempts: filters.max_attempts ? Number(filters.max_attempts) : undefined,
    status_code: filters.status_code ? Number(filters.status_code) : undefined,
    error_code: filters.error_code || undefined,
    end_reason: filters.end_reason || undefined,
    delivery: filters.delivery || undefined,
    usage_status: filters.usage_status || undefined,
    billed: filters.billed === undefined || filters.billed === '' ? undefined : filters.billed === 'true',
    channel_id: filters.channel_id || undefined,
    attempt_channel_id: filters.attempt_channel_id || undefined,

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
      <select
        aria-label={label}
        className="input select-input"
        onChange={(event) => onChange(event.target.value)}
        value={value}
      >
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
      <Segmented label="时间范围" onChange={(range) => set({ range,from:undefined,to:undefined })} options={rangeOptions} value={value.range} />
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
      <details className="call-advanced">
        <summary>高级筛选</summary>
        <div className="call-advanced-grid">
          <FilterSelect
            label="响应方式"
            value={value.stream ?? ''}
            onChange={(stream) => set({ stream })}
            options={[
              { value: 'true', label: '流式' },
              { value: 'false', label: '非流式' },
            ]}
          />
          <FilterSelect
            label="记账"
            value={value.billed ?? ''}
            onChange={(billed) => set({ billed })}
            options={[
              { value: 'true', label: '已记账' },
              { value: 'false', label: '未记账' },
            ]}
          />
          <FilterSelect
            label="交付"
            value={value.delivery ?? ''}
            onChange={(delivery) => set({ delivery })}
            options={[
              { value: 'complete', label: '完整' },
              { value: 'client_disconnected', label: '客户端断开' },
              { value: 'interrupted', label: '中断' },
              { value: 'not_started', label: '未开始' },
            ]}
          />
          <FilterSelect
            label="用量"
            value={value.usage_status ?? ''}
            onChange={(usage_status) => set({ usage_status })}
            options={[
              { value: 'reported', label: '已读到' },
              { value: 'missing', label: '未读到' },
              { value: 'parse_limit', label: '解析超限' },
              { value: 'not_observed', label: '未观察' },
            ]}
          />
          <label>
            最短耗时 ms
            <input
              className="input"
              aria-label="最短耗时 ms"
              type="number"
              min="0"
              step="1"
              value={value.min_duration_ms ?? ''}
              onChange={(e) => set({ min_duration_ms: e.target.value })}
            />
          </label>
          <label>
            最长耗时 ms
            <input
              className="input"
              aria-label="最长耗时 ms"
              type="number"
              min="0"
              step="1"
              value={value.max_duration_ms ?? ''}
              onChange={(e) => set({ max_duration_ms: e.target.value })}
            />
          </label>
          <label>
            最少 tokens
            <input
              className="input"
              aria-label="最少 tokens"
              type="number"
              min="0"
              step="1"
              value={value.min_tokens ?? ''}
              onChange={(e) => set({ min_tokens: e.target.value })}
            />
          </label>
          <label>
            最多 tokens
            <input
              className="input"
              aria-label="最多 tokens"
              type="number"
              min="0"
              step="1"
              value={value.max_tokens ?? ''}
              onChange={(e) => set({ max_tokens: e.target.value })}
            />
          </label>
          <label>
            最低费用
            <input
              className="input"
              aria-label="最低费用"
              type="number"
              min="0"
              step="any"
              value={value.min_cost ?? ''}
              onChange={(e) => set({ min_cost: e.target.value })}
            />
          </label>
          <label>
            最高费用
            <input
              className="input"
              aria-label="最高费用"
              type="number"
              min="0"
              step="any"
              value={value.max_cost ?? ''}
              onChange={(e) => set({ max_cost: e.target.value })}
            />
          </label>
          <label>
            最少尝试
            <input
              className="input"
              aria-label="最少尝试"
              type="number"
              min="0"
              step="1"
              value={value.min_attempts ?? ''}
              onChange={(e) => set({ min_attempts: e.target.value })}
            />
          </label>
          <label>
            最多尝试
            <input
              className="input"
              aria-label="最多尝试"
              type="number"
              min="0"
              step="1"
              value={value.max_attempts ?? ''}
              onChange={(e) => set({ max_attempts: e.target.value })}
            />
          </label>
          <label>
            上游状态码
            <input
              className="input"
              aria-label="上游状态码"
              type="number"
              min="0"
              step="1"
              value={value.status_code ?? ''}
              onChange={(e) => set({ status_code: e.target.value })}
            />
          </label>
          <label>
            上游错误码
            <input
              className="input"
              aria-label="上游错误码"
              type="text"
              min="0"
              step="1"
              value={value.error_code ?? ''}
              onChange={(e) => set({ error_code: e.target.value })}
            />
          </label>
          <label>
            尝试结束原因
            <input
              className="input"
              aria-label="尝试结束原因"
              type="text"
              min="0"
              step="1"
              value={value.end_reason ?? ''}
              onChange={(e) => set({ end_reason: e.target.value })}
            />
          </label>
          <label>
            开始时间
            <input
              className="input"
              aria-label="开始时间"
              type="datetime-local"
              value={toLocalInput(value.from)}
              onChange={(e) => set({ from: e.target.value ? new Date(e.target.value).toISOString() : '' })}
            />
          </label>
          <label>
            结束时间
            <input
              className="input"
              aria-label="结束时间"
              type="datetime-local"
              value={toLocalInput(value.to)}
              onChange={(e) => set({ to: e.target.value ? new Date(e.target.value).toISOString() : '' })}
            />
          </label>
          <Button variant="quiet" onClick={() => onChange({ ...emptyCallFilters })}>
            清除筛选
          </Button>
        </div>
      </details>
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
