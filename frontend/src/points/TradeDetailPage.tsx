import { Link, useParams } from 'react-router-dom'
import type { C2CTrade } from '../api/types'
import { formatFen, formatPoints } from '../money/format'
import { formatDateTime } from '../money/time'
import { Card, Mascot, PageHeader, QueryBoundary } from '../ui'
import { useC2CTrade } from './queries'
import { PaymentCountdown, PaymentInfo, TradeActions, TradeStatusBadge } from './TradeParts'

/** 时间线：创建 → 付款 → 放行 / 取消 / 申诉 → 仲裁。 */
function timeline(trade: C2CTrade) {
  return [
    ['下单', trade.created_at],
    ['已付款', trade.paid_at],
    ['申诉', trade.disputed_at],
    ['已放行', trade.released_at],
    ['已取消', trade.cancelled_at],
    ['已仲裁', trade.resolved_at],
  ].filter((item): item is [string, string] => Boolean(item[1]))
}

function TradeDetail({ trade }: { trade: C2CTrade }) {
  const counterpart = trade.viewer_role === 'buyer' ? trade.seller : trade.buyer
  return (
    <>
      <PageHeader
        back={
          <Link className="back-link" to="/points?tab=trades">
            ← 我的交易
          </Link>
        }
        title={trade.viewer_role === 'buyer' ? '买入积分' : '卖出积分'}
      />
      <div className="trade-layout">
        <Card title="交易">
          <div className="stack-form">
            <div className="trade-head">
              <TradeStatusBadge status={trade.status} />
              {trade.status === 'released' && <Mascot kind="drop" size={32} />}
              {trade.status === 'awaiting_payment' && <PaymentCountdown deadline={trade.payment_deadline} />}
            </div>
            <dl className="detail-list trade-facts">
              <div>
                <dt>{trade.viewer_role === 'buyer' ? '卖家' : '买家'}</dt>
                <dd>{counterpart.display_name}</dd>
              </div>
              <div>
                <dt>数量</dt>
                <dd className="num">{formatPoints(trade.amount)} 积分</dd>
              </div>
              <div>
                <dt>单价</dt>
                <dd className="num">{formatFen(trade.unit_price_fen)}</dd>
              </div>
              <div>
                <dt>金额</dt>
                <dd className="num">{formatFen(trade.total_fen)}</dd>
              </div>
              {trade.buyer_note && (
                <div>
                  <dt>付款说明</dt>
                  <dd>{trade.buyer_note}</dd>
                </div>
              )}
            </dl>
            {(trade.status === 'awaiting_payment' || trade.status === 'paid' || trade.status === 'disputed') && (
              <PaymentInfo trade={trade} />
            )}
            <TradeActions trade={trade} />
          </div>
        </Card>
        <Card title="时间线">
          <ol className="trade-timeline">
            {timeline(trade).map(([label, time]) => (
              <li key={label}>
                <strong>{label}</strong>
                <time className="muted num" dateTime={time}>
                  {formatDateTime(time)}
                </time>
              </li>
            ))}
          </ol>
          {(trade.buyer_statement || trade.seller_statement || trade.resolution_reason) && (
            <dl className="trade-statements">
              {trade.buyer_statement && (
                <div>
                  <dt>买家陈述</dt>
                  <dd>{trade.buyer_statement}</dd>
                </div>
              )}
              {trade.seller_statement && (
                <div>
                  <dt>卖家陈述</dt>
                  <dd>{trade.seller_statement}</dd>
                </div>
              )}
              {trade.resolution_reason && (
                <div>
                  <dt>仲裁结果</dt>
                  <dd>{trade.resolution_reason}</dd>
                </div>
              )}
            </dl>
          )}
        </Card>
      </div>
    </>
  )
}

export function TradeDetailPage() {
  const { id = '' } = useParams()
  const trade = useC2CTrade(id)
  return (
    <QueryBoundary errorFallback="交易加载失败" query={trade}>
      {(data) => <TradeDetail trade={data} />}
    </QueryBoundary>
  )
}
