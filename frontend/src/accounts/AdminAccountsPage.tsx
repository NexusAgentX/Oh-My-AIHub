import { useEffect, useState, type FormEvent } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError } from '../api/client'
import type { Account, AccountStatus } from '../api/types'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import { usernameProblem } from '../auth/credentialsRules'
import {
  Badge,
  Button,
  ButtonLink,
  Card,
  Checkbox,
  DataTable,
  Dialog,
  EmptyState,
  Icon,
  InlineError,
  Metric,
  MetricGrid,
  Notice,
  PageHeader,
  QueryBoundary,
  SearchInput,
  SelectField,
  TextField,
  Toolbar,
} from '../ui'
import { accountRiskLabel } from './accountMetrics'
import { useEphemeralCredential } from './EphemeralCredentialProvider'
import {
  useAccountMetricsQuery,
  useAdminAccountsQuery,
  useCreateAccount,
  useResetAccountPassword,
  useUpdateAccount,
} from './queries'

function RiskBadge({ account }: { account: Account }) {
  const label = accountRiskLabel(account)
  const tone = account.credit_frozen || account.over_limit ? 'danger' : label === '正常' ? 'success' : 'warning'
  return <Badge tone={tone}>{label}</Badge>
}

function AccountMetrics() {
  const query = useAccountMetricsQuery()
  const metrics = query.data
  return (
    <MetricGrid label="账户指标">
      <Metric label="账本账户" value={metrics?.ledger_account_count ?? '—'} />
      <Metric label="总信用额度" tone="accent" value={metrics?.total_credit_limit ?? '—'} />
      <Metric label="已用信用" value={metrics?.credit_capacity_used ?? '—'} />
      <Metric
        hint={`${metrics?.credit_frozen_accounts ?? '—'} 个信用冻结`}
        label="信用超限账户"
        value={metrics?.over_limit_accounts ?? '—'}
      />
    </MetricGrid>
  )
}

export function AdminAccountsPage() {
  const { account: currentAccount, synchronizeAccount } = useAuth()
  const { setCredential } = useEphemeralCredential()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const search = searchParams.get('query') ?? ''
  const [draft, setDraft] = useState(search)
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<Account | null>(null)
  const [notice, setNotice] = useState('')
  const query = useAdminAccountsQuery(search.trim())

  useEffect(() => setDraft(search), [search])

  useEffect(() => {
    if (searchParams.get('create') !== '1') return
    setCreateOpen(true)
    const next = new URLSearchParams(searchParams)
    next.delete('create')
    setSearchParams(next, { replace: true })
  }, [searchParams, setSearchParams])

  const submitSearch = (event: FormEvent) => {
    event.preventDefault()
    const next = new URLSearchParams()
    if (draft.trim()) next.set('query', draft.trim())
    setSearchParams(next, { replace: true })
  }

  return (
    <>
      <PageHeader
        actions={
          <Button icon={<Icon name="plus" />} onClick={() => setCreateOpen(true)}>
            创建账号
          </Button>
        }
        title="账户与信用"
      />
      <AccountMetrics />
      {notice && <Notice tone="warning">{notice}</Notice>}
      <Toolbar>
        <form onSubmit={submitSearch}>
          <SearchInput
            label="搜索账户"
            onChange={(event) => setDraft(event.target.value)}
            placeholder="搜索用户名或显示名称"
            value={draft}
          />
        </form>
      </Toolbar>
      <Card flush>
        <QueryBoundary errorFallback="账户列表加载失败" query={query}>
          {(accounts) => (
            <DataTable
              caption="账户列表"
              columns={[
                {
                  key: 'account',
                  header: '账户',
                  primary: true,
                  cell: (item) => (
                    <>
                      <strong>{item.display_name}</strong>
                      <small>
                        @{item.username}
                        {item.is_admin ? ' · 管理员' : ''}
                        {item.status === 'disabled' ? ' · 已停用' : ''}
                      </small>
                    </>
                  ),
                },
                { key: 'balance', header: '已入账余额', numeric: true, cell: (item) => item.posted_balance },
                { key: 'limit', header: '信用额度', numeric: true, cell: (item) => item.credit_limit },
                { key: 'used', header: '已用信用', numeric: true, cell: (item) => item.credit_used },
                { key: 'spendable', header: '可消费额度', numeric: true, cell: (item) => item.spendable_capacity },
                { key: 'risk', header: '风险状态', cell: (item) => <RiskBadge account={item} /> },
                {
                  key: 'actions',
                  header: '操作',
                  cell: (item) => (
                    <span className="table-action-group">
                      <ButtonLink size="sm" to={`/admin/ledger/accounts/${item.id}`}>
                        账本
                      </ButtonLink>
                      <Button onClick={() => setEditing(item)} size="sm" variant="secondary">
                        管理
                      </Button>
                    </span>
                  ),
                },
              ]}
              empty={<EmptyState title="没有匹配的账户" />}
              rowKey={(item) => item.id}
              rows={accounts}
            />
          )}
        </QueryBoundary>
      </Card>

      <CreateAccountDialog
        onClose={() => setCreateOpen(false)}
        onCreated={(created) => {
          setCredential({
            username: created.account.username,
            initialPassword: created.initial_password,
          })
          setCreateOpen(false)
          navigate('/admin/accounts/created')
        }}
        open={createOpen}
      />
      <EditAccountDialog
        account={editing}
        currentAccountID={currentAccount?.id ?? ''}
        onClose={() => setEditing(null)}
        onConflict={() => {
          setEditing(null)
          setNotice('账户已被其他管理员修改，已加载最新版本，请重新操作')
        }}
        onUpdated={(updated) => {
          synchronizeAccount(updated)
          setEditing(null)
          setNotice('')
        }}
      />
    </>
  )
}

