import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { errorMessage } from '../api/query'
import type { C2CMyOrder, C2COrder, C2CTrade, TradeStatus } from '../api/types'
import { useAuth } from '../auth/AuthProvider'
import { amountSign, formatFen, formatPoints } from '../money/format'
import { formatTime } from '../money/time'
import {
  Badge,
  Button,
  Card,
  ConfirmDialog,
  DataTable,
  EmptyState,

  PageHeader,
  ProgressBar,
  QueryBoundary,
  Sparkline,
  Tabs,
  type Column,
} from '../ui'
import { BillTab } from './BillTab'
import { BuyDrawer } from './BuyDrawer'
import { creditUsedPercent, needsMyAction, sortTrades, tradeStatusLabels } from './c2c'
import { useC2COrders, useCloseOrder, useMyC2COrders, useMyC2CTrades, usePoints } from './queries'
import { SellDrawer } from './SellDrawer'
import { TradeStatusBadge } from './TradeParts'

type TabKey = 'buy' | 'trades' | 'orders' | 'bill'
const tabKeys: TabKey[] = ['buy', 'trades', 'orders', 'bill']

function BalanceCard({ onSell }: { onSell: () => void }) {
  const points = usePoints()
  return (
    <Card className="points-balance">
      <QueryBoundary errorFallback="余额加载失败" query={points}>
        {(data) => {
          const used = creditUsedPercent(data.balance, data.credit_limit)
          const trend = data.trend?.map((point) => Number(point.balance)) ?? []
          return (
            <div className="points-balance-body">
              <div className="points-balance-main">
                <span className="muted-copy">余额</span>
                <strong className={`balance-value num ${amountSign(data.balance) < 0 ? 'amount-negative' : ''}`}>
                  {formatPoints(data.balance, { digits: 2 })}
                  <small>积分</small>
                </strong>
                <span className="muted-copy">
                  信用额度 <span className="num">{formatPoints(data.credit_limit, { digits: 2 })}</span> · 已用{' '}
                  <span className="num">{Math.round(used)}%</span>
                </span>
                <ProgressBar label={`信用额度已用 ${Math.round(used)}%`} value={used} warn={used >= 80} />
              </div>
              {trend.length > 1 && (
                <div className="points-trend">
                  <span className="muted-copy">30 天走势</span>
                  <Sparkline label="30 天余额走势" values={trend} width={200} />
                </div>
              )}
              <div className="points-balance-actions">
                <Button onClick={onSell} type="button" variant="secondary">
                  卖积分
                </Button>
              </div>
            </div>
          )
        }}
      </QueryBoundary>
    </Card>
  )
}

function BuyTab() {
  const orders = useC2COrders()
  const { account } = useAuth()
  const [buying, setBuying] = useState<C2COrder | null>(null)
  const columns: Column<C2COrder>[] = [
    { key: 'seller', header: '卖家', primary: true, cell: (order) => <strong>{order.seller.display_name}</strong> },
    { key: 'price', header: '单价', numeric: true, cell: (order) => formatFen(order.unit_price_fen) },
    { key: 'available', header: '可买', numeric: true, cell: (order) => formatPoints(order.available) },
    {
      key: 'range',
      header: '单笔范围',
      numeric: true,
      cell: (order) => `${formatPoints(order.min_per_trade)} ～ ${order.max_per_trade ? formatPoints(order.max_per_trade) : '不限'}`,
    },
    { key: 'methods', header: '收款方式', cell: (order) => order.payment_channels.join('、') || '—' },
    {
      key: 'action',
      header: '操作',
      cell: (order) =>
        order.seller.id === account?.id ? (
          <Badge>我的卖单</Badge>
        ) : (
          <Button onClick={() => setBuying(order)} size="sm" type="button">
            买
          </Button>
        ),
    },
  ]
  return (
    <>
      <QueryBoundary errorFallback="卖单加载失败" query={orders}>
        {(data) => (
          <div className="panel panel-flush">
            <DataTable
              caption="卖单"
              columns={columns}
              empty={<EmptyState title="暂无卖单" />}
              rowKey={(order) => order.id}
              rows={data.items.slice().sort((a, b) => a.unit_price_fen - b.unit_price_fen)}
            />
          </div>
        )}
      </QueryBoundary>
      <BuyDrawer onClose={() => setBuying(null)} order={buying} />
    </>
  )
}

