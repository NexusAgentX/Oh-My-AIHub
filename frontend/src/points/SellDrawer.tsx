import { useState, type FormEvent } from 'react'
import { errorMessage } from '../api/query'
import { formatFen, formatPoints, parseYuanToFen } from '../money/format'
import { Button, Drawer, Icon, IconButton, InlineError, SuccessMessage, TextField } from '../ui'
import { emptySellForm, maxPaymentMethods, maxSellable, sellRequest, validateSell, type SellForm } from './c2c'
import { newIdempotencyKey, useCreateOrder, usePoints } from './queries'

function SellForm({ onDone }: { onDone: () => void }) {
  const points = usePoints()
  const balance = points.data?.balance ?? '0'
  const max = maxSellable(balance)
  const [form, setForm] = useState<SellForm>(emptySellForm)
  const [confirming, setConfirming] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState(false)
  const [idempotencyKey] = useState(newIdempotencyKey)
  const create = useCreateOrder()
  const set = (patch: Partial<SellForm>) => {
    setConfirming(false)
    setForm({ ...form, ...patch })
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const problem = validateSell(form, balance)
    setError(problem)
    if (problem) return
    if (!confirming) {
      setConfirming(true)
      return
    }
    create.mutate(
      { body: sellRequest(form), idempotencyKey },
      {
        onSuccess: () => setDone(true),
        onError: (caught) => setError(errorMessage(caught, '发布失败，请重试')),
      },
    )
  }

  if (done) {
    return (
      <div className="stack-form">
        <SuccessMessage>卖单已发布</SuccessMessage>
        <div className="form-actions">
          <Button onClick={onDone} type="button">
            完成
          </Button>
        </div>
      </div>
    )
  }

  const fen = parseYuanToFen(form.unitPrice)
  return (
    <form className="stack-form" noValidate onSubmit={submit}>
      <TextField
        hint={`可卖 ${formatPoints(max)}（只能卖正余额）`}
        inputMode="decimal"
        label="数量（积分）"
        onChange={(event) => set({ amount: event.target.value.trim() })}
        value={form.amount}
      />
      <TextField inputMode="decimal" label="单价（元 / 积分）" onChange={(event) => set({ unitPrice: event.target.value.trim() })} placeholder="0.92" value={form.unitPrice} />
      <div className="field-row">
        <TextField inputMode="decimal" label="单笔最少" onChange={(event) => set({ minPerTrade: event.target.value.trim() })} value={form.minPerTrade} />
        <TextField inputMode="decimal" label="单笔最多" onChange={(event) => set({ maxPerTrade: event.target.value.trim() })} placeholder="不限" value={form.maxPerTrade} />
      </div>
      <fieldset className="form-section">
        <legend>收款方式</legend>
        {form.methods.map((method, index) => (
          // 收款方式没有独立 ID，按位置作为 key
          <div className="method-row" key={index}>
            <input
              aria-label="方式"
              className="input"
              onChange={(event) => set({ methods: form.methods.map((item, position) => (position === index ? { ...item, channel: event.target.value } : item)) })}
              placeholder="支付宝"
              value={method.channel}
            />
            <input
              aria-label="账号"
              className="input"
              onChange={(event) => set({ methods: form.methods.map((item, position) => (position === index ? { ...item, account: event.target.value } : item)) })}
              placeholder="账号与姓名"
              value={method.account}
            />
            <IconButton
              disabled={form.methods.length === 1}
              icon={<Icon name="x" />}
              label="删除收款方式"
              onClick={() => set({ methods: form.methods.filter((_, position) => position !== index) })}
            />
          </div>
        ))}
        <div>
          <Button
            disabled={form.methods.length >= maxPaymentMethods}
            icon={<Icon name="plus" />}
            onClick={() => set({ methods: [...form.methods, { channel: '', account: '' }] })}
            size="sm"
            type="button"
            variant="secondary"
          >
            添加收款方式
          </Button>
        </div>
      </fieldset>
      {confirming && (
        <div className="notice notice-warning sell-confirm" role="status">
          挂出 {formatPoints(form.amount)} 积分，单价 {fen ? formatFen(fen) : '—'}；积分将转入托管，成交前可关闭卖单取回
        </div>
      )}
      <InlineError>{error}</InlineError>
      <div className="form-actions">
        {confirming && (
          <Button onClick={() => setConfirming(false)} type="button" variant="secondary">
            返回修改
          </Button>
        )}
        <Button loading={create.isPending}>{confirming ? '确认发布' : '发布'}</Button>
      </div>
    </form>
  )
}

/** 卖积分右侧抽屉。 */
export function SellDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Drawer onClose={onClose} open={open} title="卖积分">
      {open && <SellForm onDone={onClose} />}
    </Drawer>
  )
}
