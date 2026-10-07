import { useState, type FormEvent } from 'react'
import { createPortal } from 'react-dom'
import {
  Button,
  Checkbox,
  Drawer,
  Icon,
  IconButton,
  InlineError,
  TextField,
} from '../ui'
import {
  emptyTierForm,
  moveTier,
  setTierCondition,
  tierConditionSummary,
  toggleWeekday,
  validateTierForm,
  weekdayLabel,
  type ModelForm,
  type TierForm,
} from './modelForm'

const maxTiers = 16

const priceFields = [
  { key: 'inputPrice', label: '输入' },
  { key: 'outputPrice', label: '输出' },
  { key: 'cacheWritePrice', label: '缓存写' },
  { key: 'cacheReadPrice', label: '缓存读' },
] as const

export function PriceField({
  label,
  value,
  error,
  onChange,
}: {
  label: string
  value: string
  error?: string
  onChange: (value: string) => void
}) {
  return (
    <TextField
      error={error}
      inputMode="decimal"
      label={label}
      onChange={(event) => onChange(event.target.value)}
      placeholder="0"
      required
      value={value}
    />
  )
}

type Editing = { index: number | null; tier: TierForm }

/**
 * 条件档位：紧凑列表 + 抽屉编辑。
 * 列表只显示条件摘要与四价，顺序即优先级（按序首个命中，整单按该档计价）；
 * 条件与价格在抽屉里按需展开，只出现已开启的条件字段。
 */
export function TierEditor({
  form,
  error,
  onChange,
}: {
  form: ModelForm
  error?: string
  onChange: (tiers: TierForm[]) => void
}) {
  const tiers = form.tiers
  const [editing, setEditing] = useState<Editing | null>(null)

  const apply = (tier: TierForm) => {
    if (!editing) return
    onChange(
      editing.index === null
        ? [...tiers, tier]
        : tiers.map((item, position) => (position === editing.index ? tier : item)),
    )
    setEditing(null)
  }

  return (
    <section aria-label="条件档位" className="tier-editor">
      <header className="tier-editor-heading">
        <div>
          <h3>条件档位</h3>
          <p>按序首个命中，整单按该档计价；都不命中时使用默认档。</p>
        </div>
        <Button
          disabled={tiers.length >= maxTiers}
          icon={<Icon name="plus" />}
          onClick={() => setEditing({ index: null, tier: { ...emptyTierForm } })}
          size="sm"
          type="button"
          variant="secondary"
        >
          添加档位
        </Button>
      </header>
      <InlineError>{error}</InlineError>
      {tiers.length === 0 ? (
        <p className="tier-empty">没有条件档，所有请求按默认档计价。</p>
      ) : (
        <ol className="tier-list">
          {tiers.map((tier, index) => (
            <li className="tier-row" key={index}>
              <span aria-hidden="true" className="tier-order">
                {index + 1}
              </span>
              <div className="tier-row-main">
                <strong>{tier.name.trim() || `档位 ${index + 1}`}</strong>
                <span>{tierConditionSummary(tier)}</span>
              </div>
              <dl className="tier-prices">
                {priceFields.map((field) => (
                  <div key={field.key}>
                    <dt>{field.label}</dt>
                    <dd className="num">{tier[field.key]}</dd>
                  </div>
                ))}
              </dl>
              <div className="tier-row-actions">
                <IconButton
                  disabled={index === 0}
                  icon={<span aria-hidden="true">↑</span>}
                  label={`上移档位 ${index + 1}`}
                  onClick={() => onChange(moveTier(tiers, index, -1))}
                />
                <IconButton
                  disabled={index === tiers.length - 1}
                  icon={<span aria-hidden="true">↓</span>}
                  label={`下移档位 ${index + 1}`}
                  onClick={() => onChange(moveTier(tiers, index, 1))}
                />
                <Button onClick={() => setEditing({ index, tier })} size="sm" type="button" variant="secondary">
                  编辑
                </Button>
                <Button
                  onClick={() => onChange(tiers.filter((_, position) => position !== index))}
                  size="sm"
                  type="button"
                  variant="quiet"
                >
                  删除
                </Button>
              </div>
            </li>
          ))}
          <li className="tier-row tier-row-default">
            <span aria-hidden="true" className="tier-order">
              ∞
            </span>
            <div className="tier-row-main">
              <strong>默认档</strong>
              <span>无命中时适用</span>
            </div>
            <dl className="tier-prices">
              {priceFields.map((field) => (
                <div key={field.key}>
                  <dt>{field.label}</dt>
                  <dd className="num">{form[field.key]}</dd>
                </div>
              ))}
            </dl>
          </li>
        </ol>
      )}
      {/* 抽屉里有自己的 <form>，必须脱离外层模型表单的 DOM 嵌套 */}
      {createPortal(
        <TierDrawer
          basePrices={form}
          editing={editing}
          key={editing ? String(editing.index) : 'closed'}
          onApply={apply}
          onClose={() => setEditing(null)}
        />,
        document.body,
      )}
    </section>
  )
}

