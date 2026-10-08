import { useId, useRef, type KeyboardEvent, type ReactNode } from 'react'

export type TabItem<K extends string> = {
  key: K
  label: string
  /** 标签旁的数量 */
  count?: number
}

function nextIndex(event: KeyboardEvent, current: number, length: number) {
  if (event.key === 'ArrowRight') return (current + 1) % length
  if (event.key === 'ArrowLeft') return (current - 1 + length) % length
  if (event.key === 'Home') return 0
  if (event.key === 'End') return length - 1
  return -1
}

/**
 * 标签页（WAI-ARIA tablist）：切换同页内容面板，方向键在标签间移动。
 * 面板用 renderPanel 渲染，aria-controls 已连接。
 */
export function Tabs<K extends string>({
  label,
  items,
  value,
  onChange,
  children,
}: {
  label: string
  items: TabItem<K>[]
  value: K
  onChange: (key: K) => void
  /** 当前标签对应的面板内容 */
  children: ReactNode
}) {
  const base = useId()
  const buttons = useRef<Array<HTMLButtonElement | null>>([])
  const onKeyDown = (event: KeyboardEvent, index: number) => {
    const target = nextIndex(event, index, items.length)
    if (target < 0) return
    event.preventDefault()
    onChange(items[target].key)
    buttons.current[target]?.focus()
  }
  return (
    <div className="tabs">
      <div aria-label={label} className="tab-list" role="tablist">
        {items.map((item, index) => {
          const selected = item.key === value
          return (
            <button
              aria-controls={`${base}-panel`}
              aria-selected={selected}
              className={`tab ${selected ? 'tab-active' : ''}`}
              id={`${base}-${item.key}`}
              key={item.key}
              onClick={() => onChange(item.key)}
              onKeyDown={(event) => onKeyDown(event, index)}
              ref={(node) => {
                buttons.current[index] = node
              }}
              role="tab"
              tabIndex={selected ? 0 : -1}
              type="button"
            >
              {item.label}
              {item.count !== undefined && <span className="tab-count">{item.count}</span>}
            </button>
          )
        })}
      </div>
      <div
        aria-labelledby={`${base}-${value}`}
        className="tab-panel"
        id={`${base}-panel`}
        role="tabpanel"
      >
        {children}
      </div>
    </div>
  )
}

/**
 * 分段选择：切换同一数据的视图或筛选（如时间窗口），不切换整块内容。
 * 语义为单选按钮组（aria-pressed）。
 */
export function Segmented<K extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string
  options: Array<{ key: K; label: string }>
  value: K
  onChange: (key: K) => void
}) {
  return (
    <div aria-label={label} className="segmented" role="group">
      {options.map((option) => (
        <button
          aria-pressed={option.key === value}
          className={`segmented-item ${option.key === value ? 'active' : ''}`}
          key={option.key}
          onClick={() => onChange(option.key)}
          type="button"
        >
          {option.label}
        </button>
      ))}
    </div>
  )
}
