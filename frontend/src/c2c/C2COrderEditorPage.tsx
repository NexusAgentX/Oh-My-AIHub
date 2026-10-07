import { useState, type FormEvent } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import type { C2CPaymentMethodType, C2CSide } from '../api/contracts'
import { errorMessage } from '../api/query'
import {
  Button,
  ButtonLink,
  Card,
  Icon,
  InlineError,
  Notice,
  PageHeader,
  Segmented,
  SelectField,
  TextareaField,
  TextField,
} from '../ui'
import { c2cPaymentLabels, c2cSideLabels, parseC2CPriceFen, validateOrderDraft } from './presentation'
import { useCreateC2COrder } from './queries'

type PaymentDraft = {
  id: string
  type: C2CPaymentMethodType
  contact: string
  instructions: string
  qr: File | null
}

const maxMethods = 5

function emptyMethod(): PaymentDraft {
  return { id: crypto.randomUUID(), type: 'wechat', contact: '', instructions: '', qr: null }
}

export function C2COrderEditorPage() {
  const navigate = useNavigate()
  const [search] = useSearchParams()
  const create = useCreateC2COrder()
  const [side, setSide] = useState<C2CSide>(search.get('side') === 'buy' ? 'buy' : 'sell')
  const [price, setPrice] = useState('1.00')
  const [total, setTotal] = useState('100')
  const [minimum, setMinimum] = useState('10')
  const [maximum, setMaximum] = useState('100')
  const [methods, setMethods] = useState<PaymentDraft[]>([emptyMethod()])
  const [formError, setFormError] = useState('')

  const switchSide = (next: C2CSide) => {
    setSide(next)
    // 买单只提供联系方式，不上传收款码。
    if (next === 'buy') setMethods((current) => current.map((method) => ({ ...method, qr: null })))
  }

  const updateMethod = (id: string, update: Partial<PaymentDraft>) => {
    setMethods((current) => current.map((method) => (method.id === id ? { ...method, ...update } : method)))
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const problem = validateOrderDraft({ side, price, total, minimum, maximum, methods })
    setFormError(problem)
    if (problem) return
    create.mutate(
      {
        side,
        unit_price_fen: parseC2CPriceFen(price),
        total,
        minimum,
        maximum,
        payment_methods: methods.map(({ type, contact, instructions, qr }) => ({ type, contact, instructions, qr })),
      },
      { onSuccess: () => navigate('/c2c/me', { replace: true }) },
    )
  }

  const sell = side === 'sell'
  const error = formError || (create.isError ? errorMessage(create.error, '挂单发布失败') : '')

  return (
    <>
      <PageHeader
        back={<ButtonLink size="sm" to="/c2c" variant="quiet">← C2C 市场</ButtonLink>}
        title="发布挂单"
      />
      <form className="c2c-editor" onSubmit={submit}>
        <Card title="方向与价格">
          <div className="c2c-editor-grid">
            <div className="c2c-editor-side">
              <span className="field-label">交易方向</span>
              <Segmented
                label="交易方向"
                onChange={switchSide}
                options={[
                  { key: 'sell', label: '卖单 · 出售积分' },
                  { key: 'buy', label: '买单 · 购买积分' },
                ]}
                value={side}
              />
            </div>
            <TextField inputMode="decimal" label="单价（人民币 / 积分）" onChange={(event) => setPrice(event.target.value)} required value={price} />
            <TextField inputMode="decimal" label="挂单数量（积分）" onChange={(event) => setTotal(event.target.value)} required value={total} />
            <TextField inputMode="decimal" label="单次最少" onChange={(event) => setMinimum(event.target.value)} required value={minimum} />
            <TextField inputMode="decimal" label="单次最多" onChange={(event) => setMaximum(event.target.value)} required value={maximum} />
          </div>
        </Card>

        <Card
          actions={
            <Button
              disabled={methods.length >= maxMethods}
              icon={<Icon name="plus" />}
              onClick={() => setMethods((current) => [...current, emptyMethod()])}
              size="sm"
              type="button"
              variant="secondary"
            >
              添加
            </Button>
          }
          title={sell ? '收款方式' : '联系方式'}
        >
          <div className="c2c-methods">
            {methods.map((method, index) => (
              <fieldset className="c2c-method" key={method.id}>
                <legend>方式 {index + 1}</legend>
                <div className="c2c-editor-grid">
                  <SelectField
                    label="类型"
                    onChange={(event) => updateMethod(method.id, { type: event.target.value as C2CPaymentMethodType })}
                    value={method.type}
                  >
                    {Object.entries(c2cPaymentLabels).map(([value, label]) => (
                      <option key={value} value={value}>{label}</option>
                    ))}
                  </SelectField>
                  <TextField
                    label={sell ? '收款账号或联系方式' : '联系方式'}
                    onChange={(event) => updateMethod(method.id, { contact: event.target.value })}
                    required={!sell}
                    value={method.contact}
                  />
                </div>
                <TextareaField
                  label="备注"
                  maxLength={1000}
                  onChange={(event) => updateMethod(method.id, { instructions: event.target.value })}
                  value={method.instructions}
                />
                {sell && (
                  <label className="field">
                    <span className="field-label">收款码（可选，JPG / PNG）</span>
                    <input
                      accept="image/jpeg,image/png"
                      className="input c2c-file-input"
                      onChange={(event) => updateMethod(method.id, { qr: event.target.files?.[0] ?? null })}
                      type="file"
                    />
                  </label>
                )}
                {methods.length > 1 && (
                  <Button
                    onClick={() => setMethods((current) => current.filter((item) => item.id !== method.id))}
                    size="sm"
                    type="button"
                    variant="quiet"
                  >
                    移除
                  </Button>
                )}
              </fieldset>
            ))}
          </div>
        </Card>

        {sell && <Notice tone="info">发布{c2cSideLabels.sell}后立即冻结 {total || '0'} 积分，取消时解冻未成交部分。</Notice>}
        <InlineError>{error}</InlineError>
        <div className="c2c-editor-actions">
          <ButtonLink to="/c2c">取消</ButtonLink>
          <Button loading={create.isPending} type="submit">发布挂单</Button>
        </div>
      </form>
    </>
  )
}
