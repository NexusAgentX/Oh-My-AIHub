import { emptyOverrides } from '../settings/overrides'
import { Facts } from '../ui/Facts'
import { Advanced, headerMode, numericFields, type HeaderMode } from './SettingsFields'
import { SaveBar } from '../ui/SaveBar'
import { SectionNav } from '../ui/SectionNav'
import { useUnsavedGuard } from '../ui/UnsavedGuard'
import { useBack } from '../ui/urlState'
import { formatClockTime, rangeFrom } from '../money/time'
import { formatPoints } from '../money/format'
import { useShanghaiDay } from '../ui/clock'
import { useRevenue } from './RevenuePage'
import { useState, type FormEvent } from 'react'
import { Link, useLocation, useNavigate, useParams, useSearchParams, Navigate } from 'react-router-dom'
import { errorMessage } from '../api/query'
import { ApiError } from '../api/client'
import { DomainNav } from '../layouts/DomainNav'
import { Badge, Button, ButtonLink, Card, Checkbox, ConfirmDialog, EmptyState, FormSection, Icon, IconButton, InlineError, Notice, PageHeader, PasswordField, QueryBoundary, SelectField, TextareaField, TextField, Switch, Disclosure } from '../ui'
import { useGroupAction, useGroupEvents, useGroupChannel, useGroupChannels, useGroupSources, useSaveGroupChannel, type Choice, type GroupChannel, type GroupSource, type Overrides, probing } from './queries'
import { ChannelStatsPanel, ChannelHealth } from '../channels/ChannelStatsPanel'
import { ChannelCalls } from '../channels/ChannelCalls'
import './group-channels.css'

const formatNames: Record<string, string> = { openai_chat: 'Chat', openai_responses: 'Responses', anthropic: 'Messages', gemini: 'GenerateContent', openai_images: 'Images' }
const probeReasons:Record<string,string>={upstream_rejected_probe:'上游拒绝请求',upstream_server_error:'上游服务错误',upstream_unreachable:'上游不可达',probe_cancelled_or_timed_out:'探针取消或超时',upstream_transport_error:'上游连接失败',outbound_validation_failed:'出站校验未通过',invalid_upstream_endpoint:'上游端点配置无效',invalid_probe_request:'探针请求配置无效',invalid_or_oversize_reply:'响应格式或大小无效',invalid_native_reply:'响应不符合原生协议'}
const sourceNames: Record<string, string> = { channel: '渠道', group: '分组', platform: '平台', model_route_or_platform: '模型路由／平台' }

function ModelState({ model }: { model: GroupChannel['models'][number] }) {
  if (model.excluded) return <Badge>已排除</Badge>
  if (!model.enabled) return <Badge>已停用</Badge>
  if (model.probe_state === 'pending') return <Badge tone="info">待探测</Badge>
  return <Badge tone={model.probe_state === 'available' ? 'success' : 'warning'}>{model.probe_state === 'available' ? '可调用' : '不可调用'}</Badge>
}

