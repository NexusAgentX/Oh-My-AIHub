import { useEffect, useState } from 'react'
import { Navigate, Outlet, useLocation, useNavigate, useParams } from 'react-router-dom'
import { errorMessage } from '../api/query'
import type { C2CMyOrder, C2COrder, C2CTrade, TradeStatus } from '../api/types'
import { useAuth } from '../auth/AuthProvider'
import { formatFen, formatPoints } from '../money/format'
import { formatTime } from '../money/time'
import {
  Badge,
  Button,
  ConfirmDialog,
  DataTable,
  EmptyState,

  PageHeader,
  QueryBoundary,
  type Column,
} from '../ui'
import { BillTab } from './BillTab'
import { BuyForm } from './BuyForm'
import { needsMyAction, tradeStatusLabels } from './c2c'
import { useC2COrder, useC2COrders, useCloseOrder, useMyC2COrders, useMyC2CTrades, usePoints } from './queries'
import { SellDrawer } from './SellDrawer'
import { PaymentCountdown, TradeStatusBadge } from './TradeParts'
import { BalanceCard } from './BalanceCard'
import { CallDetailDrawer, useCall } from '../calls'
import { Drawer, ButtonLink, Segmented } from '../ui'
import { Pager } from '../ui/Pager'
import { SectionNav } from '../ui/SectionNav'
import { enumField, useQueryOverlay, useRouteClose, useUrlState } from '../ui/urlState'
import { useHome } from '../home/queries'

function BuyTab() {
  const orders = useC2COrders()
  const { account } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
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
          <Button variant="secondary" onClick={() => navigate(`/points/market/${order.id}${location.search}`, { state: { overlayFrom: `${location.pathname}${location.search}`, from: { to: `${location.pathname}${location.search}`, label: '积分市场' } } })} size="sm" type="button">
            买
          </Button>
        ),
    },
  ]
  return (
    <>
      <QueryBoundary errorFallback="卖单加载失败" query={orders}>
        {(data) => (
          <div className="panel panel-flush" aria-busy={orders.isPlaceholderData}>
            <DataTable
              caption="卖单"
              columns={columns}
              empty={<EmptyState title="暂无卖单" />}
              rowKey={(order) => order.id}
              rows={data.items}
            />
          </div>
        )}
      </QueryBoundary>
      <PageMore query={orders} />
    </>
  )
}

function TradesTab() {
  const [filters, update] = useUrlState(tradeSchema)
  const { status, role } = filters
  const view = filters.pending === 'true' && !status ? 'pending' : filters.active === 'true' ? 'active' : 'all'
  const trades = useMyC2CTrades({ status: status || undefined, role: role || undefined, pending: view === 'pending' || undefined, active: view === 'active' || undefined })
  const home = useHome()
  const location = useLocation()
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
    { key: 'status', header: '状态', cell: trade => <><TradeStatusBadge status={trade.status} />{trade.status === 'awaiting_payment' && <PaymentCountdown deadline={trade.payment_deadline} />}</> },
    { key: 'time', header: '时间', cell: (trade) => <span className="num">{formatTime(trade.created_at)}</span> },
  ]
  return (
    <div className="trades-tab">
      <Segmented label="交易范围" value={view} options={[{ key: 'pending', label: `待我处理${!role && home.data ? `（${home.data.pending_c2c_trades}）` : ''}` }, { key: 'active', label: '进行中' }, { key: 'all', label: '全部' }]} onChange={value => update({ pending: value === 'pending' ? 'true' : 'false', active: value === 'active' ? 'true' : 'false', status: '' })} />
      <label className="filter-select"><span className="visually-hidden">交易角色</span><select className="input select-input" aria-label="交易角色" value={role} onChange={event => update({ role: event.target.value as 'buyer' | 'seller' | '' })}><option value="">角色：全部</option><option value="buyer">买家</option><option value="seller">卖家</option></select></label>
      <label className="filter-select">
        <span className="visually-hidden">状态</span>
        <select aria-label="状态" className="input select-input" onChange={event => update({ status: event.target.value as TradeStatus | '' })} disabled={view !== 'all'} value={status}>
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
          <div className="panel panel-flush" aria-busy={trades.isPlaceholderData}>
            <DataTable
              caption="我的交易"
              columns={columns}
              empty={<EmptyState title="还没有交易" />}
              rowHref={(trade) => `/points/trades/${trade.id}`}
              rowKey={(trade) => trade.id}
              rows={data.items}
              rowState={() => ({ from: { to: `${location.pathname}${location.search}`, label: '我的交易' } })}
            />
          </div>
        )}
      </QueryBoundary>
      <PageMore query={trades} />
    </div>
  )
}

