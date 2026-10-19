import { useEffect, useState } from 'react'
import { Pager } from '../ui/Pager'
import { useAuth } from '../auth/AuthProvider'
import { Card, EmptyState, PageHeader, QueryBoundary, Segmented, SelectField, Toolbar } from '../ui'
import { enumField, useBack, useUrlState } from '../ui/urlState'
import { InboxList, InboxRefreshState } from './Components'
import { useInbox } from './InboxProvider'
import { categoryLabels, inboxRows, reminderCount } from './model'
const schema = { category: enumField(['action', 'anomaly', 'progress', 'all'] as const, 'action'), workspace: enumField(['all', 'member', 'admin'] as const, 'all'), source: enumField(['', 'trades', 'channels', 'keys', 'points', 'contributions', 'detection', 'reviews', 'governance'] as const, '') }
export function InboxPage() {
  const back = useBack('/home', '首页')
  const query = useInbox(), { account } = useAuth(), [filters, setFilters] = useUrlState(schema)
  const sources = query.data?.sources.filter(source => (filters.workspace === 'all' || source.workspace === filters.workspace) && (!filters.source || source.id === filters.source)) ?? []
  const options = (['action', 'anomaly', 'progress', 'all'] as const).map(key => ({ key, label: key === 'all' ? '全部' : `${categoryLabels[key]}${query.data ? `（${reminderCount(sources, key) ?? '?'}）` : ''}` }))
  const [visible, setVisible] = useState(50)
  useEffect(() => setVisible(50), [filters.category, filters.source, filters.workspace])
  const rows = inboxRows(sources).filter(item => filters.category === 'all' || item.category === filters.category)
  return <><PageHeader title="待办" back={back} /><Toolbar><Segmented label="待办类型" options={options} value={filters.category} onChange={category => setFilters({ category })} />
    {account?.is_admin && <SelectField label="工作区" value={filters.workspace} onChange={event => setFilters({ workspace: event.target.value as typeof filters.workspace })}><option value="all">全部</option><option value="member">成员</option><option value="admin">治理</option></SelectField>}
    <SelectField label="来源" value={filters.source} onChange={event => setFilters({ source: event.target.value as typeof filters.source })}><option value="">全部</option>{query.data?.sources.map(source => <option key={source.id} value={source.id}>{source.label}</option>)}</SelectField>
  </Toolbar>
    {query.data && <InboxRefreshState sources={sources} />}
    <QueryBoundary query={query} errorFallback="待办加载失败">{() => <Card flush>{rows.length ? <InboxList items={rows.slice(0, visible)} /> : sources.every(source => source.state === 'complete') ? <EmptyState title="当前没有这类待办" /> : <p className="inbox-incomplete">部分来源尚未读完整，请重试或进入来源页面。</p>}<Pager hasMore={visible < rows.length} loading={false} onMore={() => setVisible(current => current + 50)} /></Card>}</QueryBoundary>
  </>
}
