import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { CallSummary, UsageReport } from '../api/types'
import {
  CallExplorer,
  CallFilterBar,
  emptyCallFilters,
  filtersToParams,
  summaryColumns,
  useUsage,
  userCallsPath,
  userCallsStreamPath,
  type CallFilterState,
} from '../calls'
import { useKeys } from '../keys/queries'
import { useModels } from '../models/queries'
import { formatPoints, formatTokens } from '../money/format'
import { BarChart, Card, EmptyState, MetricGrid, Metric, PageHeader, QueryBoundary, Segmented } from '../ui'

type GroupBy = Exclude<UsageReport['group_by'], 'channel'>

const groupOptions: Array<{ key: GroupBy; label: string }> = [
  { key: 'day', label: '按天' },
  { key: 'model', label: '按模型' },
  { key: 'key', label: '按 Key' },
  { key: 'tag', label: '按标签' },
]

function UsageSummary({ filters }: { filters: CallFilterState }) {
  const [groupBy, setGroupBy] = useState<GroupBy>('day')
  const base = filtersToParams(filters)
  const usage = useUsage({ group_by: groupBy, from: base.from, api_key_id: base.api_key_id, model: base.model, tag: base.tag })
  return (
    <Card actions={<Segmented label="分组" onChange={setGroupBy} options={groupOptions} value={groupBy} />} title="花费">
      <QueryBoundary errorFallback="用量加载失败" query={usage}>
        {(report) => (
          <div className="usage-summary">
            <MetricGrid label="汇总">
              <Metric label="花费" value={formatPoints(report.total.charged)} />
              <Metric hint={`成功 ${report.total.succeeded}`} label="调用" value={report.total.calls.toLocaleString('zh-CN')} />
              <Metric label="输入 tokens" value={formatTokens(report.total.input_tokens + report.total.cache_read_tokens + report.total.cache_write_tokens)} />
              <Metric label="输出 tokens" value={formatTokens(report.total.output_tokens)} />
            </MetricGrid>
            {report.items.length === 0 ? (
              <EmptyState title="这段时间没有调用" />
            ) : (
              <BarChart
                data={report.items.map((row) => ({
                  key: row.key,
                  label: groupBy === 'day' ? row.label.slice(5) : row.label || '（无标签）',
                  value: Number(row.charged),
                  display: `${formatPoints(row.charged)} · ${row.calls} 次`,
                }))}
                label={`按${groupOptions.find((option) => option.key === groupBy)?.label.slice(1)}花费`}
                orientation={groupBy === 'day' ? 'vertical' : 'horizontal'}
              />
            )}
          </div>
        )}
      </QueryBoundary>
    </Card>
  )
}

export function UsagePage() {
  const [search, setSearch] = useSearchParams()
  const [filters, setFilters] = useState<CallFilterState>(emptyCallFilters)
  const keys = useKeys()
  const models = useModels()
  const params = useMemo(() => filtersToParams(filters), [filters])
  const openCall = search.get('call')
  const setOpenCall = (id: string | null) => {
    const next = new URLSearchParams(search)
    if (id) next.set('call', id)
    else next.delete('call')
    setSearch(next, { replace: true })
  }
  return (
    <>
      <PageHeader title="用量" />
      <UsageSummary filters={filters} />
      <Card title="调用">
        <CallExplorer<CallSummary>
          caption="调用记录"
          columns={summaryColumns()}
          detailPath={userCallsPath}
          listPath={userCallsPath}
          onOpenCall={setOpenCall}
          openCallId={openCall}
          params={params}
          streamPath={userCallsStreamPath}
          toolbar={
            <CallFilterBar
              keys={keys.data?.items.map((key) => ({ id: key.id, name: key.name }))}
              models={models.data?.items.map((model) => model.id)}
              onChange={setFilters}
              onSearchId={setOpenCall}
              value={filters}
            />
          }
        />
      </Card>
    </>
  )
}
