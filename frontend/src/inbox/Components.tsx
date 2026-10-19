import { errorMessage } from '../api/query'
import { Link, useLocation } from 'react-router-dom'
import { Badge, Button, ButtonLink, Card, CountBadge, Icon, InlineError, LoadingState } from '../ui'
import { formatDateTime } from '../money/time'
import { useCountdown } from '../ui/countdown'
import { useInbox } from './InboxProvider'
import { categoryLabels, homeActions, reminderCount, type InboxItem, type InboxSource } from './model'
import './inbox.css'

export function ReminderCount({ sources, category, stale = false }: { sources: InboxSource[]; category?: InboxItem['category']; stale?: boolean }) {
  const count = reminderCount(sources, category)
  if (count === 0 && !stale) return null
  return <span title={stale ? '刷新失败，显示上次读取状态' : count === undefined ? '部分来源未读完整，数量未知' : undefined}><CountBadge>{stale || count === undefined ? '?' : count}</CountBadge></span>
}
export function InboxBell() {
  const location = useLocation()
  const query = useInbox(), count = query.data ? reminderCount(query.data.sources) : undefined
  const label = query.isError || count === undefined ? '待办，数量未完整读取' : `待办，${count} 条提醒`
  return <Link className="inbox-bell" to="/inbox" state={{ from: { to: location.pathname + location.search, label: '上一页' } }} aria-label={label}><Icon name="bell" />{query.data ? <ReminderCount sources={query.data.sources} stale={query.isError} /> : <span className="inbox-unknown" aria-hidden="true">{query.isPending ? '…' : '?'}</span>}</Link>
}
export function InboxNavCount({ to }: { to: string }) {
  const query = useInbox()
  if (!query.data) return null
  const all = query.data.sources
  const map: Record<string, string[]> = { '/home': ['trades', 'channels', 'keys', 'points', 'contributions', 'detection'], '/points/trades': ['trades'], '/supply/channels': ['channels'], '/supply/contributions': ['contributions'], '/admin': ['reviews', 'governance'], '/admin/contributions': ['reviews'] }
  if (!map[to]) return null
  return <ReminderCount sources={all.filter(source => map[to].includes(source.id))} stale={query.isError} />
}
function Deadline({ value }: { value: string }) {
  const seconds = useCountdown(value), minutes = Math.floor(seconds / 60), rest = Math.floor(seconds % 60)
  return <Badge tone={seconds <= 300 ? 'danger' : 'warning'}>{seconds > 0 ? `${minutes}:${String(rest).padStart(2, '0')} 内付款` : '已到期，正在更新'}</Badge>
}
export function InboxList({ items }: { items: (InboxItem & { sourceID: string; sourceLabel: string; workspace: string })[] }) {
  const location = useLocation(), label = location.pathname === '/home' ? '首页' : location.pathname === '/admin' ? '治理待处理' : '待办'
  return <ul className="inbox-list">{items.map(item => <li key={`${item.sourceID}:${item.id}`}>
    <div className="inbox-item-copy"><div className="inbox-item-title"><Badge tone={item.category === 'action' ? 'warning' : item.category === 'anomaly' ? 'danger' : 'info'}>{categoryLabels[item.category]}</Badge><strong>{item.title}</strong>{item.deadline && <Deadline value={item.deadline} />}</div><p>{item.message}</p><small className="muted-copy">{item.workspace === 'admin' ? '治理 · ' : ''}{item.sourceLabel}</small></div>
    <ButtonLink variant="secondary" size="sm" to={item.href} state={{ from: { to: location.pathname + location.search, label } }}>{item.category === 'action' ? '去处理' : item.category === 'anomaly' ? '查看问题' : '查看进度'}</ButtonLink>
  </li>)}</ul>
}
export function InboxRefreshState({ sources }: { sources?: InboxSource[] }) {
  const query = useInbox(), failed = sources?.filter(source => source.state !== 'complete') ?? []
  return <div className="inbox-refresh-state">
    {query.isError && <InlineError>{errorMessage(query.error, '待办刷新失败，请重试')}{query.data ? '，以下保留上次读取的内容。' : '，当前数量未知。'}</InlineError>}
    {failed.length > 0 && <p role="status">以下来源{failed.some(source => source.state === 'limited') ? '未读完整' : '暂不可用'}，数量未知：{failed.map((source, index) => <span key={source.id}>{index > 0 && '、'}<Link to={source.href}>{source.label}</Link></span>)}。</p>}
    <div className="inbox-refresh-actions">{query.data && <small className="muted-copy">读取于 {formatDateTime(query.data.generated_at)}</small>}<Button size="sm" variant="quiet" loading={query.isFetching} onClick={() => void query.refetch()}>刷新待办</Button></div>
  </div>
}
export function HomeActions() {
  const query = useInbox(), items = homeActions(query.data?.sources ?? [])
  if (query.isPending) return <Card title="需要留意"><LoadingState label="正在读取待办" /></Card>
  return <Card className="home-actions" title="需要留意" actions={<ButtonLink to="/inbox" size="sm" variant="quiet">全部待办</ButtonLink>}>
    {items.length ? <InboxList items={items.slice(0, 5)} /> : query.data && query.data.sources.every(source => source.state === 'complete') && !query.isError ? <p className="muted-copy">暂无有时限的操作或阻断问题。</p> : null}
    {items.length > 5 && <p className="muted-copy">另有 {items.length - 5} 条，请查看全部待办。</p>}
    {(query.isError || !query.data || query.data.sources.some(source => source.state !== 'complete')) && <InboxRefreshState sources={query.data?.sources} />}
  </Card>
}
