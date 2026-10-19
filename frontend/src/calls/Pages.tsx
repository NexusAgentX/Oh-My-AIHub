import { useMemo, useState } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import { useMarketNames } from '../market/queries'
import { useKeys } from '../keys/queries'
import { Button, Card, PageHeader, QueryBoundary } from '../ui'
import { CallSectionNav } from './CallSectionNav'
import { enumField, stringField, useBack, useUrlState } from '../ui/urlState'
import { CallAnalysis } from './CallAnalysis'
import { CallDetailView } from './CallDetail'
import { CallFilterBar } from './CallFilterBar'
import { useCall } from './queries'
import { useCallFilters } from './urlFilters'

const analysisSchema = {
  category: enumField(['reliability', 'latency', 'streaming', 'tokens', 'cost'] as const, 'reliability'),
  metric: stringField('calls'),
  group_by: enumField(['model', 'channel', 'format', 'stream', 'key', 'tag', 'account', 'outcome', 'error', 'error_code', 'status', 'stop', 'service_tier'] as const, 'model'),
  split_by: enumField(['all', 'model', 'channel', 'format', 'stream', 'key', 'tag', 'account', 'outcome', 'error', 'error_code', 'status', 'stop', 'service_tier'] as const, 'all'),
  bucket_seconds: enumField(['0', '60', '300', '900', '3600', '21600', '86400'] as const, '0'),
}

export function CallInsightsPage({ admin = false }: { admin?: boolean }) {
  const { filters, setFilters, params, clear } = useCallFilters()
  const [options, update] = useUrlState(analysisSchema)
  const [now, setNow] = useState(() => new Date().toISOString())
  const analysisParams = useMemo(() => ({ ...params, to: params.to || new Date(Math.max(Date.now(), Date.parse(now))).toISOString() }), [params, now])
  const models = useMarketNames()
  const keys = useKeys()
  const location = useLocation()
  const navigate = useNavigate()
  const base = admin ? '/admin/calls' : '/calls'
  const source = { to: `${location.pathname}${location.search}`, label: '调用分析' }
  return <>
    <PageHeader title="调用分析" actions={<Button variant="secondary" onClick={() => setNow(new Date().toISOString())}>刷新分析</Button>} />
    <CallSectionNav admin={admin} />
    <Card>
      <CallFilterBar onClear={clear} value={filters} onChange={setFilters} models={models.data?.items.map(model => model.id)}
        keys={!admin ? keys.data?.items.map(key => ({ id: key.id, name: key.name })) : undefined} />
      <CallAnalysis path={admin ? '/api/admin/calls' : '/api/calls'} params={analysisParams} live={false}
        options={options} onOptionsChange={patch => update(patch as Partial<typeof options>)}
        onDrill={drill => {
          const search = new URLSearchParams(location.search)
          for (const key of [...Object.keys(analysisSchema), 'call', 'dimension', 'dimension_value', 'second_dimension', 'second_value']) search.delete(key)
          for (const [key, value] of Object.entries(drill)) {
            if (value === undefined) search.delete(key)
            else search.set(key, String(value))
          }
          // 下钻固定在报告的时间范围，不把相对范围随下一次访问推进。
          if (!search.has('to')) search.set('to', analysisParams.to)
          if (params.from && !search.has('from')) search.set('from', params.from)
          navigate(`${base}?${search}`, { state: { from: source } })
        }} />
    </Card>
  </>
}
export function AdminCallInsightsPage() { return <CallInsightsPage admin /> }

export function CallPage() {
  const { id = '' } = useParams()
  const location = useLocation()
  const admin = location.pathname.startsWith('/admin/')
  const query = useCall(id)
  const back = useBack(admin ? '/admin/calls' : '/calls', '调用')
  return <><PageHeader title="调用详情" back={back} /><Card><QueryBoundary query={query} errorFallback="调用详情加载失败">
    {call => <CallDetailView call={call} onRefresh={query.refetch} />}
  </QueryBoundary></Card></>
}
