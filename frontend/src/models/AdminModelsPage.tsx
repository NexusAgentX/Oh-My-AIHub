import { useEffect, useState, type FormEvent } from 'react'
import { useBlocker } from 'react-router-dom'
import { ApiError } from '../api/client'
import type { CatalogModel } from '../api/contracts'
import { errorMessage } from '../api/query'
import {
  Button,
  Card,
  Checkbox,
  EmptyState,
  FormSection,
  Icon,
  InlineError,
  Notice,
  PageHeader,
  QueryBoundary,
  SearchInput,
  StatusBadge,
  SuccessMessage,
  TextField,
} from '../ui'
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
import {
  useAdminModelsQuery,
  useCreateModel,
  useFetchLatestModel,
  useUpdateModel,
} from './queries'
import { PriceField, TierEditor } from './TierEditor'

const modalityOptions = [
  { value: 'text', label: '文本' },
  { value: 'image', label: '图片' },
  { value: 'audio', label: '音频' },
  { value: 'video', label: '视频' },
]

function formatContext(value: number) {
  if (value >= 1_000_000 && value % 1_000_000 === 0) return `${value / 1_000_000}M`
  if (value >= 1_000 && value % 1_000 === 0) return `${value / 1_000}K`
  return value.toLocaleString('zh-CN')
}

