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
import { formatPoints } from './presentation'
import { useGatewayDashboardQuery } from './queries'

export function DashboardPage() {
  const { wallet } = useWallet()
  const query = useGatewayDashboardQuery()

  return (
    <>
      <PageHeader
        actions={
          <>
            <ButtonLink to="/market">浏览 API 市场</ButtonLink>
            <ButtonLink to="/keys/new" variant="primary">创建 API Key</ButtonLink>
          </>
        }
        description="消费、共享和交易的今日状态"
        title="工作台"
      />
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
            <section className="dashboard-grid">
              <Card
                actions={<Link to="/market">打开市场</Link>}
                className="dashboard-health-panel"
                flush
                title="渠道健康"
              >
                <div className="health-stat-grid">
                  <div><span>可用</span><strong>{dashboard.healthy_offer_count}</strong></div>
                  <div><span>需处理</span><strong>{dashboard.unhealthy_offer_count}</strong></div>
                  <div><span>待处理事项</span><strong>{dashboard.pending_items}</strong></div>
                </div>
              </Card>
              <Card className="dashboard-actions-panel" flush title="API Key 与模型协议池">
                <div className="health-stat-grid health-stat-grid-compact">
                  <div><span>活跃 API Keys</span><strong>{dashboard.active_key_count}</strong></div>
                  <div><span>模型协议池</span><strong>{dashboard.pool_count}</strong></div>
                </div>
              </Card>
            </section>
            <Card
              actions={<Link to="/calls">全部记录</Link>}
              className="dashboard-recent-panel"
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
