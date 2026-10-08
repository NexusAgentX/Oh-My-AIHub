import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  Badge,
  Button,
  Card,
  DataTable,
  PageHeader,
  QueryBoundary,
  Segmented,
  Toolbar,
  type BadgeTone,
  type Column,
} from '../ui'
import { ConfirmActionDialog, DetailList, LoadMore } from './components'
import { formatDateTime, formatFen, formatPoints, shortID } from './format'
import { useAdminDisputes, useAdminTrade, useAdminTransactions, useResolveTrade } from './queries'
import { TransactionTypeLabel, transactionVolume } from './transactions'
import type { C2CTrade, TradeStatus } from './types'

export const tradeStatusLabels: Record<TradeStatus, [string, BadgeTone]> = {
  awaiting_payment: ['待付款', 'info'],
  paid: ['已付款', 'info'],
  released: ['已放行', 'success'],
  cancelled: ['已取消', 'neutral'],
  disputed: ['申诉中', 'danger'],
  resolved_to_buyer: ['判给买家', 'success'],
  resolved_to_seller: ['退回卖家', 'neutral'],
}

function TradeStatusBadge({ status }: { status: TradeStatus }) {
  const [label, tone] = tradeStatusLabels[status]
  return <Badge tone={tone}>{label}</Badge>
}

function opener(trade: C2CTrade) {
  if (!trade.dispute_opened_by) return '—'
  if (trade.dispute_opened_by === trade.buyer.id) return '买家'
  if (trade.dispute_opened_by === trade.seller.id) return '卖家'
  return '—'
}

const columns: Column<C2CTrade>[] = [
  {
    key: 'trade',
    header: '交易号',
    primary: true,
    cell: (trade) => (
      <>
        <strong className="mono">{shortID(trade.id)}</strong>
        <small>{formatDateTime(trade.disputed_at ?? trade.created_at)}</small>
      </>
    ),
  },
  { key: 'buyer', header: '买家', cell: (trade) => trade.buyer.display_name },
  { key: 'seller', header: '卖家', cell: (trade) => trade.seller.display_name },
  {
    key: 'amount',
    header: '数量 / 金额',
    numeric: true,
    cell: (trade) => `${formatPoints(trade.amount)} / ${formatFen(trade.total_fen)}`,
  },
  { key: 'opener', header: '发起方', cell: opener },
  { key: 'status', header: '状态', cell: (trade) => <TradeStatusBadge status={trade.status} /> },
]

type DisputeFilter = 'disputed' | 'resolved_to_buyer' | 'resolved_to_seller'

export function DisputesPage() {
  const [status, setStatus] = useState<DisputeFilter>('disputed')
  const disputes = useAdminDisputes({ status })
  return (
    <>
      <PageHeader title="申诉" />
      <Toolbar>
        <Segmented
          label="状态"
          onChange={setStatus}
          options={[
            { key: 'disputed', label: '待处理' },
            { key: 'resolved_to_buyer', label: '判给买家' },
            { key: 'resolved_to_seller', label: '退回卖家' },
          ]}
          value={status}
        />
      </Toolbar>
      <Card flush>
        <QueryBoundary query={disputes}>
          {(rows) => (
            <>
              <DataTable
                caption="申诉列表"
                columns={columns}
                empty={<p className="muted-copy empty-pad">{status === 'disputed' ? '没有待处理的申诉' : '暂无记录'}</p>}
                rowHref={(trade) => `/admin/disputes/${trade.id}`}
                rowKey={(trade) => trade.id}
                rows={rows}
              />
              <LoadMore
                hasMore={Boolean(disputes.hasNextPage)}
                loading={disputes.isFetchingNextPage}
                onLoad={() => void disputes.fetchNextPage()}
              />
            </>
          )}
        </QueryBoundary>
      </Card>
    </>
  )
}

/** 交易时间线：只列出已经发生的节点。 */
export function tradeTimeline(trade: C2CTrade): Array<{ label: string; at: string }> {
  const steps: Array<[string, string | null]> = [
    ['下单', trade.created_at],
    ['买家标记已付款', trade.paid_at],
    ['发起申诉', trade.disputed_at],
    ['卖家放行', trade.released_at],
    ['取消', trade.cancelled_at],
    [trade.status === 'resolved_to_seller' ? '仲裁：退回卖家' : '仲裁：判给买家', trade.resolved_at],
  ]
  return steps
    .filter((step): step is [string, string] => Boolean(step[1]))
    .map(([label, at]) => ({ label, at }))
    .sort((left, right) => left.at.localeCompare(right.at))
}

