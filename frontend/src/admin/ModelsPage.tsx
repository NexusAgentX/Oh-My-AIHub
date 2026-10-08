import { useEffect, useState, type FormEvent } from 'react'
import { CatalogSync, SourceDetails } from './CatalogSync'
import { errorMessage } from '../api/query'
import {
  Badge,
  Button,
  Card,
  Checkbox,
  ConfirmDialog,
  DataTable,
  Drawer,
  Icon,
  InlineError,
  PageHeader,
  QueryBoundary,
  TextareaField,
  TextField,
  type Column,
} from '../ui'
import { Collapsible } from './components'
import {
  changedModelAdvanced,
  emptyModelForm,
  formToCreateRequest,
  formToUpdateRequest,
  modalityOptions,
  modelToForm,
  validateModelForm,
  type ModelErrors,
  type ModelForm,
} from './modelForm'
import { useAdminModels, useDeleteModel, useSaveModel } from './queries'
import { PriceFields, TierEditor } from './TierEditor'
import type { AdminModel } from './types'

function ModalityPicker({
  legend,
  values,
  onChange,
}: {
  legend: string
  values: string[]
  onChange: (values: string[]) => void
}) {
  const options = [...new Set([...modalityOptions, ...values])]
  return (
    <fieldset className="weekday-picker">
      <legend>{legend}</legend>
      {options.map((option) => (
        <Checkbox
          checked={values.includes(option)}
          key={option}
          label={option}
          onChange={(event) =>
            onChange(event.target.checked ? [...values, option] : values.filter((value) => value !== option))
          }
        />
      ))}
    </fieldset>
  )
}

function ModelEditor({
  model,
  onDone,
}: {
  model: AdminModel | null
  onDone: () => void
}) {
  const creating = model === null
 const [syncing, setSyncing] = useState(model?.source?.sync_enabled ?? false)
  const [form, setForm] = useState<ModelForm>(() => (model ? modelToForm(model) : emptyModelForm()))
  const [errors, setErrors] = useState<ModelErrors>({})
  const save = useSaveModel()
  const set = (patch: Partial<ModelForm>) => setForm((current) => ({ ...current, ...patch }))

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const found = validateModelForm(form, creating)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    save.mutate(
      creating
        ? { mode: 'create', body: formToCreateRequest(form) }
        : { mode: 'update', id: model.id, body: model.source && syncing ? { enabled: form.enabled, sort_order: Number(form.sortOrder), parameter_info: form.parameterInfo, sync_enabled: true } : { ...formToUpdateRequest(form), ...(model.source ? { sync_enabled: false } : {}) } },
      { onSuccess: onDone },
    )
  }

  return (
    <form className="stack-form" id="model-form" noValidate onSubmit={submit}>
      {model?.source && <SourceDetails model={model} syncing={syncing} onChange={setSyncing} />}
      <TextField
        disabled={!creating}
        error={errors.id}
        hint={creating ? '客户端请求中的 model，创建后不可修改' : '创建后不可修改'}
        label="模型名"
        onChange={(event) => set({ id: event.target.value })}
        value={form.id}
      />
      <fieldset disabled={syncing} className="stack-form" style={{ border: 0, padding: 0, margin: 0 }}>
      <TextField
        error={errors.displayName}
        label="显示名"
        onChange={(event) => set({ displayName: event.target.value })}
        value={form.displayName}
      />
      <PriceFields
        error={errors.prices}
        legend="基准价（积分/百万 token）"
        onChange={(prices) => set({ prices })}
        prices={form.prices}
      />
      </fieldset>
      <div className="field-row">
        <TextField
          error={errors.sortOrder}
          hint="小的在前"
          inputMode="numeric"
          label="排序"
          onChange={(event) => set({ sortOrder: event.target.value })}
          value={form.sortOrder}
        />
        <Checkbox
          checked={form.enabled}
          disabled={syncing && model?.source?.price_ready === false}
          className="field-checkbox"
          label="启用"
          onChange={(event) => set({ enabled: event.target.checked })}
        />
      </div>
      <Collapsible changed={changedModelAdvanced(form)} title="高级设置">
        <div className="stack-form">
          <fieldset disabled={syncing} className="stack-form" style={{ border: 0, padding: 0, margin: 0 }}>
          <div className="field-row">
            <TextField label="提供方" onChange={(event) => set({ provider: event.target.value })} value={form.provider} />
            <TextField
              error={errors.contextWindow}
              inputMode="numeric"
              label="上下文窗口（token）"
              onChange={(event) => set({ contextWindow: event.target.value })}
              value={form.contextWindow}
            />
          </div>
          <ModalityPicker
            legend="输入模态"
            onChange={(inputModalities) => set({ inputModalities })}
            values={form.inputModalities}
          />
          <ModalityPicker
            legend="输出模态"
            onChange={(outputModalities) => set({ outputModalities })}
            values={form.outputModalities}
          />
          <fieldset className="weekday-picker">
            <legend>能力</legend>
            <Checkbox checked={form.supportsTools} label="工具调用" onChange={(event) => set({ supportsTools: event.target.checked })} />
            <Checkbox
              checked={form.supportsStructuredOutput}
              label="结构化输出"
              onChange={(event) => set({ supportsStructuredOutput: event.target.checked })}
            />
            <Checkbox checked={form.supportsVision} label="视觉" onChange={(event) => set({ supportsVision: event.target.checked })} />
          </fieldset>
          </fieldset>
          <TextareaField
            error={errors.parameterInfo}
            label="模型备注"
            maxLength={500}
            onChange={(event) => set({ parameterInfo: event.target.value })}
            rows={3}
            value={form.parameterInfo}
          />
        </div>
      </Collapsible>
      <Collapsible badge={form.tiers.length > 0 ? `${form.tiers.length} 档` : undefined} defaultOpen={form.tiers.length > 0} title="条件价格档">
        <fieldset disabled={syncing} style={{ border: 0, padding: 0, margin: 0 }}><TierEditor basePrices={form.prices} onChange={(tiers) => set({ tiers })} tiers={form.tiers} /></fieldset>
        <InlineError>{errors.tiers}</InlineError>
      </Collapsible>
      <InlineError>{save.isError ? errorMessage(save.error, '保存失败，请重试') : ''}</InlineError>
      <div className="form-actions">
        <Button onClick={onDone} type="button" variant="secondary">
          取消
        </Button>
        <Button loading={save.isPending}>{creating ? '上架模型' : '保存'}</Button>
      </div>
    </form>
  )
}

