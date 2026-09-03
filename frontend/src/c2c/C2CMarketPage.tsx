import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { C2CMarket, C2COrder, C2CSide } from '../api/contracts'
import { useAuth } from '../auth/AuthProvider'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState } from '../ui/FormControls'
import { Icon } from '../ui/Icon'
import { formatPointAmount } from '../wallet/presentation'
import {
  c2cPaymentLabels,
  c2cSideLabels,
  formatC2CDate,
  formatC2CPrice,
} from './presentation'

export function C2CMarketPage() {
  const { account } = useAuth()
  const [market, setMarket] = useState<C2CMarket | null>(null)
  const [side, setSide] = useState<C2CSide>('sell')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setMarket(await api.c2cMarket())
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : 'C2C 行情加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const orders = side === 'sell' ? market?.sell_orders ?? [] : market?.buy_orders ?? []

  return (
    <AppShell>
      <header className="page-heading c2c-page-heading">
        <div><h1>C2C 市场</h1></div>
        <div className="c2c-heading-actions">
          <Link className="button button-secondary" to="/c2c/me">我的订单</Link>
          <Link className="button button-secondary" to="/c2c/orders/new?side=buy">发布买单</Link>
          <Link className="button button-primary" to="/c2c/orders/new?side=sell"><Icon name="plus" />发布卖单</Link>
        </div>
      </header>
      <InlineError>{error}</InlineError>
      {loading && !market ? <LoadingState /> : market ? (
        <>
          <section aria-label="C2C 行情" className="metric-grid c2c-market-metrics">
            <MarketMetric label="指导价" value={formatC2CPrice(market.metrics.guidance_price_fen)} />
            <MarketMetric label="最近成交" value={market.metrics.latest_price_fen === null ? '—' : formatC2CPrice(market.metrics.latest_price_fen)} />
            <MarketMetric label="买一" value={market.metrics.best_bid_fen === null ? '—' : formatC2CPrice(market.metrics.best_bid_fen)} tone="warm" />
            <MarketMetric label="卖一" value={market.metrics.best_ask_fen === null ? '—' : formatC2CPrice(market.metrics.best_ask_fen)} tone="accent" />
            <MarketMetric label="价差" value={market.metrics.spread_fen === null ? '—' : formatC2CPrice(market.metrics.spread_fen)} />
          </section>

          <section className="panel table-panel c2c-market-panel">
            <header className="table-toolbar c2c-market-toolbar">
              <div aria-label="挂单方向" className="c2c-tabs" role="tablist">
                {(['sell', 'buy'] as C2CSide[]).map((value) => (
                  <button
                    aria-selected={side === value}
                    className={side === value ? 'c2c-tab c2c-tab-active' : 'c2c-tab'}
                    key={value}
                    onClick={() => setSide(value)}
                    role="tab"
                    type="button"
                  >
                    {c2cSideLabels[value]} <span>{value === 'sell' ? market.sell_orders.length : market.buy_orders.length}</span>
                  </button>
                ))}
              </div>
              <Button disabled={loading} onClick={() => void load()} variant="quiet">刷新</Button>
            </header>
            <OrderList accountID={account?.id ?? ''} orders={orders} side={side} />
          </section>
        </>
      ) : null}
    </AppShell>
  )
}

function MarketMetric({ label, value, tone = '' }: { label: string; value: string; tone?: 'warm' | 'accent' | '' }) {
  return (
    <article className={`metric-card ${tone ? `metric-card-${tone}` : ''}`}>
      <span>{label}</span>
      <strong>{value}</strong>
      <small>人民币 / 积分</small>
    </article>
  )
}

function OrderList({ orders, side, accountID }: { orders: C2COrder[]; side: C2CSide; accountID: string }) {
  if (orders.length === 0) return <div className="empty-state">暂无{c2cSideLabels[side]}</div>
  return (
    <>
      <div className="desktop-table-wrap">
        <table className="data-table c2c-order-table">
          <thead><tr><th scope="col">用户</th><th scope="col">单价</th><th scope="col">可成交</th><th scope="col">限额</th><th scope="col">支付方式</th><th scope="col">发布时间</th><th scope="col"><span className="visually-hidden">操作</span></th></tr></thead>
          <tbody>{orders.map((order) => (
            <tr key={order.id}>
              <td><strong>{order.owner_display_name}</strong>{order.owner_account_id === accountID && <small>我的挂单</small>}</td>
              <td><strong>{formatC2CPrice(order.unit_price_fen)}</strong><small>每积分</small></td>
              <td>{formatPointAmount(order.available)} 积分</td>
              <td>{formatPointAmount(order.minimum)} – {formatPointAmount(order.maximum)}</td>
              <td>{order.payment_types.map((type) => c2cPaymentLabels[type]).join('、')}</td>
              <td>{formatC2CDate(order.created_at)}</td>
              <td className="table-action"><OrderAction accountID={accountID} order={order} /></td>
            </tr>
          ))}</tbody>
        </table>
      </div>
      <div className="mobile-card-list">{orders.map((order) => (
        <article className="mobile-data-card" key={order.id}>
          <header><div><strong>{order.owner_display_name}</strong><span>{formatC2CPrice(order.unit_price_fen)} / 积分</span></div><span className="c2c-side-chip">{c2cSideLabels[order.side]}</span></header>
          <dl>
            <div><dt>可成交</dt><dd>{formatPointAmount(order.available)} 积分</dd></div>
            <div><dt>限额</dt><dd>{formatPointAmount(order.minimum)} – {formatPointAmount(order.maximum)}</dd></div>
            <div><dt>支付方式</dt><dd>{order.payment_types.map((type) => c2cPaymentLabels[type]).join('、')}</dd></div>
          </dl>
          <OrderAction accountID={accountID} order={order} />
        </article>
      ))}</div>
    </>
  )
}

function OrderAction({ order, accountID }: { order: C2COrder; accountID: string }) {
  return order.owner_account_id === accountID ? (
    <Link className="button button-secondary" to="/c2c/me">管理</Link>
  ) : (
    <Link className="button button-primary" to={`/c2c/orders/${order.id}/take`}>
      {order.side === 'sell' ? '购买' : '出售'}
    </Link>
  )
}
