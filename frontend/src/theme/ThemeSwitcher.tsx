import { useEffect, useRef, useState } from 'react'
import { Icon, Segmented, type IconName } from '../ui'
import { themeOptions, useThemeMode, type ThemeMode } from './theme'

const icons: Record<ThemeMode, IconName> = { system: 'monitor', light: 'sun', dark: 'moon' }

/** 顶栏右上角的主题按钮：图标是当前模式，点开在跟随系统、浅色、深色之间选择。 */
export function ThemeToggle() {
  const [mode, setMode] = useThemeMode()
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const current = themeOptions.find((option) => option.key === mode) ?? themeOptions[0]

  useEffect(() => {
    if (!open) return
    const onPointer = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false)
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpen(false)
        button.current?.focus()
      }
    }
    document.addEventListener('pointerdown', onPointer)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('pointerdown', onPointer)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div className="theme-toggle" ref={root}>
      <button
        aria-expanded={open}
        aria-haspopup="menu"
        aria-label={`主题：${current.label}`}
        className="icon-button"
        onClick={() => setOpen((value) => !value)}
        ref={button}
        title={`主题：${current.label}`}
        type="button"
      >
        <Icon name={icons[mode]} />
      </button>
      {open && (
        <div aria-label="主题" className="theme-popover" role="menu">
          {themeOptions.map((option) => (
            <button
              aria-checked={mode === option.key}
              className="theme-popover-item"
              key={option.key}
              onClick={() => {
                setMode(option.key)
                setOpen(false)
                button.current?.focus()
              }}
              role="menuitemradio"
              type="button"
            >
              <Icon name={icons[option.key]} size={16} />
              <span>{option.label}</span>
              {mode === option.key && <Icon name="check" size={16} />}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/** 菜单以外的位置（手机“我的”页）：分段选择。 */
export function ThemeSegmented() {
  const [mode, setMode] = useThemeMode()
  return <Segmented label="主题" onChange={setMode} options={themeOptions} value={mode} />
}
