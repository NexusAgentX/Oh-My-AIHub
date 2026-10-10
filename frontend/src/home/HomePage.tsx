import { useState } from 'react'
import { useAuth } from '../auth/AuthProvider'
import { CallDetailDrawer, CallTable, summaryColumns, useCall } from '../calls'
import type { CallSummary } from '../api/types'
import { amountSign, formatPoints } from '../money/format'
import { parseNanoPoints, formatNanoPoints } from '../money/amount'
import { usePoints } from '../points/queries'
import { ButtonLink, Card, EmptyState, Icon, Notice, PageHeader, QueryBoundary, Sparkline } from '../ui'
import { useHome } from './queries'
import { StartCard } from './StartCard'

/** 还能透支：可用额度与信用额度取小（余额为正时可用额度包含余额）。 */
export function overdraftRemaining(available: string, creditLimit: string) {
  const a = parseNanoPoints(available)
  const c = parseNanoPoints(creditLimit)
  const value = a < c ? a : c
  return formatNanoPoints(value < 0n ? 0n : value)
}

function BalanceCard() {
  const points = usePoints()
  return (
    <Card className="home-balance" title="余额">
      <QueryBoundary errorFallback="余额加载失败" query={points}>
        {(data) => {
          const trend = data.trend?.map((point) => Number(point.balance)) ?? []
          return (
          <div className="balance-body">
            <strong className={`balance-value num ${amountSign(data.balance) < 0 ? 'amount-negative' : ''}`}>
              {formatPoints(data.balance, { digits: 2 })}
              <small>积分</small>
            </strong>
            <span className="balance-meta">
              <span className="muted-copy">
                还能透支 <span className="num">{formatPoints(overdraftRemaining(data.available, data.credit_limit), { digits: 2 })}</span>
              </span>
              {trend.length > 1 && <span className="muted-copy">30 天走势</span>}
            </span>
            <Sparkline height={36} label="30 天余额走势" values={trend} width={300} />
            <div className="balance-actions">
              <ButtonLink size="sm" to="/points?tab=buy" variant="primary">
                买积分
              </ButtonLink>
              <ButtonLink size="sm" to="/points?sell=1">
                卖积分
              </ButtonLink>
            </div>
          </div>
          )
        }}
      </QueryBoundary>
    </Card>
  )
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
  const [selected, setSelected] = useState<string | null>(null)
  const detail = useCall(selected)
  return (
    <Card
      actions={
        <ButtonLink size="sm" to="/usage" variant="quiet">
          全部用量
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
        <ButtonLink icon={<Icon name="chevron-right" />} size="sm" to="/points?tab=trades">
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
      <PageHeader title={`你好，${account.display_name}`} />
      <PendingTradesBanner />
      <StartCard />
      <div className="home-grid">
        <BalanceCard />
        <TodayCard />
      </div>
      <RecentCalls />
    </div>
  )
}
