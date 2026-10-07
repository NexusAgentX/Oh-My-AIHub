import { Link } from 'react-router-dom'
import {
  ButtonLink,
  Card,
  Metric,
  MetricGrid,
  PageHeader,
  QueryBoundary,
} from '../ui'
import { creditUsagePercent, formatPointAmount, remainingCredit } from '../wallet/presentation'
import { useWallet } from '../wallet/queries'
import { CallTable } from './CallTable'
import { PendingItems } from './PendingItems'
import { QuickStart } from './QuickStart'
import { formatPoints } from './presentation'
import { useGatewayDashboardQuery } from './queries'

export function DashboardPage() {
  const { wallet } = useWallet()
  const query = useGatewayDashboardQuery()

  return (
    <>
      <PageHeader
        actions={<ButtonLink to="/market">浏览 API 市场</ButtonLink>}
        title="工作台"
      />
      <div className="dashboard-main">
        <QuickStart />
        <PendingItems />
      </div>
      <QueryBoundary errorFallback="工作台加载失败" query={query}>
        {(dashboard) => (
          <>
            <MetricGrid label="今日概览">
              <Metric
                hint={`冻结 ${formatPointAmount(wallet?.asset_reserved ?? '0')}`}
                label="可用余额"
                tone="accent"
                value={formatPointAmount(wallet?.spendable_capacity ?? '0')}
              />
              <Metric
                hint={`额度 ${formatPointAmount(wallet?.credit_limit ?? '0')}`}
                label="剩余信用"
                progress={wallet ? creditUsagePercent(wallet.effective_credit_limit, wallet.credit_used) : 0}
                value={formatPointAmount(wallet ? remainingCredit(wallet.effective_credit_limit, wallet.credit_used) : '0')}
              />
              <Metric
                hint={`${dashboard.today_succeeded_calls} 次成功调用`}
                label="今日消费"
                value={formatPoints(dashboard.today_spent)}
              />
              <Metric
                hint="不含自有调用"
                label="今日渠道收入"
                value={formatPoints(dashboard.today_external_provider_income)}
              />
            </MetricGrid>
            <Card
              actions={<Link to="/calls">全部记录</Link>}
              className="dashboard-recent"
              flush
              title="最近调用"
            >
              <CallTable calls={dashboard.recent_calls} />
            </Card>
          </>
        )}
      </QueryBoundary>
    </>
  )
}
