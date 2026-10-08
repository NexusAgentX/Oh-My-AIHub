import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { errorMessage } from '../api/query'
import type { C2COrder, C2CTrade } from '../api/types'
import { formatFen, formatPoints, isPositiveAmount, tradeTotalFen } from '../money/format'
import { Button, Drawer, InlineError, TextField } from '../ui'
import { validateBuyAmount } from './c2c'
import { newIdempotencyKey, useC2CTrade, useCreateTrade } from './queries'
import { PaymentCountdown, PaymentInfo, TradeActions, TradeStatusBadge } from './TradeParts'

function BuyForm({ order, onCreated }: { order: C2COrder; onCreated: (trade: C2CTrade) => void }) {
  const [amount, setAmount] = useState('')
  const [error, setError] = useState('')
  const [idempotencyKey] = useState(newIdempotencyKey)
  const create = useCreateTrade()
  const payable = isPositiveAmount(amount) ? tradeTotalFen(amount, order.unit_price_fen) : 0
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const problem = validateBuyAmount(amount.trim(), order)
    setError(problem)
    if (problem) return
    create.mutate(
      { orderId: order.id, amount: amount.trim(), idempotencyKey },
      { onSuccess: onCreated, onError: (caught) => setError(errorMessage(caught, '下单失败，请重试')) },
    )
  }
  return (
    <form className="stack-form" noValidate onSubmit={submit}>
      <dl className="detail-list buy-order">
        <div>
          <dt>卖家</dt>
          <dd>{order.seller.display_name}</dd>
        </div>
        <div>
          <dt>单价</dt>
          <dd className="num">{formatFen(order.unit_price_fen)} / 积分</dd>
        </div>
        <div>
          <dt>可买</dt>
          <dd className="num">{formatPoints(order.available)}</dd>
        </div>
        <div>
          <dt>单笔</dt>
          <dd className="num">
            {formatPoints(order.min_per_trade)} ～ {order.max_per_trade ? formatPoints(order.max_per_trade) : formatPoints(order.available)}
          </dd>
        </div>
        <div>
          <dt>收款方式</dt>
          <dd>{order.payment_channels.join('、') || '—'}</dd>
        </div>
      </dl>
      <TextField
        autoFocus
        inputMode="decimal"
        label="数量（积分）"
        onChange={(event) => setAmount(event.target.value.trim())}
        value={amount}
      />
      <div className="buy-payable" aria-live="polite">
        <span>应付</span>
        <strong className="num">{formatFen(payable)}</strong>
      </div>
      <InlineError>{error}</InlineError>
      <div className="form-actions">
        <Button loading={create.isPending}>确认买入</Button>
      </div>
    </form>
  )
}

/** 下单后的付款面板：收款信息、备注交易号、倒计时、我已付款、取消。 */
function TradePayment({ initial }: { initial: C2CTrade }) {
  const query = useC2CTrade(initial.id)
  const trade = query.data ?? initial
  return (
    <div className="stack-form">
      <div className="trade-head">
        <TradeStatusBadge status={trade.status} />
        {trade.status === 'awaiting_payment' && <PaymentCountdown deadline={trade.payment_deadline} />}
        <Link className="trade-head-link" to={`/points/trades/${trade.id}`}>
          交易详情
        </Link>
      </div>
      <PaymentInfo trade={trade} />
      <TradeActions trade={trade} />
    </div>
  )
}

/** 买积分右侧抽屉。 */
export function BuyDrawer({ order, onClose }: { order: C2COrder | null; onClose: () => void }) {
  const [trade, setTrade] = useState<C2CTrade | null>(null)
  const close = () => {
    setTrade(null)
    onClose()
  }
  return (
    <Drawer onClose={close} open={order !== null} title={trade ? '付款' : '买积分'}>
      {order && !trade && <BuyForm key={order.id} onCreated={setTrade} order={order} />}
      {trade && <TradePayment initial={trade} />}
    </Drawer>
  )
}
