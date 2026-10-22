import { useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import { MarkdownContent } from '../markdown/MarkdownContent'
import {
  Badge,
  Button,
  Checkbox,
  ConfirmDialog,
  Dialog,
  Disclosure,
  InlineError,
  Notice,
  QueryBoundary,
  SelectField,
  TextareaField,
  TextField,
} from '../ui'
import {
  useAddContributionChild,
  useContribution,
  useContributionAction,
  useContributionIcon,
  useNewContribution,
  useReuseContributionAddress,
  useSaveContribution,
  useTargets,
  type Changes,
  type Draft,
  type NewInput,
} from './queries'
import './contributions.css'

export const stateNames: Record<string, string> = {
  draft: '草稿',
  invalid: '请补充内容',
  ready: '可提交',
  submitted: '待审核',
  rejected: '已驳回',
  withdrawn: '已撤回',
  publication_pending: '发布待完成',
  approved: '已批准',
  conflict: '生效版本已变化',
  supplier_has_groups: '供应商仍有分组',
}
export function StateBadge({ value }: { value: string }) {
  return (
    <Badge
      tone={
        value === 'approved' || value === 'ready'
          ? 'success'
          : value === 'submitted' || value === 'publication_pending'
            ? 'info'
            : value === 'conflict' || value === 'rejected'
              ? 'danger'
              : 'neutral'
      }
    >
      {stateNames[value] ?? value}
    </Badge>
  )
}

type Detail = NonNullable<ReturnType<typeof useContribution>['data']>

/** 实体页就地编辑：建立或续用一份草稿，保存后提交审核；不修改线上数据。 */
export function ContributionEditor({
  draftID,
  review = false,
  onClose,
}: {
  draftID: string
  review?: boolean
  onClose?: () => void
}) {
  const query = useContribution(draftID)
  return (
    <QueryBoundary query={query}>
      {(data) => (
        <Editor
          key={`${data.draft.id}:${data.draft.revision}`}
          data={data}
          review={review}
          onClose={onClose}
        />
      )}
    </QueryBoundary>
  )
}

/** 实体页上的入口。没有现成草稿时按实体建立一份，之后复用编辑器。 */
export function ContributionDialog({
  create,
  draftID,
  label,
  title,
  size = 'sm',
  variant = 'secondary',
  disabled,
}: {
  create?: NewInput
  draftID?: string
  label: string
  title: string
  size?: 'sm' | 'md'
  variant?: 'primary' | 'secondary' | 'quiet'
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [id, setId] = useState(draftID ?? '')
  const start = useNewContribution()
  const openDialog = () => {
    setOpen(true)
    if (!id && create) {
      start.mutate(create, { onSuccess: ({ draft }) => setId(draft.id) })
    }
  }
  return (
    <>
      <Button
        type="button"
        size={size}
        variant={variant}
        disabled={disabled}
        onClick={openDialog}
      >
        {label}
      </Button>
      <Dialog
        open={open}
        onClose={() => setOpen(false)}
        title={title}
        busy={start.isPending}
      >
        <InlineError>
          {start.isError && errorMessage(start.error, '无法开始编辑')}
        </InlineError>
        {id ? (
          <ContributionEditor
            draftID={id}
            review={false}
            onClose={() => setOpen(false)}
          />
        ) : (
          <p className="muted-copy">正在建立草稿…</p>
        )}
      </Dialog>
    </>
  )
}

function Editor({
  data,
  review,
  onClose,
}: {
  data: Detail
  review: boolean
  onClose?: () => void
}) {
  const d = data.draft,
    { account } = useAuth()
  const [title, setTitle] = useState(d.title),
    [c, setC] = useState<Changes>(structuredClone(d.changes)),
    [reason, setReason] = useState(''),
    [confirm, setConfirm] = useState(false),
    [jsonErrors, setJsonErrors] = useState<Record<string, boolean>>({}),
    [iconBusy, setIconBusy] = useState(false),
    [iconError, setIconError] = useState('')
  const iconAction = useContributionIcon(),
    save = useSaveContribution(d.id),
    action = useContributionAction(d.id),
    add = useAddContributionChild(d.id)
  const editable =
    !review &&
    (['draft', 'rejected', 'withdrawn'].includes(d.state) ||
      (!!account?.is_admin && d.state === 'submitted'))
  const dirty =
    title !== d.title || JSON.stringify(c) !== JSON.stringify(d.changes)
  const busy = save.isPending || action.isPending || add.isPending || iconBusy,
    invalid = Object.values(jsonErrors).some(Boolean)
  const saveDraft = (e: FormEvent) => {
    e.preventDefault()
    save.mutate({ revision: d.revision, title, changes: c })
  }
  const update = <K extends keyof Changes>(
    key: K,
    index: number,
    value: Changes[K][number],
  ) =>
    setC((old) => ({
      ...old,
      [key]: old[key].map((v, i) => (i === index ? value : v)),
    }))
  const jsonValid = (id: string, bad: boolean) =>
    setJsonErrors((v) => ({ ...v, [id]: bad }))
  const act = (type: 'submit' | 'withdraw' | 'approve' | 'reject') =>
    action.mutate({ action: type, revision: d.revision, reason })
  const fetchIcon = async (index: number) => {
    const source = c.suppliers[index].value.website_url
    setIconBusy(true)
    setIconError('')
    try {
      const result = await iconAction.mutateAsync(source)
      setC((v) => ({
        ...v,
        suppliers: v.suppliers.map((s, i) =>
          i === index && s.value.website_url === source
            ? { ...s, value: { ...s.value, ...result } }
            : s,
        ),
      }))
    } catch (err) {
      setIconError(errorMessage(err, '图标获取失败'))
    } finally {
      setIconBusy(false)
    }
  }
  return (
    <>
      <div className="contribution-status">
        <StateBadge value={data.stage} />
        <span className="muted-copy">
          修订 {d.revision} · {stamp(d.updated_at)}
        </span>
      </div>
      {data.stage === 'conflict' && (
        <Notice tone="warning">
          生效数据已变化。请基于最新对象重新发起修改，避免覆盖其他人的修改。
        </Notice>
      )}
      {data.stage === 'supplier_has_groups' && (
        <Notice tone="warning">供应商仍有分组，请先逐个处理并删除分组。</Notice>
      )}
      {d.state === 'publication_pending' && (
        <Notice tone="warning">
          数据已发布，快照刷新尚未完成。管理员可以重试完成发布。
        </Notice>
      )}
      {d.review_reason && (
        <Notice tone={d.state === 'rejected' ? 'warning' : 'info'}>
          {d.review_reason}
        </Notice>
      )}
      {editable && dirty && (
        <Notice tone="info">请先保存修改，再提交审核。</Notice>
      )}
      <form onSubmit={saveDraft} className="contribution-form">
        <fieldset disabled={!editable || busy} className="contribution-fields">
          <TextField
            label="标题"
            value={title}
            maxLength={128}
            required
            onChange={(e) => setTitle(e.target.value)}
          />
          {c.suppliers.map((change, index) => {
            const v = change.value
            const set = (value: Partial<typeof v>) =>
              update('suppliers', index, {
                ...change,
                value: { ...v, ...value },
              })
            return (
              <section key={v.id} className="contribution-section">
                <h3>供应商</h3>
                <TextField
                  label="名称"
                  value={v.name}
                  maxLength={64}
                  onChange={(e) => set({ name: e.target.value })}
                />
                <SelectField
                  label="类型"
                  value={v.kind}
                  onChange={(e) =>
                    set({ kind: e.target.value as typeof v.kind })
                  }
                >
                  <option value="official_api">官方 API</option>
                  <option value="official_subscription">官方订阅</option>
                  <option value="relay">中转站</option>
                </SelectField>
                <TextField
                  label="官网"
                  value={v.website_url}
                  onChange={(e) => set({ website_url: e.target.value })}
                />
                <TextField
                  label="文档地址"
                  value={v.docs_url}
                  onChange={(e) => set({ docs_url: e.target.value })}
                />
                <TextField
                  label="一句话简介"
                  value={v.summary}
                  maxLength={160}
                  onChange={(e) => set({ summary: e.target.value })}
                />
                <TextareaField
                  label="介绍（Markdown）"
                  value={v.description}
                  maxLength={8000}
                  onChange={(e) => set({ description: e.target.value })}
                />
                <div className="form-actions">
                  {v.icon_data && (
                    <img
                      className="contribution-icon"
                      src={v.icon_data}
                      alt="供应商图标"
                    />
                  )}
                  <Button
                    type="button"
                    variant="secondary"
                    disabled={!v.website_url}
                    loading={iconBusy}
                    onClick={() => void fetchIcon(index)}
                  >
                    获取官网图标
                  </Button>
                  <Button
                    type="button"
                    variant="quiet"
                    onClick={() => set({ icon_data: '', icon_source: '' })}
                  >
                    移除图标
                  </Button>
                </div>
                <InlineError>{iconError}</InlineError>
                <TextField
                  type="file"
                  label="上传图标"
                  accept="image/png,image/jpeg,image/gif,image/webp,image/x-icon"
                  onChange={(e) => {
                    const file = e.target.files?.[0]
                    if (!file) return
                    if (file.size > 256 * 1024) {
                      setIconError('图标不能超过 256 KiB')
                      return
                    }
                    const reader = new FileReader()
                    reader.onload = () =>
                      set({ icon_data: String(reader.result), icon_source: '' })
                    reader.readAsDataURL(file)
                  }}
                />
                <Disclosure title="预览介绍">
                  <MarkdownContent value={v.description} />
                </Disclosure>
                <Checkbox
                  label="启用供应商"
                  checked={v.enabled}
                  onChange={(e) => set({ enabled: e.target.checked })}
                />
                {change.base_revision === 0 && (
                  <Button
                    type="button"
                    variant="secondary"
                    disabled={dirty}
                    onClick={() =>
                      add.mutate({
                        revision: d.revision,
                        kind: 'group',
                        parent_id: v.id,
                      })
                    }
                  >
                    添加分组
                  </Button>
                )}
              </section>
            )
          })}
          {c.groups.map((change, index) => {
            const v = change.value
            const set = (value: Partial<typeof v>) =>
              update('groups', index, {
                ...change,
                value: { ...v, ...value },
              })
            return (
              <section key={v.id} className="contribution-section">
                <h3>分组</h3>
                <TextField
                  label="分组名称"
                  value={v.name}
                  onChange={(e) => set({ name: e.target.value })}
                />
                <TextareaField
                  label="分组介绍"
                  value={v.description}
                  onChange={(e) => set({ description: e.target.value })}
                />
                <TextField
                  label="状态页（可选）"
                  value={v.status_url}
                  onChange={(e) => set({ status_url: e.target.value })}
                />
                <JSONField
                  label="默认设置"
                  value={v.defaults}
                  set={(value) => set({ defaults: value })}
                  valid={(bad) => jsonValid('defaults-' + v.id, bad)}
                />
                <Checkbox
                  label="启用分组"
                  checked={v.enabled}
                  onChange={(e) => set({ enabled: e.target.checked })}
                />
                <div className="form-actions">
                  <Button
                    type="button"
                    variant="secondary"
                    disabled={dirty}
                    onClick={() =>
                      add.mutate({
                        revision: d.revision,
                        kind: 'address',
                        parent_id: v.id,
                      })
                    }
                  >
                    添加地址
                  </Button>
                  <Button
                    type="button"
                    variant="secondary"
                    disabled={dirty}
                    onClick={() =>
                      add.mutate({
                        revision: d.revision,
                        kind: 'group_model',
                        parent_id: v.id,
                      })
                    }
                  >
                    添加模型报价
                  </Button>
                </div>
                {!c.suppliers.some(
                  (s) => s.value.id === v.supplier_id && s.base_revision === 0,
                ) && (
                  <ReuseAddress
                    draft={d}
                    groupID={v.id}
                    supplierID={v.supplier_id}
                    disabled={dirty || busy}
                  />
                )}
              </section>
            )
          })}
          {c.addresses.map((change, index) => {
            const v = change.value
            return (
              <section key={v.id} className="contribution-section">
                <h3>地址</h3>
                <TextField
                  label="地址标签"
                  value={v.label}
                  onChange={(e) =>
                    update('addresses', index, {
                      ...change,
                      value: { ...v, label: e.target.value },
                    })
                  }
                />
                <TextField
                  label="Base URL"
                  value={v.base_url}
                  onChange={(e) =>
                    update('addresses', index, {
                      ...change,
                      value: { ...v, base_url: e.target.value },
                    })
                  }
                />
              </section>
            )
          })}
          {c.group_addresses.map((set, index) => (
            <div key={set.group_id} className="contribution-section">
              <h3>分组地址顺序</h3>
              {set.links.map((link, i) => (
                <div key={link.id} className="contribution-order">
                  <span>
                    {c.addresses.find((v) => v.value.id === link.address_id)
                      ?.value.label ||
                      c.addresses.find((v) => v.value.id === link.address_id)
                        ?.value.base_url ||
                      data.addresses.find((a) => a.id === link.address_id)
                        ?.label ||
                      data.addresses.find((a) => a.id === link.address_id)
                        ?.base_url ||
                      '地址'}
                  </span>
                  <Button
                    type="button"
                    variant="quiet"
                    disabled={i === 0}
                    onClick={() => {
                      const links = [...set.links]
                      ;[links[i - 1], links[i]] = [links[i], links[i - 1]]
                      update('group_addresses', index, { ...set, links })
                    }}
                  >
                    上移
                  </Button>
                  <Button
                    type="button"
                    variant="quiet"
                    onClick={() =>
                      update('group_addresses', index, {
                        ...set,
                        links: set.links.filter((v) => v.id !== link.id),
                      })
                    }
                  >
                    移除引用
                  </Button>
                </div>
              ))}
            </div>
          ))}
          {c.models.map((change, index) => {
            const v = change.value
            const set = (value: Partial<typeof v>) =>
              update('models', index, {
                ...change,
                value: { ...v, ...value },
              })
            return (
              <section key={v.id} className="contribution-section">
                <h3>模型报价</h3>
                <TextField
                  label="模型 ID"
                  value={v.model_id}
                  maxLength={256}
                  onChange={(e) => set({ model_id: e.target.value })}
                />
                <TextField
                  label="显示名（可选）"
                  value={v.display_name}
                  onChange={(e) => set({ display_name: e.target.value })}
                />
                <TextField
                  label="上游模型名（留空使用模型 ID）"
                  value={v.upstream_model}
                  onChange={(e) => set({ upstream_model: e.target.value })}
                />
                <SelectField
                  label="模型类型"
                  value={v.model_type}
                  onChange={(e) =>
                    set({ model_type: e.target.value as typeof v.model_type })
                  }
                >
                  <option value="text">文本 / 多模态</option>
                  <option value="image">图片生成</option>
                </SelectField>
                <TextField
                  label="上下文长度（可选）"
                  inputMode="numeric"
                  value={v.context_window ?? ''}
                  onChange={(e) =>
                    set({
                      context_window: e.target.value
                        ? Number(e.target.value)
                        : null,
                    })
                  }
                />
                <TextField
                  label="最大输出（可选）"
                  inputMode="numeric"
                  value={v.max_output_tokens ?? ''}
                  onChange={(e) =>
                    set({
                      max_output_tokens: e.target.value
                        ? Number(e.target.value)
                        : null,
                    })
                  }
                />
                <TextareaField
                  label="报价说明"
                  value={v.notes}
                  onChange={(e) => set({ notes: e.target.value })}
                />
                <Checkbox
                  label="启用模型"
                  checked={v.enabled}
                  onChange={(e) => set({ enabled: e.target.checked })}
                />
                <Checkbox
                  label="作为此模型的原厂参考源（仅官方 API）"
                  checked={v.is_reference}
                  onChange={(e) => set({ is_reference: e.target.checked })}
                />
              </section>
            )
          })}
          {c.quotes.map((v, index) => {
            const set = (value: Partial<typeof v>) =>
              update('quotes', index, { ...v, ...value })
            return (
              <section className="contribution-section" key={v.id}>
                <h3>
                  {c.models.find((m) => m.value.id === v.group_model_id)?.value
                    .model_id || '模型'}{' '}
                  · 价格
                </h3>
                <SelectField
                  label="币种"
                  value={v.currency}
                  onChange={(e) =>
                    set({ currency: e.target.value as typeof v.currency })
                  }
                >
                  <option value="CNY">CNY / 积分</option>
                  <option value="USD">USD</option>
                </SelectField>
                <SelectField
                  label="计价单位"
                  value={v.billing_unit}
                  onChange={(e) =>
                    set({
                      billing_unit: e.target.value as typeof v.billing_unit,
                    })
                  }
                >
                  <option value="per_million_tokens">每百万 token</option>
                  <option value="per_request">每次请求</option>
                  <option value="per_successful_image">每张成功图片</option>
                </SelectField>
                {v.billing_unit === 'per_million_tokens' &&
                  (
                    ['input', 'output', 'cache_write', 'cache_read'] as const
                  ).map((key) => (
                    <TextField
                      key={key}
                      inputMode="decimal"
                      label={priceNames[key]}
                      value={v.prices[key]}
                      placeholder="请填写价格，免费请明确填写 0"
                      onChange={(e) =>
                        set({ prices: { ...v.prices, [key]: e.target.value } })
                      }
                    />
                  ))}
                <JSONField
                  label="模态 / 缓存细分价格"
                  value={v.prices.details}
                  set={(details) => set({ prices: { ...v.prices, details } })}
                  valid={(bad) => jsonValid('prices-' + v.id, bad)}
                />
                <JSONField
                  label="条件价格"
                  value={v.tiers}
                  set={(tiers) => set({ tiers })}
                  valid={(bad) => jsonValid('tiers-' + v.id, bad)}
                />
                <JSONField
                  label="按次、图片与调用内工具单价"
                  value={v.unit_prices}
                  set={(unit_prices) => set({ unit_prices })}
                  valid={(bad) => jsonValid('units-' + v.id, bad)}
                />
              </section>
            )
          })}
          {c.governance.map((change, index) => {
            const v = change.value
            return (
              <section className="contribution-section" key={v.model_id}>
                <h3>{v.model_id}</h3>
                <Checkbox
                  label="停用此模型 ID 的全部报价"
                  checked={v.disabled}
                  onChange={(e) =>
                    update('governance', index, {
                      ...change,
                      value: { ...v, disabled: e.target.checked },
                    })
                  }
                />
                <TextareaField
                  label="治理原因"
                  value={v.reason}
                  maxLength={1000}
                  onChange={(e) =>
                    update('governance', index, {
                      ...change,
                      value: { ...v, reason: e.target.value },
                    })
                  }
                />
              </section>
            )
          })}
        </fieldset>
        <InlineError>
          {save.isError && errorMessage(save.error, '保存失败')}
        </InlineError>
        <InlineError>
          {add.isError && errorMessage(add.error, '无法添加对象')}
        </InlineError>
        {editable && (
          <div className="form-actions">
            <Button
              type="submit"
              disabled={!dirty || invalid || busy}
              loading={save.isPending}
            >
              保存草稿
            </Button>
          </div>
        )}
      </form>
      <section className="contribution-section">
        <h3>变更与影响</h3>
        <p>
          关联渠道：<strong className="num">{data.linked_channels}</strong>
        </p>
        {c.groups.some((v) => v.value.deleted_at) && (
          <Notice tone="warning">
            批准删除分组将业务删除其全部关联渠道；历史账本和在途结算保留。
          </Notice>
        )}
        <ChangeDiff base={d.base} changes={c} />
      </section>
      <section className="contribution-section">
        <h3>{review ? '审核操作' : '提交审核'}</h3>
        <InlineError>
          {action.isError && errorMessage(action.error, '操作失败')}
        </InlineError>
        {review && (
          <TextareaField
            label="审核原因"
            value={reason}
            maxLength={1000}
            onChange={(e) => setReason(e.target.value)}
          />
        )}
        <div className="form-actions">
          {editable && ['draft', 'rejected', 'withdrawn'].includes(d.state) && (
            <Button
              type="button"
              disabled={busy || dirty || invalid || data.stage !== 'ready'}
              loading={action.isPending}
              onClick={() => act('submit')}
            >
              提交审核
            </Button>
          )}
          {!review &&
            ['draft', 'submitted', 'rejected'].includes(d.state) && (
              <Button
                type="button"
                variant="quiet"
                disabled={busy || dirty}
                onClick={() => act('withdraw')}
              >
                撤回
              </Button>
            )}
          {review &&
            ['submitted', 'publication_pending'].includes(d.state) && (
              <Button
                type="button"
                disabled={busy || dirty}
                loading={action.isPending}
                onClick={() => setConfirm(true)}
              >
                {d.state === 'publication_pending' ? '重试完成发布' : '批准发布'}
              </Button>
            )}
          {review && d.state === 'submitted' && (
            <Button
              type="button"
              variant="secondary"
              disabled={busy || dirty || !reason.trim()}
              onClick={() => act('reject')}
            >
              驳回
            </Button>
          )}
          {onClose && (
            <Button type="button" variant="quiet" disabled={busy} onClick={onClose}>
              关闭
            </Button>
          )}
        </div>
      </section>
      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        title="批准此修订"
        description={`本次影响 ${data.linked_channels} 个关联渠道。${c.groups.some((v) => v.value.deleted_at) ? '关联渠道将被业务删除。' : '渠道将同步使用批准后的配置。'}历史账本和在途结算保留。`}
        confirmLabel="确认批准"
        onConfirm={() => {
          setConfirm(false)
          act('approve')
        }}
      />
    </>
  )
}

const priceNames = {
  input: '输入单价',
  output: '输出单价',
  cache_write: '缓存写入单价',
  cache_read: '缓存读取单价',
}
function stamp(v: string) {
  return new Date(v).toLocaleString('zh-CN', { hour12: false })
}

function JSONField<T>({
  label,
  value,
  set,
  valid,
}: {
  label: string
  value: T
  set: (value: T) => void
  valid: (bad: boolean) => void
}) {
  const [text, setText] = useState(() => JSON.stringify(value, null, 2)),
    [error, setError] = useState('')
  return (
    <Disclosure title={label}>
      <TextareaField
        label={label}
        rows={8}
        className="contribution-json"
        value={text}
        onChange={(e) => {
          setText(e.target.value)
          try {
            const v = JSON.parse(e.target.value) as T
            if (
              (Array.isArray(value) && !Array.isArray(v)) ||
              !v ||
              typeof v !== 'object'
            )
              throw Error()
            set(v)
            setError('')
            valid(false)
          } catch {
            setError('请输入有效的 JSON')
            valid(true)
          }
        }}
      />
      <InlineError>{error}</InlineError>
    </Disclosure>
  )
}

const labels: Record<string, string> = {
  ...priceNames,
  icon_data: '图标',
  icon_source: '图标来源',
  name: '名称',
  kind: '类型',
  website_url: '官网',
  docs_url: '文档',
  summary: '简介',
  description: '介绍',
  enabled: '启用',
  model_id: '模型 ID',
  display_name: '显示名',
  upstream_model: '上游模型',
  model_type: '模型类型',
  context_window: '上下文',
  max_output_tokens: '最大输出',
  is_reference: '原厂参考源',
  currency: '币种',
  billing_unit: '计价单位',
  prices: '价格',
  tiers: '条件价',
  unit_prices: '图片 / 工具单价',
  defaults: '默认设置',
  base_url: '地址',
  label: '地址标签',
  status_url: '状态页',
  notes: '说明',
  disabled: '停用',
  reason: '原因',
  deleted_at: '删除',
  links: '地址顺序',
}
export function ChangeDiff({
  base,
  changes,
}: {
  base: Changes
  changes: Changes
}) {
  const rows: { name: string; field: string; before: string; after: string }[] =
    []
  const show = (v: unknown) =>
    v === undefined
      ? '（未设置）'
      : typeof v === 'string'
        ? v
        : JSON.stringify(v)
  const compare = (
    name: string,
    a: Record<string, unknown>,
    b: Record<string, unknown>,
  ) => {
    for (const key of new Set([...Object.keys(a), ...Object.keys(b)])) {
      if (
        [
          'id',
          'revision',
          'updated_at',
          'created_at',
          'capability_digest',
          'connection_version',
          'contributor_id',
          'actor_id',
          'quote_id',
          'version',
          'supplier_id',
          'group_id',
          'group_model_id',
        ].includes(key)
      )
        continue
      if (JSON.stringify(a[key]) !== JSON.stringify(b[key]))
        rows.push({
          name,
          field: labels[key] ?? key,
          before:
            key === 'icon_data' ? (a[key] ? '已设置' : '未设置') : show(a[key]),
          after:
            key === 'icon_data' ? (b[key] ? '已设置' : '未设置') : show(b[key]),
        })
    }
  }
  for (const kind of [
    'suppliers',
    'groups',
    'addresses',
    'models',
    'governance',
  ] as const) {
    for (const change of changes[kind]) {
      const v = change.value as unknown as Record<string, unknown>,
        key = kind === 'governance' ? 'model_id' : 'id'
      const prior = base[kind].find(
        (a) => (a.value as unknown as Record<string, unknown>)[key] === v[key],
      )
      compare(
        String(v.name || v.model_id || v.label || '对象'),
        (prior?.value as unknown as Record<string, unknown>) ?? {},
        v,
      )
    }
  }
  for (const set of changes.group_addresses) {
    const prior = base.group_addresses.find((v) => v.group_id === set.group_id)
    const addresses = (links: typeof set.links | undefined) =>
      links?.map(
        (v) =>
          changes.addresses.find((a) => a.value.id === v.address_id)?.value
            .label ||
          changes.addresses.find((a) => a.value.id === v.address_id)?.value
            .base_url ||
          v.address_id,
      )
    compare(
      '分组地址顺序',
      { links: addresses(prior?.links) },
      { links: addresses(set.links) },
    )
  }
  for (const q of changes.quotes) {
    const prior = base.quotes.find((v) => v.group_model_id === q.group_model_id)
    compare(
      '模型报价',
      (prior as unknown as Record<string, unknown>) ?? {},
      q as unknown as Record<string, unknown>,
    )
  }
  return (
    <Disclosure title={`字段差异（${rows.length}）`}>
      <div className="contribution-diff">
        {rows.length === 0 ? (
          <p className="muted-copy">没有字段变化</p>
        ) : (
          rows.map((r, i) => (
            <div key={i}>
              <strong>
                {r.name} · {r.field}
              </strong>
              <div>
                <span className="muted-copy">原：</span>
                <code>{r.before || '（空）'}</code>
              </div>
              <div>
                <span className="muted-copy">新：</span>
                <code>{r.after || '（空）'}</code>
              </div>
            </div>
          ))
        )}
      </div>
    </Disclosure>
  )
}

function ReuseAddress({
  draft,
  groupID,
  supplierID,
  disabled,
}: {
  draft: Draft
  groupID: string
  supplierID: string
  disabled: boolean
}) {
  const [cursor, setCursor] = useState(''),
    [selected, setSelected] = useState('')
  const choices = useTargets('address', cursor, true, supplierID),
    action = useReuseContributionAddress(draft.id)
  return (
    <Disclosure title="复用同供应商地址">
      <SelectField
        label="已批准地址"
        value={selected}
        onChange={(e) => setSelected(e.target.value)}
      >
        <option value="">请选择地址</option>
        {choices.data?.items.map((a) => (
          <option key={a.id} value={a.id}>
            {a.name}
          </option>
        ))}
      </SelectField>
      <InlineError>
        {choices.isError && errorMessage(choices.error, '无法读取地址')}
      </InlineError>
      <InlineError>
        {action.isError && errorMessage(action.error, '无法复用地址')}
      </InlineError>
      <div className="form-actions">
        <Button
          type="button"
          variant="quiet"
          disabled={disabled || !cursor}
          onClick={() => setCursor('')}
        >
          第一页
        </Button>
        <Button
          type="button"
          variant="quiet"
          disabled={disabled || !choices.data?.next_cursor}
          onClick={() => setCursor(choices.data?.next_cursor ?? '')}
        >
          下一页
        </Button>
        <Button
          type="button"
          variant="secondary"
          disabled={disabled || !selected}
          loading={action.isPending}
          onClick={() =>
            action.mutate({
              revision: draft.revision,
              group_id: groupID,
              address_id: selected,
            })
          }
        >
          引用此地址
        </Button>
      </div>
    </Disclosure>
  )
}
