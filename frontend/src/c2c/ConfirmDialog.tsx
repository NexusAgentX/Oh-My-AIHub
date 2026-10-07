import type { ReactNode } from 'react'
import { Button, Dialog, InlineError } from '../ui'

/** 带风险说明的确认对话框；进行中禁止关闭，失败信息留在对话框内。 */
export function ConfirmDialog({
  open,
  title,
  confirmLabel,
  danger = false,
  busy,
  error,
  onConfirm,
  onClose,
  children,
}: {
  open: boolean
  title: string
  confirmLabel: string
  danger?: boolean
  busy: boolean
  error: string
  onConfirm: () => void
  onClose: () => void
  children: ReactNode
}) {
  return (
    <Dialog
      busy={busy}
      footer={
        <>
          <Button disabled={busy} onClick={onClose} type="button" variant="secondary">
            返回
          </Button>
          <Button
            loading={busy}
            onClick={onConfirm}
            type="button"
            variant={danger ? 'danger' : 'primary'}
          >
            {confirmLabel}
          </Button>
        </>
      }
      onClose={onClose}
      open={open}
      title={title}
    >
      <p className="c2c-confirm-copy">{children}</p>
      <InlineError>{error}</InlineError>
    </Dialog>
  )
}
