import { useId, type ReactNode } from 'react'
import type {
  ChannelOfferStatus,
  ChannelStatus,
  PriceTier,
  ValidationStatus,
} from '../api/types'
import { Badge, Button, Dialog, InlineError, type BadgeTone } from '../ui'
import { PricePair } from '../gateway/presentation'

function formatTokenBound(value: number): string {
  if (value >= 1_000_000 && value % 1_000_000 === 0) return `${value / 1_000_000}M`
  if (value >= 1_000 && value % 1_000 === 0) return `${value / 1_000}K`
  return value.toLocaleString('zh-CN')
}

function formatWeekdays(weekdays: number[]): string {
  if (weekdays.length === 5 && [1, 2, 3, 4, 5].every((day) => weekdays.includes(day))) return '工作日'
  if (weekdays.length === 2 && weekdays.includes(6) && weekdays.includes(7)) return '周末'
  const labels = ['周一', '周二', '周三', '周四', '周五', '周六', '周日']
  return weekdays.map((day) => labels[day - 1] ?? String(day)).join('/')
}

function formatMinute(value: number): string {
  return `${String(Math.floor(value / 60)).padStart(2, '0')}:${String(value % 60).padStart(2, '0')}`
}

export function tierConditionLabel(tier: PriceTier): string {
  const parts: string[] = []
  if (tier.min_prompt_tokens !== null && tier.max_prompt_tokens !== null) {
    parts.push(`${formatTokenBound(tier.min_prompt_tokens)}–${formatTokenBound(tier.max_prompt_tokens)} tokens`)
  } else if (tier.min_prompt_tokens !== null) {
    parts.push(`≥ ${formatTokenBound(tier.min_prompt_tokens)} tokens`)
  } else if (tier.max_prompt_tokens !== null) {
    parts.push(`< ${formatTokenBound(tier.max_prompt_tokens)} tokens`)
  }
  if (tier.start_minute_of_day !== null && tier.end_minute_of_day !== null) {
    const window = `${formatMinute(tier.start_minute_of_day)}–${formatMinute(tier.end_minute_of_day)}`
    const days = tier.weekdays && tier.weekdays.length > 0 ? formatWeekdays(tier.weekdays) : '每天'
    parts.push(`${days} ${window}`)
  } else if (tier.weekdays && tier.weekdays.length > 0) {
    parts.push(formatWeekdays(tier.weekdays))
  }
  if (parts.length === 0) parts.push('默认')
  return parts.join(' · ')
}

export function TierCountBadge({ tiers }: { tiers?: PriceTier[] | null }) {
  if (!tiers || tiers.length === 0) return null
  return <span className="tier-count-badge">+{tiers.length} 条件档</span>
}

export function TierPriceList({ tiers }: { tiers?: PriceTier[] | null }) {
  if (!tiers || tiers.length === 0) return null
  return (
    <ul className="tier-price-list">
      {tiers.map((tier, index) => (
        <li key={index}>
          <span className="tier-condition">{tier.name ? `${tier.name} · ` : ''}{tierConditionLabel(tier)}</span>
          <PricePair first={tier.input_price} second={tier.output_price} />
        </li>
      ))}
    </ul>
  )
}

const stateLabels: Record<ChannelStatus | ChannelOfferStatus | ValidationStatus, string> = {
  draft: '草稿',
  published: '已发布',
  paused: '已暂停',
  deleted: '已删除',
  active: '启用',
  disabled: '停用',
  in_progress: '验证中',
  passed: '已通过',
  failed: '失败',
}

const stateTones: Record<ChannelStatus | ChannelOfferStatus | ValidationStatus, BadgeTone> = {
  draft: 'neutral',
  published: 'success',
  paused: 'warning',
  deleted: 'danger',
  active: 'success',
  disabled: 'warning',
  in_progress: 'info',
  passed: 'success',
  failed: 'danger',
}

export function ChannelStateBadge({
  status,
}: {
  status: ChannelStatus | ChannelOfferStatus | ValidationStatus
}) {
  return <Badge tone={stateTones[status]}>{stateLabels[status]}</Badge>
}

/** 报价当前不可用的原因（后端 ineligible_reason 的中文呈现）。 */
export function eligibilityLabel(reason: string) {
  switch (reason) {
    case 'credential_unavailable': return '凭据不可用'
    case 'model_inactive': return '模型已停用'
    case 'offer_inactive': return '报价已停用'
    case 'validation_required': return '需要重新验证'
    case 'channel_unpublished': return '渠道未发布'
    case 'owner_inactive': return '账户已停用'
    case 'owner_password_change_required': return '账户需先改密'
    case 'price_unrepresentable': return '价格不可用'
    default: return '当前不可用'
  }
}

/** 风险确认对话框：基于 ui/Dialog，错误显示在对话框内，处理中禁止关闭。 */
export function ConfirmActionDialog({
  open,
  title,
  description,
  confirmLabel,
  cancelLabel = '取消',
  danger = false,
  busy = false,
  confirmDisabled = false,
  error,
  children,
  onCancel,
  onConfirm,
}: {
  open: boolean
  title: string
  description?: string
  confirmLabel: string
  cancelLabel?: string
  danger?: boolean
  busy?: boolean
  confirmDisabled?: boolean
  error?: string
  children?: ReactNode
  onCancel: () => void
  onConfirm: () => void
}) {
  return (
    <Dialog
      busy={busy}
      description={description}
      footer={(
        <>
          <Button disabled={busy} onClick={onCancel} type="button" variant="secondary">{cancelLabel}</Button>
          <Button disabled={confirmDisabled} loading={busy} onClick={onConfirm} type="button" variant={danger ? 'danger' : 'primary'}>
            {confirmLabel}
          </Button>
        </>
      )}
      onClose={onCancel}
      open={open}
      title={title}
    >
      {children}
      <InlineError>{error}</InlineError>
    </Dialog>
  )
}

export function StarRating({
  value,
  disabled,
  onChange,
}: {
  value: number | null
  disabled?: boolean
  onChange: (score: number) => void
}) {
  const groupName = useId()
  return (
    <fieldset className="star-rating" disabled={disabled}>
      <legend>你的评分</legend>
      <div aria-label="渠道评分" role="radiogroup">
        {[1, 2, 3, 4, 5].map((score) => (
          <label key={score}>
            <input
              checked={value === score}
              name={groupName}
              onChange={() => onChange(score)}
              type="radio"
              value={score}
            />
            <span aria-hidden="true">★</span>
            <span className="visually-hidden">{score} 星</span>
          </label>
        ))}
      </div>
    </fieldset>
  )
}
