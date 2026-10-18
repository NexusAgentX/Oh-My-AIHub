import { useState, type FormEvent, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { errorMessage } from '../api/query'
import { SharingNav } from '../sharing/components'
import { Badge, Button, ButtonLink, Card, Checkbox, ConfirmDialog, EmptyState, FormSection, Icon, IconButton, InlineError, Notice, PageHeader, PasswordField, QueryBoundary, SelectField, TextareaField, TextField, Tabs, SuccessMessage } from '../ui'
import { useGroupAction, useGroupEvents, useGroupChannel, useGroupChannels, useGroupSources, useSaveGroupChannel, type Choice, type GroupChannel, type GroupSource, type Overrides } from './queries'
import { ChannelStatsPanel, ChannelHealth } from '../channels/ChannelStatsPanel'
import { ChannelCalls } from '../channels/ChannelCalls'
import './group-channels.css'

const formatNames: Record<string, string> = { openai_chat: 'Chat', openai_responses: 'Responses', anthropic: 'Messages', gemini: 'GenerateContent', openai_images: 'Images' }
const probeReasons:Record<string,string>={upstream_rejected_probe:'上游拒绝请求',upstream_server_error:'上游服务错误',upstream_unreachable:'上游不可达',probe_cancelled_or_timed_out:'探针取消或超时',upstream_transport_error:'上游连接失败',outbound_validation_failed:'出站校验未通过',invalid_upstream_endpoint:'上游端点配置无效',invalid_probe_request:'探针请求配置无效',invalid_or_oversize_reply:'响应格式或大小无效',invalid_native_reply:'响应不符合原生协议'}
const sourceNames: Record<string, string> = { channel: '渠道', group: '分组', platform: '平台', model_route_or_platform: '模型路由／平台' }
export const emptyOverrides: Overrides = { user_agent: null, header_rules: null, concurrency_limit: null, rpm_limit: null, ttft_timeout_ms: null, request_timeout_ms: null, idle_timeout_ms: null, cooldown_failures: null, cooldown_seconds: null }

function ModelState({ model }: { model: GroupChannel['models'][number] }) {
  if (model.excluded) return <Badge>已排除</Badge>
  if (!model.enabled) return <Badge>已停用</Badge>
  if (model.probe_state === 'pending') return <Badge tone="info">待探测</Badge>
  return <Badge tone={model.probe_state === 'available' ? 'success' : 'warning'}>{model.probe_state === 'available' ? '可调用' : '不可调用'}</Badge>
}

export function GroupChannelsPage({ admin = false }: { admin?: boolean }) {
  const channels = useGroupChannels(admin)
  return <>
    <PageHeader title={admin ? '分组渠道治理' : '分组渠道'} actions={!admin && <ButtonLink to="/group-channels/new" variant="primary" icon={<Icon name="plus" />}>添加渠道</ButtonLink>} />
    {!admin && <SharingNav />}
    <QueryBoundary query={channels} errorFallback="渠道加载失败" isEmpty={(data) => data.items.length === 0} empty={<EmptyState title="还没有分组渠道" action={!admin && <ButtonLink to="/group-channels/new">添加渠道</ButtonLink>} />}>
      {(data) => <div className="channel-grid">{data.items.map((channel) => <Card key={channel.id} className="group-channel-card">
        <div className="group-channel-heading"><Link to={`${admin ? '/admin/group-channels' : '/group-channels'}/${channel.id}`}>{channel.name}</Link><Badge tone={channel.status === 'suspended' ? 'danger' : channel.available ? 'success' : 'warning'}>{channel.status === 'suspended' ? '已下架' : channel.status === 'unlisted' ? '未上架' : channel.available ? '可用' : '待处理'}</Badge></div>
        <p className="muted-copy">{channel.supplier_name} · {channel.group_name}</p>
        {admin && <p>提供者：{channel.owner.display_name}</p>}
        <dl className="channel-card-stats"><div><dt>可用模型</dt><dd className="num">{channel.models.filter((m) => m.enabled && !m.excluded && m.probe_state === 'available').length}</dd></div><div><dt>回退地址</dt><dd className="num">{channel.selected_address_ids.length}</dd></div></dl>
        {channel.suspended_reason && <Notice tone="danger">{channel.suspended_reason}</Notice>}
        {channel.reason === 'no_selected_address' && <Notice tone="warning">所选地址已移除，请重新选择地址。</Notice>}
      </Card>)}</div>}
    </QueryBoundary>
  </>
}
export function AdminGroupChannelsPage() { return <GroupChannelsPage admin /> }

type NumberKey = Exclude<keyof Overrides, 'user_agent' | 'header_rules'>
const numericFields: { key: NumberKey; label: string; min: number; max: number }[] = [
  { key: 'concurrency_limit', label: '并发上限', min: 1, max: 1073741824 }, { key: 'rpm_limit', label: '每分钟请求数', min: 1, max: 1073741824 },
  { key: 'ttft_timeout_ms', label: '首字超时（毫秒）', min: 1000, max: 600000 }, { key: 'request_timeout_ms', label: '请求超时（毫秒）', min: 1000, max: 3600000 }, { key: 'idle_timeout_ms', label: '空闲超时（毫秒）', min: 1000, max: 3600000 },
  { key: 'cooldown_failures', label: '连续失败次数', min: 1, max: 100 }, { key: 'cooldown_seconds', label: '冷却时长（秒）', min: 10, max: 86400 },
]

function Advanced({ value, defaults, onChange, sources }: { value: Overrides; defaults: Overrides; onChange: (v: Overrides) => void; sources?: Record<string, string> }) {
  const set = (patch: Partial<Overrides>) => onChange({ ...value, ...patch })
  const mode = value.header_rules === null ? 'inherit' : !value.header_rules.set?.length && !value.header_rules.remove?.length ? 'empty' : 'custom'
  const [custom, setCustom] = useState(mode === 'custom')
  const rules = value.header_rules ?? {}
  return <details className="group-channel-advanced"><summary>高级设置</summary><div className="group-advanced-fields">
    <p className="muted-copy">留空跟随分组当前默认值。超时再依次使用模型路由、平台设置。</p>
    <TextField label="User-Agent" value={value.user_agent ?? ''} maxLength={512} placeholder={defaults.user_agent ?? '平台默认'} onChange={(e) => set({ user_agent: e.target.value || null })} />
    <div className="field-row">{numericFields.map(({ key, label, min, max }) => <TextField key={key} label={label} type="number" min={min} max={max} step={1} value={value[key] ?? ''} placeholder={defaults[key] === null ? '继承' : String(defaults[key])} hint={sources?.[key] ? `已保存来源：${sourceNames[sources[key]]}` : undefined} onChange={(e) => set({ [key]: e.target.value === '' ? null : Number(e.target.value) })} />)}</div>
    <SelectField label="请求头规则" value={mode === 'empty' && custom ? 'custom' : mode} onChange={(e) => { setCustom(e.target.value === 'custom'); set({ header_rules: e.target.value === 'inherit' ? null : {} }) }}><option value="inherit">继承整个分组规则</option><option value="empty">明确清空规则</option><option value="custom">使用渠道规则</option></SelectField>
    {value.header_rules !== null && custom && <div className="group-header-editor">
      {(rules.set ?? []).map((rule, index) => <div className="header-rule" key={index}><TextField label="请求头名称" value={rule.name} onChange={(e) => set({ header_rules: { ...rules, set: rules.set?.map((r, i) => i === index ? { ...r, name: e.target.value } : r) } })} /><TextField label="请求头值" value={rule.value} onChange={(e) => set({ header_rules: { ...rules, set: rules.set?.map((r, i) => i === index ? { ...r, value: e.target.value } : r) } })} /><IconButton type="button" label="移除设置规则" icon={<Icon name="x" />} onClick={() => set({ header_rules: { ...rules, set: rules.set?.filter((_, i) => i !== index) } })} /></div>)}
      {(rules.remove ?? []).map((name, index) => <div className="header-rule header-rule-remove" key={index}><TextField label="要删除的请求头" value={name} onChange={(e) => set({ header_rules: { ...rules, remove: rules.remove?.map((r, i) => i === index ? e.target.value : r) } })} /><IconButton type="button" label="移除删除规则" icon={<Icon name="x" />} onClick={() => set({ header_rules: { ...rules, remove: rules.remove?.filter((_, i) => i !== index) } })} /></div>)}
      <div className="group-channel-actions"><Button size="sm" type="button" variant="secondary" disabled={(rules.set?.length ?? 0) >= 32} onClick={() => set({ header_rules: { ...rules, set: [...(rules.set ?? []), { name: '', value: '' }] } })}>设置请求头</Button><Button size="sm" type="button" variant="secondary" disabled={(rules.remove?.length ?? 0) >= 32} onClick={() => set({ header_rules: { ...rules, remove: [...(rules.remove ?? []), ''] } })}>删除请求头</Button></div>
    </div>}
  </div></details>
}

type Section = 'overview' | 'settings' | 'calls' | 'health'
function GroupHealth({id}:{id:string}) { const events=useGroupEvents(id);return <QueryBoundary query={events} errorFallback="健康事件加载失败">{(data)=><ChannelHealth channelId={id} events={data.items}/>}</QueryBoundary> }
function DetailTabs({section,onChange,children,enabled=true}:{section:Section;onChange:(value:Section)=>void;children:ReactNode;enabled?:boolean}) {return enabled?<Tabs label="渠道详情分区" value={section} onChange={onChange} items={[{key:'overview',label:'概览'},{key:'settings',label:'模型与配置'},{key:'calls',label:'调用记录'},{key:'health',label:'健康事件'}]}>{children}</Tabs>:<>{children}</>}
function DetailSection({channel,section,onSection}:{channel:GroupChannel;section:Section;onSection:(value:Section)=>void}) {return <>{section==='overview'&&<ChannelStatsPanel channelId={channel.id} onViewFailures={()=>onSection('health')}/>} {section==='calls'&&<ChannelCalls channelId={channel.id}/>} {section==='health'&&<GroupHealth id={channel.id}/>}</>}

function Editor({ sources, channel }: { sources: GroupSource[]; channel?: GroupChannel }) {
  const navigate = useNavigate()
  const [section,setSection]=useState<Section>(channel?'overview':'settings')
  const save = useSaveGroupChannel(channel?.id)
  const action = useGroupAction(channel?.id ?? '')
  const [baseline,setBaseline]=useState(channel)
  const [groupID, setGroupID] = useState(channel?.group_id ?? sources[0]?.id ?? '')
  const source = sources.find((g) => g.id === groupID)
  const [apiKey, setAPIKey] = useState('')
  const [name, setName] = useState(channel?.name ?? '')
  const [summary, setSummary] = useState(channel?.summary ?? '')
  const [description, setDescription] = useState(channel?.description ?? '')
  const [status, setStatus] = useState<'listed'|'unlisted'>(channel?.status==='unlisted'?'unlisted':'listed')
  const [addressIDs, setAddressIDs] = useState(channel?.selected_address_ids ?? (source?.addresses[0] ? [source.addresses[0].id] : []))
  const [overrides, setOverrides] = useState<Overrides>(channel?.overrides ?? emptyOverrides)
  const [choices, setChoices] = useState<Choice[]>(channel?.models.map((m) => ({ group_model_id: m.group_model_id, excluded: m.excluded, discount: m.discount })) ?? [])
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [localError, setLocalError] = useState('')
  const models = channel && channel.group_id === groupID ? channel.models.map((m) => ({ id: m.group_model_id, model_id: m.model_id, display_name: m.display_name, model_type: m.model_type })) : source?.models ?? []
  const setChoice = (id: string, patch: Partial<Choice>) => setChoices((previous) => { const current = previous.find((c) => c.group_model_id === id) ?? { group_model_id: id, excluded: false, discount: '1' }; return [...previous.filter((c) => c.group_model_id !== id), { ...current, ...patch }] })
  const reorder = (id: string, delta: number) => { const next = [...addressIDs]; const from = next.indexOf(id); [next[from], next[from + delta]] = [next[from + delta], next[from]]; setAddressIDs(next) }
  const dirty=Boolean(baseline&&(apiKey.trim()||groupID!==baseline.group_id||name!==baseline.name||summary!==baseline.summary||description!==baseline.description||(baseline.status!=='suspended'&&status!==baseline.status)||JSON.stringify(addressIDs)!==JSON.stringify(baseline.selected_address_ids)||JSON.stringify(overrides)!==JSON.stringify(baseline.overrides)||JSON.stringify(choices)!==JSON.stringify(baseline.models.map(m=>({group_model_id:m.group_model_id,excluded:m.excluded,discount:m.discount})))))
  async function submit(event: FormEvent) {
    event.preventDefault(); setLocalError('')
    if (!source || !addressIDs.length) { setLocalError('请选择生效分组和至少一个地址'); return }
    if ((!channel || channel.group_id !== groupID) && !apiKey.trim()) { setLocalError('请填写所选分组的 API Key'); return }
    try { const body = { group_id: groupID, name: name || undefined, summary, description, address_ids: addressIDs, overrides, models: choices, ...(apiKey.trim() ? { api_key: apiKey.trim() } : {}) }; const saved = await save.mutateAsync(channel ? { ...body, revision: baseline?.revision ?? channel.revision, ...(channel.status!=='suspended' ? { status } : {}) } : { ...body, api_key: apiKey.trim() }); setBaseline(saved);setName(saved.name);setSummary(saved.summary);setDescription(saved.description);setAddressIDs(saved.selected_address_ids);setOverrides(saved.overrides);setChoices(saved.models.map(m=>({group_model_id:m.group_model_id,excluded:m.excluded,discount:m.discount})));setAPIKey(''); void navigate(`/group-channels/${saved.id}`, { replace: !channel }) } catch { /* mutation exposes the safe API error */ }
  }
  const busy = save.isPending || action.isPending
  const orderedAddresses = [...(source?.addresses ?? channel?.addresses ?? [])].sort((a, b) => { const ai = addressIDs.indexOf(a.id), bi = addressIDs.indexOf(b.id); return (ai < 0 ? 100 : ai) - (bi < 0 ? 100 : bi) })
  return <>
    <DetailTabs enabled={Boolean(channel)} section={section} onChange={setSection}>{channel&&<DetailSection channel={channel} section={section} onSection={setSection}/>}
    <div hidden={section!=='settings'}>
    <form onSubmit={(e) => void submit(e)} className="group-channel-form">
      {channel&&baseline&&channel.revision!==baseline.revision&&<Notice tone="warning">配置版本已更新。重新载入后再保存，未保存的修改会被丢弃。<Button type="button" variant="quiet" onClick={()=>window.location.reload()}>重新载入</Button></Notice>}
      <Card title="渠道配置"><FormSection title="供应商分组">
        <SelectField label="生效分组" required value={groupID} onChange={(e) => { const g = sources.find((v) => v.id === e.target.value); setGroupID(e.target.value); setAddressIDs(g?.addresses[0] ? [g.addresses[0].id] : []); setChoices([]); setAPIKey('') }}><option value="" disabled>选择分组</option>{sources.map((g) => <option key={g.id} value={g.id}>{g.supplier_name} · {g.name}</option>)}</SelectField>
        {channel && channel.group_id !== groupID && <Notice tone="warning">更换分组需要填写该分组的 API Key。</Notice>}
        <PasswordField label="API Key" hint="自动探测会调用上游，费用按上游规则计算。" autoComplete="new-password" value={apiKey} required={!channel || channel.group_id !== groupID} placeholder={channel ? '留空保留现有 Key' : '填写上游 API Key'} onChange={(e) => setAPIKey(e.target.value)} />
        <TextField label="渠道名称" maxLength={64} placeholder={source?.name ?? '使用分组名称'} value={name} onChange={(e) => setName(e.target.value)} />
        <TextField label="一句话简介" maxLength={160} value={summary} onChange={(e) => setSummary(e.target.value)} />
        <TextareaField label="详细介绍" maxLength={8000} value={description} onChange={(e) => setDescription(e.target.value)} />
        {channel && <SelectField label="上架状态" value={channel.status==='suspended'?'suspended':status} disabled={channel.status === 'suspended'} onChange={(e) => setStatus(e.target.value as 'listed'|'unlisted')}><option value="listed">已上架</option><option value="unlisted">未上架</option>{channel.status === 'suspended' && <option value="suspended">管理员已下架</option>}</SelectField>}
      </FormSection>
      <FormSection title="地址与回退顺序" description="选择一个或多个地址，按所选顺序回退。新增地址需要手动勾选。">
        {orderedAddresses.length === 0 && <Notice tone="warning">当前分组没有可选地址。</Notice>}
        <ol className="group-addresses">{orderedAddresses.map((address) => { const index = addressIDs.indexOf(address.id); return <li key={address.id}><Checkbox label={<span>{address.label || '上游地址'}<span className="group-address-url mono">{address.url}</span></span>} checked={index >= 0} onChange={(e) => setAddressIDs(e.target.checked ? [...addressIDs, address.id] : addressIDs.filter((id) => id !== address.id))} /><div className="group-address-order">{index >= 0 && <span className="num">#{index + 1}</span>}<IconButton type="button" label={`上移${address.label || '地址'}`} disabled={index <= 0} icon={<Icon name="arrow-up" />} onClick={() => reorder(address.id, -1)} /><IconButton type="button" label={`下移${address.label || '地址'}`} disabled={index < 0 || index >= addressIDs.length - 1} icon={<Icon name="arrow-down" />} onClick={() => reorder(address.id, 1)} /></div></li> })}</ol>
        {channel?.reason === 'no_selected_address' && <Notice tone="warning">原选地址已移除；重新选择后才能恢复路由。</Notice>}
      </FormSection>
      <Advanced value={overrides} defaults={source?.defaults ?? emptyOverrides} sources={channel?.sources} onChange={setOverrides} />
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
      {save.isSuccess&&!dirty&&<SuccessMessage>配置已保存</SuccessMessage>}
      <InlineError>{localError || (save.isError ? errorMessage(save.error, '保存失败') : action.isError ? errorMessage(action.error, '操作失败') : '')}</InlineError>
      <div className="group-channel-actions"><Button loading={save.isPending} disabled={busy || !source || !addressIDs.length} type="submit">{channel ? '保存配置' : '创建渠道'}</Button>{channel && <Button type="button" variant="danger" disabled={busy} onClick={() => setDeleteOpen(true)}>删除渠道</Button>}</div>
    </form>
    </div></DetailTabs>
    {channel && <ConfirmDialog title="删除渠道" description="渠道将停止接收新调用，历史调用和账本保留。" open={deleteOpen} onClose={() => setDeleteOpen(false)} confirmLabel="删除渠道" danger busy={action.isPending} error={action.isError ? errorMessage(action.error, '删除失败') : ''} onConfirm={() => { void action.mutateAsync({ action: 'delete', revision: channel.revision }).then(() => navigate('/group-channels')).catch(() => {}) }} />}
  </>
}

export function GroupChannelEditorPage() {
  const { id = '' } = useParams()
  const detail = useGroupChannel(id)
  const sources = useGroupSources()
  return <><PageHeader title={id ? '渠道配置' : '添加分组渠道'} back={{ to: '/group-channels', label: '分组渠道' }} /><QueryBoundary query={sources} errorFallback="分组加载失败">{(data) => id ? <QueryBoundary query={detail} errorFallback="渠道加载失败">{(channel) => <Editor key={channel.id} channel={channel} sources={data.items} />}</QueryBoundary> : data.items.length ? <Editor sources={data.items} /> : <EmptyState title="还没有可用分组" action={<ButtonLink to="/contributions">贡献供应商分组</ButtonLink>} />}</QueryBoundary></>
}

function Governance({ channel }: { channel: GroupChannel }) {
  const action = useGroupAction(channel.id)
  const [section,setSection]=useState<Section>('overview')
  const [reason, setReason] = useState('')
  return <><DetailTabs section={section} onChange={setSection}><DetailSection channel={channel} section={section} onSection={setSection}/><div hidden={section!=='settings'}><Card title={channel.name}><p>{channel.supplier_name} · {channel.group_name} · {channel.owner.display_name}</p><details><summary>当前生效配置</summary><ol>{channel.selected_address_ids.map(id=>{const address=channel.addresses.find(a=>a.id===id);return address&&<li key={id}>{address.label} · <span className="mono">{address.url}</span></li>})}</ol><dl className="call-detail-grid"><div><dt>User-Agent</dt><dd>{channel.effective.user_agent??'平台默认'}</dd></div>{numericFields.map(({key,label})=><div key={key}><dt>{label}</dt><dd>{channel.effective[key]??sourceNames[channel.sources[key]]??'继承'}</dd></div>)}</dl></details>{channel.suspended_reason && <Notice tone="danger">{channel.suspended_reason}</Notice>}<ul className="group-models">{channel.models.map((m) => <li key={m.group_model_id}><strong className="mono">{m.model_id}</strong><ModelState model={m} /><span>折扣 {m.discount}</span></li>)}</ul></Card><Card title="渠道治理"><form onSubmit={(e) => { e.preventDefault(); action.mutate({ action: 'moderate', revision: channel.revision, reason: channel.status === 'suspended' ? '' : reason },{onSuccess:()=>setReason('')}) }}>{channel.status !== 'suspended' && <TextareaField label="下架原因" value={reason} required maxLength={1000} onChange={(e) => setReason(e.target.value)} />}<InlineError>{action.isError ? errorMessage(action.error, '操作失败') : ''}</InlineError><Button type="submit" loading={action.isPending} variant={channel.status === 'suspended' ? 'primary' : 'danger'}>{channel.status === 'suspended' ? '恢复上架' : '下架渠道'}</Button></form></Card></div></DetailTabs></>
}
export function AdminGroupChannelPage() { const { id = '' } = useParams(); const detail = useGroupChannel(id, true); return <><PageHeader title="分组渠道治理" back={{ to: '/admin/group-channels', label: '分组渠道' }} /><QueryBoundary query={detail} errorFallback="渠道加载失败">{(channel) => <Governance key={channel.id} channel={channel} />}</QueryBoundary></> }
