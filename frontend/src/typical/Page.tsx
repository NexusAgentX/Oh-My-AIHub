import { EstimateCard } from './EstimateCard'
import { ApiError } from '../api/client'
import { SaveBar } from '../ui/SaveBar'
import { useUnsavedGuard } from '../ui/UnsavedGuard'
import { formatClockTime } from '../money/time'
import { SettingsNav } from '../admin/SectionNav'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import { formatDateTime } from '../money/time'
import { Button, Card, ConfirmDialog, EmptyState, InlineError, Notice, PageHeader, QueryBoundary, SelectField, TextField } from '../ui'
import { useRefreshTypicalCosts, useTypicalCosts, useTypicalSettings, useUpdateTypicalSettings, type TypicalConfig } from './queries'

const fields = [
  ['window_days', '样本窗口（天）', 1, 90],
  ['min_samples', '分组最少样本', 1, 20000],
  ['interval_seconds', '更新间隔（秒）', 30, 86400],
  ['typical_tokens', '典型 token 数', 1, 1000000000],
  ['typical_requests', '典型请求数', 1, 1000000],
  ['typical_images', '典型图片数', 1, 1000000],
] as const
function SettingsEditor({ value, reload }: { value: TypicalConfig; reload: () => Promise<TypicalConfig | undefined> }) {
  const [reloadOpen, setReloadOpen] = useState(false), [reloading, setReloading] = useState(false), [reloadError, setReloadError] = useState('')
  const lastRemote = useRef(value)
  const [form, setForm] = useState(value)
  const [saved, setSaved] = useState(''), [baseline, setBaseline] = useState(value)
  const dirtyCount = fields.filter(([key]) => form[key] !== baseline[key]).length
  const update = useUpdateTypicalSettings()
  const busy = update.isPending || reloading, conflict = value.version > baseline.version || update.error instanceof ApiError && update.error.status === 409
  useUnsavedGuard(dirtyCount > 0, { busy })
  useEffect(() => { if (lastRemote.current === value) return; lastRemote.current = value; if (!dirtyCount && !busy && value.version >= baseline.version) { setForm(value); setBaseline(value) } }, [value, dirtyCount, busy, baseline.version])
  const valid = fields.every(([key, , min, max]) => Number.isSafeInteger(form[key]) && form[key] >= min && form[key] <= max)
  const submit = (event: FormEvent) => { event.preventDefault(); if (valid && !busy && !conflict) update.mutate(form, { onSuccess: (data) => { setForm(data); setBaseline(data); setSaved(formatClockTime(new Date())) } }) }
  return <Card title="估算参数">{conflict && <Notice tone="warning">配置已被更新。<Button variant="quiet" onClick={() => setReloadOpen(true)}>载入最新</Button></Notice>}<form className="stack-form" onSubmit={submit}><SaveBar dirtyCount={dirtyCount} busy={busy} disabled={!valid || conflict} savedAt={saved} onDiscard={() => { if (conflict) setReloadOpen(true); else { setForm(baseline); update.reset() } }} /><fieldset className="flat-fieldset stack-form" disabled={busy}>
    {[0, 2, 4].map((start) => <div className="field-row" key={start}>{fields.slice(start, start + 2).map(([key, label, min, max]) => <TextField key={key} label={label} type="number" min={min} max={max} step={1} required value={form[key]} onChange={(event) => { setSaved(''); setForm((old) => ({ ...old, [key]: Number(event.target.value) })) }} />)}</div>)}
    <p className="muted-copy">参数版本 {form.version} · 报价变更后重新累计分组样本，不足时采用站内同模型结构。</p>
    <InlineError>{!valid ? '请输入范围内的正整数。' : update.isError ? errorMessage(update.error, '保存失败') : ''}</InlineError>
  </fieldset></form><ConfirmDialog open={reloadOpen} busy={reloading} error={reloadError} title="载入最新参数？" description="未保存的修改将被丢弃。" confirmLabel="载入最新" onClose={() => setReloadOpen(false)} onConfirm={() => { setReloading(true); setReloadError(''); void reload().then(latest => { if (!latest) { setReloadError('载入失败，请重试'); return }; setForm(latest); setBaseline(latest); lastRemote.current = latest; update.reset(); setReloadOpen(false) }).catch(error => setReloadError(errorMessage(error, '载入失败，请重试'))).finally(() => setReloading(false)) }} /></Card>
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
    <SettingsNav />
    <QueryBoundary query={settings}>{(data) => <SettingsEditor value={data} reload={async () => (await settings.refetch({ throwOnError: true })).data} />}</QueryBoundary>
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
