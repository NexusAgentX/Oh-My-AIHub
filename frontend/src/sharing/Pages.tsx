import { useState } from 'react'
import { formats, formatLabels } from '../calls'
import { useParams, useSearchParams } from 'react-router-dom'
import { MarkdownContent } from '../markdown/MarkdownContent'
import { ModelCatalog } from '../models/ModelCatalog'
import { shortMultiplier } from '../money/MultiplierText'
import { formatMultiplier, formatPoints } from '../money/format'
import { Badge, Button, ButtonLink, Card, EmptyState, PageHeader, QueryBoundary, SelectField, Tabs, TextField } from '../ui'
import { ChannelCard, ChannelMetrics, ChannelState, SharingNav, SupplierCard, SupplierIcon } from './components'
import { QualityStats } from './QualityStats'
import { useHall, useHallChannel, useSupplier, useSuppliers } from './queries'

export function ChannelHallPage() {
  const [params, setParams] = useSearchParams()
  const filters = Object.fromEntries(['q', 'supplier', 'model', 'format'].map((key) => [key, params.get(key) ?? '']))
  const query = useHall(filters)
  const suppliers = useSuppliers()
  const set = (key: string, value: string) => setParams((current) => { const next = new URLSearchParams(current); if (value) next.set(key, value); else next.delete(key); return next }, { replace: true })
  return <><PageHeader title="渠道大厅" actions={<ButtonLink to="/channels/new">共享渠道</ButtonLink>} /><SharingNav />
    <div className="hall-filters"><TextField label="渠道" type="search" placeholder="搜索渠道或渠道主" value={filters.q} onChange={(e) => set('q', e.target.value)} />
      <SelectField label="供应商" value={filters.supplier} onChange={(e) => set('supplier', e.target.value)}><option value="">全部</option>{suppliers.data?.map((supplier) => <option key={supplier.id} value={supplier.id}>{supplier.name}</option>)}</SelectField>
      <TextField label="模型" type="search" placeholder="模型名称（精确匹配）" value={filters.model} onChange={(e) => set('model', e.target.value)} />
      <SelectField label="格式" value={filters.format} onChange={(e) => set('format', e.target.value)}><option value="">全部</option>{formats.map((format) => <option key={format} value={format}>{formatLabels[format]}</option>)}</SelectField>
    </div>
      {Object.values(filters).some(Boolean) && <Button variant="quiet" onClick={() => setParams({})}>清空</Button>}
    <QueryBoundary query={query} errorFallback="渠道大厅加载失败">{(data) => <>{data.pages.some((page) => page.items.length) ? <div className="sharing-grid">{data.pages.flatMap((page) => page.items).map((channel) => <ChannelCard channel={channel} key={channel.id} />)}</div> : <EmptyState title="暂无符合条件的渠道" />}{query.hasNextPage && <Button variant="secondary" loading={query.isFetchingNextPage} onClick={() => query.fetchNextPage()}>加载更多</Button>}</>}</QueryBoundary>
  </>
}
export function HallChannelPage() {
  const { id = '' } = useParams()
  const query = useHallChannel(id)
  return <QueryBoundary key={id} query={query} errorFallback="渠道不存在或已下架">{(channel) => <>
    <PageHeader title={channel.name} back={{ to: '/channel-hall', label: '渠道大厅' }} actions={channel.is_mine ? <ButtonLink to={`/channels/${channel.id}`} variant="secondary">管理渠道</ButtonLink> : undefined} />
    <Card><div className="sharing-line"><ChannelState channel={channel} /><span>由 {channel.owner.display_name} 提供</span>{channel.is_mine && <Badge tone="accent">我的渠道</Badge>}</div>{channel.summary && <p>{channel.summary}</p>}<ChannelMetrics channel={channel} /></Card>
    {channel.supplier ? <SupplierCard supplier={channel.supplier} /> : <p className="muted-copy">未识别供应商</p>}
    <QualityStats key={channel.id} path={`/api/channel-hall/${channel.id}`} />
    <section aria-label="渠道模型"><h2>模型 · {channel.models.length}</h2>
      <ModelCatalog key={channel.id} models={channel.models.map((offer) => offer.model)} priceLabel="参考价" renderDetails={(model) => {
        const offer = channel.models.find((item) => item.model.id === model.id)!
        return <dl className="model-card-prices">
          <div><dt>缓存读 / 写</dt><dd className="num">{formatPoints(model.lowest_prices?.cache_read ?? '0')} / {formatPoints(model.lowest_prices?.cache_write ?? '0')}</dd></div>
          {!model.custom && <div><dt>{offer.fx_discount ? '实际倍率 / 设置倍率' : '倍率'}</dt><dd className="num">{offer.fx_discount && `${shortMultiplier(offer.effective_multiplier)} / `}{formatMultiplier(offer.multiplier)}</dd></div>}
        </dl>
      }} />
    </section>
    {channel.description && <Card title="渠道说明"><MarkdownContent value={channel.description} /></Card>}
  </>}</QueryBoundary>
}
export function SuppliersPage() {
  const query = useSuppliers()
  const [search, setSearch] = useState('')
  const [kind, setKind] = useState('')
  return <><PageHeader title="供应商" /><SharingNav /><div className="supplier-filters"><TextField type="search" label="供应商" placeholder="搜索供应商" value={search} onChange={(e) => setSearch(e.target.value)} /><SelectField label="类型" value={kind} onChange={(e) => setKind(e.target.value)}><option value="">全部</option><option value="official">官方 API</option><option value="relay">中转站</option></SelectField></div><QueryBoundary query={query} errorFallback="供应商加载失败">{(items) => {
    const filtered = items.filter((supplier) => (!kind || supplier.kind === kind) && `${supplier.name} ${supplier.summary}`.toLowerCase().includes(search.toLowerCase()))
    return filtered.length ? <div className="sharing-grid">{filtered.map((supplier) => <SupplierCard supplier={supplier} key={supplier.id} />)}</div> : <EmptyState title="暂无符合条件的供应商" />
  }}</QueryBoundary></>
}
export function SupplierDetailPage() {
  const { id = '' } = useParams()
  const query = useSupplier(id)
  const [tab, setTab] = useState<'stats' | 'models' | 'channels'>('stats')
  return <QueryBoundary query={query} errorFallback="供应商不存在或已隐藏">{({ supplier, channels, models }) => <>
    <PageHeader title={supplier.name} back={{ to: '/suppliers', label: '供应商' }} /><Card><div className="sharing-title"><SupplierIcon supplier={supplier} /><Badge tone="info">{supplier.kind === 'official' ? '官方 API' : '中转站'}</Badge></div>{supplier.summary && <p>{supplier.summary}</p>}
      <div className="sharing-line">{[[supplier.website_url, '官网'], [supplier.docs_url, '文档'], [supplier.status_url, '服务状态']].filter(([url]) => url).map(([url, label]) => <a href={url} key={label} target="_blank" rel="noopener noreferrer">{label} ↗</a>)}</div>
      {supplier.description && <MarkdownContent value={supplier.description} />}<p className="muted-copy">资料更新于 {new Date(supplier.updated_at).toLocaleDateString()}</p>
    </Card>
    <Tabs label="供应商详情" value={tab} onChange={setTab} items={[{ key: 'stats', label: '质量统计' }, { key: 'models', label: `模型 ${models.length}` }, { key: 'channels', label: `关联渠道 ${channels.length}` }]}>
    {tab === 'stats' ? <QualityStats key={supplier.id} path={`/api/suppliers/${supplier.id}`} channels={channels} /> : tab === 'models' ? <ModelCatalog key={supplier.id} models={models} /> : channels.length ? <div className="sharing-grid">{channels.map((channel) => <ChannelCard key={channel.id} channel={channel} />)}</div> : <EmptyState title="暂无站内渠道" />}</Tabs>
  </>}</QueryBoundary>
}
