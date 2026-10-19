import { useEffect, useState } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom'
import { errorMessage } from '../api/query'
import type { RoutingPreference } from '../api/types'
import { routingModeLabels } from '../calls'
import { useMarketModel, useMarketNames } from '../market/queries'
import { RoutingEditor } from '../models/RoutingEditor'
import { draftFromPreference, sameDraft, toRoutingInput } from '../models/routing'
import { Badge, Button, ButtonLink, ConfirmDialog, Drawer, InlineError, QueryBoundary, SuggestInput } from '../ui'
import { SaveBar } from '../ui/SaveBar'
import { useUnsavedGuard } from '../ui/UnsavedGuard'
import { useKey, useKeyRouting } from './queries'

function ModelForm({ keyId, preference, channels, saved, onClose, onBusy }: {
  keyId: string; preference: RoutingPreference; channels: Parameters<typeof RoutingEditor>[0]['channels']; saved: boolean; onClose: () => void; onBusy: (busy: boolean) => void
}) {
  const [base, setBase] = useState(() => draftFromPreference(preference, channels))
  const [draft, setDraft] = useState(base)
  const [exists, setExists] = useState(saved)
  const [confirmRemove, setConfirmRemove] = useState(false)
  const { set, remove } = useKeyRouting(keyId)
  useEffect(() => onBusy(set.isPending || remove.isPending), [set.isPending, remove.isPending, onBusy])
  const dirty = !exists || !sameDraft(draft, base)
  const allowNavigation = useUnsavedGuard(dirty, { busy: set.isPending || remove.isPending })
  const error = set.error ?? remove.error
  return <div className="stack-form">
    <p className="muted-copy">{exists ? '覆盖账号设置' : '从账号设置开始，保存后仅对这把 Key 生效'}</p>
    <SaveBar dirtyCount={dirty ? 1 : 0} busy={set.isPending} onDiscard={() => { setDraft(base); if (!exists) { allowNavigation(); onClose() } }} onSave={() => set.mutate({ model: preference.model_id, preference: toRoutingInput(draft) }, { onSuccess: result => { const latest = draftFromPreference(result, channels); setBase(latest); setDraft(latest); setExists(true) } })} />
    <InlineError>{error ? errorMessage(error, '保存失败，请重试') : ''}</InlineError>
    <RoutingEditor channels={channels} draft={draft} onChange={setDraft} />
    {exists && <Button type="button" variant="danger" onClick={() => setConfirmRemove(true)}>移除覆盖，跟随账号</Button>}
    <ConfirmDialog open={confirmRemove} busy={remove.isPending} title="移除此模型的覆盖？" confirmLabel="跟随账号" onClose={() => setConfirmRemove(false)} onConfirm={() => remove.mutate(preference.model_id, { onSuccess: () => { allowNavigation(); onClose() } })} error={remove.isError ? errorMessage(remove.error, '移除失败') : ''} />
  </div>
}
export function KeyRoutingDrawer() {
  const { id = '', model = '' } = useParams(), detail = useKey(id), query = useMarketModel(model, new URLSearchParams())
  const navigate = useNavigate(), location = useLocation()
  const [busy, setBusy] = useState(false)
  const close = () => { if (location.state?.overlayFrom === `/keys/${id}`) navigate(-1); else navigate(`/keys/${id}`, { replace: true }) }
  return <Drawer open busy={busy} title={model} onClose={close}><QueryBoundary query={detail} errorFallback="Key 加载失败">{key => <QueryBoundary query={query} errorFallback="模型与渠道加载失败">{data => {
    const preference = key.routing.find(item => item.model_id === model)
    return <ModelForm key={model} keyId={id} saved={Boolean(preference)} preference={preference ?? data.routing} channels={data.routing_channels} onClose={close} onBusy={setBusy} />
  }}</QueryBoundary>}</QueryBoundary></Drawer>
}

type Removal = { model: string; status: 'pending' | 'success' | 'error'; error?: string }
/** 覆盖是独立对象；列表只负责进入单模型编辑器与报告批量操作结果。 */
export function KeyRoutingSection({ keyId, routing }: { keyId: string; routing: RoutingPreference[] }) {
  const models = useMarketNames(), { remove } = useKeyRouting(keyId)
  const [adding, setAdding] = useState(''), [confirm, setConfirm] = useState(false), [busy, setBusy] = useState(false)
  const [results, setResults] = useState<Removal[]>([])
  useUnsavedGuard(busy, { busy })
  const run = async (names: string[]) => {
    setBusy(true)
    setResults(current => [...current.filter(item => !names.includes(item.model)), ...names.map(model => ({ model, status: 'pending' as const }))])
    for (const model of names) {
      let result: Removal
      try { await remove.mutateAsync(model); result = { model, status: 'success' } }
      catch (error) { result = { model, status: 'error', error: errorMessage(error, '移除失败') } }
      setResults(current => current.map(item => item.model === model ? result : item))
    }
    setBusy(false); setConfirm(false)
  }
  const failed = results.filter(result => result.status === 'error')
  return <div className="key-routing">
    {!routing.length ? <p className="muted-copy">全部跟随账号设置</p> : <ul className="key-route-list">{routing.map(preference => <li key={preference.model_id}><Link to={`/keys/${keyId}/routing/${encodeURIComponent(preference.model_id)}`} state={{ overlayFrom: `/keys/${keyId}` }}><strong className="mono">{preference.model_id}</strong><span><Badge tone="info">覆盖账号设置</Badge> {routingModeLabels[preference.mode]}</span></Link></li>)}</ul>}
    <div className="key-route-add"><SuggestInput aria-label="模型 ID" placeholder="选择或输入模型 ID" value={adding} onChange={event => setAdding(event.target.value)} suggestions={(models.data?.items ?? []).filter(model => !routing.some(item => item.model_id === model.id)).map(model => ({ value: model.id }))} /><ButtonLink to={`/keys/${keyId}/routing/${encodeURIComponent(adding.trim())}`} aria-disabled={!adding.trim()} onClick={event => { if (!adding.trim()) event.preventDefault() }} state={{ overlayFrom: `/keys/${keyId}` }}>单独设置</ButtonLink></div>
    {models.isError && <p className="muted-copy">模型选项加载失败，可直接输入模型 ID。</p>}
    {routing.length > 0 && <Button variant="quiet" type="button" disabled={busy} onClick={() => { setResults([]); setConfirm(true) }}>全部恢复跟随账号</Button>}
    {results.length > 0 && <div role="status"><p>已移除 {results.filter(result => result.status === 'success').length}/{results.length}{busy ? ' · 处理中' : ''}</p><ul>{results.map(result => <li key={result.model}>{result.model}：{result.status === 'pending' ? '处理中' : result.error || '已移除'}</li>)}</ul>{failed.length > 0 && !busy && <Button variant="secondary" onClick={() => void run(failed.map(item => item.model))}>重试失败的 {failed.length} 项</Button>}</div>}
    <ConfirmDialog open={confirm} busy={busy} title={`移除 ${routing.length} 个模型的覆盖？`} confirmLabel="恢复跟随账号" onClose={() => setConfirm(false)} onConfirm={() => void run(routing.map(item => item.model_id))} />
  </div>
}
