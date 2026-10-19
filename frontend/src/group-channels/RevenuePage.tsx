import { useQuery, keepPreviousData } from '@tanstack/react-query'
import { Link, useLocation } from 'react-router-dom'
import { apiGet, type QueryParams } from '../api/client'
import type { ResponseBody } from '../api/types'
import { usePageVisible } from '../api/visibility'
import { DomainNav } from '../layouts/DomainNav'
import { formatPoints } from '../money/format'
import { rangeFrom } from '../money/time'
import { Card, DataTable, EmptyState, Metric, MetricGrid, PageHeader, QueryBoundary, Segmented, type Column } from '../ui'
import { CommittedInput } from '../ui/CommittedInput'
import { useShanghaiDay } from '../ui/clock'
import { enumField, stringField, useUrlState } from '../ui/urlState'

export function useRevenue(params: QueryParams, enabled = true) {
  const visible = usePageVisible()
  return useQuery({ queryKey: ['revenue', params], queryFn: () => apiGet<'getUsage'>('/api/usage', { ...params, view: 'revenue' }), enabled, placeholderData: keepPreviousData, refetchOnWindowFocus: true, refetchInterval: visible ? 60_000 : false })
}
const schema = { range: enumField(['today', '7d', '30d'] as const, '30d'), group_by: enumField(['day', 'model', 'channel'] as const, 'day'), channel_id: stringField(), model: stringField() }
type Row = ResponseBody<'getUsage'>['items'][number]
export function RevenuePage() {
  useShanghaiDay()
  const location = useLocation()
  const [filters, update] = useUrlState(schema)
  const revenue = useRevenue({ from: rangeFrom(filters.range), group_by: filters.group_by, channel_id: filters.channel_id || undefined, model: filters.model || undefined })
  const columns: Column<Row>[] = [{ key: 'key', header: filters.group_by === 'day' ? '日期' : filters.group_by === 'model' ? '模型' : '渠道', primary: true, cell: row => filters.group_by === 'channel' ? <Link to={`/supply/channels/${row.key}/calls?range=${filters.range}`} state={{ from: { to: location.pathname + location.search, label: '渠道收入' } }}>{row.label || row.key}</Link> : row.label || row.key }, { key: 'calls', header: '调用', numeric: true, cell: row => row.calls }, { key: 'success', header: '成功', numeric: true, cell: row => row.succeeded }, { key: 'revenue', header: '收入（积分）', numeric: true, cell: row => formatPoints(row.charged) }]
  return <><PageHeader title="渠道收入" /><DomainNav domain="供应" /><div className="call-filters"><Segmented label="时间范围" value={filters.range} onChange={range => update({ range })} options={[{ key: 'today', label: '今天' }, { key: '7d', label: '7 天' }, { key: '30d', label: '30 天' }]} /><Segmented label="汇总方式" value={filters.group_by} onChange={group_by => update({ group_by })} options={[{ key: 'day', label: '按天' }, { key: 'model', label: '按模型' }, { key: 'channel', label: '按渠道' }]} /><CommittedInput className="input" aria-label="渠道 ID" placeholder="渠道 ID" value={filters.channel_id} onCommit={channel_id => update({ channel_id })} /><CommittedInput className="input" aria-label="模型" placeholder="模型" value={filters.model} onCommit={model => update({ model })} /></div><QueryBoundary query={revenue} errorFallback="收入加载失败">{data => <div className="revenue-results" aria-busy={revenue.isPlaceholderData}><MetricGrid><Metric label="收入（积分）" value={formatPoints(data.total.charged)} /><Metric label="调用" value={data.total.calls} /><Metric label="成功" value={data.total.succeeded} /></MetricGrid><Card title="收入明细"><div aria-busy={revenue.isPlaceholderData}><DataTable caption="渠道收入" rows={data.items} columns={columns} rowKey={row => row.key} empty={<EmptyState title="当前范围没有收入记录" />} /></div></Card></div>}</QueryBoundary></>
}
