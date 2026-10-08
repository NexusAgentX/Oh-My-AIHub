import {
  useId,
  useState,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from 'react'
import { IconButton } from './Button'
import { Icon } from './Icon'
import { blurNumberInputOnWheel } from './numberInput'

type FieldMeta = {
  label: string
  /** 校验错误：显示为红色并设置 aria-invalid */
  error?: string
  /** 辅助说明，错误出现时被替换 */
  hint?: string
}

/** 统一处理 label、hint/error 与 aria-describedby，所有表单控件共用。 */
function FieldFrame({
  id,
  label,
  error,
  hint,
  className,
  children,
}: FieldMeta & { id: string; className?: string; children: ReactNode }) {
  const message = error || hint
  return (
    <label className={['field', className].filter(Boolean).join(' ')} htmlFor={id}>
      <span className="field-label">{label}</span>
      {children}
      {message && (
        <span
          className={error ? 'field-message field-error' : 'field-message'}
          id={`${id}-description`}
        >
          {message}
        </span>
      )}
    </label>
  )
}

function describedBy(id: string, meta: FieldMeta) {
  return meta.error || meta.hint ? `${id}-description` : undefined
}

export function TextField({
  label,
  error,
  hint,
  id,
  className,
  onWheel,
  ...props
}: InputHTMLAttributes<HTMLInputElement> & FieldMeta) {
  const generatedID = useId()
  const fieldID = id ?? generatedID
  return (
    <FieldFrame className={className} error={error} hint={hint} id={fieldID} label={label}>
      <input
        {...props}
        onWheel={(event) => {
          blurNumberInputOnWheel(event)
          onWheel?.(event)
        }}
        aria-describedby={describedBy(fieldID, { label, error, hint })}
        aria-invalid={Boolean(error)}
        className="input"
        id={fieldID}
      />
    </FieldFrame>
  )
}

export function PasswordField({
  label,
  error,
  hint,
  id,
  className,
  ...props
}: InputHTMLAttributes<HTMLInputElement> & FieldMeta) {
  const [visible, setVisible] = useState(false)
  const generatedID = useId()
  const fieldID = id ?? generatedID
  return (
    <FieldFrame className={className} error={error} hint={hint} id={fieldID} label={label}>
      <span className="password-input">
        <input
          {...props}
          aria-describedby={describedBy(fieldID, { label, error, hint })}
          aria-invalid={Boolean(error)}
          className="input"
          id={fieldID}
          type={visible ? 'text' : 'password'}
        />
        <IconButton
          className="password-toggle"
          icon={<Icon name={visible ? 'eye-off' : 'eye'} />}
          label={visible ? '隐藏密码' : '显示密码'}
          onClick={() => setVisible((current) => !current)}
        />
      </span>
    </FieldFrame>
  )
}

export function SelectField({
  label,
  error,
  hint,
  id,
  className,
  children,
  ...props
}: SelectHTMLAttributes<HTMLSelectElement> & FieldMeta) {
  const generatedID = useId()
  const fieldID = id ?? generatedID
  return (
    <FieldFrame className={className} error={error} hint={hint} id={fieldID} label={label}>
      <select
        {...props}
        aria-describedby={describedBy(fieldID, { label, error, hint })}
        aria-invalid={Boolean(error)}
        className="input select-input"
        id={fieldID}
      >
        {children}
      </select>
    </FieldFrame>
  )
}

export function TextareaField({
  label,
  error,
  hint,
  id,
  className,
  ...props
}: TextareaHTMLAttributes<HTMLTextAreaElement> & FieldMeta) {
  const generatedID = useId()
  const fieldID = id ?? generatedID
  return (
    <FieldFrame className={className} error={error} hint={hint} id={fieldID} label={label}>
      <textarea
        {...props}
        aria-describedby={describedBy(fieldID, { label, error, hint })}
        aria-invalid={Boolean(error)}
        className="input textarea-input"
        id={fieldID}
      />
    </FieldFrame>
  )
}

export function Checkbox({
  label,
  className,
  ...props
}: Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> & { label: ReactNode }) {
  return (
    <label className={['checkbox-control', className].filter(Boolean).join(' ')}>
      <input {...props} type="checkbox" />
      <span>{label}</span>
    </label>
  )
}

/** 带搜索图标的单行搜索框，用于 Toolbar。 */
export function SearchInput({
  label,
  className,
  ...props
}: Omit<InputHTMLAttributes<HTMLInputElement>, 'type' | 'aria-label'> & { label: string }) {
  return (
    <span className={['search-form', className].filter(Boolean).join(' ')}>
      <Icon name="search" />
      <input {...props} aria-label={label} className="input" type="search" />
    </span>
  )
}

/** 表单分区：fieldset + legend，用于把长表单按主题分组。 */
export function FormSection({
  title,
  description,
  children,
}: {
  title: string
  description?: string
  children: ReactNode
}) {
  return (
    <fieldset className="form-section">
      <legend>{title}</legend>
      {description && <p className="muted-copy">{description}</p>}
      {children}
    </fieldset>
  )
}