const columns: Column<AdminModel>[] = [
  {
    key: 'model',
    header: '模型',
    primary: true,
    cell: (model) => (
      <>
        <strong className="mono">{model.id}</strong>
        <small>{model.display_name}</small>
      </>
    ),
  },
  { key: 'source', header: '来源', cell: (model) => !model.source ? '手工' : !model.source.sync_enabled ? '已退出同步' : model.source.status === 'waiting_rate' ? '待汇率' : model.source.status === 'needs_review' ? '需人工处理' : model.source.status === 'missing' ? '来源缺失' : '自动同步' },
  {
    key: 'enabled',
    header: '状态',
    cell: (model) => <Badge tone={model.enabled ? 'success' : 'neutral'}>{model.enabled ? '启用' : '停用'}</Badge>,
  },
  { key: 'input', header: '输入', numeric: true, cell: (model) => model.source?.price_ready === false ? '待配置' : model.base_prices.input },
  { key: 'output', header: '输出', numeric: true, cell: (model) => model.source?.price_ready === false ? '待配置' : model.base_prices.output },
  { key: 'cache_read', header: '缓存读', numeric: true, cell: (model) => model.source?.price_ready === false ? '待配置' : model.base_prices.cache_read },
  { key: 'cache_write', header: '缓存写', numeric: true, cell: (model) => model.source?.price_ready === false ? '待配置' : model.base_prices.cache_write },
  {
    key: 'tiers',
    header: '价格档',
    numeric: true,
    cell: (model) => (model.price_tiers.length > 0 ? `${model.price_tiers.length} 档` : '—'),
  },
]

export function ModelsPage() {
  const models = useAdminModels()
 const [search, setSearch] = useState('')
 const [page, setPage] = useState(0)
 const filtered = (models.data?.items ?? []).filter(m => [m.id,m.display_name,m.provider,m.source?.key ?? ''].join(' ').toLowerCase().includes(search.toLowerCase())).sort((a,b) => a.sort_order-b.sort_order || a.id.localeCompare(b.id))
  const remove = useDeleteModel()
  useEffect(() => { setPage(current => Math.min(current, Math.max(0, Math.ceil(filtered.length / 50) - 1))) }, [filtered.length])
  const [deleting, setDeleting] = useState<AdminModel | null>(null)
  const [editing, setEditing] = useState<AdminModel | 'new' | null>(null)
  const close = () => setEditing(null)
  return (
    <>
      <PageHeader
        actions={
          <Button icon={<Icon name="plus" />} onClick={() => setEditing('new')} type="button">
            上架模型
          </Button>
        }
        title="模型"
      />
      <CatalogSync />
      <TextField label="查找模型" value={search} onChange={e => { setSearch(e.target.value); setPage(0) }} />
      <p className="muted-copy">{filtered.length} 个模型 · 第 {page + 1} 页</p>
      <Card flush>
        <QueryBoundary query={models}>
          {() => (
            <DataTable
              caption="模型目录"
              columns={[
                ...columns,
                {
                  key: 'action',
                  header: '操作',
                  cell: (model) => (
                    <div className="form-actions">
                      <Button onClick={() => setEditing(model)} size="sm" type="button" variant="secondary">编辑</Button>
                      <Button onClick={() => { remove.reset(); setDeleting(model) }} size="sm" type="button" variant="danger">删除</Button>
                    </div>
                  ),
                },
              ]}
              empty={<p className="muted-copy empty-pad">还没有模型</p>}
              rowKey={(model) => model.id}
              rows={filtered.slice(page * 50, (page + 1) * 50)}
            />
          )}
        </QueryBoundary>
      </Card>
      <div className="form-actions"><Button type="button" variant="secondary" disabled={page === 0} onClick={() => setPage(page - 1)}>上一页</Button><Button type="button" variant="secondary" disabled={(page + 1) * 50 >= filtered.length} onClick={() => setPage(page + 1)}>下一页</Button></div>
      <ConfirmDialog
        busy={remove.isPending}
        confirmLabel="删除"
        danger
        description="将删除模型、条件价格档及路由偏好，历史调用和账单保留。同步模型会保留忽略记录，后续同步不会重新创建。此操作不可撤销。"
        error={remove.isError ? errorMessage(remove.error, '删除失败，请重试') : ''}
        onClose={() => setDeleting(null)}
        onConfirm={() => deleting && remove.mutate(deleting.id, { onSuccess: () => setDeleting(null) })}
        open={deleting !== null}
        title={`删除 ${deleting?.id ?? '模型'}？`}
      />
      <Drawer
        onClose={close}
        open={editing !== null}
        title={editing === 'new' ? '上架模型' : editing ? `编辑 ${editing.id}` : ''}
      >
        {editing !== null && (
          <ModelEditor
            key={editing === 'new' ? 'new' : editing.id}
            model={editing === 'new' ? null : editing}
            onDone={close}
          />
        )}
      </Drawer>
    </>
  )
}
