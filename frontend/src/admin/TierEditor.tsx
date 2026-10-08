import { useState } from 'react'
import { Button, Checkbox, IconButton, Icon, InlineError, TextField } from '../ui'
import {
  emptyTier,
  formToTierInput,
  maxPriceTiers,
  moveTier,
  tierConditionSummary,
  tierToForm,
  toggleWeekday,
  validateTier,
  weekdayLabel,
  type PricesForm,
  type TierErrors,
  type TierForm,
} from './modelForm'
import type { PriceTier, PriceTierInput } from './types'

export function PriceFields({
  prices,
  onChange,
  error,
  legend,
}: {
  prices: PricesForm
  onChange: (prices: PricesForm) => void
  error?: string
  legend: string
}) {
  const field = (key: keyof PricesForm, label: string) => (
    <TextField
      inputMode="decimal"
      label={label}
      onChange={(event) => onChange({ ...prices, [key]: event.target.value })}
      value={prices[key]}
    />
  )
  return (
    <fieldset className="form-section">
      <legend>{legend}</legend>
      <div className="field-row price-grid">
        {field('input', '输入')}
        {field('output', '输出')}
        {field('cache_read', '缓存读')}
        {field('cache_write', '缓存写')}
      </div>
      <InlineError>{error}</InlineError>
    </fieldset>
  )
}

function TierEditForm({
  initial,
  onSave,
  onCancel,
}: {
  initial: TierForm
  onSave: (tier: PriceTierInput) => void
  onCancel: () => void
}) {
  const [form, setForm] = useState(initial)
  const [errors, setErrors] = useState<TierErrors>({})
  const set = (patch: Partial<TierForm>) => setForm((current) => ({ ...current, ...patch }))
  const save = () => {
    const found = validateTier(form)
    setErrors(found)
    if (Object.keys(found).length === 0) onSave(formToTierInput(form))
  }
  return (
    <div className="tier-form" role="group" aria-label="编辑价格档">
      <TextField
        error={errors.name}
        label="档位名称"
        maxLength={64}
        onChange={(event) => set({ name: event.target.value })}
        value={form.name}
      />
      <Checkbox
        checked={form.useTokens}
        label="按输入侧 token 区间（输入 + 缓存写 + 缓存读）"
        onChange={(event) =>
          set(event.target.checked ? { useTokens: true } : { useTokens: false, minPromptTokens: '', maxPromptTokens: '' })
        }
      />
      {form.useTokens && (
        <div className="field-row">
          <TextField
            error={errors.tokens}
            hint="含；留空不限"
            inputMode="numeric"
            label="下限 token"
            onChange={(event) => set({ minPromptTokens: event.target.value })}
            value={form.minPromptTokens}
          />
          <TextField
            hint="不含；留空不限"
            inputMode="numeric"
            label="上限 token"
            onChange={(event) => set({ maxPromptTokens: event.target.value })}
            value={form.maxPromptTokens}
          />
        </div>
      )}
      <Checkbox
        checked={form.useTime}
        label="按星期与时段"
        onChange={(event) =>
          set(
            event.target.checked
              ? { useTime: true }
              : { useTime: false, weekdays: [], useWindow: false, startTime: '', endTime: '' },
          )
        }
      />
      {form.useTime && (
        <div className="stack-form tier-time">
          <TextField
            error={errors.timezone}
            hint="IANA 时区，例如 Asia/Shanghai、UTC"
            label="时区"
            onChange={(event) => set({ timezone: event.target.value })}
            value={form.timezone}
          />
          <fieldset className="weekday-picker">
            <legend>星期（不选为每天）</legend>
            {[1, 2, 3, 4, 5, 6, 7].map((weekday) => (
              <Checkbox
                checked={form.weekdays.includes(weekday)}
                key={weekday}
                label={weekdayLabel(weekday)}
                onChange={(event) => setForm((current) => toggleWeekday(current, weekday, event.target.checked))}
              />
            ))}
          </fieldset>
          <Checkbox
            checked={form.useWindow}
            label="限定时段（结束早于开始表示跨午夜）"
            onChange={(event) => set({ useWindow: event.target.checked })}
          />
          {form.useWindow && (
            <div className="field-row">
              <TextField
                error={errors.time}
                label="开始"
                onChange={(event) => set({ startTime: event.target.value })}
                placeholder="22:00"
                value={form.startTime}
              />
              <TextField
                hint="不含；24:00 表示当天结束"
                label="结束"
                onChange={(event) => set({ endTime: event.target.value })}
                placeholder="06:00"
                value={form.endTime}
              />
            </div>
          )}
        </div>
      )}
      <InlineError>{errors.condition}</InlineError>
      <PriceFields
        error={errors.prices}
        legend="本档单价（积分/百万 token）"
        onChange={(prices) => set({ prices })}
        prices={form.prices}
      />
      <div className="form-actions">
        <Button onClick={onCancel} size="sm" type="button" variant="secondary">
          取消
        </Button>
        <Button onClick={save} size="sm" type="button">
          保存此档
        </Button>
      </div>
    </div>
  )
}

