import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { Account, AccountStatus, LedgerMetrics } from '../api/contracts'
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
import { accountRiskLabel } from './accountMetrics'
import { useEphemeralCredential } from './EphemeralCredentialProvider'

export function AdminAccountsPage() {
  const { account: currentAccount, synchronizeAccount } = useAuth()
  const { setCredential } = useEphemeralCredential()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const [accounts, setAccounts] = useState<Account[]>([])
  const [metrics, setMetrics] = useState<LedgerMetrics | null>(null)
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
      const [items, nextMetrics] = await Promise.all([
        api.accounts(normalizedSearch),
        api.ledgerMetrics(),
      ])
      setAccounts(items)
      setMetrics(nextMetrics)
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
          <span>账本账户</span>
          <strong>{metrics?.ledger_account_count ?? '—'}</strong>
          <small>个</small>
        </article>
        <article className="metric-card metric-card-accent">
          <span>总信用额度</span>
          <strong>{metrics?.total_credit_limit ?? '—'}</strong>
          <small>积分</small>
        </article>
        <article className="metric-card">
          <span>已用信用</span>
          <strong>{metrics?.credit_capacity_used ?? '—'}</strong>
          <small>积分</small>
        </article>
        <article className="metric-card">
          <span>信用超限账户</span>
          <strong>{metrics?.over_limit_accounts ?? '—'}</strong>
          <small>{metrics?.credit_frozen_accounts ?? '—'} 个信用冻结</small>
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
            <div className="desktop-table-wrap account-desktop-table-wrap">
              <table className="data-table">
                <caption className="visually-hidden">受邀账户列表</caption>
                <thead>
                  <tr>
                    <th scope="col">账户</th>
                    <th scope="col">已入账余额</th>
                    <th scope="col">信用额度</th>
                    <th scope="col">已用信用</th>
                    <th scope="col">可消费额度</th>
                    <th scope="col">风险状态</th>
                    <th aria-label="操作" scope="col" />
                  </tr>
                </thead>
                <tbody>
                  {accounts.map((item) => (
                    <tr key={item.id}>
                      <td>
                        <strong>{item.display_name}</strong>
                        <small>
                          @{item.username} · {item.is_admin ? '管理员' : '普通账户'} · {item.status === 'active' ? '启用' : '停用'}
                        </small>
                      </td>
                      <td>{item.posted_balance}</td>
                      <td>{item.credit_limit} 积分</td>
                      <td>{item.credit_used}</td>
                      <td>{item.spendable_capacity}</td>
                      <td>{accountRiskLabel(item)}</td>
                      <td className="table-action">
                        <span className="table-action-group">
                          <Link
                            className="button button-secondary"
                            to={`/admin/ledger/accounts/${item.id}`}
                          >
                            账本
                          </Link>
                          <Button
                            onClick={() => setEditing(item)}
                            variant="secondary"
                          >
                            管理
                          </Button>
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="mobile-card-list account-mobile-card-list">
              {accounts.map((item) => (
                <article className="mobile-data-card" key={item.id}>
                  <header>
                    <div>
                      <strong>{item.display_name}</strong>
                      <span>@{item.username} · {item.is_admin ? '管理员' : '普通账户'}</span>
                    </div>
                    <StatusBadge status={item.status} />
                  </header>
                  <dl>
                    <div><dt>已入账余额</dt><dd>{item.posted_balance}</dd></div>
                    <div><dt>信用额度</dt><dd>{item.credit_limit} 积分</dd></div>
                    <div><dt>已用信用</dt><dd>{item.credit_used}</dd></div>
                    <div><dt>可消费额度</dt><dd>{item.spendable_capacity}</dd></div>
                    <div><dt>风险状态</dt><dd>{accountRiskLabel(item)}</dd></div>
                  </dl>
                  <div className="mobile-card-actions">
                    <Link className="button button-secondary" to={`/admin/ledger/accounts/${item.id}`}>查看账本</Link>
                    <Button onClick={() => setEditing(item)} variant="secondary">管理账户</Button>
                  </div>
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
          synchronizeAccount(updated)
          setEditing(null)
          void api.ledgerMetrics().then(setMetrics)
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
  const [creditFrozen, setCreditFrozen] = useState(false)
  const [status, setStatus] = useState<AccountStatus>('active')
  const [isAdmin, setIsAdmin] = useState(false)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (account) {
      setCreditLimit(account.credit_limit)
      setCreditFrozen(account.credit_frozen)
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
          credit_frozen: creditFrozen,
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
            checked={creditFrozen}
            onChange={(event) => setCreditFrozen(event.target.checked)}
            type="checkbox"
          />
          <span>冻结新消费与持有</span>
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