function CreateAccountDialog({
  open,
  onClose,
  onCreated,
}: {
  open: boolean
  onClose: () => void
  onCreated: (created: Awaited<ReturnType<ReturnType<typeof useCreateAccount>['mutateAsync']>>) => void
}) {
  return (
    <Dialog onClose={onClose} open={open} title="创建账号">
      <CreateAccountForm onClose={onClose} onCreated={onCreated} />
    </Dialog>
  )
}

function CreateAccountForm({
  onClose,
  onCreated,
}: {
  onClose: () => void
  onCreated: (created: Awaited<ReturnType<ReturnType<typeof useCreateAccount>['mutateAsync']>>) => void
}) {
  const create = useCreateAccount()
  const [displayName, setDisplayName] = useState('')
  const [username, setUsername] = useState('')
  const [creditLimit, setCreditLimit] = useState('0')
  const [isAdmin, setIsAdmin] = useState(false)
  const [status, setStatus] = useState<AccountStatus>('active')
  const [error, setError] = useState('')

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    if (usernameProblem(username)) {
      setError('用户名不符合规则：3-32 位，小写字母、数字、.、_ 或 -，以小写字母或数字开头')
      return
    }
    try {
      onCreated(
        await create.mutateAsync({
          username,
          display_name: displayName,
          credit_limit: creditLimit,
          is_admin: isAdmin,
          status,
        }),
      )
    } catch (caught) {
      setError(errorMessage(caught, '账号创建失败'))
    }
  }

  return (
    <form className="stack-form" onSubmit={submit}>
      <InlineError>{error}</InlineError>
      <TextField
        autoFocus
        label="显示名称"
        onChange={(event) => setDisplayName(event.target.value)}
        required
        value={displayName}
      />
      <TextField
        autoComplete="off"
        hint="3-32 位，小写字母、数字、.、_ 或 -"
        label="用户名"
        onChange={(event) => setUsername(event.target.value)}
        pattern="[A-Za-z0-9][A-Za-z0-9._-]{2,31}"
        required
        value={username}
      />
      <div className="field-row">
        <TextField
          inputMode="decimal"
          label="初始信用额度"
          min="0"
          onChange={(event) => setCreditLimit(event.target.value)}
          required
          step="0.000000001"
          type="number"
          value={creditLimit}
        />
        <SelectField
          label="账户状态"
          onChange={(event) => setStatus(event.target.value as AccountStatus)}
          value={status}
        >
          <option value="active">启用</option>
          <option value="disabled">停用</option>
        </SelectField>
      </div>
      <Checkbox
        checked={isAdmin}
        label="授予管理员权限"
        onChange={(event) => setIsAdmin(event.target.checked)}
      />
      <div className="modal-actions">
        <Button disabled={create.isPending} onClick={onClose} type="button" variant="secondary">
          取消
        </Button>
        <Button loading={create.isPending} type="submit">
          创建账号
        </Button>
      </div>
    </form>
  )
}

function EditAccountDialog({
  account,
  currentAccountID,
  onClose,
  onConflict,
  onUpdated,
}: {
  account: Account | null
  currentAccountID: string
  onClose: () => void
  onConflict: () => void
  onUpdated: (account: Account) => void
}) {
  return (
    <Dialog
      description={account ? `${account.display_name} · @${account.username}` : undefined}
      onClose={onClose}
      open={Boolean(account)}
      title="管理账户"
    >
      {account && (
        <EditAccountForm
          account={account}
          currentAccountID={currentAccountID}
          key={account.id}
          onClose={onClose}
          onConflict={onConflict}
          onUpdated={onUpdated}
        />
      )}
    </Dialog>
  )
}

