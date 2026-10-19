import { DomainNav } from '../layouts/DomainNav'
import { useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { errorMessage } from '../api/query'
import {
  Badge,
  Button,
  ButtonLink,
  Card,
  Checkbox,
  ConfirmDialog,
  Dialog,
  Disclosure,
  EmptyState,
  InlineError,
  Notice,
  PageHeader,
  QueryBoundary,
  SelectField,
  TextareaField,
  TextField,
} from '../ui'
import { MarkdownContent } from '../markdown/MarkdownContent'
import {
  useReuseContributionAddress,
  useContributionIcon,
  useAddContributionChild,
  useContribution,
  useContributionAction,
  useContributions,
  useNewContribution,
  useSaveContribution,
  useTargets,
  type Changes,
  type Kind,
} from './queries'
import './contributions.css'
import { ReportPicker } from './ReportPicker'

const stateNames: Record<string, string> = {
  draft: '草稿',
  detecting: '检测中',
  invalid: '请补充内容',
  ready: '可提交',
  submitted: '待审核',
  rejected: '已驳回',
  withdrawn: '已撤回',
  publication_pending: '发布待完成',
  approved: '已批准',
  conflict: '生效版本已变化',
  supplier_has_groups: '供应商仍有分组',
  suite_pending: '检测套件待实现',
  report_required: '需要完整报告',
}
const kinds: Record<Kind, string> = {
  supplier: '供应商',
  group: '分组',
  group_model: '模型报价',
  model_id: '模型 ID 治理',
}
function Status({ value }: { value: string }) {
  return (
    <Badge
      tone={
        value === 'approved' || value === 'ready'
          ? 'success'
          : value === 'submitted' ||
              value === 'publication_pending' ||
              value === 'detecting'
            ? 'info'
            : value === 'conflict'
              ? 'danger'
              : 'neutral'
      }
    >
      {stateNames[value] ?? value}
    </Badge>
  )
}
function stamp(v: string) {
  return new Date(v).toLocaleString('zh-CN', { hour12: false })
}

export function ContributionsPage({ queue = false, initialKind='supplier',title }: { queue?: boolean;initialKind?:Kind;title?:string }) {
  const [cursor, setCursor] = useState(''),
    [creating, setCreating] = useState(false)
  const query = useContributions(queue, cursor),
    navigate = useNavigate()
  return (
    <>
      <PageHeader
        title={title??(queue ? '贡献审核' : '我的贡献')}
        actions={
          <Button type="button" onClick={() => setCreating(true)}>
            新建贡献
          </Button>
        }
      />
      {!queue && <DomainNav domain="供应" />}
      <Card>
        <QueryBoundary
          query={query}
          isEmpty={(d) => d.items.length === 0}
          empty={
            <EmptyState
              title={queue ? '暂无待审核贡献' : '还没有贡献'}
              description={
                queue
                  ? '成员提交后会出现在这里。'
                  : '可以贡献供应商、分组和模型报价。'
              }
            />
          }
        >
          {(data) => (
            <>
              <div className="contribution-list">
                {data.items.map((v) => (
                  <article key={v.id}>
                    <div>
                      <strong>{v.title}</strong>
                      <p className="muted-copy">
                        {kinds[v.kind as Kind] ?? v.kind} ·{' '}
                        {stamp(v.updated_at)}
                      </p>
                    </div>
                    <Status value={v.state} />
                    <ButtonLink
                      size="sm"
                      to={`${queue ? '/admin/contributions' : '/supply/contributions'}/${v.id}`}
                    >
                      查看
                    </ButtonLink>
                  </article>
                ))}
              </div>
              <div className="form-actions">
                <Button
                  type="button"
                  variant="quiet"
                  disabled={!cursor}
                  onClick={() => setCursor('')}
                >
                  回到最新
                </Button>
                <Button
                  type="button"
                  variant="secondary"
                  disabled={!data.next_cursor}
                  onClick={() => setCursor(data.next_cursor ?? '')}
                >
                  下一页
                </Button>
              </div>
            </>
          )}
        </QueryBoundary>
      </Card>
      <NewContribution
		initialKind={initialKind}
        open={creating}
        close={() => setCreating(false)}
        onCreated={(id) =>
          navigate(`${queue ? '/admin/contributions' : '/supply/contributions'}/${id}`)
        }
      />
    </>
  )
}
export function ContributionQueuePage() {
  return <ContributionsPage queue />
}
function NewContribution({
	initialKind,
  open,
  close,
  onCreated,
}: {
	initialKind:Kind
  open: boolean
  close: () => void
  onCreated: (id: string) => void
}) {
  const { account } = useAuth(),
    action = useNewContribution()
  const [kind, setKind] = useState<Kind>(initialKind),
    [mode, setMode] = useState(initialKind==='model_id'?'disable':'new'),
    [selected, setSelected] = useState(''),
    [cursor, setCursor] = useState(''),
    [starter, setStarter] = useState(true)
  const parentKind =
    mode === 'new'
      ? kind === 'group_model'
        ? 'group'
        : 'supplier'
      : kind === 'model_id'
        ? 'group_model'
        : kind
  const targets = useTargets(
    parentKind,
    cursor,
    open && (mode !== 'new' || kind === 'group' || kind === 'group_model'),
  )
  const submit = (e: FormEvent) => {
    e.preventDefault()
    action.mutate(
      {
        kind,
        action:
          mode === 'new' || mode === 'edit'
            ? 'edit'
            : (mode as 'delete' | 'disable' | 'restore'),
        target_id: mode === 'new' ? '' : selected,
        parent_id: mode === 'new' && kind !== 'supplier' ? selected : '',
        starter: mode === 'new' && starter,
        model_type: 'text',
      },
      { onSuccess: ({ draft }) => onCreated(draft.id) },
    )
  }
  const needTarget =
    mode !== 'new' || kind === 'group' || kind === 'group_model'
  return (
    <Dialog open={open} onClose={close} title="新建贡献">
      <form onSubmit={submit} className="stack-form">
        <SelectField
          label="对象"
          value={kind}
          onChange={(e) => {
            setKind(e.target.value as Kind)
            setSelected('')
            setCursor('')
            setMode(e.target.value === 'model_id' ? 'disable' : 'new')
          }}
        >
          <option value="supplier">供应商</option>
          <option value="group">分组</option>
          <option value="group_model">模型报价</option>
          {account?.is_admin && <option value="model_id">模型 ID 治理</option>}
        </SelectField>
        <SelectField
          label="操作"
          value={mode}
          onChange={(e) => {
            setMode(e.target.value)
            setSelected('')
            setCursor('')
          }}
        >
          {kind !== 'model_id' && <option value="new">新增</option>}
          {kind !== 'model_id' && <option value="edit">修改</option>}
          <option value="disable">停用</option>
          <option value="restore">恢复</option>
          {kind !== 'model_id' && <option value="delete">删除</option>}
        </SelectField>
        {needTarget &&
          (kind === 'model_id' ? (
            <TextField
              label="模型 ID"
              required
              value={selected}
              onChange={(e) => setSelected(e.target.value)}
            />
          ) : (
            <>
              <SelectField
                label={
                  mode === 'new'
                    ? kind === 'group'
                      ? '所属供应商'
                      : '所属分组'
                    : '选择对象'
                }
                required
                value={selected}
                onChange={(e) => setSelected(e.target.value)}
              >
                <option value="">请选择</option>
                {targets.data?.items.map((v) => (
                  <option key={v.id} value={v.id}>
                    {v.name}
                    {v.enabled ? '' : '（已停用）'}
                  </option>
                ))}
              </SelectField>
              <InlineError>
                {targets.isError && errorMessage(targets.error, '无法加载对象')}
              </InlineError>
              <div className="form-actions">
                <Button
                  type="button"
                  variant="quiet"
                  disabled={!cursor}
                  onClick={() => setCursor('')}
                >
                  第一页
                </Button>
                <Button
                  type="button"
                  variant="quiet"
                  disabled={!targets.data?.next_cursor}
                  onClick={() => setCursor(targets.data?.next_cursor ?? '')}
                >
                  下一页
                </Button>
              </div>
            </>
          ))}
        {mode === 'new' && kind !== 'group_model' && (
          <Checkbox
            label={
              kind === 'supplier'
                ? '同时贡献首个分组与模型报价'
                : '同时贡献首个模型报价'
            }
            checked={starter}
            onChange={(e) => setStarter(e.target.checked)}
          />
        )}{' '}
        {mode === 'delete' && (
          <Notice tone="warning">
            批准后执行业务删除。分组关联渠道也会删除；供应商仍有分组时不能删除。
          </Notice>
        )}
        <InlineError>
          {action.isError && errorMessage(action.error, '无法建立草稿')}
        </InlineError>
        <Button
          type="submit"
          loading={action.isPending}
          disabled={needTarget && !selected}
        >
          建立草稿
        </Button>
      </form>
    </Dialog>
  )
}

export function ContributionDetailPage() {
  const { draftID = '' } = useParams(),
    query = useContribution(draftID)
  return (
    <QueryBoundary query={query}>
      {(data) => (
        <Editor
          key={`${data.draft.id}:${data.draft.revision}`}
          data={data}
          reload={() => void query.refetch()}
        />
      )}
    </QueryBoundary>
  )
}
type Detail = NonNullable<ReturnType<typeof useContribution>['data']>
function Editor({ data, reload }: { data: Detail; reload: () => void }) {
  const d = data.draft,
    { account } = useAuth(),
    admin = !!account?.is_admin
  const [title, setTitle] = useState(d.title),
    [c, setC] = useState<Changes>(structuredClone(d.changes)),
    [reports, setReports] = useState(d.report_ids.join('\n')),
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
    ['draft', 'rejected', 'withdrawn'].includes(d.state) ||
    (admin && d.state === 'submitted')
  const dirty =
    title !== d.title ||
    reports !== d.report_ids.join('\n') ||
    JSON.stringify(c) !== JSON.stringify(d.changes)
  const busy = save.isPending || action.isPending || add.isPending || iconBusy,
    invalid = Object.values(jsonErrors).some(Boolean)
  const reportIDs = reports.split(/\s+/).filter(Boolean)
  const saveDraft = (e: FormEvent) => {
    e.preventDefault()
    save.mutate({
      revision: d.revision,
      title,
      changes: c,
      report_ids: reportIDs,
    })
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
  const basePath = admin ? '/admin/contributions' : '/supply/contributions'
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
      <PageHeader
        title={d.title}
        actions={
          <ButtonLink to={basePath} variant="secondary">
            返回列表
          </ButtonLink>
        }
      />
      <div className="contribution-status">
        <Status value={data.stage} />
        <span className="muted-copy">
          修订 {d.revision} · {stamp(d.updated_at)}
        </span>
      </div>
      {data.stage === 'conflict' && (
        <Notice tone="warning">
          生效数据已变化。请基于最新对象新建修订，避免覆盖其他人的修改。
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
      {dirty && <Notice tone="info">请先保存修改，再检测或提交审核。</Notice>}
      <form onSubmit={saveDraft} className="contribution-form">
        <Card title="贡献内容">
          <fieldset
            disabled={!editable || busy}
            className="contribution-fields"
          >
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
                        set({
                          icon_data: String(reader.result),
                          icon_source: '',
                        })
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
                    (s) =>
                      s.value.id === v.supplier_id && s.base_revision === 0,
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
                    {c.models.find((m) => m.value.id === v.group_model_id)
                      ?.value.model_id || '模型'}{' '}
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
                          set({
                            prices: { ...v.prices, [key]: e.target.value },
                          })
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
            <ReportPicker
              tasks={d.task_ids}
              targets={data.evaluation.targets.map((t) => t.id)}
              value={reportIDs}
              onChange={(values) => setReports(values.join('\n'))}
            />
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
        </Card>
      </form>
      <Card title="检测与报告">
        <Notice tone="info">
          创建需要全部适用项目测完，检测失败或不支持也可以提交。未实现套件和未执行项目不会算作完成。
        </Notice>
        {data.evaluation.targets.length === 0 && (
          <p className="muted-copy">
            资料未填完整时，请先保存。纯供应商资料及紧急停用、删除不要求付费检测。
          </p>
        )}
        <div className="contribution-list">
          {data.evaluation.targets.map((target) => (
            <article key={target.id}>
              <span>{target.model_id || '分组检测'}</span>
              <Status value={target.state} />
              {target.report ? (
                <ButtonLink
                  size="sm"
                  to={`/supply/detection/reports/${target.report.report_id}`}
                >
                  {target.report.reused ? '查看复用报告' : '查看报告'}
                </ButtonLink>
              ) : (
                <ButtonLink
                  size="sm"
                  to={`/supply/detection?draft_id=${d.id}&target_kind=${target.kind}&target_id=${target.id}`}
                  aria-disabled={dirty}
                  onClick={(e) => {
                    if (dirty) e.preventDefault()
                  }}
                >
                  开始全测
                </ButtonLink>
              )}
              {target.report && (
                <p className="muted-copy">
                  {stamp(target.report.finished_at)} · 范围{' '}
                  {target.report.manifest_version}
                </p>
              )}
            </article>
          ))}
        </div>
        {d.task_ids.map((id) => (
          <p key={id}>
            <ButtonLink variant="quiet" to={`/supply/detection/${id}`}>
              查看关联检测任务
            </ButtonLink>
          </p>
        ))}
      </Card>
      <Card title="变更与影响">
        <p>
          关联渠道：<strong className="num">{data.linked_channels}</strong>
        </p>
        {c.groups.some((v) => v.value.deleted_at) && (
          <Notice tone="warning">
            批准删除分组将业务删除其全部关联渠道；历史账本和在途结算保留。
          </Notice>
        )}
        <ChangeDiff base={d.base} changes={c} />
      </Card>
      <Card title={admin ? '审核操作' : '提交审核'}>
        <InlineError>
          {action.isError && errorMessage(action.error, '操作失败')}
        </InlineError>
        {admin && (
          <TextareaField
            label="审核原因"
            value={reason}
            maxLength={1000}
            onChange={(e) => setReason(e.target.value)}
          />
        )}
        <div className="form-actions">
          {['draft', 'rejected', 'withdrawn'].includes(d.state) && (
            <Button
              type="button"
              disabled={busy || dirty || invalid || data.stage !== 'ready'}
              loading={action.isPending}
              onClick={() => act('submit')}
            >
              提交审核
            </Button>
          )}
          {['draft', 'submitted', 'rejected'].includes(d.state) && (
            <Button
              type="button"
              variant="quiet"
              disabled={busy || dirty}
              onClick={() => act('withdraw')}
            >
              撤回
            </Button>
          )}
          {admin && ['submitted', 'publication_pending'].includes(d.state) && (
            <Button
              type="button"
              disabled={busy || dirty}
              loading={action.isPending}
              onClick={() => setConfirm(true)}
            >
              {d.state === 'publication_pending' ? '重试完成发布' : '批准发布'}
            </Button>
          )}
          {admin && d.state === 'submitted' && (
            <Button
              type="button"
              variant="secondary"
              disabled={busy || dirty || !reason.trim()}
              onClick={() => act('reject')}
            >
              驳回
            </Button>
          )}
          <Button
            type="button"
            variant="quiet"
            disabled={busy}
            onClick={reload}
          >
            重新载入
          </Button>
        </div>
      </Card>
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
function ChangeDiff({ base, changes }: { base: Changes; changes: Changes }) {
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
  draft: DraftForAddress
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
type DraftForAddress = Detail['draft']