function OrdersTab() {
  const location = useLocation()
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
          <div className="panel panel-flush" aria-busy={orders.isPlaceholderData}>
            <DataTable caption="我的卖单" columns={columns} empty={<EmptyState title="还没有卖单" />} rowKey={(order) => order.id} rows={data.items} highlight={new Set(location.state?.highlight ? [location.state.highlight] : [])} />
          </div>
        )}
      </QueryBoundary>
      <PageMore query={orders} />
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

const tradeSchema = { pending: enumField(['true', 'false'] as const, 'true'), active: enumField(['true', 'false'] as const, 'false'), status: enumField(['', ...Object.keys(tradeStatusLabels) as TradeStatus[]], ''), role: enumField(['', 'buyer', 'seller'] as const, '') }

function PageMore({ query }: { query: { hasNextPage: boolean; isFetchingNextPage: boolean; isFetchNextPageError: boolean; error: unknown; pageCount: number; fetchNextPage: () => Promise<unknown> } }) {
  return <Pager hasMore={query.hasNextPage} loading={query.isFetchingNextPage} limited={query.pageCount >= 10}
    error={query.isFetchNextPageError ? errorMessage(query.error, '加载失败，请重试') : undefined} onMore={() => void query.fetchNextPage()} />
}
export function PointsNav() { return <SectionNav label="积分分区" items={[{ to: '/points', label: '钱包' }, { to: '/points/market', label: '买积分', end: false }, { to: '/points/trades', label: '我的交易', end: false }, { to: '/points/orders', label: '我的卖单', end: false }]} /> }

export function PointsPage() {
  const location = useLocation()
  const points = usePoints()
  const [call, setCall] = useQueryOverlay('call')
  const detail = useCall(call)
  const search = new URLSearchParams(location.search)
  if (search.has('tab') || search.has('sell') || search.has('transaction')) {
    const old = search.get('tab'), sell = search.get('sell') === '1'
    const target = sell ? '/points/orders/new' : old === 'buy' ? '/points/market' : old === 'trades' ? '/points/trades' : old === 'orders' ? '/points/orders' : '/points'
    if (search.has('transaction')) search.set('transaction_id', search.get('transaction')!)
    for (const key of ['tab', 'sell', 'transaction']) search.delete(key)
    return <Navigate replace to={`${target}${search.size ? `?${search}` : ''}${location.hash}`} state={location.state} />
  }
  return <><PageHeader title="钱包" /><PointsNav /><QueryBoundary query={points} errorFallback="余额加载失败">{summary => <BalanceCard summary={summary} />}</QueryBoundary><BillTab /><CallDetailDrawer open={Boolean(call)} onClose={() => setCall(null)} query={detail} /></>
}
export function PointsMarketPage() { return <><PageHeader title="买积分" /><PointsNav /><BuyTab /><Outlet /></> }
export function MyTradesPage() { return <><PageHeader title="我的交易" /><PointsNav /><TradesTab /></> }
export function MyOrdersPage() {
  const location = useLocation()
  const navigate = useNavigate()
  useEffect(() => {
    if (!location.state?.highlight) return
    const timer = window.setTimeout(() => navigate(`${location.pathname}${location.search}`, { replace: true, state: null }), 5000)
    return () => window.clearTimeout(timer)
  }, [location.state, location.pathname, location.search, navigate])
  return <><PageHeader title="我的卖单" actions={<ButtonLink variant="primary" to="/points/orders/new" state={{ overlayFrom: `${location.pathname}${location.search}` }}>发布卖单</ButtonLink>} /><PointsNav />{location.state?.notice && <p className="muted-copy" role="status">{location.state.notice}</p>}<OrdersTab /><Outlet /></>
}
export function BuyRouteDrawer() {
  const { orderID = '' } = useParams()
  const query = useC2COrder(orderID)
  const close = useRouteClose('/points/market')
  const [busy, setBusy] = useState(false)
  const navigate = useNavigate()
  const location = useLocation()
  return <Drawer open onClose={close} busy={busy} title="买积分"><QueryBoundary query={query} errorFallback="卖单不存在或已结束">{order => <BuyForm order={order} onBusy={setBusy} onCreated={trade => navigate(`/points/trades/${trade.id}`, { replace: true, state: { from: location.state?.from ?? { to: '/points/market', label: '积分市场' } } })} />}</QueryBoundary></Drawer>
}
export function SellRouteDrawer() {
  const close = useRouteClose('/points/orders')
  const navigate = useNavigate()
  return <SellDrawer open onClose={close} onCreated={id => navigate('/points/orders', { replace: true, state: { highlight: id, notice: '卖单已发布' } })} />
}
