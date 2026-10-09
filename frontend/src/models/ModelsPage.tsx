import { useState } from 'react'
import { Link } from 'react-router-dom'
import type { CatalogModel } from '../api/types'
import { FormatTags, formatLabels, formats } from '../calls'
import { formatPoints } from '../money/format'
import { Badge, Button, Checkbox, Disclosure, EmptyState, PageHeader, QueryBoundary, SearchInput, Segmented, SelectField, Switch, TextField, Toolbar } from '../ui'
import { contextText } from './pricing'
import { useModels } from './queries'

import { capabilities, contextFilterError, defaultFilters, filterModels, priceFilterError, type ModelFilters } from './filters'

function ModelCard({ model }: { model: CatalogModel }) {
  const context = contextText(model.context_window)
  return (
    <Link className="model-card" to={`/models/${encodeURIComponent(model.id)}`}>
      <header>
        <strong>{model.display_name || model.id}</strong>
        {model.current_tier && <Badge tone="accent">{model.current_tier.name}</Badge>}
      </header>
      <span className="model-card-id mono">{model.id}</span>
      <span className="model-card-meta">
        {[model.provider, context && `上下文 ${context}`].filter(Boolean).join(' · ')}
      </span>
      <span className="model-card-caps">
        {model.supports_tools && <Badge>工具</Badge>}
        {model.supports_structured_output && <Badge>结构化</Badge>}
        {model.supports_vision && <Badge>视觉</Badge>}
      </span>
      <dl className="model-card-prices">
        <div>
          <dt>最低参考价 入/出</dt>
          <dd className="num">
            {model.lowest_prices
              ? `${formatPoints(model.lowest_prices.input, { digits: 2 })} / ${formatPoints(model.lowest_prices.output, { digits: 2 })}`
              : '—'}
          </dd>
        </div>
        <div>
          <dt>在线渠道</dt>
          <dd className="num">{model.online_channels}</dd>
        </div>
      </dl>
      <FormatTags formats={model.formats} />
    </Link>
  )
}

const formatOptions = [
  { key: 'all' as const, label: '全部' },
  ...formats.map((key) => ({ key, label: formatLabels[key] })),
]

export function ModelsPage() {
  const models = useModels()
  const [filters, setFilters] = useState<ModelFilters>(defaultFilters)
  const update = <K extends keyof ModelFilters>(key: K, value: ModelFilters[K]) =>
    setFilters((current) => ({ ...current, [key]: value }))
  const advancedCount = [filters.provider, filters.minContext, filters.maxInput, filters.maxOutput,
    ...capabilities.map(({ key }) => filters[key])].filter(Boolean).length
  const active = advancedCount > 0 || filters.query !== '' || filters.format !== 'all' || filters.onlineOnly
  const invalid = Boolean(contextFilterError(filters.minContext) || priceFilterError(filters.maxInput) || priceFilterError(filters.maxOutput))
  const providers = [...new Set((models.data?.items ?? []).map((model) => model.provider).filter(Boolean))].sort((a, b) => a.localeCompare(b))
  return (
    <>
      <PageHeader title="模型" />
      <Toolbar>
        <SearchInput label="搜索模型" onChange={(event) => update('query', event.target.value)} placeholder="搜索模型" value={filters.query} />
        <Segmented label="按格式筛选" onChange={(value) => update('format', value)} options={formatOptions} value={filters.format} />
        <Switch label="仅看有在线渠道" checked={filters.onlineOnly} onChange={(value) => update('onlineOnly', value)} />
      </Toolbar>
      <Disclosure title="更多筛选" changed={advancedCount}>
        <div className="model-filters">
          <SelectField label="提供商" value={filters.provider} onChange={(event) => update('provider', event.target.value)}>
            <option value="">全部提供商</option>
            {providers.map((provider) => <option key={provider} value={provider}>{provider}</option>)}
          </SelectField>
          <TextField label="最低上下文（token）" inputMode="numeric" placeholder="不限" value={filters.minContext}
            error={contextFilterError(filters.minContext)} onChange={(event) => update('minContext', event.target.value)} />
          <TextField label="输入参考价上限" inputMode="decimal" placeholder="不限" value={filters.maxInput}
            hint="积分 / 百万 token" error={priceFilterError(filters.maxInput)} onChange={(event) => update('maxInput', event.target.value)} />
          <TextField label="输出参考价上限" inputMode="decimal" placeholder="不限" value={filters.maxOutput}
            hint="积分 / 百万 token" error={priceFilterError(filters.maxOutput)} onChange={(event) => update('maxOutput', event.target.value)} />
        </div>
        <fieldset className="model-filter-capabilities">
          <legend>模型能力（同时满足）</legend>
          {capabilities.map(({ key, label }) => <Checkbox key={key} label={label} checked={filters[key]} onChange={(event) => update(key, event.target.checked)} />)}
        </fieldset>
      </Disclosure>
      <QueryBoundary errorFallback="模型列表加载失败" query={models}>
        {(data) => {
          const rows = filterModels(data.items, filters)
          return (
            <>
              <div className="model-filter-summary">
                <span role="status">{invalid ? '请修正筛选条件' : `显示 ${rows.length} / ${data.items.length} 个模型`}</span>
                <Button type="button" variant="quiet" size="sm" disabled={!active} onClick={() => setFilters({ ...defaultFilters, onlineOnly: false })}>清空筛选</Button>
              </div>
              {rows.length === 0
                ? <EmptyState title={invalid ? '筛选条件有误' : data.items.length === 0 ? '暂无模型' : '没有匹配的模型'} />
                : <div className="model-grid">{rows.map((model) => <ModelCard key={model.id} model={model} />)}</div>}
            </>
          )
        }}
      </QueryBoundary>
    </>
  )
}
