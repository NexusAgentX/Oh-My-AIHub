import { useState, type ReactNode } from 'react'
import { errorMessage } from '../api/query'
import { Badge, Button, Dialog, Icon, InlineError, TextareaField } from '../ui'
import { advanceConfirm, initialConfirmState, type SecretReveal } from './confirm'

/** 复制到剪贴板，按钮文字短暂变为「已复制」。 */
export function CopyButton({ value, label = '复制' }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  const [failed, setFailed] = useState(false)
  return (
    <Button
      icon={<Icon name={copied ? 'check' : 'copy'} />}
      onClick={() => {
        void navigator.clipboard
          ?.writeText(value)
          .then(() => {
            setCopied(true)
            setFailed(false)
            window.setTimeout(() => setCopied(false), 1600)
          })
          .catch(() => setFailed(true))
      }}
      size="sm"
      type="button"
      variant="secondary"
    >
      {copied ? '已复制' : failed ? '复制失败，请手动选择' : label}
    </Button>
  )
}

/**
 * 只展示一次的密码：关闭后由调用方丢弃（secretReducer dismiss），界面上无法再次取回。
 */
export function OneTimeSecretDialog({
  secret,
  onDismiss,
}: {
  secret: SecretReveal | null
  onDismiss: () => void
}) {
  return (
    <Dialog
      description="只显示这一次，关闭后无法再次查看"
      footer={
        <Button onClick={onDismiss} type="button">
          我已线下交付，关闭
        </Button>
      }
      onClose={onDismiss}
      open={secret !== null}
      title={secret?.title ?? ''}
    >
      {secret && (
        <div className="secret-reveal">
          <dl className="detail-list compact-list">
            <div>
              <dt>用户名</dt>
              <dd className="mono">{secret.username}</dd>
            </div>
            <div>
              <dt>初始密码</dt>
              <dd className="mono secret-value">{secret.password}</dd>
            </div>
          </dl>
          <CopyButton label="复制密码" value={secret.password} />
          <p className="modal-hint">请通过线下渠道交给本人；首次登录必须修改密码。</p>
        </div>
      )}
    </Dialog>
  )
}

/**
 * 原因必填 + 二次确认的操作对话框。
 * 第一步填写原因（与 children 中的其他参数），第二步复述 summary 并确认后才调用 onConfirm。
 */
export function ConfirmActionDialog({
  open,
  onClose,
  title,
  summary,
  confirmLabel,
  danger = false,
  requireReason = true,
  reasonLabel = '原因',
  validate,
  onConfirm,
  children,
}: {
  open: boolean
  onClose: () => void
  title: string
  /** 第二步展示的「将要发生什么」 */
  summary: ReactNode
  confirmLabel: string
  danger?: boolean
  requireReason?: boolean
  reasonLabel?: string
  validate?: () => string
  onConfirm: (reason: string) => Promise<unknown>
  children?: ReactNode
}) {
  const [state, setState] = useState(initialConfirmState)
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [submitError, setSubmitError] = useState('')

  const close = () => {
    if (busy) return
    setState(initialConfirmState)
    setReason('')
    setSubmitError('')
    onClose()
  }

  const submit = async () => {
    setBusy(true)
    setSubmitError('')
    try {
      await onConfirm(reason.trim())
      setBusy(false)
      setState(initialConfirmState)
      setReason('')
      onClose()
    } catch (caught) {
      setBusy(false)
      setSubmitError(errorMessage(caught, '操作失败，请重试'))
    }
  }

  const editing = state.step === 'edit'
  return (
    <Dialog
      busy={busy}
      footer={
        editing ? (
          <>
            <Button onClick={close} type="button" variant="secondary">
              取消
            </Button>
            <Button
              onClick={() => setState((current) => advanceConfirm(current, { reason, requireReason, validate }))}
              type="button"
              variant={danger ? 'danger' : 'primary'}
            >
              下一步
            </Button>
          </>
        ) : (
          <>
            <Button
              disabled={busy}
              onClick={() => setState(initialConfirmState)}
              type="button"
              variant="secondary"
            >
              返回修改
            </Button>
            <Button
              loading={busy}
              onClick={() => void submit()}
              type="button"
              variant={danger ? 'danger' : 'primary'}
            >
              {confirmLabel}
            </Button>
          </>
        )
      }
      onClose={close}
      open={open}
      title={editing ? title : `确认${title}`}
    >
      {editing ? (
        <div className="stack-form">
          {children}
          {requireReason && (
            <TextareaField
              label={reasonLabel}
              maxLength={500}
              onChange={(event) => setReason(event.target.value)}
              required
              rows={3}
              value={reason}
            />
          )}
          <InlineError>{state.error}</InlineError>
        </div>
      ) : (
        <div className="confirm-summary">
          <Icon name="alert" size={20} />
          <div>
            {summary}
            {requireReason && (
              <p className="muted-copy">
                {reasonLabel}：{reason.trim()}
              </p>
            )}
          </div>
        </div>
      )}
      {!editing && <InlineError>{submitError}</InlineError>}
    </Dialog>
  )
}

/**
 * 折叠区（「高级设置」「条件价格档」）：原生 details，标题显示已改项数。
 */
export function Collapsible({
  title,
  changed,
  badge,
  defaultOpen = false,
  children,
}: {
  title: string
  changed?: number
  /** 自定义徽标文字（替代「已改 N 项」） */
  badge?: string
  defaultOpen?: boolean
  children: ReactNode
}) {
  return (
    <details className="collapsible" open={defaultOpen || undefined}>
      <summary>
        <Icon name="chevron-right" />
        <span>{title}</span>
        {badge ? <Badge tone="accent">{badge}</Badge> : changed !== undefined && changed > 0 && <Badge tone="accent">已改 {changed} 项</Badge>}
      </summary>
      <div className="collapsible-body">{children}</div>
    </details>
  )
}

/** 游标分页「加载更多」。 */
export function LoadMore({
  hasMore,
  loading,
  onLoad,
}: {
  hasMore: boolean
  loading: boolean
  onLoad: () => void
}) {
  if (!hasMore) return null
  return (
    <div className="table-pagination">
      <Button loading={loading} onClick={onLoad} size="sm" type="button" variant="secondary">
        加载更多
      </Button>
    </div>
  )
}

/** 键值摘要列表（抽屉与详情页）。 */
export function DetailList({ items }: { items: Array<[string, ReactNode]> }) {
  return (
    <dl className="detail-list compact-list">
      {items.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  )
}
