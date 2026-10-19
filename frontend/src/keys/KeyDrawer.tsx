import { Outlet, useLocation, useNavigate, useParams } from 'react-router-dom'
import { SaveBar } from '../ui/SaveBar'
import { useUnsavedGuard } from '../ui/UnsavedGuard'
import { useBack } from '../ui/urlState'
import { formatPoints } from '../money/format'
import { formatClockTime } from '../money/time'
import { takeKeySecret } from './secret'
import { useEffect, useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import type { ApiKeyDetail } from '../api/types'
import { useMarketNames } from '../market/queries'
import {
  Button,
  Card,
  PageHeader,
  ConfirmDialog,
  CopyButton,
  Disclosure,
  Drawer,
  Icon,
  IconButton,
  InlineError,
  QueryBoundary,
  Segmented,
  SuccessMessage,
  SuggestInput,
  Switch,
  TextField,
} from '../ui'
import {
  advancedChangedCount,
  budgetProgress,
  createRequest,
  emptyKeyForm,
  formFromKey,
  updateRequest,
  validateKeyForm,
  type KeyForm,
} from './keyForm'
import { fetchKeySecret, useCreateKey, useDeleteKey, useKey, useUpdateKey } from './queries'
import { KeyRoutingSection } from './KeyRouting'

/** 高级设置中的可用模型、预算、别名与过期时间。 */
function KeyAdvancedFields({ form, onChange }: { form: KeyForm; onChange: (form: KeyForm) => void }) {
  const [search, setSearch] = useState('')
  const [selectedOnly, setSelectedOnly] = useState(false)
  const models = useMarketNames()
  const catalog = models.data?.items.map((model) => model.id) ?? []
  const modelSuggestions = catalog.map((model) => ({ value: model }))
  const set = (patch: Partial<KeyForm>) => onChange({ ...form, ...patch })
  return (
    <>
      <fieldset className="form-section">
        <legend>Codex 笔记与历史</legend>
        <Switch checked={form.codexNotes} label="使用笔记与历史工具" onChange={(codexNotes) => set({ codexNotes })} />
        <p className="muted-copy">开启后，同一会话固定使用原渠道及凭据。更改后请使用新会话，已有历史不会迁移。</p>
      </fieldset>
      <fieldset className="form-section">
        <legend>可用模型</legend>
        <Segmented
          label="可用模型"
          onChange={(modelScope) => set({ modelScope })}
          options={[
            { key: 'all', label: '全部' },
            { key: 'some', label: '指定' },
          ]}
          value={form.modelScope}
        />
        {form.modelScope === 'some' &&
          (models.isError ? (
            <p className="muted-copy">{errorMessage(models.error, '模型列表加载失败')}</p>
          ) : (
            <div>
              <TextField label="检索模型" value={search} onChange={(event) => setSearch(event.target.value)} placeholder="模型名称片段" />
              <Switch label={`只看已选（${form.allowedModels.length}）`} checked={selectedOnly} onChange={setSelectedOnly} />
              <div className="key-model-picks">
              {[...new Set([...catalog, ...form.allowedModels])].filter((model) => model.toLowerCase().includes(search.trim().toLowerCase()) && (!selectedOnly || form.allowedModels.includes(model))).map((model) => (
                <label className="checkbox-control" key={model}>
                  <input
                    checked={form.allowedModels.includes(model)}
                    onChange={(event) =>
                      set({
                        allowedModels: event.target.checked
                          ? [...form.allowedModels, model]
                          : form.allowedModels.filter((item) => item !== model),
                      })
                    }
                    type="checkbox"
                  />
                  <span className="mono">{model}</span>
                </label>
              ))}
              </div>
              {![...catalog, ...form.allowedModels].some((model) => model.toLowerCase().includes(search.trim().toLowerCase()) && (!selectedOnly || form.allowedModels.includes(model))) && <p role="status">没有匹配的模型</p>}
            </div>
          ))}
      </fieldset>
      <fieldset className="form-section">
        <legend>预算（积分，留空不限）</legend>
        <div className="field-row field-row-three">
          <TextField inputMode="decimal" label="每日" onChange={(event) => set({ budgetDaily: event.target.value.trim() })} value={form.budgetDaily} />
          <TextField inputMode="decimal" label="每月" onChange={(event) => set({ budgetMonthly: event.target.value.trim() })} value={form.budgetMonthly} />
          <TextField inputMode="decimal" label="总额" onChange={(event) => set({ budgetTotal: event.target.value.trim() })} value={form.budgetTotal} />
        </div>
      </fieldset>
      <fieldset className="form-section">
        <legend>模型别名</legend>
        {form.aliases.map((row, index) => (
          // 别名行没有稳定 ID，按位置作为 key
          <div className="alias-row" key={index}>
            <input
              aria-label="客户端名称"
              className="input mono"
              onChange={(event) =>
                set({ aliases: form.aliases.map((item, position) => (position === index ? { ...item, from: event.target.value } : item)) })
              }
              placeholder="客户端名称"
              value={row.from}
            />
            <span aria-hidden="true">→</span>
            <SuggestInput
              aria-label="平台模型"
              className="mono"
              onChange={(event) =>
                set({ aliases: form.aliases.map((item, position) => (position === index ? { ...item, to: event.target.value } : item)) })
              }
              placeholder="平台模型"
              suggestions={modelSuggestions}
              value={row.to}
            />
            <IconButton
              icon={<Icon name="x" />}
              label="删除别名"
              onClick={() => set({ aliases: form.aliases.filter((_, position) => position !== index) })}
            />
          </div>
        ))}
        <div>
          <Button
            icon={<Icon name="plus" />}
            onClick={() => set({ aliases: [...form.aliases, { from: '', to: '' }] })}
            size="sm"
            type="button"
            variant="secondary"
          >
            添加别名
          </Button>
        </div>
      </fieldset>
      <div className="key-expiry">
        <TextField
          label="过期时间"
          hint="留空永不过期"
          onChange={(event) => set({ expiresAt: event.target.value })}
          type="datetime-local"
          value={form.expiresAt}
        />
        {form.expiresAt && (
          <Button onClick={() => set({ expiresAt: '' })} size="sm" type="button" variant="quiet">
            清除
          </Button>
        )}
      </div>
    </>
  )
}

/** 完整 Key：按需读取，可再次复制。 */
function KeySecret({ keyId, prefix, initial }: { keyId: string; prefix: string; initial?: string }) {
  const [secret, setSecret] = useState(initial ?? '')
  useEffect(() => { if (initial) setSecret(initial) }, [initial])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const load = async () => {
    if (secret) return secret
    const value = await fetchKeySecret(keyId)
    setSecret(value)
    return value
  }
  return (
    <div className="copy-field">
      <span className="copy-field-label">完整 Key</span>
      <div className="copy-field-row">
        <code className={`copy-field-value ${secret ? '' : 'copy-field-masked'}`}>{secret || `${prefix}••••••••`}</code>
        {!secret && (
          <Button
            icon={<Icon name="eye" />}
            loading={loading}
            onClick={() => {
              setError('')
              setLoading(true)
              load()
                .catch((caught) => setError(errorMessage(caught, '读取 Key 失败，请重试')))
                .finally(() => setLoading(false))
            }}
            size="sm"
            type="button"
            variant="quiet"
          >
            显示
          </Button>
        )}
        <CopyButton value={load} />
      </div>
      <InlineError>{error}</InlineError>
    </div>
  )
}

function KeyCreateForm({ onCreated, onBusy }: { onCreated: (id: string) => void; onBusy: (busy: boolean) => void }) {
  const [form, setForm] = useState<KeyForm>(emptyKeyForm)
  const [error, setError] = useState('')
  const mutation = useCreateKey()
  const allowNavigation = useUnsavedGuard(JSON.stringify(form) !== JSON.stringify(emptyKeyForm), { busy: mutation.isPending })
  useEffect(() => onBusy(mutation.isPending), [mutation.isPending, onBusy])
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const problem = validateKeyForm(form)
    setError(problem)
    if (problem) return
    mutation.mutate(createRequest(form), {
      onSuccess: result => { allowNavigation(); onCreated(result.key.id) },
      onError: caught => setError(errorMessage(caught, '创建失败，请重试')),
    })
  }
  return <form className="stack-form" noValidate onSubmit={submit}>
    <TextField autoFocus label="名称" maxLength={64} onChange={event => setForm({ ...form, name: event.target.value })} required value={form.name} />
    <Disclosure changed={advancedChangedCount(form)} title="高级设置"><KeyAdvancedFields form={form} onChange={setForm} /></Disclosure>
    <InlineError>{error}</InlineError><div className="form-actions"><Button loading={mutation.isPending}>创建</Button></div>
  </form>
}