function ListingSwitch({ channel }: { channel: GroupChannel }) {
  const save = useSaveGroupChannel(channel.id)
  return <div><Switch checked={channel.status === 'listed'} disabled={channel.status === 'suspended' || save.isPending} label={channel.status === 'listed' ? '已上架' : '未上架'} onChange={listed => save.mutate({ revision: channel.revision, status: listed ? 'listed' : 'unlisted' })} /><InlineError>{save.isError ? errorMessage(save.error, '保存失败，请重试') : ''}</InlineError></div>
}
export function GroupChannelsPage({ admin = false }: { admin?: boolean }) {
  useShanghaiDay()
  const channels = useGroupChannels(admin)
  const revenue = useRevenue({ from: rangeFrom('7d'), group_by: 'channel' }, !admin)
  return <>
    <PageHeader title={admin ? '渠道治理' : '我的渠道'} actions={!admin && <ButtonLink to="/supply/channels/new" variant="primary" icon={<Icon name="plus" />}>添加渠道</ButtonLink>} />
    {!admin && <DomainNav domain="供应" />}
    {!admin && revenue.isError && <Notice tone="warning">收入暂不可用，其余渠道状态照常显示。</Notice>}
    <QueryBoundary query={channels} errorFallback="渠道加载失败" isEmpty={data => data.items.length === 0} empty={<EmptyState title="还没有分组渠道" action={!admin && <ButtonLink to="/supply/channels/new">添加渠道</ButtonLink>} />}>
      {data => <div className="channel-grid">{data.items.map(channel => {
        const eligible = channel.models.filter(model => model.enabled && !model.excluded), available = eligible.filter(model => model.probe_state === 'available').length, failed = eligible.filter(model => model.probe_state === 'unavailable').length, pending = eligible.filter(model => model.probe_state === 'pending').length
        return <Card key={channel.id} className="group-channel-card">
          <div className="group-channel-heading"><Link to={`${admin ? '/admin/channels' : '/supply/channels'}/${channel.id}`}>{channel.name}</Link><Badge tone={channel.status === 'suspended' ? 'danger' : channel.available ? 'success' : 'warning'}>{channel.status === 'suspended' ? '已下架' : channel.status === 'unlisted' ? '未上架' : channel.available ? '可用' : '待处理'}</Badge></div>
          <p className="muted-copy">{channel.supplier_name} · {channel.group_name}</p>
          {admin ? <p>提供者：{channel.owner.display_name}</p> : <ListingSwitch channel={channel} />}
          <dl className="channel-card-stats"><div><dt>可用模型</dt><dd className="num">{available}/{eligible.length}</dd></div><div><dt>回退地址</dt><dd className="num">{channel.selected_address_ids.length}</dd></div>{!admin && <div><dt>7 天收入</dt><dd className="num">{revenue.data ? formatPoints(revenue.data.items.find(row => row.key === channel.id)?.charged ?? '0') : revenue.isError ? '暂不可用' : '—'}</dd></div>}</dl>
          {channel.suspended_reason && <Notice tone="danger">{channel.suspended_reason}</Notice>}
          {!channel.selected_address_ids.length && <p className="amount-negative">没有可用地址</p>}
          {!eligible.length && <p className="needs-action">没有启用的模型</p>}
          {failed > 0 && <p className="needs-action">{failed} 个模型探测失败</p>}
          {probing(channel) && <p className="muted-copy" role="status">探测中 {eligible.length - pending}/{eligible.length}</p>}
        </Card>
      })}</div>}
    </QueryBoundary>
  </>
}
export function AdminGroupChannelsPage() { return <GroupChannelsPage admin /> }

type Section = 'overview' | 'config' | 'calls' | 'health'
function sectionFor(path: string): Section { return path.endsWith('/config') ? 'config' : path.endsWith('/calls') ? 'calls' : path.endsWith('/health') ? 'health' : 'overview' }
function GroupHealth({ id }: { id: string }) { const events = useGroupEvents(id); return <QueryBoundary query={events} errorFallback="健康事件加载失败">{data => <ChannelHealth channelId={id} events={data.items} />}</QueryBoundary> }
function DetailNav({ id, dirty = false, admin = false }: { id: string; dirty?: boolean; admin?: boolean }) {
  const base = `${admin ? '/admin/channels' : '/supply/channels'}/${id}`
  return <SectionNav label="渠道详情分区" items={[{ to: base, label: '概览' }, { to: base + '/config', label: '配置', dirty }, { to: admin ? `/admin/calls?channel_id=${id}` : base + '/calls', label: '调用' }, { to: base + '/health', label: '健康' }]} />
}
function ChannelProblems({ channel }: { channel: GroupChannel }) {
  const eligible = channel.models.filter(model => model.enabled && !model.excluded)
  const failed = eligible.filter(model => model.probe_state === 'unavailable').length
  const pending = eligible.filter(model => model.probe_state === 'pending').length
  return <>{channel.suspended_reason && <Notice tone="danger">{channel.suspended_reason}</Notice>}{!channel.selected_address_ids.length && <Notice tone="warning">没有可用地址，请重新选择。<ButtonLink variant="quiet" to={`/supply/channels/${channel.id}/config`}>配置地址</ButtonLink></Notice>}{failed > 0 && <Notice tone="warning">{failed} 个模型探测失败<ButtonLink variant="quiet" to={`/supply/channels/${channel.id}/config`}>查看模型</ButtonLink></Notice>}{!eligible.length && <Notice tone="warning">没有启用的模型</Notice>}{probing(channel) && <p role="status" className="muted-copy">探测中 {eligible.length - pending}/{eligible.length}</p>}</>
}
function DetailSection({ channel, section }: { channel: GroupChannel; section: Section }) {
  const navigate = useNavigate()
  return <>{section === 'overview' && <><ChannelProblems channel={channel} /><ChannelStatsPanel channelId={channel.id} onViewFailures={() => navigate('health')} /></>}{section === 'calls' && <ChannelCalls channelId={channel.id} />}{section === 'health' && <GroupHealth id={channel.id} />}</>
}