export function AdminModelsPage() {
  const [search, setSearch] = useState('')
  const [draftSearch, setDraftSearch] = useState('')
  const [selectedModel, setSelectedModel] = useState<CatalogModel | null>(null)
  const [form, setForm] = useState<ModelForm>({ ...emptyModelForm })
  const [error, setError] = useState('')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [message, setMessage] = useState('')
  const [conflictPending, setConflictPending] = useState(false)
  const list = useAdminModelsQuery(search)
  const createModel = useCreateModel()
  const updateModel = useUpdateModel()
  const fetchLatest = useFetchLatestModel()
  const saving = createModel.isPending || updateModel.isPending
  const selectedID = selectedModel?.id ?? null
  const hasUnsavedChanges = modelFormHasChanges(form, selectedModel)
  const navigationBlocker = useBlocker(hasUnsavedChanges)

  useEffect(() => {
    if (navigationBlocker.state !== 'blocked') return
    if (window.confirm('放弃未保存的模型更改？')) navigationBlocker.proceed()
    else navigationBlocker.reset()
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
    !hasUnsavedChanges || window.confirm('放弃未保存的模型更改？')

  const reset = (model: CatalogModel | null) => {
    setSelectedModel(model)
    setForm(model ? modelToForm(model) : { ...emptyModelForm })
    setConflictPending(false)
    setFieldErrors({})
    setError('')
    setMessage('')
  }

  const selectModel = (model: CatalogModel) => {
    if (model.id === selectedID || !confirmDiscardDraft()) return
    reset(model)
  }

  const startNew = () => {
    if (!confirmDiscardDraft()) return
    reset(null)
  }

  const reloadLatestAfterConflict = async (baseline: CatalogModel, draft: ModelForm) => {
    try {
      const latest = await fetchLatest(baseline.id)
      setSelectedModel(latest)
      setForm(mergeModelDraft(baseline, draft, latest))
      setFieldErrors({})
      setMessage('')
      setError('')
      setConflictPending(true)
    } catch {
      setError('模型版本冲突，且最新版本加载失败，请刷新页面')
    }
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (conflictPending) return
    const validation = validateModelForm(form)
    setFieldErrors(validation)
    if (Object.keys(validation).length > 0) return
    setError('')
    setMessage('')
    try {
      const input = formToModelInput(form)
      let saved: CatalogModel
      if (selectedModel) {
        const { id: _id, ...update } = input
        saved = await updateModel.mutateAsync({
          modelID: selectedModel.id,
          expectedVersion: selectedModel.version,
          update,
        })
      } else {
        saved = await createModel.mutateAsync(input)
      }
      setSelectedModel(saved)
      setForm(modelToForm(saved))
      setConflictPending(false)
      setMessage('模型已保存')
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'conflict' && selectedModel) {
        await reloadLatestAfterConflict(selectedModel, form)
      } else {
        setError(errorMessage(caught, '模型保存失败'))
      }
    }
  }

  const toggleStatus = async () => {
    if (!selectedModel) return
    const nextStatus: ModelForm['status'] =
      selectedModel.status === 'active' ? 'disabled' : 'active'
    setError('')
    try {
      const saved = await updateModel.mutateAsync({
        modelID: selectedModel.id,
        expectedVersion: selectedModel.version,
        update: modelStatusUpdate(selectedModel, nextStatus),
      })
      setSelectedModel(saved)
      setForm((current) => ({ ...current, status: saved.status }))
      setMessage(
        saved.status === 'active'
          ? '模型已启用；未保存的表单修改仍保留'
          : '模型已停用；未保存的表单修改仍保留',
      )
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'conflict') {
        await reloadLatestAfterConflict(selectedModel, form)
      } else {
        setError(errorMessage(caught, '状态更新失败'))
      }
    }
  }

  const patch = (value: Partial<ModelForm>) => setForm((current) => ({ ...current, ...value }))

  return (
    <>
      <PageHeader
        actions={
          <Button icon={<Icon name="plus" />} onClick={startNew}>
            新增模型
          </Button>
        }
        title="模型目录"
      />
      <div className="model-workspace">
        <Card className="model-list-card" flush>
          <div className="model-list-search">
            <form
              onSubmit={(event) => {
                event.preventDefault()
                setSearch(draftSearch.trim())
              }}
            >
              <SearchInput
                label="搜索模型"
                onChange={(event) => setDraftSearch(event.target.value)}
                placeholder="搜索模型"
                value={draftSearch}
              />
            </form>
          </div>
          <QueryBoundary
            empty={<EmptyState title="没有匹配的模型" />}
            errorFallback="模型目录加载失败"
            isEmpty={(models) => models.length === 0}
            query={list}
          >
            {(models) => (
              <ul className="model-list">
                {models.map((model) => (
                  <li key={model.id}>
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
                  </li>
                ))}
              </ul>
            )}
          </QueryBoundary>
        </Card>

        <Card
          actions={<StatusBadge status={form.status} />}
          className="model-editor-card"
          title={selectedModel ? `编辑模型 · ${selectedModel.id}` : '新增模型'}
        >
          <InlineError>{error}</InlineError>
          <SuccessMessage>{message}</SuccessMessage>
          {conflictPending && selectedModel && (
            <Notice
              action={
                <span className="model-conflict-actions">
                  <Button
                    onClick={() => {
                      setConflictPending(false)
                      setMessage('已基于最新版本保留草稿，请确认后保存')
                    }}
                    size="sm"
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
                    size="sm"
                    variant="secondary"
                  >
                    载入最新版
                  </Button>
                </span>
              }
            >
              模型已被其他管理员修改，本地字段已合并到最新版。
            </Notice>
          )}
          <form className="model-form" onSubmit={submit}>
            <div className="field-row field-row-three">
              <TextField
                disabled={Boolean(selectedID)}
                error={fieldErrors.id}
                label="模型 ID"
                onChange={(event) => patch({ id: event.target.value })}
                placeholder="provider/model-id"
                required
                value={form.id}
              />
              <TextField
                error={fieldErrors.name}
                label="模型名称"
                onChange={(event) => patch({ name: event.target.value })}
                placeholder="例如 GPT-5"
                required
                value={form.name}
              />
              <TextField
                error={fieldErrors.provider}
                label="官方提供商"
                onChange={(event) => patch({ provider: event.target.value })}
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
                onChange={(event) => patch({ contextWindow: event.target.value })}
                required
                type="number"
                value={form.contextWindow}
              />
              <TextField
                label="参数信息"
                onChange={(event) => patch({ parameterInfo: event.target.value })}
                placeholder="未知可留空"
                value={form.parameterInfo}
              />
            </div>
            <div className="field-row">
              <ModalityField
                error={fieldErrors.inputModalities}
                label="输入模态"
                onChange={(values) => patch({ inputModalities: values })}
                values={form.inputModalities}
              />
              <ModalityField
                error={fieldErrors.outputModalities}
                label="输出模态"
                onChange={(values) => patch({ outputModalities: values })}
                values={form.outputModalities}
              />
            </div>
            <fieldset className="model-capabilities">
              <legend className="visually-hidden">模型能力</legend>
              <Checkbox
                checked={form.supportsTools}
                label="工具调用"
                onChange={(event) => patch({ supportsTools: event.target.checked })}
              />
              <Checkbox
                checked={form.supportsStructuredOutput}
                label="结构化输出"
                onChange={(event) => patch({ supportsStructuredOutput: event.target.checked })}
              />
              <Checkbox
                checked={form.supportsVision}
                label="视觉理解"
                onChange={(event) => patch({ supportsVision: event.target.checked })}
              />
            </fieldset>
            <FormSection title="默认价 · 积分 / 百万 tokens">
              <div className="field-row model-price-row">
                <PriceField error={fieldErrors.inputPrice} label="输入" onChange={(value) => patch({ inputPrice: value })} value={form.inputPrice} />
                <PriceField error={fieldErrors.outputPrice} label="输出" onChange={(value) => patch({ outputPrice: value })} value={form.outputPrice} />
                <PriceField error={fieldErrors.cacheWritePrice} label="缓存写" onChange={(value) => patch({ cacheWritePrice: value })} value={form.cacheWritePrice} />
                <PriceField error={fieldErrors.cacheReadPrice} label="缓存读" onChange={(value) => patch({ cacheReadPrice: value })} value={form.cacheReadPrice} />
              </div>
            </FormSection>
            <TierEditor
              error={fieldErrors.tiers}
              form={form}
              onChange={(tiers) => patch({ tiers })}
            />
            <footer className="form-actions">
              {selectedID && (
                <Button
                  disabled={saving || conflictPending}
                  onClick={() => void toggleStatus()}
                  type="button"
                  variant="secondary"
                >
                  {form.status === 'active' ? '停用模型' : '启用模型'}
                </Button>
              )}
              <Button disabled={conflictPending} loading={saving} type="submit">
                保存更改
              </Button>
            </footer>
          </form>
        </Card>
      </div>
    </>
  )
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
