import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { Account, AccountStatus } from '../api/contracts'
import { useAuth } from '../auth/AuthProvider'
import { AppShell } from '../layouts/AppShell'
import {
  Button,
  InlineError,
  LoadingState,
  StatusBadge,
  TextField,
} from '../ui/FormControls'
import { Icon } from '../ui/Icon'
import { formatNanoPoints } from '../money/amount'
import { summarizeAccounts } from './accountMetrics'
import { useEphemeralCredential } from './EphemeralCredentialProvider'

export function AdminAccountsPage() {
  const { account: currentAccount, synchronizeAccount } = useAuth()
  const { setCredential } = useEphemeralCredential()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const [accounts, setAccounts] = useState<Account[]>([])
  const [metricAccounts, setMetricAccounts] = useState<Account[]>([])
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<Account | null>(null)

  const load = async (search = query, successMessage = '') => {
    setLoading(true)
    setError('')
    try {
      const normalizedSearch = search.trim()
      const [items, completeDirectory] = normalizedSearch
        ? await Promise.all([api.accounts(normalizedSearch), api.accounts('')])
        : await api.accounts('').then((allItems) => [allItems, allItems] as const)
      setAccounts(items)
      setMetricAccounts(completeDirectory)
      setError(successMessage)
    } catch (caught) {
      setError(
        caught instanceof ApiError ? caught.message : '账户列表加载失败',
      )
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load('')
  }, [])

  useEffect(() => {
    if (searchParams.get('create') !== '1') return
    setCreateOpen(true)
    setSearchParams({}, { replace: true })
  }, [searchParams, setSearchParams])

  const totals = useMemo(() => summarizeAccounts(metricAccounts), [metricAccounts])

  const search = (event: FormEvent) => {
    event.preventDefault()
    void load(query)
  }

  return (
    <AppShell admin>
      <header className="page-heading">
        <div>
          <h1>账户与信用</h1>
        </div>
        <Button
          aria-label="创建账号"
          icon={<Icon name="plus" />}
          onClick={() => setCreateOpen(true)}
        >
          创建账号
        </Button>
      </header>

      <section className="metric-grid" aria-label="账户指标">
        <article className="metric-card">
          <span>全部账户</span>
          <strong>{totals.total}</strong>
          <small>个</small>
        </article>
        <article className="metric-card metric-card-accent">
          <span>启用账户</span>
          <strong>{totals.active}</strong>
          <small>个</small>
        </article>
        <article className="metric-card">
          <span>停用账户</span>
          <strong>{totals.disabled}</strong>
          <small>个</small>
        </article>
        <article className="metric-card">
          <span>信用额度合计</span>
          <strong>{formatNanoPoints(totals.credit)}</strong>
          <small>积分</small>
        </article>
      </section>

      <section className="panel table-panel">
        <header className="table-toolbar">
          <div>
            <h2>账户</h2>
          </div>
          <form className="search-form" onSubmit={search}>
            <Icon name="search" />
            <input
              aria-label="搜索账户"
              onChange={(event) => setQuery(event.target.value)}
              placeholder="搜索用户名或显示名称"
              value={query}
            />
            <button className="visually-hidden" type="submit">
              搜索
            </button>
          </form>
        </header>
        <InlineError>{error}</InlineError>
        {loading ? (
          <LoadingState />
        ) : (
          <>
            <div className="desktop-table-wrap">
              <table className="data-table">
                <caption className="visually-hidden">受邀账户列表</caption>
                <thead>
                  <tr>
                    <th scope="col">账户</th>
                    <th scope="col">权限</th>
                    <th scope="col">状态</th>
                    <th scope="col">信用额度</th>
                    <th scope="col">首次改密</th>
                    <th scope="col"><span className="visually-hidden">操作</span></th>
                  </tr>
                </thead>
                <tbody>
                  {accounts.map((item) => (
                    <tr key={item.id}>
                      <td>
                        <strong>{item.display_name}</strong>
                        <small>@{item.username}</small>
                      </td>
                      <td>{item.is_admin ? '管理员' : '普通账户'}</td>
                      <td><StatusBadge status={item.status} /></td>
                      <td>{item.credit_limit} 积分</td>
                      <td>{item.must_change_password ? '待完成' : '已完成'}</td>
                      <td className="table-action">
                        <Button
                          onClick={() => setEditing(item)}
                          variant="secondary"
                        >
                          管理
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="mobile-card-list">
              {accounts.map((item) => (
                <article className="mobile-data-card" key={item.id}>
                  <header>
                    <div>
                      <strong>{item.display_name}</strong>
                      <span>@{item.username}</span>
                    </div>
                    <StatusBadge status={item.status} />
                  </header>
                  <dl>
                    <div><dt>权限</dt><dd>{item.is_admin ? '管理员' : '普通账户'}</dd></div>
                    <div><dt>信用额度</dt><dd>{item.credit_limit} 积分</dd></div>
                    <div><dt>首次改密</dt><dd>{item.must_change_password ? '待完成' : '已完成'}</dd></div>
                  </dl>
                  <Button onClick={() => setEditing(item)} variant="secondary">
                    管理账户
                  </Button>
                </article>
              ))}
            </div>
            {accounts.length === 0 && <div className="empty-state">没有匹配的账户</div>}
          </>
        )}
      </section>

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
        onUpdated={(updated) => {
          setAccounts((items) =>
            items.map((item) => (item.id === updated.id ? updated : item)),
          )
          setMetricAccounts((items) =>
            items.map((item) => (item.id === updated.id ? updated : item)),
          )
          synchronizeAccount(updated)
          setEditing(null)
        }}
        onConflict={() => {
          setEditing(null)
          void load(query, '账户已被其他管理员修改，已加载最新版本，请重新操作')
        }}
      />
    </AppShell>
  )
}

function useDialog(open: boolean) {
  const reference = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const dialog = reference.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    if (!open && dialog.open) dialog.close()
  }, [open])
  return reference
}

function CreateAccountDialog({
  open,
  onClose,
  onCreated,
}: {
  open: boolean
  onClose: () => void
  onCreated: (created: Awaited<ReturnType<typeof api.createAccount>>) => void
}) {
  const reference = useDialog(open)
  const [displayName, setDisplayName] = useState('')
  const [username, setUsername] = useState('')
  const [creditLimit, setCreditLimit] = useState('0')
  const [isAdmin, setIsAdmin] = useState(false)
  const [status, setStatus] = useState<AccountStatus>('active')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (open) {
      setDisplayName('')
      setUsername('')
      setCreditLimit('0')
      setIsAdmin(false)
      setStatus('active')
      setError('')
    }
  }, [open])

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      onCreated(
        await api.createAccount({
          username,
          display_name: displayName,
          credit_limit: creditLimit,
          is_admin: isAdmin,
          status,
        }),
      )
    } catch (caught) {
      setError(
        caught instanceof ApiError ? caught.message : '账号创建失败',
      )
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <dialog
      aria-labelledby="create-account-title"
      className="modal"
      onCancel={(event) => {
        if (submitting) event.preventDefault()
        else onClose()
      }}
      onClose={onClose}
      ref={reference}
    >
      <form className="modal-form" onSubmit={submit}>
        <header className="modal-heading">
          <div>
            <h2 id="create-account-title">创建账号</h2>
          </div>
          <button
            aria-label="关闭"
            className="icon-button"
            disabled={submitting}
            onClick={onClose}
            type="button"
          >
            ×
          </button>
        </header>
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
          <label className="field">
            <span className="field-label">账户状态</span>
            <select
              className="input"
              onChange={(event) => setStatus(event.target.value as AccountStatus)}
              value={status}
            >
              <option value="active">启用</option>
              <option value="disabled">停用</option>
            </select>
          </label>
        </div>
        <label className="checkbox-control">
          <input
            checked={isAdmin}
            onChange={(event) => setIsAdmin(event.target.checked)}
            type="checkbox"
          />
          <span>授予管理员权限</span>
        </label>
        <footer className="modal-actions">
          <Button disabled={submitting} onClick={onClose} type="button" variant="secondary">
            取消
          </Button>
          <Button disabled={submitting} type="submit">
            {submitting ? '正在创建' : '创建账号'}
          </Button>
        </footer>
      </form>
    </dialog>
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
  const reference = useDialog(Boolean(account))
  const [creditLimit, setCreditLimit] = useState('0')
  const [status, setStatus] = useState<AccountStatus>('active')
  const [isAdmin, setIsAdmin] = useState(false)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (account) {
      setCreditLimit(account.credit_limit)
      setStatus(account.status)
      setIsAdmin(account.is_admin)
      setError('')
    }
  }, [account])

  if (!account) return <dialog className="modal" ref={reference} />

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setSubmitting(true)
    setError('')
    try {
      onUpdated(
        await api.updateAccount(account.id, account.version, {
          credit_limit: creditLimit,
          status,
          is_admin: isAdmin,
        }),
      )
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'conflict') {
        onConflict()
        return
      }
      setError(
        caught instanceof ApiError ? caught.message : '账户更新失败',
      )
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <dialog
      aria-labelledby="edit-account-title"
      className="modal"
      onCancel={(event) => {
        if (submitting) event.preventDefault()
        else onClose()
      }}
      onClose={onClose}
      ref={reference}
    >
      <form className="modal-form" onSubmit={submit}>
        <header className="modal-heading">
          <div>
            <h2 id="edit-account-title">管理账户</h2>
            <p>{account.display_name} · @{account.username}</p>
          </div>
          <button aria-label="关闭" className="icon-button" onClick={onClose} type="button">×</button>
        </header>
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
        <label className="field">
          <span className="field-label">账户状态</span>
          <select
            className="input"
            disabled={account.id === currentAccountID && account.is_admin}
            onChange={(event) => setStatus(event.target.value as AccountStatus)}
            value={status}
          >
            <option value="active">启用</option>
            <option value="disabled">停用</option>
          </select>
        </label>
        <label className="checkbox-control">
          <input
            checked={isAdmin}
            disabled={account.id === currentAccountID}
            onChange={(event) => setIsAdmin(event.target.checked)}
            type="checkbox"
          />
          <span>管理员权限</span>
        </label>
        <footer className="modal-actions">
          <Button onClick={onClose} type="button" variant="secondary">取消</Button>
          <Button disabled={submitting} type="submit">{submitting ? '正在保存' : '保存更改'}</Button>
        </footer>
      </form>
    </dialog>
  )
}