function KeyEditForm({ detail, onDeleted }: { detail: ApiKeyDetail; onDeleted: () => void }) {
  const [base, setBase] = useState(() => formFromKey(detail.key))
  const [form, setForm] = useState<KeyForm>(base)
  const [error, setError] = useState('')
  const [savedAt, setSavedAt] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [confirmDisable, setConfirmDisable] = useState(false)
  const update = useUpdateKey(detail.key.id)
  const remove = useDeleteKey()
  const patch = updateRequest(base, form)
  const dirtyCount = Object.keys(patch).length
  const allowNavigation = useUnsavedGuard(dirtyCount > 0, { scope: `/keys/${detail.key.id}`, busy: update.isPending || remove.isPending })
  const save = () => update.mutate(patch, {
    onSuccess: result => { const latest = formFromKey(result.key); setBase(latest); setForm(latest); setSavedAt(formatClockTime(new Date())); setConfirmDisable(false); setError('') },
    onError: caught => setError(errorMessage(caught, '保存失败，请重试')),
  })
  const submit = (event: FormEvent) => {
    event.preventDefault(); const problem = validateKeyForm(form); setError(problem)
    if (problem || !dirtyCount) return
    if (base.enabled && !form.enabled) setConfirmDisable(true)
    else save()
  }
  return <Card title="设置"><form className="stack-form" noValidate onSubmit={submit}>
    <SaveBar dirtyCount={dirtyCount} busy={update.isPending} savedAt={savedAt} onDiscard={() => { setForm(base); setError('') }} />
    <InlineError>{error}</InlineError>
    <TextField label="名称" maxLength={64} onChange={event => setForm({ ...form, name: event.target.value })} value={form.name} />
    <Switch checked={form.enabled} label={form.enabled ? '已启用' : '已停用'} onChange={enabled => setForm({ ...form, enabled })} />
    <Disclosure changed={advancedChangedCount(form)} title="高级设置"><KeyAdvancedFields form={form} onChange={setForm} /></Disclosure>
    <div className="key-danger"><Button icon={<Icon name="trash" />} onClick={() => setConfirmDelete(true)} type="button" variant="danger">删除 Key</Button></div>
    <ConfirmDialog busy={update.isPending} confirmLabel="停用并保存" error={error} onClose={() => setConfirmDisable(false)} onConfirm={save} open={confirmDisable} title="停用这把 Key？" description="使用这把 Key 的客户端会立即无法调用。" />
    <ConfirmDialog busy={remove.isPending} confirmLabel="删除" danger description="使用这把 Key 的客户端会立即无法调用。" error={remove.isError ? errorMessage(remove.error, '删除失败，请重试') : ''} onClose={() => setConfirmDelete(false)} onConfirm={() => remove.mutate(detail.key.id, { onSuccess: () => { allowNavigation(); onDeleted() } })} open={confirmDelete} title={`删除 ${detail.key.name}？`} />
  </form></Card>
}

