import { Link, useParams } from 'react-router-dom'
import type { Wallet } from '../api/types'
import { Badge, Card, Metric, MetricGrid, PageHeader, QueryBoundary } from '../ui'
import { LedgerEntriesTable } from '../wallet/LedgerEntriesTable'
import {
  creditUsagePercent,
  formatPointAmount,
  walletRiskLabel,
  walletRiskTone,
} from '../wallet/presentation'
import { useAdminAccountEntriesQuery, useAdminAccountWalletQuery } from './queries'

function AccountSummary({ wallet }: { wallet: Wallet }) {
  return (
    <MetricGrid label="账户摘要">
      <Metric label="已入账余额" value={formatPointAmount(wallet.posted_balance)} />
      <Metric
        label="可消费额度"
        tone="accent"
        value={formatPointAmount(wallet.spendable_capacity)}
      />
      <Metric
        hint={`已用 ${formatPointAmount(wallet.credit_used)}`}
        label="信用额度"
        progress={creditUsagePercent(wallet.credit_limit, wallet.credit_used)}
        value={formatPointAmount(wallet.credit_limit)}
      />
      <Metric
        hint="资产冻结 / 消费授权"
        label="持有中的积分"
        value={`${formatPointAmount(wallet.asset_reserved)} / ${formatPointAmount(wallet.spend_authorized)}`}
      />
    </MetricGrid>
  )
}

export function AdminAccountLedgerPage() {
  const { accountID = '' } = useParams()
  const wallet = useAdminAccountWalletQuery(accountID)
  const entries = useAdminAccountEntriesQuery(accountID)
  const risk = wallet.data?.risk_status

  return (
    <>
      <PageHeader
        actions={risk && <Badge tone={walletRiskTone(risk)}>{walletRiskLabel(risk)}</Badge>}
        back={
          <Link className="back-link" to="/admin/accounts">
            ← 账户与信用
          </Link>
        }
        title="账户账本"
      />
      <QueryBoundary errorFallback="账户账本加载失败" query={wallet}>
        {(data) => <AccountSummary wallet={data} />}
      </QueryBoundary>
      <Card flush title="不可变分录">
        <QueryBoundary errorFallback="账户分录加载失败" query={entries}>
          {(data) => (
            <LedgerEntriesTable
              entries={data.pages.flatMap((page) => page.entries)}
              loadingMore={entries.isFetchingNextPage}
              nextBefore={entries.hasNextPage ? 'more' : ''}
              onLoadMore={() => void entries.fetchNextPage()}
            />
          )}
        </QueryBoundary>
      </Card>
    </>
  )
}
