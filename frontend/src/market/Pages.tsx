import { Pager } from '../ui/Pager'
import { CommittedInput } from '../ui/CommittedInput'
import { UrlSearchInput } from '../ui/UrlSearchInput'
import { enumField, stringField, useBack, useUrlState } from '../ui/urlState'
import { featureLabels, marketDefaults, marketParams, protocols, useMarketFilters } from './filters'
import { formatDateTime } from '../money/time'
import { SaveBar } from '../ui/SaveBar'
import { useUnsavedGuard } from '../ui/UnsavedGuard'
import { useKeys } from '../keys/queries'
import { formatClockTime } from '../money/time'
import { DomainNav } from '../layouts/DomainNav'
import { MarketQualitySection } from './Quality'
import { useState } from 'react'
import { Link, useLocation, useParams, useSearchParams } from 'react-router-dom'
import { errorMessage } from '../api/query'
import type { components } from '../api/schema.gen'
import { FormatTags } from '../calls'
import { MarkdownContent } from '../markdown/MarkdownContent'
import { DetectionObservations } from '../detection/Observations'
import { RoutingEditor } from '../models/RoutingEditor'
import { draftFromPreference, sameDraft, toRoutingInput } from '../models/routing'
import { SupplierIcon } from '../sharing/components'
import { EstimateCard } from '../typical/EstimateCard'
import { useTypicalCosts } from '../typical/queries'
import { formatTypicalPrice } from '../typical/format'
import { Badge, Button, ButtonLink, Card, Checkbox, Disclosure, EmptyState, InlineError, Notice, PageHeader, QueryBoundary, SelectField, Switch, Toolbar } from '../ui'
import { useMarketGroup, useMarketModel, useMarketModels, useMarketReport, useMarketSuppliers,useSetMarketRouting } from './queries'
import './market.css'

type Schema = components['schemas']
type Model = Schema['MarketBrowseModel']
type Detail = Schema['MarketBrowseDetail']
type Price = Schema['MarketBrowsePrice']
const kinds: Record<string, string> = { official_api: '官方 API', official_subscription: '官方订阅', relay: '中转站' }
const states: Record<string, string> = { pass: '通过', fail: '未通过', partial: '部分支持', unsupported: '不支持', inconclusive: '无法确认', unobserved: '未观测', untested: '未执行', finished: '已结束' }
const reasons: Record<string, string> = { ok: '已核验', unsupported: '上游不支持', not_observed: '条件未出现', prerequisite_failed: '前置未通过', client_disabled: '客户端未启用或未调用', platform_unavailable: '平台入口不可用', detector_error: '检测器异常', external_error: '上游或网络异常', assertion_failed: '断言未通过' }
function status(value: string) { return states[value] ?? value }
function bounds(value: number | null) { return value && value > 0 ? value.toLocaleString() : '未知' }
export function PriceText({ value }: { value: Price | null }) {
  return <>{formatTypicalPrice(value)}</>
}
function ModelCard({ model, search }: { model: Model; search: string }) {
  const location = useLocation(), from = { to: location.pathname + location.search, label: location.pathname.startsWith('/groups/') ? '分组' : location.pathname.startsWith('/suppliers/') ? '供应商' : '模型' }
  return <Link state={{ from }} className="model-card" to={`/models/${encodeURIComponent(model.id)}${search ? `?${search}` : ''}`}>
    <header><strong className="mono">{model.id}</strong></header>
    <span className="model-card-meta">{model.model_types.map(t => t === 'image' ? '图片生成' : '文本推理').join(' · ')}</span>
    <dl className="model-card-prices"><div><dt>匹配候选的典型费用</dt><dd className="num"><PriceText value={model.price} /></dd></div><div><dt>在线渠道</dt><dd className="num">{model.online_channels}</dd></div></dl>
    <FormatTags formats={model.formats} />
    <span className="model-card-meta">{new Set(model.offers.map(o => o.group_id)).size} 个分组{model.protocols.includes('codex-special') && ' · 含 Codex 专项报告'}</span>
  </Link>
}

