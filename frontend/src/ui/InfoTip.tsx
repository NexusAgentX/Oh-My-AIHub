import { useEffect, useId, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { Icon } from './Icon'

const bubbleWidth = 300
const gutter = 16

/** 气泡固定定位在按钮下方并夹在视口内，不受表格等容器的 overflow 裁剪。 */
function bubblePosition(button: HTMLElement): CSSProperties {
  const rect = button.getBoundingClientRect()
  const width = Math.min(bubbleWidth, window.innerWidth - gutter * 2)
  const left = Math.min(Math.max(rect.left + rect.width / 2 - width / 2, gutter), window.innerWidth - gutter - width)
  // 下方放不下时翻到按钮上方。
  if (rect.bottom + 240 > window.innerHeight && rect.top > 240) {
    return { bottom: window.innerHeight - rect.top + 6, left, width }
  }
  return { top: rect.bottom + 6, left, width }
}

/** 说明气泡：悬停、聚焦或点按 ⓘ 显示，点外部或 Esc 关闭。用于不宜常驻的解释文字。 */
export function InfoTip({ label, children }: { label: string; children: ReactNode }) {
  const [position, setPosition] = useState<CSSProperties | null>(null)
  const open = position !== null
  const root = useRef<HTMLSpanElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const show = () => button.current && setPosition(bubblePosition(button.current))
  const setOpen = (value: boolean) => (value ? show() : setPosition(null))
  const id = useId()

  useEffect(() => {
    if (!open) return
    const onPointer = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false)
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    const onScroll = () => setOpen(false)
    document.addEventListener('pointerdown', onPointer)
    document.addEventListener('keydown', onKey)
    window.addEventListener('scroll', onScroll, true)
    return () => {
      document.removeEventListener('pointerdown', onPointer)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('scroll', onScroll, true)
    }
  }, [open])

  return (
    <span
      className="info-tip"
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
      ref={root}
    >
      <button
        aria-describedby={open ? id : undefined}
        aria-expanded={open}
        aria-label={label}
        className="info-tip-button"
        onBlur={() => setOpen(false)}
        onClick={() => setOpen(true)}
        onFocus={() => setOpen(true)}
        ref={button}
        type="button"
      >
        <Icon name="info" size={14} />
      </button>
      {open && (
        <span className="info-tip-bubble" id={id} role="tooltip" style={position}>
          {children}
        </span>
      )}
    </span>
  )
}