function Editor({ sources, channel, onReload }: { sources: GroupSource[]; channel?: GroupChannel; onReload?: () => Promise<GroupChannel | undefined> }) {
  const navigate = useNavigate()
  const location = useLocation()
  const [search] = useSearchParams()
  const section = channel ? sectionFor(location.pathname) : 'config'
  const [savedAt, setSavedAt] = useState('')
  const [reloadOpen, setReloadOpen] = useState(false)
  const [reloading, setReloading] = useState(false)
  const [groupSearch, setGroupSearch] = useState('')
  const save = useSaveGroupChannel(channel?.id)
  const action = useGroupAction(channel?.id ?? '')
  const [baseline,setBaseline]=useState(channel)
  const [initialGroup] = useState(channel?.group_id ?? (sources.some(group => group.id === search.get('group')) ? search.get('group')! : ''))
  const [groupID, setGroupID] = useState(initialGroup)
  const availableSource = sources.find(g => g.id === groupID)
  const source: GroupSource | undefined = availableSource ?? (channel && channel.group_id === groupID ? { id: channel.group_id, name: channel.group_name, supplier_name: channel.supplier_name, revision: channel.group_revision, defaults: emptyOverrides, addresses: channel.addresses, models: channel.models.map(m => ({ id: m.group_model_id, model_id: m.model_id, display_name: m.display_name, model_type: m.model_type })) } : undefined)
  const [apiKey, setAPIKey] = useState('')
  const [name, setName] = useState(channel?.name ?? '')
  const [summary, setSummary] = useState(channel?.summary ?? '')
  const [description, setDescription] = useState(channel?.description ?? '')
  const [status, setStatus] = useState<'listed'|'unlisted'>(channel?.status==='unlisted'?'unlisted':'listed')
  const [addressIDs, setAddressIDs] = useState(channel?.selected_address_ids ?? (source?.addresses[0] ? [source.addresses[0].id] : []))
  const [overrides, setOverrides] = useState<Overrides>(channel?.overrides ?? emptyOverrides)
  const [headersMode, setHeadersMode] = useState<HeaderMode>(() => headerMode(channel?.overrides.header_rules ?? null))
  const [choices, setChoices] = useState<Choice[]>(channel?.models.map((m) => ({ group_model_id: m.group_model_id, excluded: m.excluded, discount: m.discount })) ?? [])
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [localError, setLocalError] = useState('')
  const models = channel && channel.group_id === groupID ? channel.models.map((m) => ({ id: m.group_model_id, model_id: m.model_id, display_name: m.display_name, model_type: m.model_type })) : source?.models ?? []
  const setChoice = (id: string, patch: Partial<Choice>) => setChoices((previous) => { const current = previous.find((c) => c.group_model_id === id) ?? { group_model_id: id, excluded: false, discount: '1' }; return [...previous.filter((c) => c.group_model_id !== id), { ...current, ...patch }] })
  const reorder = (id: string, delta: number) => { const next = [...addressIDs]; const from = next.indexOf(id); [next[from], next[from + delta]] = [next[from + delta], next[from]]; setAddressIDs(next) }
  const dirtyCount = baseline ? [Boolean(apiKey.trim()), groupID !== baseline.group_id, name !== baseline.name, summary !== baseline.summary, description !== baseline.description, baseline.status !== 'suspended' && status !== baseline.status, JSON.stringify(addressIDs) !== JSON.stringify(baseline.selected_address_ids), JSON.stringify(overrides) !== JSON.stringify(baseline.overrides), JSON.stringify(choices) !== JSON.stringify(baseline.models.map(m => ({ group_model_id: m.group_model_id, excluded: m.excluded, discount: m.discount }))), headersMode !== headerMode(baseline.overrides.header_rules)].filter(Boolean).length : [Boolean(apiKey || name || summary || description), groupID !== initialGroup, choices.length > 0, JSON.stringify(overrides) !== JSON.stringify(emptyOverrides)].filter(Boolean).length
  const dirty = dirtyCount > 0
  const busy = save.isPending || action.isPending || reloading
  const conflict = Boolean(channel && baseline && channel.revision !== baseline.revision) || (save.error instanceof ApiError && save.error.status === 409) || (action.error instanceof ApiError && action.error.status === 409)
  const allowNavigation = useUnsavedGuard(dirty, { scope: channel ? `/supply/channels/${channel.id}` : undefined, busy })
  const reset = (latest: GroupChannel) => { setBaseline(latest); setGroupID(latest.group_id); setName(latest.name); setSummary(latest.summary); setDescription(latest.description); setStatus(latest.status === 'unlisted' ? 'unlisted' : 'listed'); setAddressIDs(latest.selected_address_ids); setOverrides(latest.overrides); setHeadersMode(headerMode(latest.overrides.header_rules)); setChoices(latest.models.map(m => ({ group_model_id: m.group_model_id, excluded: m.excluded, discount: m.discount }))); setAPIKey(''); setLocalError(''); save.reset(); action.reset() }
  async function submit(event: FormEvent) {
    event.preventDefault(); setLocalError('')
    if (conflict) { setLocalError('配置已被更新，请先载入最新配置。'); return }
    if (!source || !addressIDs.length) { setLocalError('请选择生效分组和至少一个地址'); return }
    if ((!channel || channel.group_id !== groupID) && !apiKey.trim()) { setLocalError('请填写所选分组的 API Key'); return }
    if (headersMode === 'custom' && !overrides.header_rules?.set?.length && !overrides.header_rules?.remove?.length) { setLocalError('请添加请求头规则，或选择明确不设。'); return }
    try {
      const body = { group_id: groupID, name: name || undefined, summary, description, address_ids: addressIDs, overrides, models: choices, ...(apiKey.trim() ? { api_key: apiKey.trim() } : {}) }
      const saved = await save.mutateAsync(channel ? { ...body, revision: baseline?.revision ?? channel.revision, ...(channel.status !== 'suspended' ? { status } : {}) } : { ...body, api_key: apiKey.trim() })
      reset(saved); setSavedAt(formatClockTime(new Date()))
      if (!channel) { allowNavigation(); navigate(`/supply/channels/${saved.id}/config`, { replace: true, state: location.state?.from ? { from: location.state.from } : null }) }
    } catch { /* mutation exposes the safe API error */ }
  }
  const orderedAddresses = [...(source?.addresses ?? channel?.addresses ?? [])].sort((a, b) => { const ai = addressIDs.indexOf(a.id), bi = addressIDs.indexOf(b.id); return (ai < 0 ? 100 : ai) - (bi < 0 ? 100 : bi) })
  return <>
    {channel && <><DetailNav id={channel.id} dirty={dirty} /><DetailSection channel={channel} section={section} /></>}
    <div hidden={section !== 'config'}>
    <form onSubmit={(e) => void submit(e)} className="group-channel-form">
      {channel && <SaveBar dirtyCount={dirtyCount} busy={busy} disabled={conflict || !source || !addressIDs.length} savedAt={savedAt} onDiscard={() => { if (conflict) setReloadOpen(true); else if (baseline) reset(baseline) }} />}
      {conflict && <Notice tone="warning">配置版本已更新，请载入最新配置后再保存。<Button type="button" variant="quiet" onClick={() => setReloadOpen(true)}>载入最新</Button></Notice>}
      <InlineError>{localError || (save.isError ? errorMessage(save.error, '保存失败') : action.isError ? errorMessage(action.error, '操作失败') : '')}</InlineError>
      <Card title="渠道配置"><FormSection title="供应商分组">
        <TextField label="检索分组" value={groupSearch} placeholder="供应商或分组名称" onChange={event => setGroupSearch(event.target.value)} />
        <SelectField label="生效分组" required value={groupID} onChange={(e) => { const g = sources.find((v) => v.id === e.target.value); setGroupID(e.target.value); setAddressIDs(g?.addresses[0] ? [g.addresses[0].id] : []); setChoices([]); setAPIKey('') }}><option value="" disabled>选择分组</option>{(source && !availableSource ? [source, ...sources] : sources).filter(g => g.id === groupID || `${g.supplier_name} ${g.name}`.toLowerCase().includes(groupSearch.trim().toLowerCase())).map((g) => <option key={g.id} value={g.id}>{g.supplier_name} · {g.name}</option>)}</SelectField>
        {channel && channel.group_id !== groupID && <Notice tone="warning">更换分组需要填写该分组的 API Key。</Notice>}
        <PasswordField label="API Key" hint="自动探测会调用上游，费用按上游规则计算。" autoComplete="new-password" value={apiKey} required={!channel || channel.group_id !== groupID} placeholder={channel ? '留空保留现有 Key' : '填写上游 API Key'} onChange={(e) => setAPIKey(e.target.value)} />
        {channel && <SelectField label="上架状态" value={channel.status==='suspended'?'suspended':status} disabled={channel.status === 'suspended'} onChange={(e) => setStatus(e.target.value as 'listed'|'unlisted')}><option value="listed">已上架</option><option value="unlisted">未上架</option>{channel.status === 'suspended' && <option value="suspended">管理员已下架</option>}</SelectField>}
      </FormSection>
      <FormSection title="地址与回退顺序" description="选择一个或多个地址，按所选顺序回退。新增地址需要手动勾选。">
        {orderedAddresses.length === 0 && <Notice tone="warning">当前分组没有可选地址。</Notice>}
        <ol className="group-addresses">{orderedAddresses.map((address) => { const index = addressIDs.indexOf(address.id); return <li key={address.id}><Checkbox label={<span>{address.label || '上游地址'}<span className="group-address-url mono">{address.url}</span></span>} checked={index >= 0} onChange={(e) => setAddressIDs(e.target.checked ? [...addressIDs, address.id] : addressIDs.filter((id) => id !== address.id))} /><div className="group-address-order">{index >= 0 && <span className="num">#{index + 1}</span>}<IconButton type="button" label={`上移${address.label || '地址'}`} disabled={index <= 0} icon={<Icon name="arrow-up" />} onClick={() => reorder(address.id, -1)} /><IconButton type="button" label={`下移${address.label || '地址'}`} disabled={index < 0 || index >= addressIDs.length - 1} icon={<Icon name="arrow-down" />} onClick={() => reorder(address.id, 1)} /></div></li> })}</ol>
        {channel?.reason === 'no_selected_address' && <Notice tone="warning">原选地址已移除；重新选择后才能恢复路由。</Notice>}
      </FormSection>
        <TextField label="渠道名称" maxLength={64} placeholder={source?.name ?? '使用分组名称'} value={name} onChange={(e) => setName(e.target.value)} />
        <TextField label="一句话简介" maxLength={160} value={summary} onChange={(e) => setSummary(e.target.value)} />
        <TextareaField label="详细介绍" maxLength={8000} value={description} onChange={(e) => setDescription(e.target.value)} />
      <Advanced defaultsKnown={Boolean(availableSource)} value={overrides} defaults={source?.defaults ?? emptyOverrides} mode={headersMode} onModeChange={setHeadersMode} onChange={setOverrides} />
      </Card>
      <Card title="模型与折扣" actions={channel && <Button size="sm" type="button" variant="secondary" disabled={busy||dirty||baseline?.revision!==channel.revision} title={dirty?'请先保存修改后再探测':undefined} onClick={() => action.mutate({ action: 'test', revision: baseline?.revision??channel.revision },{onSuccess:(value)=>{if(value)setBaseline(value)}})}>重新探测已保存 Key</Button>}>
        <p className="muted-copy">新模型自动探测。排除项会保留，折扣作用于该模型的整张分组价表。</p>
        {models.length === 0 ? <EmptyState title="当前分组没有模型" /> : <ul className="group-models">{models.map((model) => { const c = choices.find((choice) => choice.group_model_id === model.id); const tested = channel?.models.find((m) => m.group_model_id === model.id); return <li key={model.id}>
          <div className="group-model-main"><Checkbox checked={!c?.excluded} label={<strong className="mono">{model.model_id}</strong>} onChange={(e) => setChoice(model.id, { excluded: !e.target.checked })} />{tested && <ModelState model={tested} />}{model.display_name !== model.model_id && <span className="muted-copy">{model.display_name}</span>}</div>
          <TextField label="折扣" aria-label={`${model.model_id} 折扣`} inputMode="decimal" type="number" min={0} max={1000} step="0.000000001" value={c?.discount ?? '1'} onChange={(e) => setChoice(model.id, { discount: e.target.value })} />
          {tested && <div className="group-probe-results">{Object.entries(tested.format_tests).map(([format, test]) => <span key={format} className={test.ok ? 'amount-positive' : 'muted'}>{formatNames[format] ?? format}：{test.ok ? '通过' : probeReasons[test.error??''] || test.error || '未通过'}</span>)}</div>}
        </li> })}</ul>}
      </Card>
      {channel?.suspended_reason && <Notice tone="danger">{channel.suspended_reason}</Notice>}
      <div className="group-channel-actions">{!channel && <Button loading={save.isPending} disabled={busy || !source || !addressIDs.length} type="submit">创建渠道</Button>}{channel && <Button type="button" variant="danger" disabled={busy} onClick={() => setDeleteOpen(true)}>删除渠道</Button>}</div>
    </form>
    </div>
    <ConfirmDialog open={reloadOpen} busy={reloading} title="载入最新配置？" error={localError} description="未保存的修改将被丢弃。" confirmLabel="载入最新" onClose={() => setReloadOpen(false)} onConfirm={() => { setReloading(true); void onReload?.().then(latest => { if (latest) { reset(latest); setReloadOpen(false) } else setLocalError('载入失败，请重试') }).catch(error => setLocalError(errorMessage(error, '载入失败，请重试'))).finally(() => setReloading(false)) }} />
    {channel && <ConfirmDialog title="删除渠道" description="渠道将停止接收新调用，历史调用和账本保留。" open={deleteOpen} onClose={() => setDeleteOpen(false)} confirmLabel="删除渠道" danger busy={action.isPending} error={action.isError ? errorMessage(action.error, '删除失败') : ''} onConfirm={() => { void action.mutateAsync({ action: 'delete', revision: channel.revision }).then(() => { allowNavigation(); navigate('/supply/channels') }).catch(() => {}) }} />}
  </>
}

export function GroupChannelEditorPage() {
  const { id = '' } = useParams(), location = useLocation()
  const detail = useGroupChannel(id), sources = useGroupSources()
  const back = useBack('/supply/channels', '我的渠道')
  return <><PageHeader title={id ? sectionFor(location.pathname) === 'config' ? '渠道配置' : detail.data?.name || '渠道详情' : '添加渠道'} back={back} />{id ? <QueryBoundary query={detail} errorFallback="渠道加载失败">{channel => <>{sources.isError && <Notice tone="warning">分组选项加载失败，仍可修改当前配置。<Button variant="quiet" onClick={() => void sources.refetch()}>重试选项</Button></Notice>}<Editor key={channel.id} channel={channel} sources={sources.data?.items ?? []} onReload={async () => (await detail.refetch({ throwOnError: true })).data} /></>}</QueryBoundary> : <QueryBoundary query={sources} errorFallback="分组加载失败">{data => data.items.length ? <Editor sources={data.items} /> : <EmptyState title="还没有可用分组" action={<ButtonLink to="/supply/contributions">贡献供应商分组</ButtonLink>} />}</QueryBoundary>}</>

}

function Governance({ channel }: { channel: GroupChannel }) {
  const action = useGroupAction(channel.id), location = useLocation(), navigate = useNavigate()
  const section = sectionFor(location.pathname)
  const [reason, setReason] = useState(''), [open, setOpen] = useState(false)
  const suspended = channel.status === 'suspended'
  return <>
    <div className="group-channel-heading"><p>{channel.supplier_name} · {channel.group_name} · {channel.owner.display_name}</p><Button type="button" variant={suspended ? 'secondary' : 'danger'} onClick={() => setOpen(true)}>{suspended ? '恢复上架' : '下架渠道'}</Button></div>
    <DetailNav id={channel.id} admin />
    {section === 'overview' && <><div>{channel.suspended_reason && <Notice tone="danger">{channel.suspended_reason}</Notice>}</div><ChannelStatsPanel channelId={channel.id} onViewFailures={() => navigate(`/admin/channels/${channel.id}/health`)} /></>}
    {section === 'health' && <GroupHealth id={channel.id} />}
    {section === 'config' && <Card title="当前生效配置"><ol>{channel.selected_address_ids.map(id => { const address = channel.addresses.find(a => a.id === id); return address && <li key={id}>{address.label} · <span className="mono">{address.url}</span></li> })}</ol><Facts items={[{ label: 'User-Agent', value: <>{channel.effective.user_agent ?? '继承'} <Badge>{sourceNames[channel.sources.user_agent] ?? '来源未知'}</Badge></> }, ...numericFields.map(({ key, label }) => ({ label, value: <><span className="num">{channel.effective[key] ?? (key === 'concurrency_limit' || key === 'rpm_limit' ? '不限' : '继承')}</span> <Badge>{sourceNames[channel.sources[key]] ?? '来源未知'}</Badge></> }))]} /><Disclosure title="请求头规则"><p className="muted-copy">来源：{sourceNames[channel.sources.header_rules]}</p>{channel.effective.header_rules?.set?.map(rule => <p key={rule.name}>{rule.name}: {rule.value}</p>)}{channel.effective.header_rules?.remove?.map(name => <p key={name}>删除 {name}</p>)}</Disclosure><ul className="group-models">{channel.models.map(model => <li key={model.group_model_id}><strong className="mono">{model.model_id}</strong><ModelState model={model} /><span>折扣 {model.discount}</span></li>)}</ul></Card>}
    <ConfirmDialog open={open} busy={action.isPending} title={suspended ? '恢复渠道上架？' : '下架此渠道？'} confirmLabel={suspended ? '恢复上架' : '下架渠道'} confirmDisabled={!suspended && !reason.trim()} danger={!suspended} onClose={() => setOpen(false)} onConfirm={() => action.mutate({ action: 'moderate', revision: channel.revision, reason: suspended ? '' : reason.trim() }, { onSuccess: () => { setReason(''); setOpen(false) } })} error={action.isError ? errorMessage(action.error, '操作失败') : ''}>{!suspended && <TextareaField label="下架原因" required maxLength={1000} value={reason} onChange={event => setReason(event.target.value)} />}</ConfirmDialog>
  </>
}
export function AdminGroupChannelPage() {
  const { id = '' } = useParams(), location = useLocation(), detail = useGroupChannel(id, true)
  const back = useBack('/admin/channels', '渠道治理')
  if (sectionFor(location.pathname) === 'calls') return <Navigate replace to={`/admin/calls?channel_id=${id}`} />
  return <><PageHeader title={detail.data?.name || '渠道治理'} back={back} /><QueryBoundary query={detail} errorFallback="渠道加载失败">{channel => <Governance key={channel.id} channel={channel} />}</QueryBoundary></>
}
