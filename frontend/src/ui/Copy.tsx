import { useEffect, useState } from 'react'
import { Button, IconButton } from './Button'
import { Icon } from './Icon'

/** 写入剪贴板；不支持 Clipboard API 时退回到选区复制。 */
export async function copyText(text: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text)
    return
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.opacity = '0'
  document.body.appendChild(area)
  area.select()
  document.execCommand('copy')
  area.remove()
}

function useCopied() {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  useEffect(() => {
    if (state === 'idle') return
    const timer = window.setTimeout(() => setState('idle'), 1800)
    return () => window.clearTimeout(timer)
  }, [state])
  return [state, setState] as const
}

/** 复制按钮：value 可以是函数（例如先请求完整 Key 再复制）。 */
export function CopyButton({
  value,
  label = '复制',
  size = 'sm',
  variant = 'secondary',
  iconOnly = false,
  disabled,
}: {
  value: string | (() => Promise<string>)
  label?: string
  size?: 'sm' | 'md'
  variant?: 'secondary' | 'quiet' | 'primary'
  /** 只显示图标，label 作为无障碍名称与悬停提示；用于紧挨着被复制值的地方 */
  iconOnly?: boolean
  disabled?: boolean
}) {
  const [state, setState] = useCopied()
  const [busy, setBusy] = useState(false)
  const onClick = async () => {
    setBusy(true)
    try {
      await copyText(typeof value === 'string' ? value : await value())
      setState('copied')
    } catch {
      setState('failed')
    } finally {
      setBusy(false)
    }
  }
  const text = state === 'copied' ? '已复制' : state === 'failed' ? '复制失败' : label
  const icon = <Icon name={state === 'copied' ? 'check' : 'copy'} />
  if (iconOnly) {
    return <IconButton aria-live="polite" disabled={disabled || busy} icon={icon} label={text} onClick={() => void onClick()} />
  }
  return (
    <Button
      aria-live="polite"
      disabled={disabled}
      icon={icon}
      loading={busy}
      onClick={() => void onClick()}
      size={size}
      type="button"
      variant={variant}
    >
      {text}
    </Button>
  )
}

/** 只读值 + 复制：接口地址、完整 Key、请求 ID。 */
export function CopyField({
  label,
  value,
  display,
  copyValue,
  masked,
}: {
  label: string
  value: string
  /** 显示文本（默认同 value） */
  display?: string
  /** 复制内容（默认同 value），可以是延迟获取 */
  copyValue?: string | (() => Promise<string>)
  masked?: boolean
}) {
  return (
    <div className="copy-field">
      <span className="copy-field-label">{label}</span>
      <div className="copy-field-row">
        <code className={`copy-field-value ${masked ? 'copy-field-masked' : ''}`}>{display ?? value}</code>
        <CopyButton value={copyValue ?? value} />
      </div>
    </div>
  )
}