export function MarketModelGrid({ groupID, supplierID }: { groupID?: string; supplierID?: string }) {
  const [filters, setFilters] = useMarketFilters(), location = useLocation()
  const effective = marketParams({ ...filters, ...(groupID ? { group_id: groupID } : {}), ...(supplierID ? { supplier_id: supplierID } : {}) })
  const query = useMarketModels(effective), suppliers = useMarketSuppliers()
  const features = filters.features.split(',').filter(Boolean)
  const update = <K extends keyof typeof filters>(key: K, value: typeof filters[K]) => setFilters({ [key]: value, ...(key === 'supplier_id' ? { group_id: '' } : {}) })
  const advanced = Number(Boolean(filters.model_id)) + Number(Boolean(filters.format)) + Number(Boolean(filters.min_context)) + Number(Boolean(filters.min_output)) + features.length + Number(!groupID && Boolean(filters.group_id)) + Number(!supplierID && Boolean(filters.supplier_id))
  const groups = suppliers.data?.groups.filter(group => !effective.get('supplier_id') || group.supplier_id === effective.get('supplier_id')) ?? []
  const selected = (value: string, rows: { id: string }[]) => value && !rows.some(row => row.id === value) ? <option value={value}>当前选择 · {value}</option> : null
  return <>
    <div className="model-filter-toolbar"><Toolbar>
      <UrlSearchInput label="搜索模型 ID" value={filters.q} onCommit={q => update('q', q)} />
      <SelectField label="模型类型" value={filters.model_type} onChange={event => update('model_type', event.target.value as typeof filters.model_type)}><option value="">全部</option><option value="text">文本推理</option><option value="image">图片生成</option></SelectField>
      <SelectField label="协议入口" value={filters.protocol} onChange={event => update('protocol', event.target.value)}><option value="">全部</option>{Object.entries(protocols).map(([id, label]) => <option value={id} key={id}>{label}</option>)}</SelectField>
      <SelectField label="排序" value={filters.sort} onChange={event => update('sort', event.target.value as typeof filters.sort)}><option value="id">模型 ID</option><option value="price">典型费用</option></SelectField>
    </Toolbar></div>
    <Disclosure title={`能力与分组筛选${advanced ? `（${advanced}）` : ''}`} defaultOpen={advanced > 0}>
      <div className="model-filters">
        {!supplierID && (suppliers.isError ? <label className="field"><span className="field-label">供应商 ID</span><CommittedInput className="input" value={filters.supplier_id} onCommit={id => update('supplier_id', id.trim())} /></label> : <SelectField label="供应商" value={filters.supplier_id} onChange={event => update('supplier_id', event.target.value)}><option value="">全部</option>{selected(filters.supplier_id, suppliers.data?.items ?? [])}{suppliers.data?.items.map(row => <option key={row.id} value={row.id}>{row.name}</option>)}</SelectField>)}
        {!groupID && (suppliers.isError ? <label className="field"><span className="field-label">分组 ID</span><CommittedInput className="input" value={filters.group_id} onCommit={id => update('group_id', id.trim())} /></label> : <SelectField label="分组" value={filters.group_id} onChange={event => update('group_id', event.target.value)}><option value="">全部</option>{selected(filters.group_id, groups)}{groups.map(row => <option key={row.id} value={row.id}>{row.name}</option>)}</SelectField>)}
        <label className="field"><span className="field-label">最低实测上下文（token）</span><CommittedInput className="input" type="number" min={0} step={1} value={filters.min_context} onCommit={value => update('min_context', value === '0' ? '' : value)} /></label>
        <label className="field"><span className="field-label">最低实测输出（token）</span><CommittedInput className="input" type="number" min={0} step={1} value={filters.min_output} onCommit={value => update('min_output', value === '0' ? '' : value)} /></label>
        <label className="field"><span className="field-label">模型 ID（精确）</span><CommittedInput className="input" value={filters.model_id} onCommit={id => update('model_id', id.trim())} /></label>
        <SelectField label="调用格式" value={filters.format} onChange={event => update('format', event.target.value)}><option value="">全部</option>{['openai_chat', 'openai_responses', 'anthropic', 'gemini', 'openai_images'].map(value => <option key={value} value={value}>{({ openai_chat: 'OpenAI Chat', openai_responses: 'OpenAI Responses', anthropic: 'Anthropic', gemini: 'Gemini', openai_images: '图片' })[value]}</option>)}</SelectField>
      </div>
      <InlineError>{suppliers.isError && '供应商与分组选项加载失败，可填写 ID，其他筛选仍可使用。'}</InlineError>
      <fieldset className="model-filter-capabilities"><legend>同一候选同时满足</legend>{Object.entries(featureLabels).map(([id, label]) => <Checkbox key={id} label={label} checked={features.includes(id)} onChange={event => update('features', (event.target.checked ? [...new Set([...features, id])] : features.filter(value => value !== id)).join(','))} />)}</fieldset>
    </Disclosure>
    <div className="model-filter-summary"><Switch label="仅看有在线渠道" checked={filters.only_online} onChange={value => update('only_online', value)} /><Button variant="quiet" disabled={!marketParams(filters).size} onClick={() => setFilters(marketDefaults)}>清空筛选</Button></div>
    <QueryBoundary query={query} errorFallback="模型目录加载失败">{data => <div aria-busy={query.isFetching} className={query.isPlaceholderData ? 'market-updating' : undefined}>
      <p className="muted-copy" role="status">共 {data.total} 个模型 · 能力和费用来自符合筛选的候选{query.isPlaceholderData && ' · 正在更新…'}</p>
      {data.price_sort_fallback && <Notice tone="info">报价的计量单位不同，当前按模型 ID 排序。</Notice>}
      {data.items.length ? <div className="model-grid">{data.items.map(model => <ModelCard key={model.id} model={model} search={effective.toString()} />)}</div> : <EmptyState title="没有符合条件的模型" action={<ButtonLink to="/supply/contributions/new" state={{ overlayFrom: location.pathname + location.search, from: { to: location.pathname + location.search, label: '模型' } }}>贡献供应商分组</ButtonLink>} />}
      <Pager hasMore={query.hasNextPage || query.limited} limited={query.limited} loading={query.isFetchingNextPage} error={query.isFetchNextPageError ? errorMessage(query.error, '目录已变化或加载失败，请重试') : undefined} onMore={() => void query.fetchNextPage()} />
      {query.isFetchNextPageError && <Button variant="quiet" onClick={() => void query.refetch()}>刷新目录</Button>}
    </div>}</QueryBoundary>
  </>
}
export function MarketModelsPage() { return <><PageHeader title="模型" /><DomainNav domain="市场" /><MarketModelGrid /></> }

