import { useEffect, useId, useRef, type ReactNode } from 'react'
import type {
  ChannelOfferStatus,
  ChannelProtocol,
  ChannelStatus,
  ValidationStatus,
} from '../api/contracts'
import { Button } from '../ui/FormControls'

export const protocolLabels: Record<ChannelProtocol, string> = {
  openai_chat_completions: 'OpenAI Chat Completions',
  openai_responses: 'OpenAI Responses',
  anthropic_messages: 'Anthropic Messages',
  google_gemini_generate_content: 'Gemini GenerateContent',
}

export function PricePair({ first, second }: { first?: string | null; second?: string | null }) {
  return <span className="channel-price-pair"><span>{first ?? '—'} / {second ?? '—'}</span><small>积分 / 百万 tokens</small></span>
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

export function ChannelStateBadge({
  status,
}: {
  status: ChannelStatus | ChannelOfferStatus | ValidationStatus
}) {
  const tone = ['published', 'active', 'passed'].includes(status)
    ? 'positive'
    : ['deleted', 'failed'].includes(status)
      ? 'danger'
      : ['paused', 'disabled'].includes(status)
        ? 'warning'
        : 'neutral'
  return <span className={`channel-state channel-state-${tone}`}>{stateLabels[status]}</span>
}

export function formatDate(value?: string | null) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))
}

export function ratingText(rating: string | null, count: number) {
  return rating ? `${rating} · ${count}` : '暂无评分'
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  danger = false,
  busy = false,
  confirmDisabled = false,
  children,
  onCancel,
  onConfirm,
}: {
  open: boolean
  title: string
  description?: string
  confirmLabel: string
  danger?: boolean
  busy?: boolean
  confirmDisabled?: boolean
  children?: ReactNode
  onCancel: () => void
  onConfirm: () => void
}) {
  const reference = useRef<HTMLDialogElement>(null)
  const triggerReference = useRef<HTMLElement | null>(null)
  const titleID = useId()

  useEffect(() => {
    const dialog = reference.current
    if (!dialog) return
    if (open && !dialog.open) {
      triggerReference.current = document.activeElement as HTMLElement | null
      dialog.showModal()
      requestAnimationFrame(() => dialog.querySelector<HTMLElement>('button, input, textarea')?.focus())
    } else if (!open && dialog.open) {
      dialog.close()
    }
  }, [open])

  const close = () => {
    if (busy) return
    onCancel()
    requestAnimationFrame(() => triggerReference.current?.focus())
  }

  return (
    <dialog
      aria-labelledby={titleID}
      className="modal"
      onCancel={(event) => {
        event.preventDefault()
        close()
      }}
      onClose={() => {
        if (open) onCancel()
      }}
      ref={reference}
    >
      <div className="modal-form">
        <header className="modal-heading">
          <div>
            <h2 id={titleID}>{title}</h2>
            {description && <p>{description}</p>}
          </div>
        </header>
        {children}
        <div className="modal-actions">
          <Button disabled={busy} onClick={close} type="button" variant="secondary">取消</Button>
          <Button disabled={busy || confirmDisabled} onClick={onConfirm} type="button" variant={danger ? 'danger' : 'primary'}>
            {busy ? '处理中' : confirmLabel}
          </Button>
        </div>
      </div>
    </dialog>
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