function TierDrawer({
  editing,
  basePrices,
  onApply,
  onClose,
}: {
  editing: Editing | null
  basePrices: Pick<ModelForm, 'inputPrice' | 'outputPrice' | 'cacheWritePrice' | 'cacheReadPrice'>
  onApply: (tier: TierForm) => void
  onClose: () => void
}) {
  const [draft, setDraft] = useState<TierForm>(editing?.tier ?? emptyTierForm)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const patch = (value: Partial<TierForm>) => setDraft((current) => ({ ...current, ...value }))

  const submit = (event: FormEvent) => {
    event.preventDefault()
    event.stopPropagation()
    const found = validateTierForm(draft)
    setErrors(found)
    if (Object.keys(found).length === 0) onApply(draft)
  }

  return (
    <Drawer
      footer={
        <>
          <Button onClick={onClose} type="button" variant="secondary">
            取消
          </Button>
          <Button form="tier-form" type="submit">
            {editing?.index === null ? '添加' : '应用'}
          </Button>
        </>
      }
      onClose={onClose}
      open={editing !== null}
      title={editing?.index === null ? '添加条件档' : `编辑档位 ${(editing?.index ?? 0) + 1}`}
    >
      <form className="tier-form" id="tier-form" onSubmit={submit}>
        <InlineError>{errors['tier.conditions']}</InlineError>
        <TextField
          error={errors['tier.name']}
          label="名称（可选）"
          onChange={(event) => patch({ name: event.target.value })}
          placeholder="例如 长上下文、工作日高峰"
          value={draft.name}
        />

        <fieldset className="tier-condition">
          <Checkbox
            checked={draft.useTokens}
            label="按输入 Token 区间"
            onChange={(event) => setDraft(setTierCondition(draft, 'tokens', event.target.checked))}
          />
          {draft.useTokens && (
            <>
              <div className="field-row">
                <TextField
                  error={errors['tier.minPromptTokens']}
                  inputMode="numeric"
                  label="下界（含）"
                  onChange={(event) => patch({ minPromptTokens: event.target.value })}
                  placeholder="留空不限"
                  value={draft.minPromptTokens}
                />
                <TextField
                  error={errors['tier.maxPromptTokens']}
                  inputMode="numeric"
                  label="上界（不含）"
                  onChange={(event) => patch({ maxPromptTokens: event.target.value })}
                  placeholder="留空不限"
                  value={draft.maxPromptTokens}
                />
              </div>
              <p className="muted-copy">输入 + 缓存写 + 缓存读 tokens 的总量。</p>
            </>
          )}
        </fieldset>

        <fieldset className="tier-condition">
          <Checkbox
            checked={draft.useTime}
            label="按星期与时间窗"
            onChange={(event) => setDraft(setTierCondition(draft, 'time', event.target.checked))}
          />
          {draft.useTime && (
            <>
              <TextField
                error={errors['tier.timezone']}
                label="时区（IANA）"
                onChange={(event) => patch({ timezone: event.target.value })}
                placeholder="Asia/Shanghai"
                value={draft.timezone}
              />
              <div className="choice-field">
                <span className="field-label">星期（不选为每天）</span>
                <div className="choice-pills">
                  {[1, 2, 3, 4, 5, 6, 7].map((weekday) => (
                    <label key={weekday}>
                      <input
                        checked={draft.weekdays.includes(weekday)}
                        onChange={(event) =>
                          setDraft(toggleWeekday(draft, weekday, event.target.checked))
                        }
                        type="checkbox"
                      />
                      <span>{weekdayLabel(weekday)}</span>
                    </label>
                  ))}
                </div>
                {errors['tier.weekdays'] && (
                  <span className="field-message field-error">{errors['tier.weekdays']}</span>
                )}
              </div>
              <Checkbox
                checked={draft.useWindow}
                label="限定时段（可跨午夜）"
                onChange={(event) => patch({ useWindow: event.target.checked })}
              />
              {draft.useWindow && (
                <div className="field-row">
                  <TextField
                    error={errors['tier.startTime']}
                    label="开始 HH:MM"
                    onChange={(event) => patch({ startTime: event.target.value })}
                    placeholder="09:00"
                    value={draft.startTime}
                  />
                  <TextField
                    error={errors['tier.endTime']}
                    label="结束 HH:MM（不含）"
                    onChange={(event) => patch({ endTime: event.target.value })}
                    placeholder="12:00"
                    value={draft.endTime}
                  />
                </div>
              )}
            </>
          )}
        </fieldset>

        <fieldset className="tier-condition">
          <div className="tier-price-heading">
            <legend>命中后价格 · 积分 / 百万 tokens</legend>
            <Button
              onClick={() =>
                patch({
                  inputPrice: basePrices.inputPrice,
                  outputPrice: basePrices.outputPrice,
                  cacheWritePrice: basePrices.cacheWritePrice,
                  cacheReadPrice: basePrices.cacheReadPrice,
                })
              }
              size="sm"
              type="button"
              variant="quiet"
            >
              填入默认价
            </Button>
          </div>
          <div className="field-row">
            {priceFields.map((field) => (
              <PriceField
                error={errors[`tier.${field.key}`]}
                key={field.key}
                label={field.label}
                onChange={(value) => patch({ [field.key]: value })}
                value={draft[field.key]}
              />
            ))}
          </div>
        </fieldset>
      </form>
    </Drawer>
  )
}
