import { useEffect, useState, type FormEvent } from 'react'
import { useBlocker } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { CatalogModel } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import {
  Button,
  InlineError,
  LoadingState,
  StatusBadge,
  TextField,
} from '../ui/FormControls'
import { Icon } from '../ui/Icon'
import {
  emptyModelForm,
  formToModelInput,
  mergeModelDraft,
  modelFormHasChanges,
  modelStatusUpdate,
  modelToForm,
  validateModelForm,
  type ModelForm,
} from './modelForm'

const modalityOptions = [
  { value: 'text', label: '文本' },
  { value: 'image', label: '图片' },
  { value: 'audio', label: '音频' },
  { value: 'video', label: '视频' },
]

export function AdminModelsPage() {
  const [models, setModels] = useState<CatalogModel[]>([])
  const [query, setQuery] = useState('')
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const [selectedModel, setSelectedModel] = useState<CatalogModel | null>(null)
  const [form, setForm] = useState<ModelForm>({ ...emptyModelForm })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [message, setMessage] = useState('')
  const [conflictPending, setConflictPending] = useState(false)
  const hasUnsavedChanges = modelFormHasChanges(form, selectedModel)
  const navigationBlocker = useBlocker(hasUnsavedChanges)

  const load = async (search = query) => {
    setLoading(true)
    setError('')
    try {
      const items = await api.models(search, true)
      setModels(items)
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '模型目录加载失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load('')
  }, [])

  useEffect(() => {
    if (navigationBlocker.state !== 'blocked') return
    if (window.confirm('放弃未保存的模型更改？')) {
      navigationBlocker.proceed()
    } else {
      navigationBlocker.reset()
    }
  }, [navigationBlocker])

  useEffect(() => {
    const protectDraft = (event: BeforeUnloadEvent) => {
      if (!hasUnsavedChanges) return
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', protectDraft)
    return () => window.removeEventListener('beforeunload', protectDraft)
  }, [hasUnsavedChanges])

  const confirmDiscardDraft = () =>
    !modelFormHasChanges(form, selectedModel) ||
    window.confirm('放弃未保存的模型更改？')

  const selectModel = (model: CatalogModel) => {
    if (model.id === selectedID || !confirmDiscardDraft()) return
    setSelectedID(model.id)
    setSelectedModel(model)
    setForm(modelToForm(model))
    setConflictPending(false)
    setFieldErrors({})
    setError('')
    setMessage('')
  }

  const startNew = () => {
    if (!confirmDiscardDraft()) return
    setSelectedID(null)
    setSelectedModel(null)
    setForm({ ...emptyModelForm, inputModalities: ['text'], outputModalities: ['text'] })
    setConflictPending(false)
    setFieldErrors({})
    setError('')
    setMessage('')
  }

  const reloadLatestAfterConflict = async (
    modelID: string,
    baseline: CatalogModel,
    draft: ModelForm,
  ) => {
    setLoading(true)
    try {
      const latest = await api.model(modelID, true)
      setModels((items) =>
        items.map((item) => (item.id === latest.id ? latest : item)),
      )
      setSelectedID(latest.id)
      setSelectedModel(latest)
      setForm(mergeModelDraft(baseline, draft, latest))
      setFieldErrors({})
      setMessage('')
      setError('')
      setConflictPending(true)
    } catch {
      setError('模型版本冲突，且最新版本加载失败，请刷新页面')
    } finally {
      setLoading(false)
    }
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (conflictPending) return
    const validation = validateModelForm(form)
    setFieldErrors(validation)
    if (Object.keys(validation).length > 0) return
    setSaving(true)
    setError('')
    setMessage('')
    try {
      const input = formToModelInput(form)
      const saved = selectedID
        ? await api.updateModel(selectedID, selectedModel?.version ?? 0, (() => {
            const { id: _id, ...update } = input
            return update
          })())
        : await api.createModel(input)
      setModels((items) => {
        const exists = items.some((item) => item.id === saved.id)
        return exists
          ? items.map((item) => (item.id === saved.id ? saved : item))
          : [...items, saved].sort((left, right) =>
              left.provider.localeCompare(right.provider, 'zh-CN'),
            )
      })
      setSelectedID(saved.id)
      setSelectedModel(saved)
      setForm(modelToForm(saved))
      setConflictPending(false)
      setMessage('模型已保存')
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'conflict' && selectedID) {
        if (selectedModel) {
          await reloadLatestAfterConflict(selectedID, selectedModel, form)
        }
      } else {
        setError(caught instanceof ApiError ? caught.message : '模型保存失败')
      }
    } finally {
      setSaving(false)
    }
  }

  const toggleStatus = async () => {
    if (!selectedID || !selectedModel) return
    const nextStatus: ModelForm['status'] =
      selectedModel.status === 'active' ? 'disabled' : 'active'
    setSaving(true)
    setError('')
    try {
      const saved = await api.updateModel(
        selectedID,
        selectedModel.version,
        modelStatusUpdate(selectedModel, nextStatus),
      )
      setModels((items) =>
        items.map((item) => (item.id === saved.id ? saved : item)),
      )
      setSelectedModel(saved)
      setForm((current) => ({ ...current, status: saved.status }))
      setMessage(saved.status === 'active'
        ? '模型已启用；未保存的表单修改仍保留'
        : '模型已停用；未保存的表单修改仍保留')
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'conflict') {
        await reloadLatestAfterConflict(selectedID, selectedModel, form)
      } else {
        setError(caught instanceof ApiError ? caught.message : '状态更新失败')
      }
    } finally {
      setSaving(false)
    }
  }

  const search = (event: FormEvent) => {
    event.preventDefault()
    void load(query)
  }

  return (
    <AppShell admin>
      <header className="page-heading">
        <div><h1>模型目录</h1></div>
        <Button aria-label="新增模型" icon={<Icon name="plus" />} onClick={startNew}>新增模型</Button>
      </header>
      <InlineError>{error}</InlineError>
      <div className="model-workspace">
        <section className="panel model-list-panel">
          <header className="model-list-heading">
            <div>
              <h2>公开模型</h2>
              <span className="count-badge">{models.length} 个</span>
            </div>
            <form className="search-form" onSubmit={search}>
              <Icon name="search" />
              <input
                aria-label="搜索模型"
                onChange={(event) => setQuery(event.target.value)}
                placeholder="搜索模型"
                value={query}
              />
              <button className="visually-hidden" type="submit">搜索</button>
            </form>
          </header>
          {loading ? (
            <LoadingState />
          ) : (
            <>
            {models.length > 0 && (
            <div className="model-list" role="list">
              {models.map((model) => (
                <div key={model.id} role="listitem">
                  <button
                    aria-current={selectedID === model.id ? 'true' : undefined}
                    className={`model-list-item ${selectedID === model.id ? 'model-list-item-active' : ''}`}
                    onClick={() => selectModel(model)}
                    type="button"
                  >
                    <span>
                      <strong>{model.name}</strong>
                      <small>{model.id}</small>
                    </span>
                    <span>
                      <small>{formatContext(model.context_window)}</small>
                      <StatusBadge status={model.status} />
                    </span>
                  </button>
                </div>
              ))}
            </div>
            )}
            {models.length === 0 && <div className="empty-state">没有匹配的模型</div>}
            </>
          )}
        </section>

        <section className="panel model-editor-panel">
          <header className="model-editor-heading">
            <div>
              <h2>{selectedModel ? `编辑模型 · ${selectedModel.id}` : '新增模型'}</h2>
            </div>
            <StatusBadge status={form.status} />
          </header>
          {message && <div aria-live="polite" className="success-message">{message}</div>}
          {conflictPending && selectedModel && (
            <div className="conflict-message" role="alert">
              <span>模型已被其他管理员修改，本地字段已合并到最新版。</span>
              <div>
                <Button
                  onClick={() => {
                    setConflictPending(false)
                    setMessage('已基于最新版本保留草稿，请确认后保存')
                  }}
                  type="button"
                  variant="secondary"
                >
                  接受合并结果
                </Button>
                <Button
                  onClick={() => {
                    setForm(modelToForm(selectedModel))
                    setConflictPending(false)
                    setMessage('已载入其他管理员保存的最新版本')
                  }}
                  type="button"
                  variant="secondary"
                >
                  载入最新版
                </Button>
              </div>
            </div>
          )}
          <form className="model-form" onSubmit={submit}>
            <div className="field-row field-row-three">
              <TextField
                disabled={Boolean(selectedID)}
                error={fieldErrors.id}
                label="模型 ID"
                onChange={(event) => setForm({ ...form, id: event.target.value })}
                placeholder="provider/model-id"
                required
                value={form.id}
              />
              <TextField
                error={fieldErrors.name}
                label="模型名称"
                onChange={(event) => setForm({ ...form, name: event.target.value })}
                placeholder="例如 GPT-5"
                required
                value={form.name}
              />
              <TextField
                error={fieldErrors.provider}
                label="官方提供商"
                onChange={(event) => setForm({ ...form, provider: event.target.value })}
                placeholder="例如 OpenAI"
                required
                value={form.provider}
              />
            </div>
            <div className="field-row">
              <TextField
                error={fieldErrors.contextWindow}
                inputMode="numeric"
                label="上下文大小（tokens）"
                min="1"
                onChange={(event) => setForm({ ...form, contextWindow: event.target.value })}
                required
                type="number"
                value={form.contextWindow}
              />
              <TextField
                label="参数信息"
                onChange={(event) => setForm({ ...form, parameterInfo: event.target.value })}
                placeholder="未知可留空"
                value={form.parameterInfo}
              />
            </div>
            <div className="field-row">
              <ModalityField
                error={fieldErrors.inputModalities}
                label="输入模态"
                onChange={(values) => setForm({ ...form, inputModalities: values })}
                values={form.inputModalities}
              />
              <ModalityField
                error={fieldErrors.outputModalities}
                label="输出模态"
                onChange={(values) => setForm({ ...form, outputModalities: values })}
                values={form.outputModalities}
              />
            </div>
            <fieldset className="checkbox-row">
              <legend className="visually-hidden">模型能力</legend>
              <Checkbox
                checked={form.supportsTools}
                label="工具调用"
                onChange={(checked) => setForm({ ...form, supportsTools: checked })}
              />
              <Checkbox
                checked={form.supportsStructuredOutput}
                label="结构化输出"
                onChange={(checked) => setForm({ ...form, supportsStructuredOutput: checked })}
              />
              <Checkbox
                checked={form.supportsVision}
                label="视觉理解"
                onChange={(checked) => setForm({ ...form, supportsVision: checked })}
              />
            </fieldset>
            <fieldset className="price-fieldset">
              <legend>平台基准价 · 积分 / 1M tokens</legend>
              <div className="price-grid">
                <PriceField label="输入" error={fieldErrors.inputPrice} value={form.inputPrice} onChange={(value) => setForm({ ...form, inputPrice: value })} />
                <PriceField label="输出" error={fieldErrors.outputPrice} value={form.outputPrice} onChange={(value) => setForm({ ...form, outputPrice: value })} />
                <PriceField label="缓存创建" error={fieldErrors.cacheWritePrice} value={form.cacheWritePrice} onChange={(value) => setForm({ ...form, cacheWritePrice: value })} />
                <PriceField label="缓存读取" error={fieldErrors.cacheReadPrice} value={form.cacheReadPrice} onChange={(value) => setForm({ ...form, cacheReadPrice: value })} />
              </div>
            </fieldset>
            <footer className="form-actions">
              {selectedID && (
                <Button disabled={saving || conflictPending} onClick={() => void toggleStatus()} type="button" variant="secondary">
                  {form.status === 'active' ? '停用模型' : '启用模型'}
                </Button>
              )}
              <Button disabled={saving || conflictPending} type="submit">{saving ? '正在保存' : '保存更改'}</Button>
            </footer>
          </form>
        </section>
      </div>
    </AppShell>
  )
}

