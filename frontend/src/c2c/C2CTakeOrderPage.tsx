import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { C2COrder } from '../api/contracts'
import { useAuth } from '../auth/AuthProvider'
import { AppShell } from '../layouts/AppShell'
import { Button, InlineError, LoadingState, TextField } from '../ui/FormControls'
import { formatPointAmount } from '../wallet/presentation'
import { c2cFiatFen, c2cPaymentLabels, c2cSideLabels, formatC2CFiat, formatC2CPrice } from './presentation'

export function C2CTakeOrderPage() {
  const { orderID = '' } = useParams()
  const { account } = useAuth()
  const navigate = useNavigate()
  const [order, setOrder] = useState<C2COrder | null>(null)
  const [quantity, setQuantity] = useState('')
  const [paymentMethodID, setPaymentMethodID] = useState('')
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    setLoading(true)
    void api.c2cOrder(orderID)
      .then((result) => {
        setOrder(result)
        setQuantity(result.minimum)
        setPaymentMethodID(result.payment_methods[0]?.id ?? '')
      })
      .catch((caught) => setError(caught instanceof ApiError ? caught.message : '挂单加载失败'))
      .finally(() => setLoading(false))
  }, [orderID])

  const fiat = useMemo(() => {
    if (!order) return '—'
    try { return formatC2CFiat(c2cFiatFen(quantity, order.unit_price_fen)) } catch { return '—' }
  }, [order, quantity])

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!order || !paymentMethodID) return
    setSubmitting(true)
    setError('')
    try {
      const trade = await api.takeC2COrder(order.id, quantity, paymentMethodID)
      navigate(`/c2c/trades/${trade.id}`, { replace: true })
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '成交创建失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AppShell>
      <header className="page-heading c2c-page-heading"><div><Link className="back-link" to="/c2c">← C2C 市场</Link><h1>确认{order?.side === 'sell' ? '购买' : '出售'}</h1></div></header>
      <InlineError>{error}</InlineError>
      {loading ? <LoadingState /> : order ? (
        order.owner_account_id === account?.id ? (
          <section className="panel upcoming-panel"><h2>这是你的{c2cSideLabels[order.side]}</h2><Link className="button button-primary" to="/c2c/me">管理挂单</Link></section>
        ) : (
          <form className="c2c-take-layout" onSubmit={submit}>
            <section className="panel c2c-take-form">
              <header className="panel-heading"><h2>成交数量</h2></header>
              <div className="c2c-form-section">
                <TextField inputMode="decimal" label="积分数量" onChange={(event) => setQuantity(event.target.value)} required value={quantity} />
                <div className="c2c-amount-range"><span>可成交 {formatPointAmount(order.available)}</span><span>限额 {formatPointAmount(order.minimum)} – {formatPointAmount(order.maximum)}</span></div>
              </div>
              <header className="panel-heading"><h2>{order.side === 'sell' ? '收款方式' : '联系买家'}</h2></header>
              <div className="c2c-payment-choice-list">
                {order.payment_methods.map((method) => (
                  <label className={paymentMethodID === method.id ? 'c2c-payment-choice c2c-payment-choice-active' : 'c2c-payment-choice'} key={method.id}>
                    <input checked={paymentMethodID === method.id} name="payment-method" onChange={() => setPaymentMethodID(method.id)} type="radio" />
                    <span><strong>{c2cPaymentLabels[method.type]}</strong>{method.contact && <small>{method.contact}</small>}{method.instructions && <small>{method.instructions}</small>}</span>
                    {method.qr_available && <img alt={`${c2cPaymentLabels[method.type]}收款码`} src={method.qr_url} />}
                  </label>
                ))}
              </div>
            </section>
            <aside className="panel c2c-trade-summary">
              <header className="panel-heading"><h2>订单</h2></header>
              <dl className="detail-list">
                <div><dt>用户</dt><dd>{order.owner_display_name}</dd></div>
                <div><dt>单价</dt><dd>{formatC2CPrice(order.unit_price_fen)}</dd></div>
                <div><dt>数量</dt><dd>{quantity || '—'} 积分</dd></div>
                <div><dt>应付</dt><dd>{fiat}</dd></div>
              </dl>
              <div className="c2c-summary-actions"><Link className="button button-secondary" to="/c2c">取消</Link><Button disabled={submitting || !paymentMethodID} type="submit">{submitting ? '正在提交' : '确认成交'}</Button></div>
            </aside>
          </form>
        )
      ) : null}
    </AppShell>
  )
}
