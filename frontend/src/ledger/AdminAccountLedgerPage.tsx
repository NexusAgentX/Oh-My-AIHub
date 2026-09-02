import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { LedgerEntry, Wallet } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { InlineError, LoadingState } from '../ui/FormControls'
import { LedgerEntriesTable } from '../wallet/LedgerEntriesTable'
import { formatPointAmount, walletRiskLabel } from '../wallet/presentation'
import { WalletSummary } from '../wallet/WalletPage'

export function AdminAccountLedgerPage() {
  const { accountID = '' } = useParams()
  const [wallet, setWallet] = useState<Wallet | null>(null)
  const [entries, setEntries] = useState<LedgerEntry[]>([])
  const [nextBefore, setNextBefore] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')

  const loadEntries = async (before = '') => {
    before ? setLoadingMore(true) : setLoading(true)
    try {
      const response = await api.adminAccountEntries(accountID, before)
      setEntries((current) => before ? [...current, ...response.entries] : response.entries)
      setNextBefore(response.next_before)
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '账户分录加载失败')
    } finally {
      setLoading(false)
      setLoadingMore(false)
    }
  }

  useEffect(() => {
    void Promise.all([api.adminAccountWallet(accountID), api.adminAccountEntries(accountID)])
      .then(([nextWallet, response]) => {
        setWallet(nextWallet)
        setEntries(response.entries)
        setNextBefore(response.next_before)
      })
      .catch((caught) => {
        setError(caught instanceof ApiError ? caught.message : '账户账本加载失败')
      })
      .finally(() => setLoading(false))
  }, [accountID])

  return (
    <AppShell admin>
      <header className="page-heading">
        <div>
          <Link className="back-link" to="/admin/accounts">← 账户与信用</Link>
          <h1>账户账本</h1>
        </div>
        {wallet && <span className="count-badge">{walletRiskLabel(wallet.risk_status)}</span>}
      </header>
      <InlineError>{error}</InlineError>
      {loading && !wallet ? <LoadingState /> : wallet ? (
        <>
          <WalletSummary wallet={wallet} />
          <section className="panel table-panel">
            <header className="table-toolbar">
              <h2>不可变分录</h2>
              <span className="muted-copy">当前余额 {formatPointAmount(wallet.posted_balance)}</span>
            </header>
            <LedgerEntriesTable
              entries={entries}
              loadingMore={loadingMore}
              nextBefore={nextBefore}
              onLoadMore={() => void loadEntries(nextBefore)}
            />
          </section>
        </>
      ) : null}
    </AppShell>
  )
}
