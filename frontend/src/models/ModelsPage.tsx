import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import type { CatalogModel, Format } from '../api/types'
import { FormatTags, formatLabels, formats } from '../calls'
import { formatPoints } from '../money/format'
import { Badge, EmptyState, PageHeader, QueryBoundary, SearchInput, Segmented, Toolbar } from '../ui'
import { contextText } from './pricing'
import { useModels } from './queries'

type FormatFilter = Format | 'all'

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
          <dt>最低现价 入/出</dt>
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

export function filterModels(models: CatalogModel[], query: string, format: FormatFilter) {
  const needle = query.trim().toLowerCase()
  return models.filter(
    (model) =>
      (format === 'all' || model.formats.includes(format)) &&
      (!needle ||
        model.id.toLowerCase().includes(needle) ||
        model.display_name.toLowerCase().includes(needle) ||
        model.provider.toLowerCase().includes(needle)),
  )
}

export function ModelsPage() {
  const models = useModels()
  const [query, setQuery] = useState('')
  const [format, setFormat] = useState<FormatFilter>('all')
  const options = useMemo(
    () => [{ key: 'all' as FormatFilter, label: '全部' }, ...formats.map((key) => ({ key: key as FormatFilter, label: formatLabels[key] }))],
    [],
  )
  return (
    <>
      <PageHeader title="模型" />
      <Toolbar>
        <SearchInput label="搜索模型" onChange={(event) => setQuery(event.target.value)} placeholder="搜索模型" value={query} />
        <Segmented label="按格式筛选" onChange={setFormat} options={options} value={format} />
      </Toolbar>
      <QueryBoundary errorFallback="模型列表加载失败" query={models}>
        {(data) => {
          const rows = filterModels(data.items, query, format)
          if (rows.length === 0) return <EmptyState title={data.items.length === 0 ? '暂无模型' : '没有匹配的模型'} />
          return (
            <div className="model-grid">
              {rows.map((model) => (
                <ModelCard key={model.id} model={model} />
              ))}
            </div>
          )
        }}
      </QueryBoundary>
    </>
  )
}
