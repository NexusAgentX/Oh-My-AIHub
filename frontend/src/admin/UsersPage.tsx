import { Facts } from '../ui/Facts'
import { Outlet, useLocation, useOutletContext, useParams } from 'react-router-dom'
import { Pager } from '../ui/Pager'
import { SaveBar } from '../ui/SaveBar'
import { UrlSearchInput } from '../ui/UrlSearchInput'
import { enumField, stringField, useRouteClose, useUrlState } from '../ui/urlState'
import { useUnsavedGuard } from '../ui/UnsavedGuard'
import { formatClockTime } from '../money/time'
import { takeAdminSecret } from './secret'
import { useEffect, useReducer, useRef, useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import { parseNanoPoints, formatNanoPoints } from '../money/amount'
import {
  Badge,
  Button,
  ButtonLink,
  Card,
  Checkbox,
  DataTable,
  Dialog,
  Drawer,
  Icon,
  InlineError,
  PageHeader,
  QueryBoundary,
  Segmented,
  TextField,
  Toolbar,
  type Column,
} from '../ui'
import { ConfirmActionDialog, OneTimeSecretDialog } from './components'
import { secretReducer } from './confirm'
import { formatDateTime, formatLastActive, formatExactPoints as formatPoints, isAmount, isNegative, pointsRatio } from '../money'
import {
  useAdjustAccount,
  useAdminAccounts,
  useAdminAccount,
  useAdminSettings,
  useCreateAccount,
  useResetPassword,
  useUpdateAccount,
  useWriteOffAccount,
} from './queries'
import type { AdminAccount } from './types'

/** 已用信用 = 负余额的绝对值。 */
export function creditUsed(account: Pick<AdminAccount, 'balance'>) {
  const balance = parseNanoPoints(account.balance)
  return formatNanoPoints(balance < 0n ? -balance : 0n)
}

function Balance({ value }: { value: string }) {
  return <span className={isNegative(value) ? 'amount-negative num' : 'num'}>{formatPoints(value)}</span>
}

const columns: Column<AdminAccount>[] = [
  {
    key: 'user',
    header: '用户',
    primary: true,
    cell: (account) => (
      <>
        <strong>{account.display_name}</strong>
        <small className="mono">@{account.username}</small>
      </>
    ),
  },
  {
    key: 'role',
    header: '角色',
    cell: (account) => (account.is_admin ? <Badge tone="accent">管理员</Badge> : <span className="muted">用户</span>),
  },
  {
    key: 'status',
    header: '状态',
    cell: (account) => (
      <span className="badge-row">
        <Badge tone={account.status === 'active' ? 'success' : 'warning'}>{account.status === 'active' ? '启用' : '停用'}</Badge>
        {account.must_change_password && <Badge tone="info">待改密</Badge>}{parseNanoPoints(account.balance) < -parseNanoPoints(account.credit_limit) ? <Badge tone="danger">超额</Badge> : isNegative(account.balance) && <Badge tone="warning">负余额</Badge>}
      </span>
    ),
  },
  { key: 'balance', header: '余额', numeric: true, cell: (account) => <Balance value={account.balance} /> },
  { key: 'credit', header: '信用额度', numeric: true, cell: (account) => <span className="num">{formatPoints(account.credit_limit)}</span> },
  { key: 'active', header: '最近活跃', hideOnMobile: true, cell: (account) => formatLastActive(account.last_active_at) },
]

function CreateUserDialog({
  open,
  onClose,
  onCreated,
}: {
  open: boolean
  onClose: () => void
  onCreated: (account: AdminAccount, password: string) => void
}) {
  const settings = useAdminSettings()
  const create = useCreateAccount()
  const [username, setUsername] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [creditLimit, setCreditLimit] = useState('')
  const [isAdmin, setIsAdmin] = useState(false)
  const [error, setError] = useState('')
  const defaultCredit = settings.data ? formatPoints(settings.data.settings.default_credit_limit) : undefined
  const allowNavigation = useUnsavedGuard(Boolean(username || displayName || creditLimit || isAdmin), { busy: create.isPending })

  const reset = () => {
    setUsername('')
    setDisplayName('')
    setCreditLimit('')
    setIsAdmin(false)
    setError('')
    create.reset()
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const name = username.trim()
    if (!/^[a-z0-9][a-z0-9._-]{2,31}$/.test(name)) {
      setError('用户名为 3～32 位小写字母、数字或 . _ -，以字母或数字开头')
      return
    }
    if (!displayName.trim()) {
      setError('请填写显示名')
      return
    }
    const credit = creditLimit.trim()
    if (credit && (!isAmount(credit) || credit.startsWith('-'))) {
      setError('信用额度不小于 0，最多 9 位小数')
      return
    }
    setError('')
    create.mutate(
      {
        username: name,
        display_name: displayName.trim(),
        is_admin: isAdmin,
        ...(credit ? { credit_limit: credit } : {}),
      },
      {
        onSuccess: (result) => {
          // 初始密码只交给一次性展示，立即清掉 mutation 中的副本
          onCreated(result.account, takeAdminSecret(result.account.id))
          reset()
          allowNavigation()
          onClose()
        },
      },
    )
  }

  return (
    <Dialog
      busy={create.isPending}
      onClose={onClose}
      open={open}
      title="新建用户"
    >
      <form className="stack-form" noValidate onSubmit={submit}>
        <TextField autoComplete="off" label="用户名" onChange={(event) => setUsername(event.target.value)} value={username} />
        <TextField disabled={create.isPending} label="显示名" maxLength={64} onChange={(event) => setDisplayName(event.target.value)} value={displayName} />
        <TextField
          hint={defaultCredit !== undefined ? `留空使用平台默认 ${defaultCredit}` : '留空使用平台默认'}
          inputMode="decimal"
          label="信用额度（积分）"
          onChange={(event) => setCreditLimit(event.target.value)}
          placeholder={defaultCredit}
          value={creditLimit}
        />
        <Checkbox checked={isAdmin} label="管理员" onChange={(event) => setIsAdmin(event.target.checked)} />
        <InlineError>{error || (create.isError ? errorMessage(create.error, '创建失败，请重试') : '')}</InlineError>
        <div className="form-actions">
          <Button loading={create.isPending}>创建并生成初始密码</Button>
        </div>
      </form>
    </Dialog>
  )
}

type DrawerAction = 'reset' | 'adjust' | 'write-off' | null

function UserDrawerBody({
  account,
  onReveal, onBusyChange,
}: {
  account: AdminAccount
  onReveal: (account: AdminAccount, password: string) => void
  onBusyChange: (busy: boolean) => void
}) {
  const { account: me } = useAuth(), location = useLocation()
  const self = me?.id === account.id
  const update = useUpdateAccount()
  const reset = useResetPassword()
  const adjust = useAdjustAccount()
  const writeOff = useWriteOffAccount()
  const lastRemote = useRef(account)
  const [baseline, setBaseline] = useState(account)
  const [displayName, setDisplayName] = useState(account.display_name)
  const [creditLimit, setCreditLimit] = useState(formatPoints(account.credit_limit))
  const [active, setActive] = useState(account.status === 'active')
  const [isAdmin, setIsAdmin] = useState(account.is_admin)
  const [formError, setFormError] = useState('')
  const [saved, setSaved] = useState('')
  const [action, setAction] = useState<DrawerAction>(null)
  const [amount, setAmount] = useState('')
  const [idempotencyKey, setIdempotencyKey] = useState('')
  const busy = update.isPending || reset.isPending || adjust.isPending || writeOff.isPending
  const creditDirty = !isAmount(creditLimit) || parseNanoPoints(creditLimit) !== parseNanoPoints(baseline.credit_limit)
  const dirtyCount = Number(displayName.trim() !== baseline.display_name) + Number(creditDirty) + Number(active !== (baseline.status === 'active')) + Number(isAdmin !== baseline.is_admin)
  const apply = (latest: AdminAccount) => { setBaseline(latest); setDisplayName(latest.display_name); setCreditLimit(formatPoints(latest.credit_limit)); setActive(latest.status === 'active'); setIsAdmin(latest.is_admin); setFormError('') }
  useEffect(() => { onBusyChange(busy) }, [busy, onBusyChange])
  useEffect(() => { if (lastRemote.current === account) return; lastRemote.current = account; if (!dirtyCount && !busy) { setBaseline(account); setDisplayName(account.display_name); setCreditLimit(formatPoints(account.credit_limit)); setActive(account.status === 'active'); setIsAdmin(account.is_admin) } }, [account, baseline, dirtyCount, busy])
  useUnsavedGuard(dirtyCount > 0 || action !== null, { busy })


  const openAction = (next: DrawerAction) => {
    if (busy) return
    setAmount('')
    setIdempotencyKey(crypto.randomUUID())
    setAction(next)
  }

  const save = (event: FormEvent) => {
    event.preventDefault()
    setSaved('')
    const body: Record<string, unknown> = {}
    if (displayName.trim() !== baseline.display_name) body.display_name = displayName.trim()
    const credit = creditLimit.trim()
    if (!isAmount(credit) || credit.startsWith('-')) {
      setFormError('信用额度不小于 0，最多 9 位小数')
      return
    }
    if (parseNanoPoints(credit) !== parseNanoPoints(baseline.credit_limit)) body.credit_limit = credit
    if (active !== (baseline.status === 'active')) body.status = active ? 'active' : 'disabled'
    if (isAdmin !== baseline.is_admin) body.is_admin = isAdmin
    if (!displayName.trim()) {
      setFormError('请填写显示名')
      return
    }
    if (Object.keys(body).length === 0) {
      setFormError('没有修改')
      return
    }
    setFormError('')
    if (busy) return
    update.mutate({ id: account.id, body }, { onSuccess: data => { apply(data.account); setSaved(formatClockTime(new Date())) } })
  }

  const negative = isNegative(account.balance)
  const normalizedAmount = amount.trim().replace(/^\+/, '')
  return (
    <div className="drawer-sections">
      <Facts
        items={[
          /* oxlint-disable react/jsx-key -- [标签, 值] 元组，不是渲染的子元素列表 */
          ['用户名', <span className="mono">@{account.username}</span>],
          ['余额', <Balance value={account.balance} />],
          ['可用额度', <span className="num">{formatPoints(account.available)}</span>],
          ['已用信用', `${formatPoints(creditUsed(account))}（${Math.round(pointsRatio(creditUsed(account), account.credit_limit))}%）`],
          ['最近活跃', account.last_active_at ? formatDateTime(account.last_active_at) : '从未'],
          ['创建时间', formatDateTime(account.created_at)],
          ['密码修改', account.must_change_password ? '等待首次改密' : formatDateTime(account.password_changed_at)],
          /* oxlint-enable react/jsx-key */
        ]}
      />
      <div className="drawer-links">
        <ButtonLink size="sm" to={`/admin/points/transactions?account_id=${account.id}`} state={{ from: { to: location.pathname + location.search, label: account.display_name } }}>
          账单
        </ButtonLink>
        <ButtonLink size="sm" to={`/admin/calls?account_id=${account.id}`}>
          调用
        </ButtonLink>
      </div>

      <form className="stack-form" noValidate onSubmit={save}><SaveBar dirtyCount={dirtyCount} busy={busy} savedAt={saved} onDiscard={() => { apply(baseline); update.reset() }} />
        <h3 className="section-title">资料与额度</h3>
        <TextField disabled={busy} label="显示名" maxLength={64} onChange={(event) => setDisplayName(event.target.value)} value={displayName} />
        <TextField disabled={busy} inputMode="decimal" label="信用额度（积分）" onChange={(event) => setCreditLimit(event.target.value)} value={creditLimit} />
        <Checkbox checked={active} disabled={self || busy} label="启用（停用后立即退出全部会话）" onChange={(event) => setActive(event.target.checked)} />
        <Checkbox checked={isAdmin} disabled={self || busy} label="管理员" onChange={(event) => setIsAdmin(event.target.checked)} />
        <InlineError>{formError || (update.isError ? errorMessage(update.error, '保存失败，请重试') : '')}</InlineError>
      </form>

      <div className="stack-form">
        <h3 className="section-title">账务与安全</h3>
        <div className="drawer-links">
          <Button onClick={() => openAction('adjust')} size="sm" type="button" variant="secondary">
            调账
          </Button>
          <Button disabled={!negative || busy} onClick={() => openAction('write-off')} size="sm" type="button" variant="secondary">
            坏账核销
          </Button>
          <Button disabled={self || busy} onClick={() => openAction('reset')} size="sm" type="button" variant="secondary">
            重置密码
          </Button>
        </div>
        {self && <p className="muted-copy">不能停用、撤销或重置自己的账户。</p>}
      </div>

      <ConfirmActionDialog
        confirmLabel="确认调账"
        onClose={() => setAction(null)}
        onConfirm={(reason) =>
          adjust.mutateAsync({ id: account.id, body: { amount: normalizedAmount, reason }, idempotencyKey })
        }
        open={action === 'adjust'}
        summary={
          <p>
            给 <strong>{account.display_name}</strong> {normalizedAmount.startsWith('-') ? '扣减' : '增加'}{' '}
            <strong className="num">{normalizedAmount.replace(/^-/, '')}</strong> 积分，同时
            {normalizedAmount.startsWith('-') ? '增加' : '减少'}「平台账户」余额{' '}
            <strong className="num">{normalizedAmount.replace(/^-/, '')}</strong> 积分。调账单独列示，不计入平台收入。
          </p>
        }
        title="调账"
        validate={() =>
          !isAmount(normalizedAmount) || parseNanoPoints(normalizedAmount) === 0n
            ? '金额为非零数字，负数表示扣减，最多 9 位小数'
            : ''
        }
      >
        <TextField
          hint="正数增加用户积分，等额减少「平台账户」余额；负数扣减用户积分，等额增加该账户余额。调账单独列示，不计入平台收入。"
          inputMode="decimal"
          label="金额（积分）"
          onChange={(event) => setAmount(event.target.value)}
          value={amount}
        />
      </ConfirmActionDialog>

      <ConfirmActionDialog
        confirmLabel="确认核销"
        danger
        onClose={() => setAction(null)}
        onConfirm={(reason) => writeOff.mutateAsync({ id: account.id, body: { reason }, idempotencyKey })}
        open={action === 'write-off'}
        summary={
          <p>
            把 <strong>{account.display_name}</strong> 的负余额 <strong className="num">{formatPoints(account.balance)}</strong>{' '}
            转入坏账，余额归零。
          </p>
        }
        title="坏账核销"
      />

      <ConfirmActionDialog
        confirmLabel="确认重置"
        danger
        onClose={() => setAction(null)}
        onConfirm={async () => {
          const result = await reset.mutateAsync(account.id)
          reset.reset()
          onReveal(result.account, takeAdminSecret(result.account.id))
        }}
        open={action === 'reset'}
        requireReason={false}
        summary={
          <p>
            为 <strong>{account.display_name}</strong> 生成新的初始密码；对方全部会话立即失效，下次登录必须改密。
          </p>
        }
        title="重置密码"
      />
    </div>
  )
}

type Reveal = (account: AdminAccount, password: string) => void
type UsersContext = { created: Reveal; reset: Reveal }
const usersSchema = { q: stringField(''), status: enumField(['all', 'active', 'disabled'] as const, 'all') }

export function UsersPage() {
  const [filters, setFilters] = useUrlState(usersSchema), location = useLocation()
  const accounts = useAdminAccounts({ q: filters.q || undefined, status: filters.status === 'all' ? undefined : filters.status })
  const [secret, dispatchSecret] = useReducer(secretReducer, null)
  const reveal = (title: string): Reveal => (account, password) => dispatchSecret({ type: 'reveal', secret: { title, username: account.username, password } })
  const origin = location.pathname + location.search
  return <>
    <PageHeader title="用户" actions={<ButtonLink to={`/admin/users/new${location.search}`} state={{ overlayFrom: origin }} icon={<Icon name="plus" />}>新建用户</ButtonLink>} />
    <Toolbar><UrlSearchInput label="搜索用户" value={filters.q} onCommit={q => setFilters({ q })} placeholder="用户名、显示名或 ID" /><Segmented label="状态" value={filters.status} onChange={status => setFilters({ status })} options={[{ key: 'all', label: '全部' }, { key: 'active', label: '启用' }, { key: 'disabled', label: '停用' }]} /></Toolbar>
    <Card flush><QueryBoundary query={accounts}>{rows => <div aria-busy={accounts.isFetching}><DataTable caption="用户列表" columns={[...columns, { key: 'action', header: '操作', cell: account => <ButtonLink size="sm" variant="secondary" to={`/admin/users/${account.id}${location.search}`} state={{ overlayFrom: origin }}>管理</ButtonLink> }]} empty={<p className="muted-copy empty-pad">没有匹配的用户</p>} rowKey={account => account.id} rows={rows} /><Pager hasMore={accounts.hasNextPage || accounts.limited} limited={accounts.limited} error={accounts.isFetchNextPageError ? errorMessage(accounts.error, '加载失败，请重试') : undefined} loading={accounts.isFetchingNextPage} onMore={() => void accounts.fetchNextPage()} /></div>}</QueryBoundary></Card>
    <Outlet context={{ created: reveal('用户已创建'), reset: reveal('密码已重置') } satisfies UsersContext} />
    <OneTimeSecretDialog onDismiss={() => dispatchSecret({ type: 'dismiss' })} secret={secret} />
  </>
}
export function NewUserRoute() {
  const close = useRouteClose('/admin/users'), { created } = useOutletContext<UsersContext>()
  return <CreateUserDialog open onClose={close} onCreated={created} />
}
export function UserRoute() {
  const { id = '' } = useParams(), query = useAdminAccount(id), close = useRouteClose('/admin/users'), { reset } = useOutletContext<UsersContext>(), [busy, setBusy] = useState(false)
  return <Drawer open busy={busy} title={query.data?.account.display_name || '用户详情'} onClose={close}>{query.isError && query.data && <InlineError>资料刷新失败，正在保留上次数据。<Button variant="quiet" onClick={() => void query.refetch()}>重试读取</Button></InlineError>}<QueryBoundary query={query}>{({ account }) => <UserDrawerBody key={account.id} account={account} onReveal={reset} onBusyChange={setBusy} />}</QueryBoundary></Drawer>
}
