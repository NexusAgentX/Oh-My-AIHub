import { useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import type { ApiKeyDetail } from '../api/types'
import { useModels } from '../models/queries'
import {
  Button,
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
  Switch,
  TextField,
} from '../ui'
import {
  advancedChangedCount,
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
  const models = useModels()
  const catalog = models.data?.items.map((model) => model.id) ?? []
  const set = (patch: Partial<KeyForm>) => onChange({ ...form, ...patch })
  return (
    <>
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
            <div className="key-model-picks">
              {[...new Set([...catalog, ...form.allowedModels])].map((model) => (
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
            <input
              aria-label="平台模型"
              className="input mono"
              list="key-alias-models"
              onChange={(event) =>
                set({ aliases: form.aliases.map((item, position) => (position === index ? { ...item, to: event.target.value } : item)) })
              }
              placeholder="平台模型"
              value={row.to}
            />
            <IconButton
              icon={<Icon name="x" />}
              label="删除别名"
              onClick={() => set({ aliases: form.aliases.filter((_, position) => position !== index) })}
            />
          </div>
        ))}
        <datalist id="key-alias-models">
          {catalog.map((model) => (
            <option key={model} value={model} />
          ))}
        </datalist>
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

function KeyCreateForm({ onCreated }: { onCreated: (id: string, secret: string) => void }) {
  const [form, setForm] = useState<KeyForm>(emptyKeyForm)
  const [error, setError] = useState('')
  const mutation = useCreateKey()
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const problem = validateKeyForm(form)
    setError(problem)
    if (problem) return
    mutation.mutate(createRequest(form), {
      onSuccess: (result) => onCreated(result.key.id, result.secret),
      onError: (caught) => setError(errorMessage(caught, '创建失败，请重试')),
    })
  }
  return (
    <form className="stack-form" noValidate onSubmit={submit}>
      <TextField autoFocus label="名称" maxLength={64} onChange={(event) => setForm({ ...form, name: event.target.value })} required value={form.name} />
      <Disclosure changed={advancedChangedCount(form)} title="高级设置">
        <KeyAdvancedFields form={form} onChange={setForm} />
        <p className="muted-copy">路由可在创建后单独设置</p>
      </Disclosure>
      <InlineError>{error}</InlineError>
      <div className="form-actions">
        <Button loading={mutation.isPending}>创建</Button>
      </div>
    </form>
  )
}

function KeyEditForm({ detail, initialSecret, onDeleted }: { detail: ApiKeyDetail; initialSecret?: string; onDeleted: () => void }) {
  const original = formFromKey(detail.key)
  const [form, setForm] = useState<KeyForm>(original)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)
  const update = useUpdateKey(detail.key.id)
  const remove = useDeleteKey()
  const patch = updateRequest(original, form)
  const dirty = Object.keys(patch).length > 0

  const submit = (event: FormEvent) => {
    event.preventDefault()
    setMessage('')
    const problem = validateKeyForm(form)
    setError(problem)
    if (problem || !dirty) return
    update.mutate(patch, {
      onSuccess: () => setMessage('已保存'),
      onError: (caught) => setError(errorMessage(caught, '保存失败，请重试')),
    })
  }

  return (
    <form className="stack-form" noValidate onSubmit={submit}>
      {initialSecret && <SuccessMessage>Key 已创建</SuccessMessage>}
      <KeySecret initial={initialSecret} keyId={detail.key.id} prefix={detail.key.prefix} />
      <TextField label="名称" maxLength={64} onChange={(event) => setForm({ ...form, name: event.target.value })} value={form.name} />
      <Switch checked={form.enabled} label={form.enabled ? '已启用' : '已停用'} onChange={(enabled) => setForm({ ...form, enabled })} />
      <Disclosure changed={advancedChangedCount(form, detail.routing.length)} title="高级设置">
        <KeyAdvancedFields form={form} onChange={setForm} />
        <fieldset className="form-section">
          <legend>路由</legend>
          <KeyRoutingSection keyId={detail.key.id} routing={detail.routing} />
        </fieldset>
      </Disclosure>
      <InlineError>{error}</InlineError>
      <SuccessMessage>{dirty ? '' : message}</SuccessMessage>
      <div className="form-actions key-form-actions">
        <Button icon={<Icon name="trash" />} onClick={() => setConfirmDelete(true)} type="button" variant="danger">
          删除
        </Button>
        <Button disabled={!dirty} loading={update.isPending}>
          保存
        </Button>
      </div>
      <ConfirmDialog
        busy={remove.isPending}
        confirmLabel="删除"
        danger
        description="使用这把 Key 的客户端会立即无法调用"
        error={remove.isError ? errorMessage(remove.error, '删除失败，请重试') : ''}
        onClose={() => setConfirmDelete(false)}
        onConfirm={() => remove.mutate(detail.key.id, { onSuccess: onDeleted })}
        open={confirmDelete}
        title={`删除 ${detail.key.name}？`}
      />
    </form>
  )
}

function KeyEditor({ id, initialSecret, onDeleted }: { id: string; initialSecret?: string; onDeleted: () => void }) {
  const key = useKey(id)
  return (
    <QueryBoundary errorFallback="Key 加载失败" query={key}>
      {(detail) => <KeyEditForm detail={detail} initialSecret={initialSecret} onDeleted={onDeleted} />}
    </QueryBoundary>
  )
}

export type KeyDrawerState = { mode: 'create' } | { mode: 'edit'; id: string; secret?: string } | null

/** API Key 右侧抽屉：新建（成功后原地显示完整 Key）与编辑。 */
export function KeyDrawer({ state, onChange }: { state: KeyDrawerState; onChange: (state: KeyDrawerState) => void }) {
  return (
    <Drawer onClose={() => onChange(null)} open={state !== null} title={state?.mode === 'create' ? '新建 Key' : 'Key 设置'}>
      {state?.mode === 'create' && <KeyCreateForm onCreated={(id, secret) => onChange({ mode: 'edit', id, secret })} />}
      {state?.mode === 'edit' && <KeyEditor id={state.id} initialSecret={state.secret} key={state.id} onDeleted={() => onChange(null)} />}
    </Drawer>
  )
}
