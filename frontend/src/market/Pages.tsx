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
import { EstimateCard } from '../typical/Page'
import { useTypicalCosts } from '../typical/queries'
import { formatTypicalPrice } from '../typical/format'
import { Badge, Button, ButtonLink, Card, Checkbox, Disclosure, EmptyState, InlineError, Notice, PageHeader, QueryBoundary, SearchInput, SelectField, Switch, TextField, Toolbar } from '../ui'
import { useMarketGroup, useMarketModel, useMarketModels, useMarketReport, useMarketSuppliers,useSetMarketRouting } from './queries'
import './market.css'

type Schema = components['schemas']
type Model = Schema['MarketBrowseModel']
type Detail = Schema['MarketBrowseDetail']
type Price = Schema['MarketBrowsePrice']
export const featureLabels: Record<string, string> = {
  'input:text': '文本输入', 'input:image': '图片输入', 'input:audio': '音频输入', 'input:video': '视频输入', 'input:document': '文档输入',
  'output:text': '文本输出', 'output:image': '图片输出', 'output:audio': '音频输出', tools: '函数工具', strict_json: '严格结构化', json_object: 'JSON 输出',
  vision_url: '图片 URL', vision_inline: '请求内图片', reasoning: '推理配置', self_contained_replay: '完整推理状态续用', codex_cli_basic: 'Codex CLI 基本调用',
}
const protocols: Record<string, string> = { 'openai-chat-completions': 'OpenAI Chat', 'openai-responses': 'OpenAI Responses', 'native-messages': 'Anthropic Messages', 'native-generate-content': 'Gemini', 'openai-images': '图片', 'codex-special': 'Codex 专项兼容' }
const kinds: Record<string, string> = { official_api: '官方 API', official_subscription: '官方订阅', relay: '中转站' }
const states: Record<string, string> = { pass: '通过', fail: '未通过', partial: '部分支持', unsupported: '不支持', inconclusive: '无法确认', unobserved: '未观测', untested: '未执行', finished: '已结束' }
const reasons: Record<string, string> = { ok: '已核验', unsupported: '上游不支持', not_observed: '条件未出现', prerequisite_failed: '前置未通过', client_disabled: '客户端未启用或未调用', platform_unavailable: '平台入口不可用', detector_error: '检测器异常', external_error: '上游或网络异常', assertion_failed: '断言未通过' }
function status(value: string) { return states[value] ?? value }
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
    <span className="model-card-meta">{new Set(model.offers.map(o => o.group_id)).size} 个分组{model.protocols.includes('codex-special') && ' · 含 Codex 专项报告'}</span>
  </Link>
}