function TradesTab() {
  const [status, setStatus] = useState<'' | TradeStatus>('')
  const trades = useMyC2CTrades({ status: status || undefined })
  const columns: Column<C2CTrade>[] = [
    {
      key: 'who',
      header: '对方',
      primary: true,
      cell: (trade) => (
        <>
          <strong>
            {trade.viewer_role === 'buyer' ? `买自 ${trade.seller.display_name}` : `卖给 ${trade.buyer.display_name}`}
          </strong>
          {needsMyAction(trade) && <small className="needs-action">待我处理</small>}
        </>
      ),
    },
    { key: 'amount', header: '数量', numeric: true, cell: (trade) => formatPoints(trade.amount) },
    { key: 'total', header: '金额', numeric: true, cell: (trade) => formatFen(trade.total_fen) },
    { key: 'status', header: '状态', cell: (trade) => <TradeStatusBadge status={trade.status} /> },
    { key: 'time', header: '时间', cell: (trade) => <span className="num">{formatTime(trade.created_at)}</span> },
  ]
  return (
    <div className="trades-tab">
      <label className="filter-select">
        <span className="visually-hidden">状态</span>
        <select aria-label="状态" className="input select-input" onChange={(event) => setStatus(event.target.value as TradeStatus | '')} value={status}>
          <option value="">状态：全部</option>
          {(Object.keys(tradeStatusLabels) as TradeStatus[]).map((item) => (
            <option key={item} value={item}>
              {tradeStatusLabels[item].label}
            </option>
          ))}
        </select>
      </label>
      <QueryBoundary errorFallback="交易加载失败" query={trades}>
        {(data) => (
          <div className="panel panel-flush">
            <DataTable
              caption="我的交易"
              columns={columns}
              empty={<EmptyState title="还没有交易" />}
              rowHref={(trade) => `/points/trades/${trade.id}`}
              rowKey={(trade) => trade.id}
              rows={sortTrades(data.items)}
            />
          </div>
        )}
      </QueryBoundary>
    </div>
  )
}

function OrdersTab() {
  const orders = useMyC2COrders()
  const close = useCloseOrder()
  const [closing, setClosing] = useState<C2CMyOrder | null>(null)
  const statusLabel = { open: '出售中', closed: '已关闭', filled: '已售完' } as const
  const columns: Column<C2CMyOrder>[] = [
    {
      key: 'price',
      header: '单价',
      primary: true,
      cell: (order) => (
        <>
          <strong className="num">{formatFen(order.unit_price_fen)}</strong>
          <small>{formatTime(order.created_at)}</small>
        </>
      ),
    },
    { key: 'available', header: '可买', numeric: true, cell: (order) => formatPoints(order.available) },
    { key: 'in_trade', header: '交易中', numeric: true, cell: (order) => formatPoints(order.in_trade) },
    { key: 'sold', header: '已售', numeric: true, cell: (order) => formatPoints(order.sold) },
    { key: 'closed', header: '已关闭', numeric: true, cell: (order) => formatPoints(order.closed) },
    { key: 'status', header: '状态', cell: (order) => <Badge tone={order.status === 'open' ? 'success' : 'neutral'}>{statusLabel[order.status]}</Badge> },
    {
      key: 'action',
      header: '操作',
      cell: (order) =>
        order.status === 'open' ? (
          <Button onClick={() => setClosing(order)} size="sm" type="button" variant="secondary">
            关闭卖单
          </Button>
        ) : (
          '—'
        ),
    },
  ]
  return (
    <>
      <QueryBoundary errorFallback="卖单加载失败" query={orders}>
        {(data) => (
          <div className="panel panel-flush">
            <DataTable caption="我的卖单" columns={columns} empty={<EmptyState title="还没有卖单" />} rowKey={(order) => order.id} rows={data.items} />
          </div>
        )}
      </QueryBoundary>
      <ConfirmDialog
        busy={close.isPending}
        confirmLabel="关闭卖单"
        description={closing ? `未售出的 ${formatPoints(closing.available)} 积分退回余额；交易中的部分不受影响` : undefined}
        error={close.isError ? errorMessage(close.error, '关闭失败，请重试') : ''}
        onClose={() => setClosing(null)}
        onConfirm={() => closing && close.mutate(closing.id, { onSuccess: () => setClosing(null) })}
        open={closing !== null}
        title="关闭这个卖单？"
      />
    </>
  )
}

export function PointsPage() {
  const [search, setSearch] = useSearchParams()
  const tabParam = search.get('tab') as TabKey | null
  const tab: TabKey = tabParam && tabKeys.includes(tabParam) ? tabParam : 'buy'
  const selling = search.get('sell') === '1'
  const update = (patch: Record<string, string | null>) => {
    const next = new URLSearchParams(search)
    for (const [key, value] of Object.entries(patch)) {
      if (value === null) next.delete(key)
      else next.set(key, value)
    }
    setSearch(next, { replace: true })
  }
  return (
    <>
      <PageHeader title="积分" />
      <BalanceCard onSell={() => update({ sell: '1' })} />
      <Tabs
        items={[
          { key: 'buy', label: '买积分' },
          { key: 'trades', label: '我的交易' },
          { key: 'orders', label: '我的卖单' },
          { key: 'bill', label: '账单' },
        ]}
        label="积分"
        onChange={(key) => update({ tab: key })}
        value={tab}
      >
        {tab === 'buy' && <BuyTab />}
        {tab === 'trades' && <TradesTab />}
        {tab === 'orders' && <OrdersTab />}
        {tab === 'bill' && <BillTab />}
      </Tabs>
      <SellDrawer onClose={() => update({ sell: null })} open={selling} />
    </>
  )
}

