import { PageHeader, Card, QueryBoundary } from '../ui'
import { LedgerEntriesPanel } from './LedgerEntriesTable'
import { RecoveryActions, WalletSummary } from './WalletPage'
import { useWalletQuery } from './queries'

export function InsufficientBalancePage() {
  const query = useWalletQuery()
  return (
    <>
      <PageHeader title="钱包" />
      <QueryBoundary errorFallback="钱包加载失败" query={query}>
        {({ wallet, recovery_actions }) => (
          <>
            <WalletSummary wallet={wallet} />
            <div className="insufficient-layout">
              <Card flush title="最近分录">
                <LedgerEntriesPanel />
              </Card>
              {wallet.risk_status === 'credit_frozen' ? (
                <Card className="recovery-panel" title="信用已冻结">
                  <p className="muted">请联系管理员恢复新消费与持有。</p>
                </Card>
              ) : (
                <Card className="recovery-panel" title="可消费额度偏低">
                  <RecoveryActions actions={recovery_actions} />
                </Card>
              )}
            </div>
          </>
        )}
      </QueryBoundary>
    </>
  )
}