export function MarketModelGrid({ groupID, supplierID }: { groupID?: string; supplierID?: string }) {
  const [params, setParams] = useSearchParams()
  const effective = new URLSearchParams(params)
  if (groupID) effective.set('group_id', groupID)
  if (supplierID) effective.set('supplier_id', supplierID)
  const query = useMarketModels(effective)
  const suppliers = useMarketSuppliers()
  const features = (params.get('features') ?? '').split(',').filter(Boolean)
  const update = (key: string, value: string) => setParams(current => {
    const next = new URLSearchParams(current)
    if (value) next.set(key, value); else next.delete(key)
    next.delete('cursor')
    if (key === 'supplier_id') next.delete('group_id')
    return next
  }, { replace: true })
  const search = new URLSearchParams(effective); search.delete('cursor'); search.delete('limit')
  return <>
    <div className="model-filter-toolbar"><Toolbar>
      <SearchInput label="搜索模型 ID" value={params.get('q') ?? ''} onChange={e => update('q', e.target.value)} />
      <SelectField label="模型类型" value={params.get('model_type') ?? ''} onChange={e => update('model_type', e.target.value)}><option value="">全部</option><option value="text">文本推理</option><option value="image">图片生成</option></SelectField>
      <SelectField label="协议入口" value={params.get('protocol') ?? ''} onChange={e => update('protocol', e.target.value)}><option value="">全部</option>{Object.entries(protocols).map(([id, label]) => <option key={id} value={id}>{label}</option>)}</SelectField>
      <SelectField label="排序" value={params.get('sort') ?? 'id'} onChange={e => update('sort', e.target.value)}><option value="id">模型 ID</option><option value="price">典型费用</option></SelectField>
    </Toolbar></div>
    <Disclosure title="能力与分组筛选" defaultOpen={features.length > 0}>
      <div className="model-filters">
        {!supplierID && <SelectField label="供应商" value={params.get('supplier_id') ?? ''} onChange={e => update('supplier_id', e.target.value)}><option value="">全部</option>{suppliers.data?.items.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}</SelectField>}
        {!groupID && <SelectField label="分组" value={params.get('group_id') ?? ''} onChange={e => update('group_id', e.target.value)}><option value="">全部</option>{suppliers.data?.groups.filter(g => !effective.get('supplier_id') || g.supplier_id === effective.get('supplier_id')).map(g => <option key={g.id} value={g.id}>{g.name}</option>)}</SelectField>}
        <TextField label="最低实测上下文（token）" inputMode="numeric" value={params.get('min_context') ?? ''} onChange={e => update('min_context', e.target.value)} />
        <TextField label="最低实测输出（token）" inputMode="numeric" value={params.get('min_output') ?? ''} onChange={e => update('min_output', e.target.value)} />
      </div>
      <fieldset className="model-filter-capabilities"><legend>同一候选同时满足</legend>{Object.entries(featureLabels).map(([id, label]) => <Checkbox key={id} label={label} checked={features.includes(id)} onChange={e => update('features', (e.target.checked ? [...features, id] : features.filter(f => f !== id)).join(','))} />)}</fieldset>
    </Disclosure>
    <div className="model-filter-summary"><Switch label="仅看有在线渠道" checked={params.get('only_online') === 'true'} onChange={v => update('only_online', String(v))} /><Button variant="quiet" onClick={() => setParams({})} disabled={!params.size}>清空筛选</Button></div>
    <QueryBoundary query={query} errorFallback="模型目录加载失败">{data => <>
      <p className="muted-copy" role="status">共 {data.total} 个模型 · 能力和费用来自符合筛选的候选</p>
      {data.price_sort_fallback && <Notice tone="info">报价的计量单位不同，当前按模型 ID 排序。</Notice>}
      {data.items.length ? <div className="model-grid">{data.items.map(model => <ModelCard key={model.id} model={model} search={search.toString()} />)}</div> : <EmptyState title="没有符合条件的模型" action={<ButtonLink to="/supply/contributions">贡献供应商分组</ButtonLink>} />}
      <div className="form-actions"><Button variant="quiet" disabled={!params.get('cursor')} onClick={() => update('cursor', '')}>回到第一页</Button><Button variant="secondary" disabled={!data.next_cursor} onClick={() => setParams(current => { const next = new URLSearchParams(current); next.set('cursor', data.next_cursor ?? ''); return next })}>下一页</Button></div>
    </>}</QueryBoundary>
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
      return <Card key={offer.id} title={group?.name ?? '分组'}>
        <div className="sharing-line"><Link to={`/suppliers/${supplier?.id}`}>{supplier?.name}</Link><Link to={`/groups/${offer.group_id}`}>分组与检测报告 →</Link>{offer.reference && <Badge tone="info">原厂参考源</Badge>}</div>
        {offer.display_name && offer.display_name !== offer.model_id && <p className="muted-copy">本报价显示名：{offer.display_name}</p>}
        <p>声明上下文 {bounds(offer.declared_context)} · 声明最大输出 {bounds(offer.declared_output)}</p>
        <QuoteDetails offer={offer} />
        {channels.length ? channels.map(c => <section className="market-channel" key={`${c.id}/${c.protocol}`}><h3><Link to={`/channel-hall/${c.id}`}>{c.name}</Link> · {protocols[c.protocol]}</h3><p>分组折扣 {c.discount}× · 典型费用 <PriceText value={c.price} /></p><CapabilitySummary facts={c.capabilities} /></section>) : <p className="muted-copy">暂无满足本次筛选的在线渠道</p>}
      </Card>
    })}
    <TypicalModelCosts detail={detail} />
    {detail.model.protocols.includes('codex-special') && <CodexSetup model={detail.model.id} />}
  </>}</QueryBoundary>
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

