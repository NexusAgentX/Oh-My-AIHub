import { AppShell } from '../layouts/AppShell'
import { InlineError, LoadingState } from '../ui/FormControls'
import { LedgerEntriesTable } from './LedgerEntriesTable'
import {
  RecoveryActions,
  useLedgerEntries,
  WalletSummary,
} from './WalletPage'
import { useWallet } from './WalletProvider'

export function InsufficientBalancePage() {
  const { wallet, recoveryActions, loading, error } = useWallet()
  const ledger = useLedgerEntries()

  return (
    <AppShell>
      <header className="page-heading">
        <div><h1>积分钱包</h1></div>
      </header>
      <InlineError>{error || ledger.error}</InlineError>
      {loading && !wallet ? <LoadingState /> : wallet ? (
        <>
          <WalletSummary wallet={wallet} />
          <div className="insufficient-layout">
            <section className="panel table-panel">
              <header className="table-toolbar"><h2>最近分录</h2></header>
              {ledger.loading ? <LoadingState /> : (
                <LedgerEntriesTable
                  entries={ledger.entries}
                  loadingMore={ledger.loadingMore}
                  nextBefore={ledger.nextBefore}
                  onLoadMore={() => void ledger.load(ledger.nextBefore)}
                />
              )}
            </section>
            {wallet.risk_status === 'credit_frozen' ? (
              <aside className="panel recovery-panel">
                <header className="panel-heading"><h2>信用已冻结</h2></header>
                <p className="muted">请联系管理员恢复新消费与持有。</p>
              </aside>
            ) : (
              <aside className="panel recovery-panel">
                <header className="panel-heading"><h2>可消费额度偏低</h2></header>
                <RecoveryActions actions={recoveryActions} />
              </aside>
            )}
          </div>
        </>
      ) : null}
    </AppShell>
  )
}
