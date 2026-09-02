import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type {
  LedgerEntry,
  Wallet,
  WalletRecoveryAction,
} from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState } from '../ui/FormControls'
import { Icon } from '../ui/Icon'
import { LedgerEntriesTable } from './LedgerEntriesTable'
import { formatPointAmount, walletRiskLabel, walletRiskTone } from './presentation'
import { useWallet } from './WalletProvider'

export function WalletSummary({ wallet }: { wallet: Wallet }) {
  return (
    <section className="metric-grid wallet-metrics" aria-label="钱包摘要">
      <article className="metric-card metric-card-warm">
        <span>已入账余额</span>
        <strong>{formatPointAmount(wallet.posted_balance)}</strong>
        <small>积分</small>
      </article>
      <article className="metric-card metric-card-accent">
        <span>可消费额度</span>
        <strong>{formatPointAmount(wallet.spendable_capacity)}</strong>
        <small>积分</small>
      </article>
      <article className="metric-card">
        <span>信用额度</span>
        <strong>{formatPointAmount(wallet.credit_limit)}</strong>
        <small>已用 {formatPointAmount(wallet.credit_used)}</small>
      </article>
      <article className="metric-card">
        <span>持有中的积分</span>
        <strong>
          {formatPointAmount(wallet.asset_reserved)} / {formatPointAmount(wallet.spend_authorized)}
        </strong>
        <small>资产冻结 / 消费授权</small>
      </article>
    </section>
  )
}

export function WalletRiskNotice({ wallet }: { wallet: Wallet }) {
  if (wallet.risk_status === 'normal') return null
  return (
    <div className={`wallet-risk wallet-risk-${walletRiskTone(wallet.risk_status)}`} role="status">
      <strong>{walletRiskLabel(wallet.risk_status)}</strong>
      <span>当前可消费 {formatPointAmount(wallet.spendable_capacity)} 积分</span>
    </div>
  )
}

const recoveryCopy: Record<WalletRecoveryAction['kind'], { title: string; meta: string }> = {
  market: { title: '进入 C2C 市场', meta: '查看当前买单和卖单' },
  create_buy_order: { title: '发布买单', meta: '按期望价格等待卖家' },
  my_orders: { title: '查看我的挂单', meta: '管理未成交与进行中订单' },
}

export function RecoveryActions({ actions }: { actions: WalletRecoveryAction[] }) {
  return (
    <div className="recovery-grid">
      {actions.map((action) => (
        <Link className="recovery-card" key={action.kind} to={action.href}>
          <span className="recovery-card-icon"><Icon name="wallet" /></span>
          <span>
            <strong>{recoveryCopy[action.kind].title}</strong>
            <small>{recoveryCopy[action.kind].meta}</small>
          </span>
          <span aria-hidden="true">›</span>
        </Link>
      ))}
    </div>
  )
}

export function useLedgerEntries() {
  const [entries, setEntries] = useState<LedgerEntry[]>([])
  const [nextBefore, setNextBefore] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')

  const load = async (before = '') => {
    before ? setLoadingMore(true) : setLoading(true)
    setError('')
    try {
      const response = await api.walletEntries(before)
      setEntries((current) => before ? [...current, ...response.entries] : response.entries)
      setNextBefore(response.next_before)
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '账本分录加载失败')
    } finally {
      setLoading(false)
      setLoadingMore(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  return { entries, nextBefore, loading, loadingMore, error, load }
}

export function WalletPage() {
  const { wallet, recoveryActions, loading, error, refresh } = useWallet()
  const ledger = useLedgerEntries()

  return (
    <AppShell>
      <header className="page-heading">
        <div><h1>积分钱包</h1></div>
        <Link className="button button-primary" to="/c2c">进入 C2C 市场</Link>
      </header>
      <InlineError>{error || ledger.error}</InlineError>
      {loading && !wallet ? (
        <LoadingState />
      ) : wallet ? (
        <>
          <WalletSummary wallet={wallet} />
          <WalletRiskNotice wallet={wallet} />
          <section className="panel table-panel">
            <header className="table-toolbar">
              <h2>最近分录</h2>
              <Button onClick={() => void refresh()} variant="quiet">刷新余额</Button>
            </header>
            {ledger.loading ? <LoadingState /> : (
              <LedgerEntriesTable
                entries={ledger.entries}
                loadingMore={ledger.loadingMore}
                nextBefore={ledger.nextBefore}
                onLoadMore={() => void ledger.load(ledger.nextBefore)}
              />
            )}
          </section>
        </>
      ) : null}
      {wallet && (
        wallet.risk_status === 'insufficient' ||
        wallet.risk_status === 'over_limit'
      ) && (
        <section className="wallet-recovery-section">
          <h2>补足积分</h2>
          <RecoveryActions actions={recoveryActions} />
        </section>
      )}
      {wallet?.risk_status === 'credit_frozen' && (
        <section className="wallet-recovery-section">
          <h2>信用已冻结</h2>
          <p className="muted">请联系管理员恢复新消费与持有。</p>
        </section>
      )}
    </AppShell>
  )
}