function RoutingCard({ detail }: { detail: Detail }) {
  const location = useLocation()
  const [base, setBase] = useState(() => draftFromPreference(detail.routing, detail.routing_channels))
  const [draft, setDraft] = useState(base)
  const [savedAt, setSavedAt] = useState('')
  const mutation = useSetMarketRouting(detail.model.id)
  const keys = useKeys()
  const dirty = !sameDraft(draft, base)
  useUnsavedGuard(dirty, { busy: mutation.isPending })
  return <Card title="我的调用方式">
    <div className="stack-form"><p className="muted-copy">{detail.routing.source === 'account' ? '账号设置' : '平台默认'}</p>
    <SaveBar dirtyCount={dirty ? 1 : 0} busy={mutation.isPending} savedAt={savedAt} onDiscard={() => setDraft(base)} onSave={() => mutation.mutate(toRoutingInput(draft), { onSuccess: result => { const latest = draftFromPreference(result, detail.routing_channels); setBase(latest); setDraft(latest); setSavedAt(formatClockTime(new Date())) } })} />
    <InlineError>{mutation.isError ? errorMessage(mutation.error, '保存失败') : ''}</InlineError>
    <RoutingEditor channels={detail.routing_channels} draft={draft} onChange={setDraft} scopeHint="该模型的全部渠道，对你所有 Key 生效；单把 Key 的覆盖优先" />
    {(keys.data?.items ?? []).filter(key => key.routed_models.includes(detail.model.id)).map(key => <Link key={key.id} to={`/keys/${key.id}/routing/${encodeURIComponent(detail.model.id)}`} state={{ from: { to: location.pathname + location.search, label: '模型' } }}>Key「{key.name}」单独设置了此模型 →</Link>)}
    </div>
  </Card>
}
export function ModelRoutingCard({id}:{id:string}){
  const query=useMarketModel(id,new URLSearchParams())
  return <QueryBoundary query={query} errorFallback="模型调用设置加载失败">{detail=><RoutingCard key={detail.model.id} detail={detail}/>}</QueryBoundary>
}
function TypicalModelCosts({ detail }: { detail: Detail }) {
  const query = useTypicalCosts('', detail.model.id)
  const groups = new Set(detail.offers.map(o => o.group_id)), channels = new Set(detail.channels.map(c => c.id))
  return <Disclosure title="最近一轮用量、参考倍率与样本"><QueryBoundary query={query} errorFallback="典型费用加载失败">{data => <>
    {(data.status.pending||data.status.running||data.status.last_error)&&<Notice tone="info">画像与报价尚待更新，以下为最近一轮估算；当前候选费用以上方为准。</Notice>}<p className="muted-copy">{data.snapshot ? `估算于 ${formatDateTime(data.snapshot.updated_at)} · 仅本站样本` : '暂无可靠样本，典型费用与参考倍率未知'}</p>
    {data.items.filter(row => row.level === 'model' || row.level === 'group' && groups.has(row.group_id ?? '') || row.level === 'channel' && channels.has(row.channel_id ?? '')).map(row => <EstimateCard key={`${row.level}/${row.group_id}/${row.channel_id}`} item={row} />)}
  </>}</QueryBoundary></Disclosure>
}
export function MarketModelDetailPage() {
  const { model = '' } = useParams(), [params] = useSearchParams()
  const query = useMarketModel(model, params), location = useLocation()
  const back = useBack(`/models${marketParams(Object.fromEntries(params)).size ? '?' + marketParams(Object.fromEntries(params)) : ''}`, '模型')
  const from = { to: location.pathname + location.search, label: model }
  return <><PageHeader title={model || '模型详情'} back={back} /><QueryBoundary query={query} errorFallback="模型不存在或没有符合条件的报价">{detail => <>
    <p className="muted-copy">{detail.model.online_channels} 个在线渠道 · 典型费用 <PriceText value={detail.model.price} /></p>
    <ModelRoutingCard id={detail.model.id} />
    {detail.offers.map(offer => {
      const group = detail.groups.find(g => g.id === offer.group_id), supplier = detail.suppliers.find(s => s.id === group?.supplier_id)
      const channels = detail.channels.filter(c => c.group_model_id === offer.id)
      return <Card key={offer.id} title={group?.name ?? '分组'}>
        <div className="sharing-line">{supplier && <Link to={`/suppliers/${supplier.id}`} state={{ from }}>{supplier.name}</Link>}<Link state={{ from }} to={`/groups/${offer.group_id}`}>分组与检测报告 →</Link>{offer.reference && <Badge tone="info">原厂参考源</Badge>}</div>
        {offer.display_name && offer.display_name !== offer.model_id && <p className="muted-copy">本报价显示名：{offer.display_name}</p>}
        <p>声明上下文 {bounds(offer.declared_context)} · 声明最大输出 {bounds(offer.declared_output)}</p>
        <QuoteDetails offer={offer} />
        {channels.length ? channels.map(c => <section className="market-channel" key={`${c.id}/${c.protocol}`}><h3><Link state={{ from }} to={`/channel-hall/${c.id}`}>{c.name}</Link> · {protocols[c.protocol]}</h3><p>分组折扣 {c.discount}× · 典型费用 <PriceText value={c.price} /></p><CapabilitySummary facts={c.capabilities} /></section>) : <p className="muted-copy">暂无满足本次筛选的在线渠道</p>}
      </Card>
    })}
    <TypicalModelCosts detail={detail} />
    {detail.model.protocols.includes('codex-special') && <CodexSetup model={detail.model.id} />}
  </>}</QueryBoundary></>
}
function CapabilitySummary({ facts }: { facts: Schema['MarketBrowseCapabilities'] }) {
  return <><p className="muted-copy">实测上下文下界 {bounds(facts.context_lower)} · 实测输出下界 {bounds(facts.output_lower)}</p><div className="sharing-line">{facts.supported.map(f => <Badge key={f}>{featureLabels[f] ?? f}</Badge>)}</div>{facts.supported.length === 0 && <p className="muted-copy">高级能力缺少当前有效证据</p>}</>
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
function CodexSetup({ model }: { model: string }) {
  return <Disclosure title="Codex 0.163.0 接入"><p>在独立 CODEX_HOME 的 auth.json 填入自己的平台 Key：</p><pre className="market-code">{'{"personal_access_token":"<PLATFORM_API_KEY>"}'}</pre><p>config.toml：</p><pre className="market-code">{`model = ${JSON.stringify(model)}\nmodel_provider = "openai"\nopenai_base_url = "https://<GATEWAY_HOST>/backend-api/codex"\nchatgpt_base_url = "https://<GATEWAY_HOST>/backend-api/"\n\n[features.token_budget]\nenabled = true\nuse_history_notes_extension = true`}</pre><p>启动时设置 CODEX_AUTHAPI_BASE_URL=https://&lt;GATEWAY_HOST&gt;。notes/history 等能力请分别查阅分组报告。</p><p className="muted-copy">客户端会将平台身份归类为 ChatGPT，套餐显示未知 aihub；官方订阅暂仅保留分类。</p></Disclosure>
}

const supplierSchema = { q: stringField(''), kind: enumField(['', 'official_api', 'official_subscription', 'relay'] as const, '') }
export function MarketSuppliersPage() {
  const query = useMarketSuppliers(), [filters, setFilters] = useUrlState(supplierSchema), location = useLocation()
  const from = { to: location.pathname + location.search, label: '供应商' }
  return <><PageHeader title="供应商" actions={<ButtonLink to="/supply/contributions/new" state={{ overlayFrom: from.to, from }}>贡献供应商</ButtonLink>} /><DomainNav domain="市场" /><Toolbar><UrlSearchInput label="搜索供应商" value={filters.q} onCommit={q => setFilters({ q })} /><SelectField label="供应商分类" value={filters.kind} onChange={event => setFilters({ kind: event.target.value as typeof filters.kind })}><option value="">全部</option>{Object.entries(kinds).map(([kind, label]) => <option value={kind} key={kind}>{label}</option>)}</SelectField></Toolbar>
    <QueryBoundary query={query} errorFallback="供应商加载失败">{data => {
      const rows = data.items.filter(row => (!filters.kind || row.kind === filters.kind) && `${row.name} ${row.summary}`.toLowerCase().includes(filters.q.toLowerCase()))
      return rows.length ? <div className="sharing-grid">{rows.map(row => <Card key={row.id}><div className="sharing-title"><SupplierIcon supplier={row} /><h2><Link to={`/suppliers/${row.id}`} state={{ from }}>{row.name}</Link></h2><Badge>{kinds[row.kind]}</Badge></div><p>{row.summary}</p><p className="muted-copy">{row.groups.length} 个分组</p></Card>)}</div> : <EmptyState title="暂无符合条件的供应商" />
    }}</QueryBoundary></>
}
export function MarketSupplierDetailPage() {
  const { id = '' } = useParams(), query = useMarketSuppliers(), location = useLocation()
  const supplier = query.data?.items.find(row => row.id === id), back = useBack('/suppliers', '供应商')
  const from = { to: location.pathname + location.search, label: supplier?.name || '供应商' }
  return <><PageHeader title={supplier?.name || '供应商详情'} back={back} /><QueryBoundary query={query} errorFallback="供应商加载失败">{data => {
    const s = data.items.find(s => s.id === id)
    if (!s) return <EmptyState title="供应商不存在或已停用" />
    return <><Card><div className="sharing-title"><SupplierIcon supplier={s} /><Badge>{kinds[s.kind]}</Badge></div><p>{s.summary}</p><MarkdownContent value={s.description} /><div className="sharing-line">{s.website_url && <a href={s.website_url} target="_blank" rel="noopener noreferrer">官网 ↗</a>}{s.docs_url && <a href={s.docs_url} target="_blank" rel="noopener noreferrer">文档 ↗</a>}{s.icon_source && <a href={s.icon_source} target="_blank" rel="noopener noreferrer">图标来源 ↗</a>}</div></Card><Card title="分组">{data.groups.filter(g => g.supplier_id === id).map(g => <div className="market-channel" key={g.id}><Link state={{ from }} to={`/groups/${g.id}`}>{g.name}</Link><p className="muted-copy">{g.model_ids.length} 个模型</p></div>)}</Card><MarketModelGrid supplierID={id} /><MarketQualitySection key={id} kind="supplier" id={id} /></>
  }}</QueryBoundary></>
}
export function MarketGroupPage() {
  const { id = '' } = useParams(), query = useMarketGroup(id), location = useLocation()
  const back = useBack(query.data ? `/suppliers/${query.data.supplier.id}` : '/suppliers', query.data?.supplier.name || '供应商')
  const from = { to: location.pathname + location.search, label: query.data?.group.name || '分组' }
  return <><PageHeader title={query.data?.group.name || '分组详情'} back={back} actions={query.data && <ButtonLink to={`/supply/channels/new?group=${encodeURIComponent(id)}`} state={{ from }}>用此分组共享渠道</ButtonLink>} /><QueryBoundary query={query} errorFallback="分组不存在或已停用">{data => <><Card><MarkdownContent value={data.group.description} />{data.group.status_url && <a href={data.group.status_url} target="_blank" rel="noopener noreferrer">服务状态 ↗</a>}<div className="sharing-line">{data.reports.map(id => <ButtonLink key={id} to={`/reports/${id}`} state={{ from }} variant="secondary">查看检测报告</ButtonLink>)}</div>{data.reports.length === 0 && <p className="muted-copy">暂无已批准检测报告</p>}</Card><MarketModelGrid groupID={id} /></>}</QueryBoundary></>
}
export function MarketReportPage() {
  const { id = '' } = useParams(), query = useMarketReport(id)
  const back = useBack(query.data ? `/groups/${query.data.group_id}` : '/suppliers', query.data ? '分组' : '供应商')
  return <><PageHeader title="公开检测报告" back={back} /><QueryBoundary query={query} errorFallback="报告不存在或未公开">{report => <>
    <Notice tone={report.validity === 'current' ? 'info' : 'warning'}>{report.validity === 'current' ? '报告证据与当前配置匹配' : report.validity === 'partial' ? '部分场景的配置或检测范围已变化，仅仍有效的独立证据用于路由' : '配置或检测范围已变化，此报告不再提供当前能力支持证据'}</Notice>
    <p className="muted-copy">{formatDateTime(report.updated_at)} · 范围 {report.manifest_version} · {report.complete ? '已完成该版本全测' : '未完成全测'} · 实际 {report.usage.http} 次 HTTP / {report.usage.input_tokens + report.usage.output_tokens} tokens</p>
    {report.scenarios.map(scenario => <Card key={scenario.id} title={scenario.id.split('@')[0]}><p><Badge>{({ upstream_protocol: '上游协议', gateway_proxy: '平台代理', official_cli: '官方 CLI' })[scenario.layer] ?? scenario.layer}</Badge> <Badge tone={scenario.state === 'pass' ? 'success' : 'neutral'}>{status(scenario.state)}</Badge> {!scenario.current && !scenario.aggregate && <Badge tone="warning">当前证据失效</Badge>}</p><p className="muted-copy">{reasons[scenario.reason] ?? scenario.reason} · 预计 HTTP {scenario.estimated_http ?? '未知'} / 实际 {scenario.usage.http}</p><Disclosure title="原检测项与子断言">{scenario.endpoints.map(e=><p key={e.id} className="mono">{e.method} {e.path} · {e.proxy_supported?'平台代理已开放':'平台代理未开放'}</p>)}<p className="mono">{scenario.source_ids.join(' · ')}</p><ul>{scenario.assertions.map(a => <li key={a.id}><span className="mono">{a.label}</span> · {status(a.state)} · {reasons[a.reason] ?? a.reason}</li>)}</ul><p className="market-digest">范围 {scenario.scope_version} · 实现 {scenario.code_version}</p></Disclosure><DetectionObservations result={{ capacity: scenario.capacity ?? undefined, settings: scenario.settings ?? undefined, images: scenario.images ?? undefined, fingerprint: scenario.fingerprint ?? undefined }} /></Card>)}
    <Disclosure title="固定版本与本轮移出范围"><p className="market-digest">报告代码 {report.registry_version}</p>{report.sources.map(s => <p key={s.id}><a href={s.url} target="_blank" rel="noopener noreferrer">{s.id}</a> · {s.version}</p>)}{report.exclusions.map((e, i) => <p key={i}>{e.protocol} · {e.source_ids.join(' / ')} · {e.reason}</p>)}</Disclosure>
  </>}</QueryBoundary></>
}
