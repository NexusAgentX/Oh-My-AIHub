import { Link, useLocation, useParams } from 'react-router-dom'
import type { components } from '../api/schema.gen'
import { errorMessage } from '../api/query'
import { FormatTags } from '../calls'
import { MarkdownContent } from '../markdown/MarkdownContent'
import { DomainNav } from '../layouts/DomainNav'
import { Badge, Button, ButtonLink, Card, EmptyState, Icon, PageHeader, QueryBoundary, Toolbar } from '../ui'
import { CommittedInput } from '../ui/CommittedInput'
import { Pager } from '../ui/Pager'
import { UrlSearchInput } from '../ui/UrlSearchInput'
import { useBack } from '../ui/urlState'
import { marketDefaults, marketParams, useHallFilters } from './filters'
import { MarketQualitySection } from './Quality'
import { useMarketChannel, useMarketHall } from './queries'
import './Hall.css'

type Channel = components['schemas']['MarketHallChannel']
function useSource(label: string) {
  const location = useLocation()
  return { from: { to: location.pathname + location.search, label } }
}
function Parents({ channel }: { channel: Channel }) {
  const source = useSource(channel.name)
  return <div className="sharing-line"><Link state={source} to={`/suppliers/${channel.supplier_id}`}>{channel.supplier_name}</Link><span aria-hidden="true">/</span><Link state={source} to={`/groups/${channel.group_id}`}>{channel.group_name}</Link>{channel.is_mine && <Badge tone="accent">我的渠道</Badge>}</div>
}
function HallChannelCard({ channel }: { channel: Channel }) {
  const source = useSource('渠道大厅')
  return <Card className="hall-channel-card">
    <header className="hall-channel-heading"><h2><Link state={source} to={`/channel-hall/${channel.id}`}>{channel.name}</Link></h2><Parents channel={channel} /></header>
    {channel.summary && <p className="hall-channel-summary">{channel.summary}</p>}
    <footer className="hall-channel-footer"><div className="hall-channel-meta"><span><strong className="num">{channel.models.length}</strong> 个模型</span><FormatTags formats={channel.formats} /></div><ButtonLink variant="quiet" state={source} to={`/channel-hall/${channel.id}`}>查看详情<Icon name="chevron-right" size={16} /></ButtonLink></footer>
  </Card>
}
export function MarketHallPage() {
  const [filters, setFilters] = useHallFilters(), params = marketParams(filters, true), query = useMarketHall(params)
  const source = useSource('渠道大厅')
  const extra = [...params.entries()].filter(([key]) => key !== 'channel_q' && key !== 'model_id')
  const labels: Record<string, string> = { q: '模型搜索', supplier_id: '供应商', group_id: '分组', model_type: '模型类型', format: '调用格式', protocol: '协议', sort: '排序', features: '能力', min_context: '最低上下文', min_output: '最低输出', only_online: '仅在线' }
  return <><PageHeader title="渠道大厅" actions={<ButtonLink state={source} to={`/supply/channels/new${filters.group_id ? `?group=${encodeURIComponent(filters.group_id)}` : ''}`}>共享渠道</ButtonLink>} /><DomainNav domain="市场" />
    <div className="hall-toolbar"><Toolbar><UrlSearchInput label="搜索渠道" value={filters.channel_q} onCommit={channel_q => setFilters({ channel_q })} /><label className="field"><span className="field-label">模型 ID（精确）</span><CommittedInput className="input" value={filters.model_id} onCommit={value => setFilters({ model_id: value.trim() })} /></label></Toolbar></div>
    {extra.length > 0 && <div className="form-actions">{extra.map(([key, value]) => <Button key={key} variant="quiet" size="sm" onClick={() => setFilters({ [key]: marketDefaults[key as keyof typeof marketDefaults] })}>{labels[key] ?? key}：{value === 'true' ? '是' : value} ×</Button>)}</div>}
    {params.size > 0 && <Button variant="quiet" onClick={() => setFilters({ ...marketDefaults, channel_q: '' })}>清空筛选</Button>}
    <QueryBoundary query={query} errorFallback="渠道大厅加载失败">{data => <div aria-busy={query.isFetching} className={query.isPlaceholderData ? 'market-updating' : undefined}><p className="muted-copy" role="status">共 {data.total} 个渠道{query.isPlaceholderData && ' · 正在更新…'}</p>{data.items.length ? <div className="sharing-grid">{data.items.map(channel => <HallChannelCard key={channel.id} channel={channel} />)}</div> : <EmptyState title="暂无符合条件的渠道" />}<Pager hasMore={query.hasNextPage || query.limited} limited={query.limited} loading={query.isFetchingNextPage} error={query.isFetchNextPageError ? errorMessage(query.error, '大厅已变化或加载失败，请重试') : undefined} onMore={() => void query.fetchNextPage()} />{query.isFetchNextPageError && <Button variant="quiet" onClick={() => void query.refetch()}>刷新大厅</Button>}</div>}</QueryBoundary>
  </>
}
export function MarketHallChannelPage() {
  const { id = '' } = useParams(), query = useMarketChannel(id), back = useBack('/channel-hall', '渠道大厅'), source = useSource(query.data?.name ?? '渠道')
  return <><PageHeader title={query.data?.name ?? '渠道详情'} back={back} actions={query.data?.is_mine ? <ButtonLink state={source} to={`/supply/channels/${id}`}>管理渠道</ButtonLink> : undefined} /><QueryBoundary query={query} errorFallback="渠道不存在或已下架">{channel => <><Card><Parents channel={channel} /><p>{channel.summary}</p><MarkdownContent value={channel.description} /><FormatTags formats={channel.formats} /></Card><ButtonLink state={source} to={`/supply/channels/new?group=${encodeURIComponent(channel.group_id)}`}>用此分组共享渠道</ButtonLink><Card title="分组模型">{channel.models.map(model => <div className="market-channel" key={model}><Link state={source} className="mono" to={`/models/${encodeURIComponent(model)}?group_id=${encodeURIComponent(channel.group_id)}`}>{model}</Link></div>)}</Card><MarketQualitySection key={channel.id} kind="channel" id={channel.id} /></>}</QueryBoundary></>
}
