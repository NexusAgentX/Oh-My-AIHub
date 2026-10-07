import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { Link, type LinkProps } from 'react-router-dom'

export type ButtonVariant = 'primary' | 'secondary' | 'quiet' | 'danger'
export type ButtonSize = 'md' | 'sm'

function buttonClass(variant: ButtonVariant, size: ButtonSize, extra?: string) {
  return ['button', `button-${variant}`, size === 'sm' ? 'button-sm' : '', extra]
    .filter(Boolean)
    .join(' ')
}

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  children: ReactNode
  variant?: ButtonVariant
  size?: ButtonSize
  icon?: ReactNode
  /** 进行中：禁用并显示旋转指示，用于提交类按钮 */
  loading?: boolean
}

/**
 * 按钮。首要操作用 primary（墨色实底），一屏最多一个。
 * 沿用原生 type 默认值：表单内不写 type 即为提交按钮，其余场景显式传 type="button"。
 */
export function Button({
  children,
  variant = 'primary',
  size = 'md',
  icon,
  loading = false,
  className,
  disabled,
  ...props
}: ButtonProps) {
  return (
    <button
      {...props}
      aria-busy={loading || undefined}
      className={buttonClass(variant, size, className)}
      disabled={disabled || loading}
    >
      {loading ? <span aria-hidden="true" className="spinner spinner-sm" /> : icon}
      <span>{children}</span>
    </button>
  )
}

type ButtonLinkProps = LinkProps & {
  variant?: ButtonVariant
  size?: ButtonSize
  icon?: ReactNode
}

/** 外观与 Button 相同的路由链接：用于「去某页」类操作。 */
export function ButtonLink({
  children,
  variant = 'secondary',
  size = 'md',
  icon,
  className,
  ...props
}: ButtonLinkProps) {
  return (
    <Link {...props} className={buttonClass(variant, size, className)}>
      {icon}
      <span>{children}</span>
    </Link>
  )
}

/** 仅图标按钮，必须提供 label 作为无障碍名称。 */
export function IconButton({
  label,
  icon,
  className,
  type = 'button',
  ...props
}: Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children' | 'aria-label'> & {
  label: string
  icon: ReactNode
}) {
  return (
    <button
      {...props}
      aria-label={label}
      className={['icon-button', className].filter(Boolean).join(' ')}
      title={label}
      type={type}
    >
      {icon}
    </button>
  )
}