export function MarketSuppliersPage() {
  const query = useMarketSuppliers(), [params, setParams] = useSearchParams()
  return <><PageHeader title="供应商" actions={<ButtonLink to="/supply/contributions">贡献供应商</ButtonLink>} /><DomainNav domain="市场" /><Toolbar><SearchInput label="搜索供应商" value={params.get('q') ?? ''} onChange={e => setParams(p => { const n = new URLSearchParams(p); n.set('q', e.target.value); return n })} /><SelectField label="供应商分类" value={params.get('kind') ?? ''} onChange={e => setParams(p => { const n = new URLSearchParams(p); n.set('kind', e.target.value); return n })}><option value="">全部</option>{Object.entries(kinds).map(([k, label]) => <option value={k} key={k}>{label}</option>)}</SelectField></Toolbar>
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
    return <><PageHeader title={s.name} back={{ to: '/suppliers', label: '供应商' }} /><Card><div className="sharing-title"><SupplierIcon supplier={s} /><Badge>{kinds[s.kind]}</Badge></div><p>{s.summary}</p><MarkdownContent value={s.description} /><div className="sharing-line">{s.website_url && <a href={s.website_url} target="_blank" rel="noopener noreferrer">官网 ↗</a>}{s.docs_url && <a href={s.docs_url} target="_blank" rel="noopener noreferrer">文档 ↗</a>}{s.icon_source && <a href={s.icon_source} target="_blank" rel="noopener noreferrer">图标来源 ↗</a>}</div></Card><Card title="分组">{data.groups.filter(g => g.supplier_id === id).map(g => <div className="market-channel" key={g.id}><Link to={`/groups/${g.id}`}>{g.name}</Link><p className="muted-copy">{g.model_ids.length} 个模型</p></div>)}</Card><MarketModelGrid supplierID={id} /><MarketQualitySection key={id} kind="supplier" id={id} /></>
  }}</QueryBoundary>
}
export function MarketGroupPage() {
  const { id = '' } = useParams(), query = useMarketGroup(id)
  return <QueryBoundary query={query} errorFallback="分组不存在或已停用">{data => <><PageHeader title={data.group.name} back={{ to: `/suppliers/${data.supplier.id}`, label: data.supplier.name }} actions={<ButtonLink to={`/supply/channels/new?group=${encodeURIComponent(id)}`} state={{ from: { to: `/groups/${id}`, label: data.group.name } }}>用此分组共享渠道</ButtonLink>} /><Card><MarkdownContent value={data.group.description} />{data.group.status_url && <a href={data.group.status_url} target="_blank" rel="noopener noreferrer">服务状态 ↗</a>}<div className="sharing-line">{data.reports.map(id => <ButtonLink key={id} to={`/reports/${id}`} variant="secondary">查看检测报告</ButtonLink>)}</div>{data.reports.length === 0 && <p className="muted-copy">暂无已批准检测报告</p>}</Card><MarketModelGrid groupID={id} /></>}</QueryBoundary>
}
export function MarketReportPage() {
  const { id = '' } = useParams(), query = useMarketReport(id)
  return <QueryBoundary query={query} errorFallback="报告不存在或未公开">{report => <>
    <PageHeader title="检测报告" back={{ to: `/groups/${report.group_id}`, label: '分组' }} />
    <Notice tone={report.validity === 'current' ? 'info' : 'warning'}>{report.validity === 'current' ? '报告证据与当前配置匹配' : report.validity === 'partial' ? '部分场景的配置或检测范围已变化，仅仍有效的独立证据用于路由' : '配置或检测范围已变化，此报告不再提供当前能力支持证据'}</Notice>
    <p className="muted-copy">{new Date(report.updated_at).toLocaleString()} · 范围 {report.manifest_version} · {report.complete ? '已完成该版本全测' : '未完成全测'} · 实际 {report.usage.http} 次 HTTP / {report.usage.input_tokens + report.usage.output_tokens} tokens</p>
    {report.scenarios.map(scenario => <Card key={scenario.id} title={scenario.id.split('@')[0]}><p><Badge>{({ upstream_protocol: '上游协议', gateway_proxy: '平台代理', official_cli: '官方 CLI' })[scenario.layer] ?? scenario.layer}</Badge> <Badge tone={scenario.state === 'pass' ? 'success' : 'neutral'}>{status(scenario.state)}</Badge> {!scenario.current && !scenario.aggregate && <Badge tone="warning">当前证据失效</Badge>}</p><p className="muted-copy">{reasons[scenario.reason] ?? scenario.reason} · 预计 HTTP {scenario.estimated_http ?? '未知'} / 实际 {scenario.usage.http}</p><Disclosure title="原检测项与子断言">{scenario.endpoints.map(e=><p key={e.id} className="mono">{e.method} {e.path} · {e.proxy_supported?'平台代理已开放':'平台代理未开放'}</p>)}<p className="mono">{scenario.source_ids.join(' · ')}</p><ul>{scenario.assertions.map(a => <li key={a.id}><span className="mono">{a.label}</span> · {status(a.state)} · {reasons[a.reason] ?? a.reason}</li>)}</ul><p className="market-digest">范围 {scenario.scope_version} · 实现 {scenario.code_version}</p></Disclosure><DetectionObservations result={{ capacity: scenario.capacity ?? undefined, settings: scenario.settings ?? undefined, images: scenario.images ?? undefined, fingerprint: scenario.fingerprint ?? undefined }} /></Card>)}
    <Disclosure title="固定版本与本轮移出范围"><p className="market-digest">报告代码 {report.registry_version}</p>{report.sources.map(s => <p key={s.id}><a href={s.url} target="_blank" rel="noopener noreferrer">{s.id}</a> · {s.version}</p>)}{report.exclusions.map((e, i) => <p key={i}>{e.protocol} · {e.source_ids.join(' / ')} · {e.reason}</p>)}</Disclosure>
  </>}</QueryBoundary>
}