function EditAccountForm({
  account,
  currentAccountID,
  onClose,
  onConflict,
  onUpdated,
}: {
  account: Account
  currentAccountID: string
  onClose: () => void
  onConflict: () => void
  onUpdated: (account: Account) => void
}) {
  const update = useUpdateAccount()
  const reset = useResetAccountPassword()
  const [creditLimit, setCreditLimit] = useState(account.credit_limit)
  const [creditFrozen, setCreditFrozen] = useState(account.credit_frozen)
  const [status, setStatus] = useState<AccountStatus>(account.status)
  const [isAdmin, setIsAdmin] = useState(account.is_admin)
  const [error, setError] = useState('')
  const [confirmingReset, setConfirmingReset] = useState(false)
  const [newInitialPassword, setNewInitialPassword] = useState('')
  const [copyState, setCopyState] = useState('')
  const isSelf = account.id === currentAccountID

  const resetPassword = async () => {
    setError('')
    try {
      const result = await reset.mutateAsync(account.id)
      setNewInitialPassword(result.initial_password)
      setConfirmingReset(false)
      reset.reset()
    } catch (caught) {
      setError(
        caught instanceof ApiError && caught.code === 'conflict'
          ? '该账户密码刚被其他操作修改，请重试'
          : errorMessage(caught, '密码重置失败'),
      )
    }
  }

  const copyNewPassword = async () => {
    try {
      await navigator.clipboard.writeText(newInitialPassword)
      setCopyState('已复制')
    } catch {
      setCopyState('复制失败，请手动复制')
    }
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    try {
      onUpdated(
        await update.mutateAsync({
          accountID: account.id,
          expectedVersion: account.version,
          patch: {
            credit_limit: creditLimit,
            credit_frozen: creditFrozen,
            status,
            is_admin: isAdmin,
          },
        }),
      )
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'conflict') {
        onConflict()
        return
      }
      setError(errorMessage(caught, '账户更新失败'))
    }
  }

  return (
    <form className="stack-form" onSubmit={submit}>
      <InlineError>{error}</InlineError>
      <TextField
        inputMode="decimal"
        label="信用额度"
        min="0"
        onChange={(event) => setCreditLimit(event.target.value)}
        required
        step="0.000000001"
        type="number"
        value={creditLimit}
      />
      <SelectField
        disabled={isSelf && account.is_admin}
        label="账户状态"
        onChange={(event) => setStatus(event.target.value as AccountStatus)}
        value={status}
      >
        <option value="active">启用</option>
        <option value="disabled">停用</option>
      </SelectField>
      <Checkbox
        checked={creditFrozen}
        label="冻结新消费与持有"
        onChange={(event) => setCreditFrozen(event.target.checked)}
      />
      <Checkbox
        checked={isAdmin}
        disabled={isSelf}
        label="管理员权限"
        onChange={(event) => setIsAdmin(event.target.checked)}
      />
      <div className="modal-divider">
        <span className="field-label">密码</span>
        {isSelf ? (
          <p className="modal-hint">自己的密码请在账户设置中修改。</p>
        ) : newInitialPassword ? (
          <>
            <p className="modal-hint">
              新初始密码仅显示这一次，请立即复制并通过可信渠道交付；关闭后无法再次查看。
            </p>
            <div className="reset-credential">
              <strong>{newInitialPassword}</strong>
              <Button
                icon={<Icon name="copy" />}
                onClick={() => void copyNewPassword()}
                type="button"
                variant="secondary"
              >
                复制
              </Button>
            </div>
            <p aria-live="polite" className="modal-hint">
              {copyState}
            </p>
          </>
        ) : (
          <>
            <p className="modal-hint">
              重置会生成仅显示一次的新初始密码；该账户全部登录会话立即失效，用户下次登录必须修改密码。
            </p>
            {confirmingReset ? (
              <div className="reset-confirm-row">
                <Button
                  loading={reset.isPending}
                  onClick={() => void resetPassword()}
                  type="button"
                  variant="danger"
                >
                  确认重置
                </Button>
                <Button
                  disabled={reset.isPending}
                  onClick={() => setConfirmingReset(false)}
                  type="button"
                  variant="quiet"
                >
                  取消
                </Button>
              </div>
            ) : (
              <Button
                disabled={update.isPending}
                onClick={() => setConfirmingReset(true)}
                type="button"
                variant="secondary"
              >
                重置密码
              </Button>
            )}
          </>
        )}
      </div>
      <div className="modal-actions">
        <Button onClick={onClose} type="button" variant="secondary">
          取消
        </Button>
        <Button loading={update.isPending} type="submit">
          保存更改
        </Button>
      </div>
    </form>
  )
}