/**
 * 条件价格档紧凑列表：序号、名称、条件摘要、四价；添加 / 编辑 / 删除 / 上下移动。
 * 编辑结果只改动模型表单，随模型一起保存（整组替换）。
 */
export function TierEditor({
  tiers,
  onChange,
  basePrices,
}: {
  tiers: PriceTierInput[]
  onChange: (tiers: PriceTierInput[]) => void
  basePrices: PricesForm
}) {
  const [editing, setEditing] = useState<number | 'new' | null>(null)
  if (editing !== null) {
    const initial =
      editing === 'new' ? emptyTier(basePrices) : tierToForm({ ...tiers[editing], seq: editing + 1 } as PriceTier)
    return (
      <TierEditForm
        initial={initial}
        onCancel={() => setEditing(null)}
        onSave={(tier) => {
          onChange(editing === 'new' ? [...tiers, tier] : tiers.map((item, index) => (index === editing ? tier : item)))
          setEditing(null)
        }}
      />
    )
  }
  const full = tiers.length >= maxPriceTiers
  return (
    <div className="tier-list">
      {tiers.length === 0 ? (
        <p className="muted-copy">无条件价格档，始终按基准价计费。</p>
      ) : (
        <ol>
          {tiers.map((tier, index) => (
            <li className="tier-row" key={`${index}-${tier.name}`}>
              <span className="tier-seq num">{index + 1}</span>
              <div className="tier-copy">
                <strong>{tier.name}</strong>
                <small>{tierConditionSummary(tier)}</small>
                <small className="num">
                  输入 {tier.prices.input} · 输出 {tier.prices.output} · 缓存读 {tier.prices.cache_read} · 缓存写{' '}
                  {tier.prices.cache_write}
                </small>
              </div>
              <div className="tier-actions">
                <IconButton
                  disabled={index === 0}
                  icon={<Icon name="arrow-left" />}
                  className="rotate-up"
                  label={`上移 ${tier.name}`}
                  onClick={() => onChange(moveTier(tiers, index, -1))}
                />
                <IconButton
                  disabled={index === tiers.length - 1}
                  icon={<Icon name="arrow-left" />}
                  className="rotate-down"
                  label={`下移 ${tier.name}`}
                  onClick={() => onChange(moveTier(tiers, index, 1))}
                />
                <Button onClick={() => setEditing(index)} size="sm" type="button" variant="quiet">
                  编辑
                </Button>
                <Button
                  onClick={() => onChange(tiers.filter((_, item) => item !== index))}
                  size="sm"
                  type="button"
                  variant="quiet"
                >
                  删除
                </Button>
              </div>
            </li>
          ))}
        </ol>
      )}
      <div className="tier-footer">
        <Button
          disabled={full}
          icon={<Icon name="plus" />}
          onClick={() => setEditing('new')}
          size="sm"
          type="button"
          variant="secondary"
        >
          添加价格档
        </Button>
        <span className="muted-copy">
          {tiers.length} / {maxPriceTiers}，按顺序首个命中
        </span>
      </div>
    </div>
  )
}
