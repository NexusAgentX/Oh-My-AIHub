import type { Points } from '../api/types'
import { formatPoints } from '../money/format'
import { ButtonLink, Card, ProgressBar, Sparkline } from '../ui'
import { creditUsedPercent } from './c2c'

/** 金额一律来自同一份积分概况；可用额度包含正余额和信用额度。 */
export function BalanceCard({ summary, variant = 'full' }: { summary: Points; variant?: 'compact' | 'full' }) {
  const compact = variant === 'compact'
  const used = creditUsedPercent(summary.balance, summary.credit_limit)
  const trend = summary.trend?.map(point => Number(point.balance)) ?? []
  return <Card className={compact ? 'home-balance' : 'points-balance'} title={compact ? '余额' : undefined}>
    <div className={compact ? 'balance-body' : 'points-balance-body'}>
      <div className={compact ? undefined : 'points-balance-main'}>
        {!compact && <span className="muted-copy">余额</span>}
        <strong className="balance-value num">{formatPoints(summary.balance, { digits: 2 })}<small>积分</small></strong>
        <span className="balance-meta muted-copy"><span>可用额度 <span className="num">{formatPoints(summary.available, { digits: 2 })}</span></span><span>信用额度 <span className="num">{formatPoints(summary.credit_limit, { digits: 2 })}</span></span></span>
        {!compact && <ProgressBar label={`信用额度已用 ${Math.round(used)}%`} value={used} warn={used >= 80} />}
      </div>
      {trend.length > 1 && <div className={compact ? undefined : 'points-trend'}><span className="muted-copy">30 天走势</span><Sparkline height={36} label="30 天余额走势" values={trend} width={compact ? 300 : 200} /></div>}
      <div className={compact ? 'balance-actions' : 'points-balance-actions'}>
        <ButtonLink to="/points/market" size="sm" variant="primary">买积分</ButtonLink>
        <ButtonLink to="/points/orders/new" size="sm">卖积分</ButtonLink>
      </div>
    </div>
  </Card>
}
