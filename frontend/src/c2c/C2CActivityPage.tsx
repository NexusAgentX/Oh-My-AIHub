import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { C2COrder, C2CTrade } from '../api/contracts'
import { useAuth } from '../auth/AuthProvider'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState } from '../ui/FormControls'
import { formatPointAmount } from '../wallet/presentation'
import {
  c2cOrderStatusLabels,
  c2cSideLabels,
  c2cStatusTone,
  c2cTradeStatusLabels,
  formatC2CDate,
  formatC2CFiat,
  formatC2CPrice,
} from './presentation'

export function C2CActivityPage() {
  const { account } = useAuth()
  const [orders, setOrders] = useState<C2COrder[]>([])
  const [trades, setTrades] = useState<C2CTrade[]>([])
  const [loading, setLoading] = useState(true)
  const [actingID, setActingID] = useState('')
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const result = await api.c2cActivity()
      setOrders(result.orders)
      setTrades(result.trades)
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : 'C2C 订单加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const cancelOrder = async (order: C2COrder) => {
    if (!window.confirm(`取消这笔${c2cSideLabels[order.side]}？未成交数量将不再展示。`)) return
    setActingID(order.id)
    setError('')
    try {
      await api.cancelC2COrder(order.id)
      await load()
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '挂单取消失败')
    } finally {
      setActingID('')
    }
  }

  return (
    <AppShell>
      <header className="page-heading c2c-page-heading">
        <div><Link className="back-link" to="/c2c">← C2C 市场</Link><h1>我的挂单与交易</h1></div>
        <div className="c2c-heading-actions"><Link className="button button-primary" to="/c2c/orders/new">发布挂单</Link></div>
      </header>
      <InlineError>{error}</InlineError>
      {loading && orders.length === 0 && trades.length === 0 ? <LoadingState /> : (
        <>
          <section className="panel table-panel c2c-activity-panel">
            <header className="table-toolbar"><h2>我的挂单</h2><span className="count-badge">{orders.length}</span></header>
            <div className="desktop-table-wrap"><table className="data-table"><thead><tr><th scope="col">方向</th><th scope="col">单价</th><th scope="col">可成交 / 总量</th><th scope="col">处理中 / 已成交</th><th scope="col">状态</th><th scope="col">更新</th><th scope="col"><span className="visually-hidden">操作</span></th></tr></thead><tbody>{orders.map((order) => <tr key={order.id}>
              <td><strong>{c2cSideLabels[order.side]}</strong></td><td>{formatC2CPrice(order.unit_price_fen)}</td><td>{formatPointAmount(order.available)} / {formatPointAmount(order.total)}</td><td>{formatPointAmount(order.allocated)} / {formatPointAmount(order.settled)}</td><td><C2CState label={c2cOrderStatusLabels[order.status]} tone={c2cStatusTone(order.status)} /></td><td>{formatC2CDate(order.updated_at)}</td><td className="table-action"><div className="table-action-group">{(order.status === 'open' || order.status === 'allocated') && <Button disabled={actingID === order.id} onClick={() => void cancelOrder(order)} variant="danger">取消</Button>}</div></td>
            </tr>)}</tbody></table></div>
            {orders.length === 0 ? <div className="empty-state">暂无挂单</div> : <div className="mobile-card-list">{orders.map((order) => <article className="mobile-data-card" key={order.id}><header><div><strong>{c2cSideLabels[order.side]} · {formatC2CPrice(order.unit_price_fen)}</strong><span>{formatPointAmount(order.available)} / {formatPointAmount(order.total)} 积分</span></div><C2CState label={c2cOrderStatusLabels[order.status]} tone={c2cStatusTone(order.status)} /></header>{(order.status === 'open' || order.status === 'allocated') && <Button disabled={actingID === order.id} onClick={() => void cancelOrder(order)} variant="danger">取消挂单</Button>}</article>)}</div>}
          </section>

          <section className="panel table-panel c2c-activity-panel">
            <header className="table-toolbar"><h2>我的交易</h2><span className="count-badge">{trades.length}</span></header>
            <div className="desktop-table-wrap"><table className="data-table"><thead><tr><th scope="col">交易</th><th scope="col">对方</th><th scope="col">数量</th><th scope="col">人民币</th><th scope="col">状态</th><th scope="col">更新</th><th scope="col"><span className="visually-hidden">操作</span></th></tr></thead><tbody>{trades.map((trade) => {
              const buying = trade.buyer_account_id === account?.id
              return <tr key={trade.id}><td><strong>{buying ? '购买' : '出售'}</strong><small>{trade.id.slice(0, 8)}</small></td><td>{buying ? trade.seller_display_name : trade.buyer_display_name}</td><td>{formatPointAmount(trade.quantity)} 积分</td><td>{formatC2CFiat(trade.fiat_amount_fen)}</td><td><C2CState label={c2cTradeStatusLabels[trade.status]} tone={c2cStatusTone(trade.status)} /></td><td>{formatC2CDate(trade.updated_at)}</td><td className="table-action"><Link className="button button-secondary" to={`/c2c/trades/${trade.id}`}>查看</Link></td></tr>
            })}</tbody></table></div>
            {trades.length === 0 ? <div className="empty-state">暂无交易</div> : <div className="mobile-card-list">{trades.map((trade) => {
              const buying = trade.buyer_account_id === account?.id
              return <article className="mobile-data-card" key={trade.id}><header><div><strong>{buying ? '购买' : '出售'} {formatPointAmount(trade.quantity)} 积分</strong><span>{buying ? trade.seller_display_name : trade.buyer_display_name} · {formatC2CFiat(trade.fiat_amount_fen)}</span></div><C2CState label={c2cTradeStatusLabels[trade.status]} tone={c2cStatusTone(trade.status)} /></header><Link className="button button-secondary" to={`/c2c/trades/${trade.id}`}>交易详情</Link></article>
            })}</div>}
          </section>
        </>
      )}
    </AppShell>
  )
}

export function C2CState({ label, tone }: { label: string; tone: string }) {
  return <span className={`c2c-state c2c-state-${tone}`}>{label}</span>
}
