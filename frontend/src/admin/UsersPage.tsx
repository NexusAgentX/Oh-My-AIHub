import { useReducer, useState, type FormEvent } from 'react'
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
  SearchInput,
  Segmented,
  SuccessMessage,
  TextField,
  Toolbar,
  type Column,
} from '../ui'
import { ConfirmActionDialog, DetailList, LoadMore, OneTimeSecretDialog } from './components'
import { secretReducer } from './confirm'
import { formatDateTime, formatLastActive, formatPoints, isAmount, isNegative, pointsRatio } from './format'
import {
  useAdjustAccount,
  useAdminAccounts,
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
        {account.must_change_password && <Badge tone="info">待改密</Badge>}
      </span>
    ),
  },
  { key: 'balance', header: '余额', numeric: true, cell: (account) => <Balance value={account.balance} /> },
  {
    key: 'credit',
    header: '信用额度（已用）',
    numeric: true,
    cell: (account) => (
      <>
        {formatPoints(account.credit_limit)}
        <small className="muted"> （{formatPoints(creditUsed(account))}）</small>
      </>
    ),
  },
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
          onCreated(result.account, result.initial_password)
          reset()
          onClose()
        },
      },
    )
  }

  return (
    <Dialog
      busy={create.isPending}
      onClose={() => {
        reset()
        onClose()
      }}
      open={open}
      title="新建用户"
    >
      <form className="stack-form" noValidate onSubmit={submit}>
        <TextField autoComplete="off" label="用户名" onChange={(event) => setUsername(event.target.value)} value={username} />
        <TextField label="显示名" maxLength={64} onChange={(event) => setDisplayName(event.target.value)} value={displayName} />
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
  onReveal,
}: {
  account: AdminAccount
  onReveal: (account: AdminAccount, password: string) => void
}) {
  const { account: me } = useAuth()
  const self = me?.id === account.id
  const update = useUpdateAccount()
  const reset = useResetPassword()
  const adjust = useAdjustAccount()
  const writeOff = useWriteOffAccount()
  const [displayName, setDisplayName] = useState(account.display_name)
  const [creditLimit, setCreditLimit] = useState(formatPoints(account.credit_limit))
  const [active, setActive] = useState(account.status === 'active')
  const [isAdmin, setIsAdmin] = useState(account.is_admin)
  const [formError, setFormError] = useState('')
  const [saved, setSaved] = useState('')
  const [action, setAction] = useState<DrawerAction>(null)
  const [amount, setAmount] = useState('')
  const [idempotencyKey, setIdempotencyKey] = useState('')

  const openAction = (next: DrawerAction) => {
    setAmount('')
    setIdempotencyKey(crypto.randomUUID())
    setAction(next)
  }

  const save = (event: FormEvent) => {
    event.preventDefault()
    setSaved('')
    const body: Record<string, unknown> = {}
    if (displayName.trim() !== account.display_name) body.display_name = displayName.trim()
    const credit = creditLimit.trim()
    if (!isAmount(credit) || credit.startsWith('-')) {
      setFormError('信用额度不小于 0，最多 9 位小数')
      return
    }
    if (parseNanoPoints(credit) !== parseNanoPoints(account.credit_limit)) body.credit_limit = credit
    if (active !== (account.status === 'active')) body.status = active ? 'active' : 'disabled'
    if (isAdmin !== account.is_admin) body.is_admin = isAdmin
    if (!displayName.trim()) {
      setFormError('请填写显示名')
      return
    }
    if (Object.keys(body).length === 0) {
      setFormError('没有修改')
      return
    }
    setFormError('')
    update.mutate({ id: account.id, body }, { onSuccess: () => setSaved('已保存') })
  }

  const negative = isNegative(account.balance)
  const normalizedAmount = amount.trim().replace(/^\+/, '')
  return (
    <div className="drawer-sections">
      <DetailList
        items={[
          ['用户名', <span className="mono">@{account.username}</span>],
          ['余额', <Balance value={account.balance} />],
          ['还能透支', <span className="num">{formatPoints(account.available)}</span>],
          ['已用信用', `${formatPoints(creditUsed(account))}（${Math.round(pointsRatio(creditUsed(account), account.credit_limit))}%）`],
          ['最近活跃', account.last_active_at ? formatDateTime(account.last_active_at) : '从未'],
          ['创建时间', formatDateTime(account.created_at)],
          ['密码修改', account.must_change_password ? '等待首次改密' : formatDateTime(account.password_changed_at)],
        ]}
      />
      <div className="drawer-links">
        <ButtonLink size="sm" to={`/admin/points?tab=transactions&account_id=${account.id}`}>
          账单
        </ButtonLink>
        <ButtonLink size="sm" to={`/admin/calls?account_id=${account.id}`}>
          调用
        </ButtonLink>
      </div>

      <form className="stack-form" noValidate onSubmit={save}>
        <h3 className="section-title">资料与额度</h3>
        <TextField label="显示名" maxLength={64} onChange={(event) => setDisplayName(event.target.value)} value={displayName} />
        <TextField inputMode="decimal" label="信用额度（积分）" onChange={(event) => setCreditLimit(event.target.value)} value={creditLimit} />
        <Checkbox checked={active} disabled={self} label="启用（停用后立即退出全部会话）" onChange={(event) => setActive(event.target.checked)} />
        <Checkbox checked={isAdmin} disabled={self} label="管理员" onChange={(event) => setIsAdmin(event.target.checked)} />
        <InlineError>{formError || (update.isError ? errorMessage(update.error, '保存失败，请重试') : '')}</InlineError>
        <SuccessMessage>{saved}</SuccessMessage>
        <div className="form-actions">
          <Button loading={update.isPending} size="sm">
            保存
          </Button>
        </div>
      </form>

      <div className="stack-form">
        <h3 className="section-title">账务与安全</h3>
        <div className="drawer-links">
          <Button onClick={() => openAction('adjust')} size="sm" type="button" variant="secondary">
            调账
          </Button>
          <Button disabled={!negative} onClick={() => openAction('write-off')} size="sm" type="button" variant="secondary">
            坏账核销
          </Button>
          <Button disabled={self} onClick={() => openAction('reset')} size="sm" type="button" variant="secondary">
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
            <strong className="num">{normalizedAmount.replace(/^-/, '')}</strong> 积分，对手方为平台收入。
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
          hint="正数增加，负数扣减"
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
          onReveal(result.account, result.initial_password)
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

export function UsersPage() {
  const [q, setQ] = useState('')
  const [status, setStatus] = useState<'all' | 'active' | 'disabled'>('all')
  const accounts = useAdminAccounts({ q: q.trim() || undefined, status: status === 'all' ? undefined : status })
  const [creating, setCreating] = useState(false)
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const [secret, dispatchSecret] = useReducer(secretReducer, null)
  const selected = accounts.data?.find((account) => account.id === selectedID) ?? null

  const reveal = (title: string) => (account: AdminAccount, password: string) =>
    dispatchSecret({ type: 'reveal', secret: { title, username: account.username, password } })

  return (
    <>
      <PageHeader
        actions={
          <Button icon={<Icon name="plus" />} onClick={() => setCreating(true)} type="button">
            新建用户
          </Button>
        }
        title="用户"
      />
      <Toolbar>
        <SearchInput label="搜索用户" onChange={(event) => setQ(event.target.value)} placeholder="用户名、显示名或 ID" value={q} />
        <Segmented
          label="状态"
          onChange={setStatus}
          options={[
            { key: 'all', label: '全部' },
            { key: 'active', label: '启用' },
            { key: 'disabled', label: '停用' },
          ]}
          value={status}
        />
      </Toolbar>
      <Card flush>
        <QueryBoundary query={accounts}>
          {(rows) => (
            <>
              <DataTable
                caption="用户列表"
                columns={[
                  ...columns,
                  {
                    key: 'action',
                    header: '操作',
                    cell: (account) => (
                      <Button onClick={() => setSelectedID(account.id)} size="sm" type="button" variant="secondary">
                        管理
                      </Button>
                    ),
                  },
                ]}
                empty={<p className="muted-copy empty-pad">没有匹配的用户</p>}
                rowKey={(account) => account.id}
                rows={rows}
              />
              <LoadMore
                hasMore={Boolean(accounts.hasNextPage)}
                loading={accounts.isFetchingNextPage}
                onLoad={() => void accounts.fetchNextPage()}
              />
            </>
          )}
        </QueryBoundary>
      </Card>
      <CreateUserDialog onClose={() => setCreating(false)} onCreated={reveal('用户已创建')} open={creating} />
      <Drawer
        onClose={() => setSelectedID(null)}
        open={selected !== null}
        title={selected ? selected.display_name : ''}
      >
        {selected && <UserDrawerBody account={selected} key={selected.id} onReveal={reveal('密码已重置')} />}
      </Drawer>
      <OneTimeSecretDialog onDismiss={() => dispatchSecret({ type: 'dismiss' })} secret={secret} />
    </>
  )
}
