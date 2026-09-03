import { useState, type FormEvent } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, ApiError } from '../api/client'
import type { C2CPaymentMethodType, C2CSide } from '../api/contracts'
import { AppShell } from '../layouts/AppShell'
import { parseNanoPoints } from '../money/amount'
import { Button, InlineError, TextField } from '../ui/FormControls'
import { Icon } from '../ui/Icon'
import { c2cPaymentLabels, c2cSideLabels, parseC2CPriceFen } from './presentation'

type PaymentDraft = {
  id: string
  type: C2CPaymentMethodType
  contact: string
  instructions: string
  qr: File | null
}

function emptyPaymentMethod(): PaymentDraft {
  return { id: crypto.randomUUID(), type: 'wechat', contact: '', instructions: '', qr: null }
}

export function C2COrderEditorPage() {
  const navigate = useNavigate()
  const [search] = useSearchParams()
  const [side, setSide] = useState<C2CSide>(search.get('side') === 'buy' ? 'buy' : 'sell')
  const [price, setPrice] = useState('1.00')
  const [total, setTotal] = useState('100')
  const [minimum, setMinimum] = useState('10')
  const [maximum, setMaximum] = useState('100')
  const [methods, setMethods] = useState<PaymentDraft[]>([emptyPaymentMethod()])
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const switchSide = (next: C2CSide) => {
    setSide(next)
    if (next === 'buy') {
      setMethods((current) => current.map((method) => ({ ...method, qr: null })))
    }
  }

  const updateMethod = (id: string, update: Partial<PaymentDraft>) => {
    setMethods((current) => current.map((method) => method.id === id ? { ...method, ...update } : method))
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    let unitPriceFen: number
    try {
      unitPriceFen = parseC2CPriceFen(price)
      const totalNano = parseNanoPoints(total)
      const minimumNano = parseNanoPoints(minimum)
      const maximumNano = parseNanoPoints(maximum)
      if (totalNano <= 0n || minimumNano <= 0n || maximumNano < minimumNano || maximumNano > totalNano) {
        throw new Error('invalid amount range')
      }
      if (methods.some((method) => side === 'buy' ? !method.contact.trim() : !method.contact.trim() && !method.instructions.trim() && !method.qr)) {
        throw new Error('invalid payment method')
      }
    } catch {
      setError('请检查单价、数量范围和支付方式')
      return
    }
    setSubmitting(true)
    try {
      await api.createC2COrder({
        side, unit_price_fen: unitPriceFen, total, minimum, maximum,
        payment_methods: methods.map(({ type, contact, instructions, qr }) => ({ type, contact, instructions, qr })),
      })
      navigate('/c2c/me', { replace: true })
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : '挂单发布失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AppShell>
      <header className="page-heading c2c-page-heading">
        <div><Link className="back-link" to="/c2c">← C2C 市场</Link><h1>发布挂单</h1></div>
      </header>
      <InlineError>{error}</InlineError>
      <form className="c2c-order-editor" onSubmit={submit}>
        <section className="panel c2c-form-panel">
          <header className="panel-heading"><h2>交易方向</h2></header>
          <div className="c2c-form-section">
            <div aria-label="交易方向" className="c2c-side-selector" role="radiogroup">
              {(['sell', 'buy'] as C2CSide[]).map((value) => (
                <label className={side === value ? 'c2c-side-option c2c-side-option-active' : 'c2c-side-option'} key={value}>
                  <input checked={side === value} name="side" onChange={() => switchSide(value)} type="radio" value={value} />
                  <strong>发布{c2cSideLabels[value]}</strong>
                  <span>{value === 'sell' ? '出售积分' : '购买积分'}</span>
                </label>
              ))}
            </div>
          </div>
        </section>

        <section className="panel c2c-form-panel">
          <header className="panel-heading"><h2>价格与数量</h2></header>
          <div className="c2c-form-section field-row">
            <TextField inputMode="decimal" label="单价（人民币 / 积分）" onChange={(event) => setPrice(event.target.value)} required value={price} />
            <TextField inputMode="decimal" label="挂单数量（积分）" onChange={(event) => setTotal(event.target.value)} required value={total} />
            <TextField inputMode="decimal" label="单次最少" onChange={(event) => setMinimum(event.target.value)} required value={minimum} />
            <TextField inputMode="decimal" label="单次最多" onChange={(event) => setMaximum(event.target.value)} required value={maximum} />
          </div>
        </section>

        <section className="panel c2c-form-panel">
          <header className="panel-heading">
            <h2>{side === 'sell' ? '收款方式' : '联系方式'}</h2>
            <Button disabled={methods.length >= 5} onClick={() => setMethods((current) => [...current, emptyPaymentMethod()])} type="button" variant="secondary"><Icon name="plus" />添加</Button>
          </header>
          <div className="c2c-payment-editor-list">
            {methods.map((method, index) => (
              <article className="c2c-payment-editor" key={method.id}>
                <header><strong>方式 {index + 1}</strong>{methods.length > 1 && <Button onClick={() => setMethods((current) => current.filter((item) => item.id !== method.id))} type="button" variant="quiet">移除</Button>}</header>
                <div className="field-row">
                  <label className="field"><span className="field-label">类型</span><select className="input" onChange={(event) => updateMethod(method.id, { type: event.target.value as C2CPaymentMethodType })} value={method.type}>{Object.entries(c2cPaymentLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
                  <TextField label={side === 'sell' ? '收款账号或联系方式' : '联系方式'} onChange={(event) => updateMethod(method.id, { contact: event.target.value })} required={side === 'buy'} value={method.contact} />
                </div>
                <label className="field"><span className="field-label">备注</span><textarea className="input textarea-input" maxLength={1000} onChange={(event) => updateMethod(method.id, { instructions: event.target.value })} value={method.instructions} /></label>
                {side === 'sell' && <label className="field"><span className="field-label">收款码（可选）</span><input accept="image/jpeg,image/png" className="input c2c-file-input" onChange={(event) => updateMethod(method.id, { qr: event.target.files?.[0] ?? null })} type="file" /></label>}
              </article>
            ))}
          </div>
        </section>

        <div className="c2c-editor-actions">
          {side === 'sell' && <span>发布后冻结 {total || '0'} 积分</span>}
          <div><Link className="button button-secondary" to="/c2c">取消</Link><Button disabled={submitting} type="submit">{submitting ? '正在发布' : '发布挂单'}</Button></div>
        </div>
      </form>
    </AppShell>
  )
}