function KeyContent({ detail }: { detail: ApiKeyDetail }) {
  const navigate = useNavigate()
  const [secret, setSecret] = useState('')
  const progress = budgetProgress(detail.key)
  useEffect(() => { const value = takeKeySecret(detail.key.id); if (value) setSecret(value) }, [detail.key.id])
  return <>
    <Card title="密钥">{secret && <SuccessMessage>Key 已创建</SuccessMessage>}<KeySecret initial={secret} keyId={detail.key.id} prefix={detail.key.prefix} /></Card>
    <Card title="花费与预算"><dl className="key-detail-spend"><div><dt>今日</dt><dd className="num">{formatPoints(detail.key.spend.today)}</dd></div><div><dt>本月</dt><dd className="num">{formatPoints(detail.key.spend.month)}</dd></div><div><dt>累计</dt><dd className="num">{formatPoints(detail.key.spend.total)}</dd></div></dl>{progress && <p className={progress.percent >= 100 ? 'amount-negative' : progress.warn ? 'needs-action' : 'muted-copy'}>{progress.label}预算已用 {Math.round(progress.percent)}%{progress.percent >= 100 ? ' · 超预算，调用将被拒' : ''}</p>}</Card>
    <KeyEditForm detail={detail} onDeleted={() => navigate('/keys', { replace: true })} />
    <Card title="路由覆盖"><KeyRoutingSection keyId={detail.key.id} routing={detail.routing} /></Card>
  </>
}
export function KeyDetailPage() {
  const { id = '' } = useParams(), key = useKey(id)
  const back = useBack('/keys', 'API Key')
  return <><PageHeader title={key.data?.key.name || 'Key 详情'} back={back} /><QueryBoundary errorFallback="Key 加载失败" query={key}>{detail => <KeyContent key={id} detail={detail} />}</QueryBoundary><Outlet /></>
}
export function KeyCreateDrawer() {
  const navigate = useNavigate(), location = useLocation()
  const [busy, setBusy] = useState(false)
  return <Drawer open busy={busy} title="新建 Key" onClose={() => location.state?.overlayFrom === '/keys' ? navigate(-1) : navigate('/keys', { replace: true })}><KeyCreateForm onBusy={setBusy} onCreated={id => navigate(`/keys/${id}`, { replace: true })} /></Drawer>
}