function formatContext(value: number) {
  if (value >= 1_000_000 && value % 1_000_000 === 0) return `${value / 1_000_000}M`
  if (value >= 1_000 && value % 1_000 === 0) return `${value / 1_000}K`
  return value.toLocaleString('zh-CN')
}

function ModalityField({
  label,
  values,
  error,
  onChange,
}: {
  label: string
  values: string[]
  error?: string
  onChange: (values: string[]) => void
}) {
  const toggle = (value: string, checked: boolean) => {
    onChange(checked ? [...values, value] : values.filter((item) => item !== value))
  }
  return (
    <fieldset className="choice-field">
      <legend>{label}</legend>
      <div className="choice-pills">
        {modalityOptions.map((option) => (
          <label key={option.value}>
            <input
              checked={values.includes(option.value)}
              onChange={(event) => toggle(option.value, event.target.checked)}
              type="checkbox"
            />
            <span>{option.label}</span>
          </label>
        ))}
      </div>
      {error && <span className="field-message field-error">{error}</span>}
    </fieldset>
  )
}

function Checkbox({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <label className="checkbox-control">
      <input checked={checked} onChange={(event) => onChange(event.target.checked)} type="checkbox" />
      <span>{label}</span>
    </label>
  )
}

function PriceField({ label, value, error, onChange }: { label: string; value: string; error?: string; onChange: (value: string) => void }) {
  return (
    <TextField
      error={error}
      inputMode="decimal"
      label={label}
      onChange={(event) => onChange(event.target.value)}
      placeholder="0"
      required
      value={value}
    />
  )
}
