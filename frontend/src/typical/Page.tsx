import { useEffect, useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import { formatDateTime } from '../admin/format'
import { Button, Card, EmptyState, InlineError, Notice, PageHeader, QueryBoundary, SelectField, SuccessMessage, TextField } from '../ui'
import { useRefreshTypicalCosts, useTypicalCosts, useTypicalSettings, useUpdateTypicalSettings, type TypicalConfig, type TypicalEstimate } from './queries'

const fields = [
  ['window_days', '样本窗口（天）', 1, 90],
  ['min_samples', '分组最少样本', 1, 20000],
  ['interval_seconds', '更新间隔（秒）', 30, 86400],
  ['typical_tokens', '典型 token 数', 1, 1000000000],
  ['typical_requests', '典型请求数', 1, 1000000],
  ['typical_images', '典型图片数', 1, 1000000],
] as const
const reasons: Record<string, string> = { no_samples: '无可靠样本', missing_reliable_conversion: '缺少用量换算依据', unpriced_workload_component: '工作负载有未计价分量', no_reference: '无原厂参考价', zero_reference: '原厂参考价为零', reference_workload_unpriced: '参考价未覆盖全部用量', estimate_overflow: '费用超过估算范围' }
const sources: Record<string, string> = { group_current_quote: '本分组当前报价样本', model_site_fallback: '站内同模型结构推算', site_active_groups_weighted: '站内有效分组加权', site_no_samples: '站内暂无样本' }
function SettingsEditor({ value }: { value: TypicalConfig }) {
  const [form, setForm] = useState(value)
  const [saved, setSaved] = useState(false)
  useEffect(() => { setForm(value) }, [value])
  const update = useUpdateTypicalSettings()
  const valid = fields.every(([key, , min, max]) => Number.isSafeInteger(form[key]) && form[key] >= min && form[key] <= max)
  const submit = (event: FormEvent) => { event.preventDefault(); if (valid) update.mutate(form, { onSuccess: (data) => { setForm(data); setSaved(true) } }) }
  return <Card title="估算参数"><form className="stack-form" onSubmit={submit}>
    {[0, 2, 4].map((start) => <div className="field-row" key={start}>{fields.slice(start, start + 2).map(([key, label, min, max]) => <TextField key={key} label={label} type="number" min={min} max={max} step={1} required value={form[key]} onChange={(event) => { setSaved(false); setForm((old) => ({ ...old, [key]: Number(event.target.value) })) }} />)}</div>)}
    <p className="muted-copy">参数版本 {form.version} · 报价变更后重新累计分组样本，不足时采用站内同模型结构。</p>
    <InlineError>{!valid ? '请输入范围内的正整数。' : update.isError ? errorMessage(update.error, '保存失败') : ''}</InlineError>
    <SuccessMessage>{saved ? '已保存，正在更新估算' : ''}</SuccessMessage>
    <div className="form-actions"><Button loading={update.isPending} disabled={!valid}>保存参数</Button></div>
  </form></Card>
}
export function EstimateCard({ item }: { item: TypicalEstimate }) {
  const unit = item.unit === 'successful_images' ? '张成功图片' : 'tokens'
  const name = item.level === 'channel' ? item.channel_name : item.level === 'group' ? item.group_name : '站内整体'
  return <Card title={`${name || (item.level === 'channel' ? '渠道' : '分组')} · ${item.model_id}`}>
    {item.supplier_name && <p className="muted-copy">{item.supplier_name}</p>}
    <dl className="detail-list compact-list">
      <div><dt>典型费用 / {item.quantity.toLocaleString()} {unit}</dt><dd>{item.cost === null ? '未知' : `${item.cost} 积分`}</dd></div>
      <div><dt>手续费（另计）</dt><dd>{item.fee === null ? '未知' : `${item.fee} 积分`}</dd></div>
      <div><dt>原厂参考倍率</dt><dd>{item.ratio === null ? '未知' : `${Number(item.ratio).toLocaleString(undefined, { maximumFractionDigits: 4 })}×`}</dd></div>
      <div><dt>{item.request_quantity.toLocaleString()} 次请求估算</dt><dd>{item.request_cost === null ? '未知' : `${item.request_cost} 积分`}</dd></div>
      <div><dt>样本 / 当前分组</dt><dd>{item.samples} / {item.own_samples}</dd></div>
      <div><dt>缓存读占提示侧 token</dt><dd>{item.cache_read_share === null ? '未知' : `${(Number(item.cache_read_share) * 100).toLocaleString(undefined, { maximumFractionDigits: 2 })}%`}</dd></div>
    </dl>
    <p className="muted-copy">{sources[item.source] || item.source}{item.quote_version ? ` · 报价版本 ${item.quote_version}` : ''}</p>
    {item.reason && <Notice tone={item.cost === null ? 'warning' : 'info'}>{reasons[item.reason] || '暂时无法估算'}</Notice>}
  </Card>
}
export function TypicalCostsPage() {
  const settings = useTypicalSettings()
  const refresh = useRefreshTypicalCosts()
  const [level, setLevel] = useState('group')
  const [draft, setDraft] = useState('')
  const [model, setModel] = useState('')
  const [cursor, setCursor] = useState('')
  const costs = useTypicalCosts(level, model, cursor)
  return <>
    <PageHeader title="典型费用" description="按可靠业务样本估算，用于报价比较。实际账单按每次可靠用量结算。" actions={<Button variant="secondary" loading={refresh.isPending} disabled={costs.data?.status.running || costs.data?.status.pending} onClick={() => refresh.mutate()}>立即更新</Button>} />
    <InlineError>{refresh.isError ? errorMessage(refresh.error, '请求更新失败') : ''}</InlineError>
    <QueryBoundary query={settings}>{(data) => <SettingsEditor value={data} />}</QueryBoundary>
    <Card title="估算结果"><form className="stack-form" onSubmit={(event) => { event.preventDefault(); setModel(draft.trim()); setCursor('') }}>
      <div className="field-row"><SelectField label="层级" value={level} onChange={(event) => { setLevel(event.target.value); setCursor('') }}><option value="model">模型站内整体</option><option value="group">分组模型</option><option value="channel">渠道模型</option></SelectField><TextField label="模型 ID" value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="全部模型" /></div>
      <div className="form-actions"><Button variant="secondary">筛选</Button></div>
    </form></Card>
    <QueryBoundary query={costs}>{(page) => <>
      {(page.status.running || page.status.pending) && <Notice>正在更新，期间保留上次结果。</Notice>}
      {page.status.last_error && <Notice tone="warning">{page.status.saturated ? '统计容量已满，请缩短样本窗口。' : '更新暂未完成，稍后自动重试。'} 上次估算仍可查看。</Notice>}
      {page.snapshot && <p className="muted-copy">窗口 {page.snapshot.config.window_days} 天 · 1 美元 = {page.snapshot.usd_cny_rate_nano / 1e9} 积分 · 更新于 {formatDateTime(page.snapshot.updated_at)} · 数据水位 {formatDateTime(page.snapshot.watermark)}</p>}
      {page.items.length ? <div className="stack-form">{page.items.map((item) => <EstimateCard key={`${item.level}-${item.model_id}-${item.group_id}-${item.channel_id}`} item={item} />)}</div> : <EmptyState title="暂无估算结果" description="有可靠业务样本后自动计算；没有样本时费用保持未知。" />}
      <div className="form-actions"><Button variant="secondary" disabled={!cursor} onClick={() => setCursor('')}>第一页</Button><Button variant="secondary" disabled={!page.next_cursor} onClick={() => setCursor(page.next_cursor || '')}>下一页</Button></div>
    </>}</QueryBoundary>
  </>
}
