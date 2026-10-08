import { useEffect, useId, useRef, type ReactNode } from 'react'
import { Button, IconButton } from './Button'
import { Icon } from './Icon'

type OverlayProps = {
  open: boolean
  onClose: () => void
  title: string
  description?: string
  children: ReactNode
  /** 提交等进行中状态：禁止 Esc、遮罩点击与关闭按钮 */
  busy?: boolean
  /** 底部操作区（取消 / 确认） */
  footer?: ReactNode
}

/**
 * 基于原生 <dialog>.showModal()：浏览器提供焦点陷阱、背景惰性与 Esc，
 * 关闭后焦点自动回到触发元素。这里补充：遮罩点击关闭、滚动锁定、busy 保护。
 */
function useModalDialog({
  open,
  onClose,
  busy = false,
}: Pick<OverlayProps, 'open' | 'onClose' | 'busy'>) {
  const reference = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    const dialog = reference.current
    if (!dialog) return
    if (open && !dialog.open && typeof dialog.showModal === 'function') {
      dialog.showModal()
    }
    if (!open && dialog.open) dialog.close()
  }, [open])

  useEffect(() => {
    if (!open) return
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = previous
    }
  }, [open])

  return {
    reference,
    handlers: {
      onCancel(event: React.SyntheticEvent<HTMLDialogElement>) {
        event.preventDefault()
        if (!busy) onClose()
      },
      onClick(event: React.MouseEvent<HTMLDialogElement>) {
        // 点击 ::backdrop 时事件目标是 dialog 自身
        if (event.target === event.currentTarget && !busy) onClose()
      },
    },
  }
}

/** 对话框：确认、短表单、一次性凭据展示。需要更多空间的二级编辑用 Drawer。 */
export function Dialog({
  open,
  onClose,
  title,
  description,
  children,
  busy,
  footer,
}: OverlayProps) {
  const titleID = useId()
  const { reference, handlers } = useModalDialog({ open, onClose, busy })
  return (
    <dialog
      aria-labelledby={titleID}
      className="modal"
      ref={reference}
      {...handlers}
    >
      {open && (
        <div className="overlay-body">
          <header className="modal-heading">
            <div>
              <h2 id={titleID}>{title}</h2>
              {description && <p>{description}</p>}
            </div>
            <IconButton
              disabled={busy}
              icon={<Icon name="x" />}
              label="关闭"
              onClick={onClose}
            />
          </header>
          {children}
          {footer && <footer className="modal-actions">{footer}</footer>}
        </div>
      )}
    </dialog>
  )
}

/**
 * 抽屉：从右侧（桌面）滑入的二级操作面板，用于详情查看与较长表单。
 * placement="bottom" 用于移动端底部弹层（如「更多」导航）。
 */
export function Drawer({
  open,
  onClose,
  title,
  description,
  children,
  busy,
  footer,
  placement = 'right',
}: OverlayProps & { placement?: 'right' | 'left' | 'bottom' }) {
  const titleID = useId()
  const { reference, handlers } = useModalDialog({ open, onClose, busy })
  return (
    <dialog
      aria-labelledby={titleID}
      className={`drawer drawer-${placement}`}
      ref={reference}
      {...handlers}
    >
      {open && (
        <div className="overlay-body drawer-body">
          <header className="modal-heading">
            <div>
              <h2 id={titleID}>{title}</h2>
              {description && <p>{description}</p>}
            </div>
            <IconButton
              disabled={busy}
              icon={<Icon name="x" />}
              label="关闭"
              onClick={onClose}
            />
          </header>
          <div className="drawer-content">{children}</div>
          {footer && <footer className="modal-actions">{footer}</footer>}
        </div>
      )}
    </dialog>
  )
}

/** 风险确认：删除、放行、申诉等不可撤销的操作。 */
export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  description,
  confirmLabel,
  danger,
  busy,
  error,
  children,
}: {
  open: boolean
  onClose: () => void
  onConfirm: () => void
  title: string
  description?: string
  confirmLabel: string
  danger?: boolean
  busy?: boolean
  error?: string
  children?: ReactNode
}) {
  return (
    <Dialog
      busy={busy}
      description={description}
      footer={
        <>
          <Button disabled={busy} onClick={onClose} type="button" variant="secondary">
            取消
          </Button>
          <Button loading={busy} onClick={onConfirm} type="button" variant={danger ? 'danger' : 'primary'}>
            {confirmLabel}
          </Button>
        </>
      }
      onClose={onClose}
      open={open}
      title={title}
    >
      {children}
      {error && (
        <div className="inline-error" role="alert">
          {error}
        </div>
      )}
    </Dialog>
  )
}
