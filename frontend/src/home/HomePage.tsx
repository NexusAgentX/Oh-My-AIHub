import { BalanceCard } from '../points/BalanceCard'
import { useQueryOverlay } from '../ui/urlState'
import { useAuth } from '../auth/AuthProvider'
import { CallDetailDrawer, CallTable, summaryColumns, useCall } from '../calls'
import type { CallSummary } from '../api/types'
import { formatPoints } from '../money/format'
import { usePoints } from '../points/queries'
import { ButtonLink, Card, EmptyState, Icon, Notice, PageHeader, QueryBoundary } from '../ui'
import { useHome } from './queries'
import { StartCard } from './StartCard'

function HomeBalance() {
  const points = usePoints()
  return <QueryBoundary query={points} errorFallback="余额加载失败">{summary => <BalanceCard summary={summary} variant="compact" />}</QueryBoundary>
}

function TodayCard() {
  const home = useHome()
  return (
    <Card className="home-today" title="今天">
      <QueryBoundary errorFallback="今日数据加载失败" query={home}>
        {(data) => (
          <dl className="today-grid">
            <div>
              <dt>花费</dt>
              <dd className="num">{formatPoints(data.today.spend)}</dd>
            </div>
            <div>
              <dt>调用</dt>
              <dd className="num">{data.today.calls.toLocaleString('zh-CN')}</dd>
            </div>
            <div>
              <dt>渠道收入</dt>
              <dd className="num amount-positive">{formatPoints(data.channels.today_revenue)}</dd>
            </div>
            <div>
              <dt>在线渠道</dt>
              <dd className="num">
                {data.channels.online} / {data.channels.total}
              </dd>
            </div>
          </dl>
        )}
      </QueryBoundary>
    </Card>
  )
}

function RecentCalls() {
  const home = useHome()
  const [selected, setSelected] = useQueryOverlay('call')
  const detail = useCall(selected)
  return (
    <Card
      actions={
        <ButtonLink size="sm" to="/calls" variant="quiet">
          全部调用
        </ButtonLink>
      }
      flush
      title="最近调用"
    >
      <QueryBoundary errorFallback="最近调用加载失败" query={home}>
        {(data) => (
          <CallTable<CallSummary>
            caption="最近调用"
            columns={summaryColumns({ showKey: false, showFormat: false })}
            empty={<EmptyState title="还没有调用" />}
            onSelect={(row) => setSelected(row.id)}
            rows={data.recent_calls.slice(0, 5)}
          />
        )}
      </QueryBoundary>
      <CallDetailDrawer onClose={() => setSelected(null)} open={Boolean(selected)} query={detail} />
    </Card>
  )
}

function PendingTradesBanner() {
  const home = useHome()
  const count = home.data?.pending_c2c_trades ?? 0
  if (count <= 0) return null
  return (
    <Notice
      action={
        <ButtonLink icon={<Icon name="chevron-right" />} size="sm" to="/points/trades">
          去处理
        </ButtonLink>
      }
      tone="warning"
    >
      有 {count} 笔积分交易等你处理
    </Notice>
  )
}

export function HomePage() {
  const { account } = useAuth()
  if (!account) return null
  return (
    <div className="home-page">
      <PageHeader title={`你好，${account.display_name}`} topbarTitle="首页" />
      <PendingTradesBanner />
      <StartCard />
      <div className="home-grid">
        <HomeBalance />
        <TodayCard />
      </div>
      <RecentCalls />
    </div>
  )
}
