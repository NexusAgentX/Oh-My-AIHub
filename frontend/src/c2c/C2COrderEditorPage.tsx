import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import type { C2CPaymentMethodType } from '../api/types'
import { errorMessage } from '../api/query'
import {
  Button,
  ButtonLink,
  Card,
  Icon,
  InlineError,
  Notice,
  PageHeader,
  SelectField,
  TextareaField,
  TextField,
} from '../ui'
import { c2cPaymentLabels, parseC2CPriceFen, validateOrderDraft } from './presentation'
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
  const create = useCreateC2COrder()
  const [price, setPrice] = useState('1.00')
  const [total, setTotal] = useState('100')
  const [minimum, setMinimum] = useState('10')
  const [maximum, setMaximum] = useState('100')
  const [methods, setMethods] = useState<PaymentDraft[]>([emptyMethod()])
  const [formError, setFormError] = useState('')

  const updateMethod = (id: string, update: Partial<PaymentDraft>) => {
    setMethods((current) => current.map((method) => (method.id === id ? { ...method, ...update } : method)))
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const problem = validateOrderDraft({ price, total, minimum, maximum, methods })
    setFormError(problem)
    if (problem) return
    create.mutate(
      {
        unit_price_fen: parseC2CPriceFen(price),
        total,
        minimum,
        maximum,
        payment_methods: methods.map(({ type, contact, instructions, qr }) => ({ type, contact, instructions, qr })),
      },
      { onSuccess: () => navigate('/c2c/me', { replace: true }) },
    )
  }

  const error = formError || (create.isError ? errorMessage(create.error, '挂单发布失败') : '')

  return (
    <>
      <PageHeader
        back={<ButtonLink size="sm" to="/c2c" variant="quiet">← C2C 市场</ButtonLink>}
        title="发布卖单"
      />
      <form className="c2c-editor" onSubmit={submit}>
        <Card title="价格与数量">
          <div className="c2c-editor-grid">
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
          title="收款方式"
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
                    label="收款账号或联系方式"
                    onChange={(event) => updateMethod(method.id, { contact: event.target.value })}
                    value={method.contact}
                  />
                </div>
                <TextareaField
                  label="备注"
                  maxLength={1000}
                  onChange={(event) => updateMethod(method.id, { instructions: event.target.value })}
                  value={method.instructions}
                />
                <label className="field">
                  <span className="field-label">收款码（可选，JPG / PNG）</span>
                  <input
                    accept="image/jpeg,image/png"
                    className="input c2c-file-input"
                    onChange={(event) => updateMethod(method.id, { qr: event.target.files?.[0] ?? null })}
                    type="file"
                  />
                </label>
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

        <Notice tone="info">发布卖单后立即冻结 {total || '0'} 积分，取消时解冻未成交部分。</Notice>
        <InlineError>{error}</InlineError>
        <div className="c2c-editor-actions">
          <ButtonLink to="/c2c">取消</ButtonLink>
          <Button loading={create.isPending} type="submit">发布卖单</Button>
        </div>
      </form>
    </>
  )
}
