import { MarketQualitySection } from './Quality'
import { useEffect, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { errorMessage } from '../api/query'
import type { components } from '../api/schema.gen'
import { FormatTags } from '../calls'
import { ContributionDialog } from '../contributions/Editor'
import { MarkdownContent } from '../markdown/MarkdownContent'
import { RoutingEditor } from '../models/RoutingEditor'
import { draftFromPreference, sameDraft, toRoutingInput } from '../models/routing'
import { SupplierIcon } from '../sharing/components'
import { EstimateCard } from '../typical/Page'
import { useTypicalCosts } from '../typical/queries'
import { formatTypicalPrice } from '../typical/format'
import { Badge, Button, Card, Disclosure, EmptyState, InlineError, Notice, PageHeader, Pagination, QueryBoundary, SearchInput, SelectField, SuccessMessage, Switch, Toolbar } from '../ui'
import { useMarketGroup, useMarketModel, useMarketModels, useMarketSuppliers,useSetMarketRouting } from './queries'
import { ParentFilter } from './ParentFilter'
import { readParentSelection, writeParentSelection } from './parentSelection'
import './market.css'

type Schema = components['schemas']
type Model = Schema['MarketBrowseModel']
type Detail = Schema['MarketBrowseDetail']
type Price = Schema['MarketBrowsePrice']
const protocols: Record<string, string> = { 'openai-chat-completions': 'OpenAI Chat', 'openai-responses': 'OpenAI Responses', 'native-messages': 'Anthropic Messages', 'native-generate-content': 'Gemini', 'openai-images': '图片' }
const kinds: Record<string, string> = { official_api: '官方 API', official_subscription: '官方订阅', relay: '中转站' }
function bounds(value: number | null) { return value && value > 0 ? value.toLocaleString() : '未知' }
export function PriceText({ value }: { value: Price | null }) {
  return <>{formatTypicalPrice(value)}</>
}
function ModelCard({ model, search }: { model: Model; search: string }) {
  return <Link className="model-card" to={`/models/${encodeURIComponent(model.id)}${search ? `?${search}` : ''}`}>
    <header><strong className="mono">{model.id}</strong></header>
    <span className="model-card-meta">{model.model_types.map(t => t === 'image' ? '图片生成' : '文本推理').join(' · ')}</span>
    <dl className="model-card-prices"><div><dt>匹配候选的典型费用</dt><dd className="num"><PriceText value={model.price} /></dd></div><div><dt>在线渠道</dt><dd className="num">{model.online_channels}</dd></div></dl>
    <FormatTags formats={model.formats} />
    <span className="model-card-meta">{new Set(model.offers.map(o => o.group_id)).size} 个分组</span>
  </Link>
}

export function MarketModelGrid({ groupID, supplierID }: { groupID?: string; supplierID?: string }) {
  const [params, setParams] = useSearchParams()
  const suppliers = useMarketSuppliers()
  const scopedGroups = supplierID ? (suppliers.data?.groups.filter(group => group.supplier_id === supplierID) ?? []) : []
  const effective = new URLSearchParams(params)
  if (groupID) {
    // 详情页已经锁定分组：忽略 URL 上的父级筛选，避免并集把范围放大。
    effective.delete('supplier_id')
    effective.delete('group_id')
    effective.set('group_id', groupID)
  } else if (supplierID) {
    // 供应商详情：选了分组就只发分组（分组已隐含供应商），否则发供应商全范围。
    effective.delete('supplier_id')
    effective.delete('group_id')
    const chosen = scopedGroups.find(group => params.getAll('group_id').includes(group.id))
    if (chosen) effective.set('group_id', chosen.id)
    else effective.set('supplier_id', supplierID)
  }
  const query = useMarketModels(effective)
  const update = (key: string, value: string) => setParams(current => {
    const next = new URLSearchParams(current)
    if (value) next.set(key, value); else next.delete(key)
    next.delete('cursor')
    return next
  }, { replace: true })
  const updateGroup = (value: string) => setParams(current => {
    const next = new URLSearchParams(current)
    next.delete('group_id')
    if (value) next.set('group_id', value)
    next.delete('cursor')
    return next
  }, { replace: true })
  const selectedGroup = scopedGroups.find(group => params.getAll('group_id').includes(group.id))?.id ?? ''
  const search = new URLSearchParams(effective); search.delete('cursor'); search.delete('limit')
  const parentFilter = !supplierID && !groupID
  return <>
    <div className="model-filter-toolbar"><Toolbar end={<Switch label="仅看有在线渠道" checked={params.get('only_online') === 'true'} onChange={v => update('only_online', String(v))} />}>
      <SearchInput label="搜索模型 ID" value={params.get('q') ?? ''} onChange={e => update('q', e.target.value)} />
      <SelectField hideLabel label="模型类型" value={params.get('model_type') ?? ''} onChange={e => update('model_type', e.target.value)}><option value="">全部模型</option><option value="text">文本推理</option><option value="image">图片生成</option></SelectField>
      <SelectField hideLabel label="协议入口" value={params.get('protocol') ?? ''} onChange={e => update('protocol', e.target.value)}><option value="">全部协议</option>{Object.entries(protocols).map(([id, label]) => <option key={id} value={id}>{label}</option>)}</SelectField>
      <SelectField hideLabel label="排序" value={params.get('sort') ?? 'id'} onChange={e => update('sort', e.target.value)}><option value="id">模型 ID</option><option value="price">典型费用</option></SelectField>
      {parentFilter && <ParentFilter groups={suppliers.data?.groups ?? []} loading={suppliers.isPending} onChange={next => setParams(writeParentSelection(params, next), { replace: true })} selection={readParentSelection(params)} suppliers={suppliers.data?.items ?? []} />}
      {supplierID && !groupID && <SelectField hideLabel label="分组" value={selectedGroup} onChange={e => updateGroup(e.target.value)}><option value="">全部分组</option>{scopedGroups.map(group => <option key={group.id} value={group.id}>{group.name}</option>)}</SelectField>}
    </Toolbar></div>
    <QueryBoundary query={query} errorFallback="模型目录加载失败">{data => <>
      <p className="visually-hidden" role="status">共 {data.total} 个模型</p>
      {data.price_sort_fallback && <Notice tone="info">报价的计量单位不同，当前按模型 ID 排序。</Notice>}
      {data.items.length ? <div className="model-grid">{data.items.map(model => <ModelCard key={model.id} model={model} search={search.toString()} />)}</div> : <EmptyState title="没有符合条件的模型" />}
      <Pagination hasPrevious={Boolean(params.get('cursor'))} onPrevious={() => update('cursor', '')} hasNext={Boolean(data.next_cursor)} onNext={() => setParams(current => { const next = new URLSearchParams(current); next.set('cursor', data.next_cursor ?? ''); return next })} />
    </>}</QueryBoundary>
  </>
}
export function MarketModelsPage() { return <><PageHeader title="模型" /><MarketModelGrid /></> }

function RoutingCard({ detail }: { detail: Detail }) {
  const saved = draftFromPreference(detail.routing, detail.routing_channels)
  const [draft, setDraft] = useState(saved)
  const [message, setMessage] = useState('')
  const mutation=useSetMarketRouting(detail.model.id)
  useEffect(() => { setDraft(draftFromPreference(detail.routing, detail.routing_channels)) }, [detail.routing, detail.routing_channels])
  const dirty = !sameDraft(draft, saved)
  return <Card title="调用方式" actions={<Button size="sm" disabled={!dirty} loading={mutation.isPending} onClick={() => { setMessage(''); mutation.mutate(toRoutingInput(draft), { onSuccess: () => setMessage('已保存') }) }}>保存</Button>}>
    <SuccessMessage>{dirty ? '' : message}</SuccessMessage><InlineError>{mutation.isError ? errorMessage(mutation.error, '保存失败') : ''}</InlineError>
    <RoutingEditor channels={detail.routing_channels} draft={draft} onChange={setDraft} scopeHint="该模型的全部渠道，对你所有 Key 生效，单把 Key 可以另设" />
  </Card>
}
export function ModelRoutingCard({id}:{id:string}){
  const query=useMarketModel(id,new URLSearchParams())
  return <QueryBoundary query={query} errorFallback="模型调用设置加载失败">{detail=><RoutingCard detail={detail}/>}</QueryBoundary>
}
function TypicalModelCosts({ detail }: { detail: Detail }) {
  const query = useTypicalCosts('', detail.model.id)
  const groups = new Set(detail.offers.map(o => o.group_id)), channels = new Set(detail.channels.map(c => c.id))
  return <Disclosure title="最近一轮用量、参考倍率与样本"><QueryBoundary query={query} errorFallback="典型费用加载失败">{data => <>
    {(data.status.pending||data.status.running||data.status.last_error)&&<Notice tone="info">画像与报价尚待更新，以下为最近一轮估算；当前候选费用以上方为准。</Notice>}<p className="muted-copy">{data.snapshot ? `估算于 ${new Date(data.snapshot.updated_at).toLocaleString()} · 仅本站样本` : '暂无可靠样本，典型费用与参考倍率未知'}</p>
    {data.items.filter(row => row.level === 'model' || row.level === 'group' && groups.has(row.group_id ?? '') || row.level === 'channel' && channels.has(row.channel_id ?? '')).map(row => <EstimateCard key={`${row.level}/${row.group_id}/${row.channel_id}`} item={row} />)}
  </>}</QueryBoundary></Disclosure>
}
export function MarketModelDetailPage() {
  const { model = '' } = useParams(), [params] = useSearchParams()
  const query = useMarketModel(model, params)
  return <QueryBoundary query={query} errorFallback="模型不存在或没有符合条件的报价">{detail => <>
    <PageHeader title={detail.model.id} back={{ to: `/models?${params}`, label: '模型' }} />
    <p className="muted-copy">{detail.model.online_channels} 个在线渠道 · 典型费用 <PriceText value={detail.model.price} /></p>
    <ModelRoutingCard id={detail.model.id} />
    {detail.offers.map(offer => {
      const group = detail.groups.find(g => g.id === offer.group_id), supplier = detail.suppliers.find(s => s.id === group?.supplier_id)
      const channels = detail.channels.filter(c => c.group_model_id === offer.id)
      return <Card key={offer.id} title={group?.name ?? '分组'} actions={<ContributionDialog create={{ kind: 'group_model', target_id: offer.id, action: 'edit' }} label="编辑报价" title="编辑模型报价" />}>
        <div className="sharing-line"><Link to={`/suppliers/${supplier?.id}`}>{supplier?.name}</Link><Link to={`/groups/${offer.group_id}`}>分组详情 →</Link>{offer.reference && <Badge tone="info">原厂参考源</Badge>}</div>
        {offer.display_name && offer.display_name !== offer.model_id && <p className="muted-copy">本报价显示名：{offer.display_name}</p>}
        <p>声明上下文 {bounds(offer.declared_context)} · 声明最大输出 {bounds(offer.declared_output)}</p>
        <QuoteDetails offer={offer} />
        {channels.length ? channels.map(c => <section className="market-channel" key={`${c.id}/${c.protocol}`}><h3><Link to={`/channel-hall/${c.id}`}>{c.name}</Link> · {protocols[c.protocol]}</h3><p>分组折扣 {c.discount}× · 典型费用 <PriceText value={c.price} /></p></section>) : <p className="muted-copy">暂无满足本次筛选的在线渠道</p>}
      </Card>
    })}
    <TypicalModelCosts detail={detail} />
  </>}</QueryBoundary>
}
function QuoteDetails({ offer }: { offer: Schema['MarketBrowseOffer'] }) {
  const q = offer.quote
  if (!q) return <p className="muted-copy">报价未知</p>
  return <Disclosure title={`分组报价 · ${q.currency} · v${q.version}`}>
    <p className="muted-copy">{q.billing_unit === 'per_million_tokens' ? '每百万 token' : q.billing_unit === 'per_request' ? '每次请求' : '每张成功图片'} · 渠道折扣作用于整张价表，手续费另计</p>
    {q.billing_unit === 'per_million_tokens' && <dl className="detail-list"><div><dt>输入 / 输出</dt><dd>{q.prices.input} / {q.prices.output}</dd></div><div><dt>缓存写 / 读</dt><dd>{q.prices.cache_write} / {q.prices.cache_read}</dd></div>{Object.entries(q.prices.details).map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl>}
    {q.tiers.map((tier, i) => <div className="market-channel" key={i}><strong>{tier.name}</strong><p>输入 {tier.prices.input} · 输出 {tier.prices.output} · 缓存写 {tier.prices.cache_write} · 读 {tier.prices.cache_read}</p><pre className="market-code">{JSON.stringify(tier.conditions, null, 2)}</pre></div>)}
    {q.unit_prices.map((u, i) => <p key={i}>{[u.unit, u.tool, u.image_size, u.image_quality, u.image_background, u.service_tier].filter(Boolean).join(' · ')}：{u.price}</p>)}
  </Disclosure>
}

export function MarketSuppliersPage() {
  const query = useMarketSuppliers(), [params, setParams] = useSearchParams()
  return <><PageHeader title="供应商" actions={<ContributionDialog create={{ kind: 'supplier', starter: true }} label="贡献供应商" title="新增供应商" variant="primary" />} /><Toolbar><SearchInput label="搜索供应商" value={params.get('q') ?? ''} onChange={e => setParams(p => { const n = new URLSearchParams(p); n.set('q', e.target.value); return n })} /><SelectField hideLabel label="供应商分类" value={params.get('kind') ?? ''} onChange={e => setParams(p => { const n = new URLSearchParams(p); n.set('kind', e.target.value); return n })}><option value="">全部供应商</option>{Object.entries(kinds).map(([k, label]) => <option value={k} key={k}>{label}</option>)}</SelectField></Toolbar>
    <QueryBoundary query={query} errorFallback="供应商加载失败">{data => {
      const rows = data.items.filter(s => (!params.get('kind') || s.kind === params.get('kind')) && `${s.name} ${s.summary}`.toLowerCase().includes((params.get('q') ?? '').toLowerCase()))
      return rows.length ? <div className="sharing-grid">{rows.map(s => <Card key={s.id}><div className="sharing-title"><SupplierIcon supplier={s} /><h2><Link to={`/suppliers/${s.id}`}>{s.name}</Link></h2><Badge>{kinds[s.kind]}</Badge></div><p>{s.summary}</p><p className="muted-copy">{s.groups.length} 个分组</p></Card>)}</div> : <EmptyState title="暂无符合条件的供应商" />
    }}</QueryBoundary></>
}
export function MarketSupplierDetailPage() {
  const { id = '' } = useParams(), query = useMarketSuppliers()
  return <QueryBoundary query={query} errorFallback="供应商加载失败">{data => {
    const s = data.items.find(s => s.id === id)
    if (!s) return <EmptyState title="供应商不存在或已停用" />
    return <><PageHeader title={s.name} back={{ to: '/suppliers', label: '供应商' }} actions={<><ContributionDialog create={{ kind: 'supplier', target_id: s.id, action: 'edit' }} label="编辑资料" title="编辑供应商" /><ContributionDialog create={{ kind: 'group', parent_id: s.id }} label="新增分组" title="新增分组" /></>} /><Card><div className="sharing-title"><SupplierIcon supplier={s} /><Badge>{kinds[s.kind]}</Badge></div>{s.summary ? <p>{s.summary}</p> : null}{s.description.trim() ? <MarkdownContent value={s.description} /> : null}{!s.summary && !s.description.trim() ? <p className="muted-copy">暂无简介</p> : null}<div className="sharing-line">{s.website_url && <a href={s.website_url} target="_blank" rel="noopener noreferrer">官网 ↗</a>}{s.docs_url && <a href={s.docs_url} target="_blank" rel="noopener noreferrer">文档 ↗</a>}{s.icon_source && <a href={s.icon_source} target="_blank" rel="noopener noreferrer">图标来源 ↗</a>}</div></Card><Card title="分组">{data.groups.filter(g => g.supplier_id === id).map(g => <div className="market-channel" key={g.id}><Link to={`/groups/${g.id}`}>{g.name}</Link><p className="muted-copy">{g.model_ids.length} 个模型</p></div>)}</Card><MarketModelGrid supplierID={id} /><MarketQualitySection key={id} kind="supplier" id={id} /></>
  }}</QueryBoundary>
}
export function MarketGroupPage() {
  const { id = '' } = useParams(), query = useMarketGroup(id)
  return <QueryBoundary query={query} errorFallback="分组不存在或已停用">{data => <><PageHeader title={data.group.name} back={{ to: `/suppliers/${data.supplier.id}`, label: data.supplier.name }} /><Card>{data.group.description.trim() ? <MarkdownContent value={data.group.description} /> : <p className="muted-copy">暂无分组说明</p>}{data.group.status_url && <a href={data.group.status_url} target="_blank" rel="noopener noreferrer">服务状态 ↗</a>}</Card><MarketModelGrid groupID={id} /></>}</QueryBoundary>
}