/** 相关账本记录：按关联对象查询本交易的记账与所属卖单的挂单记账。 */
function RelatedLedger({ trade }: { trade: C2CTrade }) {
  const byTrade = useAdminTransactions({ related_type: 'c2c_trade', related_id: trade.id, limit: 50 })
  const byOrder = useAdminTransactions({ related_type: 'c2c_order', related_id: trade.order_id, limit: 50 })
  const merged =
    byTrade.data && byOrder.data
      ? [...byTrade.data, ...byOrder.data].sort((left, right) => left.created_at.localeCompare(right.created_at))
      : undefined
  return (
    <QueryBoundary
      query={{
        data: merged,
        isPending: byTrade.isPending || byOrder.isPending,
        isError: byTrade.isError || byOrder.isError,
        error: byTrade.error ?? byOrder.error,
        refetch: () => Promise.all([byTrade.refetch(), byOrder.refetch()]),
      }}
    >
      {(rows) => {
        if (rows.length === 0) return <p className="muted-copy">暂无相关记账</p>
        return (
          <ul className="plain-list">
            {rows.map((transaction) => (
              <li key={transaction.id}>
                <TransactionTypeLabel type={transaction.type} />
                <span className="muted-copy">
                  {formatDateTime(transaction.created_at)} · {formatPoints(transactionVolume(transaction))} 积分
                  {transaction.related?.type === 'c2c_order' ? ' · 卖单' : ''}
                </span>
                <Link to={`/admin/points?tab=transactions&transaction=${transaction.id}`}>查看分录</Link>
              </li>
            ))}
          </ul>
        )
      }}
    </QueryBoundary>
  )
}

function DisputeDetail({ trade }: { trade: C2CTrade }) {
  const resolve = useResolveTrade()
  const [result, setResult] = useState<'buyer' | 'seller' | null>(null)
  const open = trade.status === 'disputed'
  return (
    <>
      <PageHeader
        actions={
          open && (
            <>
              <Button onClick={() => setResult('seller')} type="button" variant="secondary">
                退回卖家
              </Button>
              <Button onClick={() => setResult('buyer')} type="button">
                判给买家
              </Button>
            </>
          )
        }
        back={
          <Link className="back-link" to="/admin/disputes">
            ← 申诉
          </Link>
        }
        title={`交易 ${shortID(trade.id)}`}
      />
      <div className="admin-two-column">
        <Card title="交易">
          <DetailList
            items={[
              ['状态', <TradeStatusBadge status={trade.status} />],
              ['买家', trade.buyer.display_name],
              ['卖家', trade.seller.display_name],
              ['数量', `${formatPoints(trade.amount)} 积分`],
              ['单价 / 金额', `${formatFen(trade.unit_price_fen)} / ${formatFen(trade.total_fen)}`],
              ['发起方', opener(trade)],
              ['付款截止', formatDateTime(trade.payment_deadline)],
            ]}
          />
        </Card>
        <Card title="时间线">
          <ol className="timeline">
            {tradeTimeline(trade).map((step) => (
              <li key={step.label}>
                <span className="timeline-time">{formatDateTime(step.at)}</span>
                <strong>{step.label}</strong>
              </li>
            ))}
          </ol>
          {trade.resolution_reason && <p className="muted-copy">仲裁原因：{trade.resolution_reason}</p>}
        </Card>
      </div>
      <div className="admin-two-column">
        <Card title="买家陈述">
          <p className="statement">{trade.buyer_statement || '未陈述'}</p>
          <h3 className="section-title">付款说明</h3>
          <p className="statement">{trade.buyer_note || '未填写'}</p>
        </Card>
        <Card title="卖家陈述">
          <p className="statement">{trade.seller_statement || '未陈述'}</p>
        </Card>
      </div>
      <Card title="相关账本记录">
        <RelatedLedger trade={trade} />
      </Card>
      <ConfirmActionDialog
        confirmLabel={result === 'buyer' ? '确认判给买家' : '确认退回卖家'}
        danger
        onClose={() => setResult(null)}
        onConfirm={(reason) => resolve.mutateAsync({ id: trade.id, body: { result: result ?? 'seller', reason } })}
        open={result !== null}
        summary={
          result === 'buyer' ? (
            <p>
              托管中的 <strong className="num">{formatPoints(trade.amount)}</strong> 积分转给买家{' '}
              <strong>{trade.buyer.display_name}</strong>。
            </p>
          ) : (
            <p>
              托管中的 <strong className="num">{formatPoints(trade.amount)}</strong> 积分退回卖家{' '}
              <strong>{trade.seller.display_name}</strong>。
            </p>
          )
        }
        title={result === 'buyer' ? '判给买家' : '退回卖家'}
      />
    </>
  )
}

export function DisputeDetailPage() {
  const { tradeID = '' } = useParams()
  const trade = useAdminTrade(tradeID)
  if (trade.data) return <DisputeDetail trade={trade.data.trade} />
  return (
    <>
      <PageHeader
        back={
          <Link className="back-link" to="/admin/disputes">
            ← 申诉
          </Link>
        }
        title="申诉详情"
      />
      <Card>
        <QueryBoundary query={trade}>{() => null}</QueryBoundary>
      </Card>
    </>
  )
}
