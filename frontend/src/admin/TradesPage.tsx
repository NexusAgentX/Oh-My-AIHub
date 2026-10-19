import { errorMessage } from '../api/query'
import { Facts } from '../ui/Facts'
import { FinanceNav } from './SectionNav'
import { Pager } from '../ui/Pager'
import { enumField, useBack, useUrlState } from '../ui/urlState'
import { useUnsavedGuard } from '../ui/UnsavedGuard'
import { useState } from 'react'
import { Link, useLocation, useParams } from 'react-router-dom'
import {
  Badge,
  Button,
  Card,
  DataTable,
  PageHeader,
  QueryBoundary,
  SelectField,
  Toolbar,
  type BadgeTone,
  type Column,
} from '../ui'
import { ConfirmActionDialog } from './components'
import { formatDateTime, formatFen, formatExactPoints as formatPoints, shortID } from '../money'
import { useAdminTrades, useAdminTrade, useAdminTransactions, useResolveTrade } from './queries'
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

const tradeSchema = { status: enumField(['all', 'disputed', 'awaiting_payment', 'paid', 'released', 'cancelled', 'resolved_to_buyer', 'resolved_to_seller'] as const, 'disputed') }
export function AdminTradesPage() {
  const [filters, setFilters] = useUrlState(tradeSchema), location = useLocation()
  const query = useAdminTrades({ status: filters.status === 'all' ? undefined : filters.status })
  const from = { to: location.pathname + location.search, label: 'C2C 交易' }
  return <><PageHeader title="C2C 交易" /><FinanceNav /><Toolbar><SelectField label="状态" value={filters.status} onChange={event => setFilters({ status: event.target.value as typeof filters.status })}><option value="all">全部状态</option>{Object.entries(tradeStatusLabels).map(([value, [label]]) => <option key={value} value={value}>{label}</option>)}</SelectField></Toolbar>
    <Card flush><QueryBoundary query={query}>{rows => <div aria-busy={query.isFetching}><DataTable caption="C2C 交易列表" columns={columns} empty={<p className="muted-copy empty-pad">没有符合条件的交易</p>} rowHref={trade => `/admin/trades/${trade.id}`} rowState={() => ({ from })} rowKey={trade => trade.id} rows={rows} /><Pager hasMore={query.hasNextPage || query.limited} limited={query.limited} error={query.isFetchNextPageError ? errorMessage(query.error, '加载失败，请重试') : undefined} loading={query.isFetchingNextPage} onMore={() => void query.fetchNextPage()} /></div>}</QueryBoundary></Card></>
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
                <Link to={`/admin/points/transactions?tx=${transaction.id}`}>查看分录</Link>
              </li>
            ))}
          </ul>
        )
      }}
    </QueryBoundary>
  )
}

function TradeDetail({ trade }: { trade: C2CTrade }) {
  const resolve = useResolveTrade()
  const [result, setResult] = useState<'buyer' | 'seller' | null>(null)
  const open = trade.status === 'disputed'
  useUnsavedGuard(result !== null, { busy: resolve.isPending })
  return (
    <>
      <div className="admin-two-column">
        <Card title="交易" actions={open && <><Button onClick={() => setResult('seller')} variant="secondary">退回卖家</Button><Button onClick={() => setResult('buyer')}>判给买家</Button></>}>
          <Facts
            items={[
              /* oxlint-disable react/jsx-key -- [标签, 值] 元组，不是渲染的子元素列表 */
              ['状态', <TradeStatusBadge status={trade.status} />],
              ['买家', trade.buyer.display_name],
              ['卖家', trade.seller.display_name],
              ['数量', `${formatPoints(trade.amount)} 积分`],
              ['单价 / 金额', `${formatFen(trade.unit_price_fen)} / ${formatFen(trade.total_fen)}`],
              ['发起方', opener(trade)],
              ['付款截止', formatDateTime(trade.payment_deadline)],
              /* oxlint-enable react/jsx-key */
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

export function AdminTradeDetailPage() {
  const { tradeID = '' } = useParams(), query = useAdminTrade(tradeID), back = useBack('/admin/trades', 'C2C 交易')
  return <><PageHeader title={`交易 ${shortID(tradeID)}`} back={back} actions={query.data && query.data.trade.status !== 'disputed' && <Badge>只读</Badge>} /><QueryBoundary query={query}>{({ trade }) => <TradeDetail key={trade.id} trade={trade} />}</QueryBoundary></>
}
