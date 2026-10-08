import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import type { C2COrder } from '../api/types'
import { errorMessage } from '../api/query'
import { useAuth } from '../auth/AuthProvider'
import { formatPointAmount } from '../wallet/presentation'
import { Button, ButtonLink, Drawer, InlineError, Notice, QueryBoundary, TextField } from '../ui'
import {
  c2cFiatFen,
  c2cPaymentLabels,
  c2cSideLabels,
  c2cTakeLabel,
  c2cTakeQuantityError,
  formatC2CFiat,
  formatC2CPrice,
} from './presentation'
import { useC2COrderQuery, useTakeC2COrder } from './queries'

const formID = 'c2c-take-form'

function fiatText(order: C2COrder, quantity: string) {
  try {
    return formatC2CFiat(c2cFiatFen(quantity, order.unit_price_fen))
  } catch {
    return '—'
  }
}

type Take = ReturnType<typeof useTakeC2COrder>

function TakeForm({ order, take, onClose }: { order: C2COrder; take: Take; onClose: () => void }) {
  const navigate = useNavigate()
  const [quantity, setQuantity] = useState(order.minimum)
  const [paymentMethodID, setPaymentMethodID] = useState(order.payment_methods[0]?.id ?? '')
  const [touched, setTouched] = useState(false)
  const sellOrder = order.side === 'sell'
  const quantityError = c2cTakeQuantityError(order, quantity)

  const submit = (event: FormEvent) => {
    event.preventDefault()
    setTouched(true)
    if (quantityError || !paymentMethodID) return
    take.mutate(
      { orderID: order.id, quantity, paymentMethodID },
      {
        onSuccess: (trade) => {
          onClose()
          navigate(`/c2c/trades/${trade.id}`)
        },
      },
    )
  }

  return (
    <form className="c2c-take-form" id={formID} onSubmit={submit}>
      <dl className="detail-list c2c-take-facts">
        <div><dt>用户</dt><dd>{order.owner_display_name}</dd></div>
        <div><dt>单价</dt><dd>{formatC2CPrice(order.unit_price_fen)} / 积分</dd></div>
        <div><dt>可成交</dt><dd>{formatPointAmount(order.available)} 积分</dd></div>
      </dl>
      <TextField
        error={touched ? quantityError : ''}
        hint={`单次 ${formatPointAmount(order.minimum)} – ${formatPointAmount(order.maximum)}`}
        inputMode="decimal"
        label="积分数量"
        onChange={(event) => setQuantity(event.target.value)}
        required
        value={quantity}
      />
      <fieldset className="c2c-choice-group">
        <legend>{sellOrder ? '收款方式' : '买家联系方式'}</legend>
        {order.payment_methods.map((method) => (
          <label
            className={paymentMethodID === method.id ? 'c2c-choice c2c-choice-active' : 'c2c-choice'}
            key={method.id}
          >
            <input
              checked={paymentMethodID === method.id}
              name="payment-method"
              onChange={() => setPaymentMethodID(method.id)}
              type="radio"
            />
            <span className="c2c-choice-copy">
              <strong>{c2cPaymentLabels[method.type]}</strong>
              {method.contact && <small>{method.contact}</small>}
              {method.instructions && <small>{method.instructions}</small>}
            </span>
            {method.qr_available && (
              <img alt={`${c2cPaymentLabels[method.type]}收款码`} className="c2c-qr-thumb" src={method.qr_url} />
            )}
          </label>
        ))}
      </fieldset>
      <div className="c2c-take-total">
        <span>{sellOrder ? '应付' : '应收'}</span>
        <strong className="num">{fiatText(order, quantity)}</strong>
      </div>
      {!sellOrder && (
        <Notice tone="warning">承接后将冻结你的 {quantity || '0'} 积分，直至交易结束。</Notice>
      )}
      <InlineError>{take.isError ? errorMessage(take.error, '成交创建失败') : ''}</InlineError>
    </form>
  )
}

function TakeContent({ order, take, onClose }: { order: C2COrder; take: Take; onClose: () => void }) {
  const { account } = useAuth()
  if (order.owner_account_id === account?.id) {
    return (
      <Notice action={<ButtonLink size="sm" to="/c2c/me">管理</ButtonLink>} tone="info">
        这是你的{c2cSideLabels[order.side]}
      </Notice>
    )
  }
  if (!order.takeable) return <Notice tone="warning">该挂单暂不可承接</Notice>
  return <TakeForm key={order.id} onClose={onClose} order={order} take={take} />
}

/** 承接挂单抽屉：由市场页的 ?take=<orderID> 驱动，旧的 /c2c/orders/:id/take 路由重定向至此。 */
export function TakeOrderDrawer({ orderID, onClose }: { orderID: string; onClose: () => void }) {
  const { account } = useAuth()
  const query = useC2COrderQuery(orderID)
  const take = useTakeC2COrder()
  const order = query.data
  const close = () => {
    take.reset()
    onClose()
  }
  const takeable = Boolean(order && order.takeable && order.owner_account_id !== account?.id)
  return (
    <Drawer
      busy={take.isPending}
      footer={
        takeable ? (
          <>
            <Button disabled={take.isPending} onClick={close} type="button" variant="secondary">
              取消
            </Button>
            <Button form={formID} loading={take.isPending} type="submit">
              确认{order ? c2cTakeLabel(order.side) : '成交'}
            </Button>
          </>
        ) : undefined
      }
      onClose={close}
      open={Boolean(orderID)}
      title={order ? `${c2cTakeLabel(order.side)}积分` : '承接挂单'}
    >
      <QueryBoundary errorFallback="挂单加载失败" query={query}>
        {(data) => <TakeContent onClose={close} order={data} take={take} />}
      </QueryBoundary>
    </Drawer>
  )
}
