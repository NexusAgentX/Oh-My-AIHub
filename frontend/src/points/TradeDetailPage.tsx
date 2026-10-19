import { useEffect } from 'react'
import { useParams } from 'react-router-dom'
import type { C2CTrade } from '../api/types'
import { formatFen, formatPoints } from '../money/format'
import { formatDateTime } from '../money/time'
import { Card, Mascot, Notice, PageHeader, QueryBoundary } from '../ui'
import { useBack } from '../ui/urlState'
import { useC2CTrade } from './queries'
import { PaymentCountdown, PaymentInfo, TradeActions, TradeStatusBadge, useCountdown } from './TradeParts'

function nextStep(trade: C2CTrade) {
  if (trade.status === 'awaiting_payment' && Date.now() >= new Date(trade.payment_deadline).getTime()) return '付款已超时，等待系统取消。'
  if (trade.status === 'awaiting_payment') return trade.viewer_role === 'buyer' ? '请在倒计时结束前付款，并点击“我已付款”。' : '等待买家付款。'
  if (trade.status === 'paid') return trade.viewer_role === 'seller' ? '请核对收款，到账后确认放行积分。' : '已通知卖家，请等待确认收款并放行积分。'
  if (trade.status === 'disputed') return (trade.viewer_role === 'buyer' ? trade.buyer_statement : trade.seller_statement) ? '已提交陈述，等待管理员仲裁。' : '请提交申诉陈述，供管理员仲裁。'
  return ''
}

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
  const active = ['awaiting_payment', 'paid', 'disputed'].includes(trade.status)
  return (
    <>
      {nextStep(trade) && <Notice>{nextStep(trade)}</Notice>}
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
              {!active && <div>
                <dt>数量</dt>
                <dd className="num">{formatPoints(trade.amount)} 积分</dd>
              </div>}
              <div>
                <dt>单价</dt>
                <dd className="num">{formatFen(trade.unit_price_fen)}</dd>
              </div>
              {!active && <div>
                <dt>金额</dt>
                <dd className="num">{formatFen(trade.total_fen)}</dd>
              </div>}
              {trade.buyer_note && (
                <div>
                  <dt>付款说明</dt>
                  <dd>{trade.buyer_note}</dd>
                </div>
              )}
            </dl>
            {active && (
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
  const back = useBack('/points/trades', '我的交易')
  const deadline = trade.data?.status === 'awaiting_payment' ? trade.data.payment_deadline : null
  const left = useCountdown(deadline)
  const { refetch } = trade
  useEffect(() => { if (deadline && left === 0) void refetch() }, [deadline, left, refetch])
  return (
    <><PageHeader back={back} title={trade.data ? trade.data.viewer_role === 'buyer' ? '买入积分' : '卖出积分' : '交易详情'} />
    <QueryBoundary errorFallback="交易加载失败" query={trade}>
      {(data) => <TradeDetail trade={data} />}
    </QueryBoundary></>
  )
}
