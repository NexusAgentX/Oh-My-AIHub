import {
  useId,
  useState,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
} from 'react'
import { Icon } from './Icon'

type FieldProps = InputHTMLAttributes<HTMLInputElement> & {
  label: string
  error?: string
  hint?: string
}

export function TextField({ label, error, hint, id, ...props }: FieldProps) {
  const generatedID = useId()
  const fieldID = id ?? generatedID
  const descriptionID = error || hint ? `${fieldID}-description` : undefined
  return (
    <label className="field" htmlFor={fieldID}>
      <span className="field-label">{label}</span>
      <input
        {...props}
        aria-describedby={descriptionID}
        aria-invalid={Boolean(error)}
        className="input"
        id={fieldID}
      />
      {(error || hint) && (
        <span
          className={error ? 'field-message field-error' : 'field-message'}
          id={descriptionID}
        >
          {error ?? hint}
        </span>
      )}
    </label>
  )
}

export function PasswordField({
  label,
  error,
  hint,
  id,
  ...props
}: FieldProps) {
  const [visible, setVisible] = useState(false)
  const generatedID = useId()
  const fieldID = id ?? generatedID
  const descriptionID = error || hint ? `${fieldID}-description` : undefined
  return (
    <label className="field" htmlFor={fieldID}>
      <span className="field-label">{label}</span>
      <span className="password-input">
        <input
          {...props}
          aria-describedby={descriptionID}
          aria-invalid={Boolean(error)}
          className="input"
          id={fieldID}
          type={visible ? 'text' : 'password'}
        />
        <button
          aria-label={visible ? '隐藏密码' : '显示密码'}
          className="icon-button password-toggle"
          onClick={() => setVisible((current) => !current)}
          type="button"
        >
          <Icon name={visible ? 'eye-off' : 'eye'} />
        </button>
      </span>
      {(error || hint) && (
        <span
          className={error ? 'field-message field-error' : 'field-message'}
          id={descriptionID}
        >
          {error ?? hint}
        </span>
      )}
    </label>
  )
}

export function Button({
  children,
  variant = 'primary',
  icon,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  children: ReactNode
  variant?: 'primary' | 'secondary' | 'quiet' | 'danger'
  icon?: ReactNode
}) {
  return (
    <button className={`button button-${variant}`} {...props}>
      {icon}
      <span>{children}</span>
    </button>
  )
}

export function StatusBadge({ status }: { status: 'active' | 'disabled' }) {
  return (
    <span className={`status-badge status-${status}`}>
      {status === 'active' ? '启用' : '停用'}
    </span>
  )
}

export function InlineError({ children }: { children: ReactNode }) {
  if (!children) return null
  return (
    <div aria-live="polite" className="inline-error" role="alert">
      {children}
    </div>
  )
}

export function LoadingState() {
  return (
    <div aria-live="polite" className="loading-state">
      <span className="spinner" /> 正在加载
    </div>
  )
}
