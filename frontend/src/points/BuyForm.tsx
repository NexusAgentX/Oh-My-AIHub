import { useEffect, useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import type { C2COrder, C2CTrade } from '../api/types'
import { formatFen, formatPoints, isPositiveAmount, tradeTotalFen } from '../money/format'
import { Button, InlineError, TextField } from '../ui'
import { validateBuyAmount } from './c2c'
import { newIdempotencyKey, useCreateTrade } from './queries'

export function BuyForm({ order, onCreated, onBusy }: { order: C2COrder; onCreated: (trade: C2CTrade) => void; onBusy: (busy: boolean) => void }) {
  const [amount, setAmount] = useState('')
  const [error, setError] = useState('')
  const [idempotencyKey] = useState(newIdempotencyKey)
  const create = useCreateTrade()
  useEffect(() => onBusy(create.isPending), [create.isPending, onBusy])
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

