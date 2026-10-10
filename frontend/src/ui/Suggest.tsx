import { useId, useLayoutEffect, useRef, useState, type InputHTMLAttributes, type KeyboardEvent } from 'react'

export type Suggestion = { value: string; label?: string }

/** 最近的滚动容器的可见范围（抽屉内容区等）；没有时用视口。 */
function visibleBounds(element: HTMLElement) {
  for (let node = element.parentElement; node; node = node.parentElement) {
    const overflow = getComputedStyle(node).overflowY
    if (overflow === 'auto' || overflow === 'scroll') return node.getBoundingClientRect()
  }
  return { top: 0, bottom: window.innerHeight }
}

/** 填入选中的建议：走原生 input 事件，调用方照常用 onChange 接收。 */
function fillInput(input: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

/**
 * 可以自由输入、同时给出建议的文本框（替代浏览器原生的 datalist，建议列表画成贴纸浮层）。
 * 点击或 ↓ 打开全部建议，输入时按值和说明筛选；↑↓ 移动、回车填入、Esc 收起。
 */
export function SuggestInput({
  suggestions,
  className,
  onBlur,
  onChange,
  onClick,
  onKeyDown,
  ...props
}: Omit<InputHTMLAttributes<HTMLInputElement>, 'list'> & { suggestions: Suggestion[] }) {
  const listID = useId()
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLUListElement>(null)
  const [open, setOpen] = useState(false)
  const [upward, setUpward] = useState(false)
  const [query, setQuery] = useState('') // 打开后输入的内容；为空时显示全部
  const [active, setActive] = useState(-1)
  const needle = query.trim().toLowerCase()
  const items = needle
    ? suggestions.filter((item) => item.value.toLowerCase().includes(needle) || item.label?.toLowerCase().includes(needle))
    : suggestions
  const shown = open && items.length > 0 && !props.disabled && !props.readOnly

  // 下方放不下时向上弹出（例如抽屉底部的字段）；上下都放不下时把列表滚进可见范围
  useLayoutEffect(() => {
    if (!shown || !input.current || !list.current) return
    const field = input.current.getBoundingClientRect()
    const bounds = visibleBounds(input.current)
    const height = list.current.offsetHeight + 6
    const below = bounds.bottom - field.bottom
    const above = field.top - bounds.top
    const up = below < height && above > below
    setUpward(up)
    if (!up && below < height) list.current.scrollIntoView?.({ block: 'nearest' })
  }, [shown, items.length])

  const show = (first = -1) => {
    setOpen(true)
    setQuery('')
    setActive(first)
  }
  const choose = (item: Suggestion) => {
    if (input.current) fillInput(input.current, item.value)
    setOpen(false)
    setActive(-1)
  }
  const handleKey = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      if (shown) setActive((index) => (index + 1) % items.length)
      else show(0)
    } else if (event.key === 'ArrowUp' && shown) {
      event.preventDefault()
      setActive((index) => (index <= 0 ? items.length - 1 : index - 1))
    } else if (event.key === 'Enter' && shown && active >= 0) {
      event.preventDefault()
      choose(items[active])
    } else if (event.key === 'Escape' && shown) {
      // 只收起列表，不关掉外面的抽屉或对话框
      event.preventDefault()
      event.stopPropagation()
      setOpen(false)
    }
  }

  return (
    <span className="suggest">
      <input
        {...props}
        aria-activedescendant={shown && active >= 0 ? `${listID}-${active}` : undefined}
        aria-autocomplete="list"
        aria-controls={listID}
        aria-expanded={shown}
        autoComplete="off"
        className={['input', 'suggest-input', className].filter(Boolean).join(' ')}
        onBlur={(event) => {
          setOpen(false)
          onBlur?.(event)
        }}
        onChange={(event) => {
          setQuery(event.target.value)
          setOpen(true)
          setActive(-1)
          onChange?.(event)
        }}
        onClick={(event) => {
          if (!open) show()
          onClick?.(event)
        }}
        onKeyDown={(event) => {
          onKeyDown?.(event)
          if (!event.defaultPrevented) handleKey(event)
        }}
        ref={input}
        role="combobox"
      />
      {shown && (
        <ul className={`suggest-list${upward ? ' suggest-list--up' : ''}`} id={listID} ref={list} role="listbox">
          {items.map((item, index) => (
            <li
              aria-selected={index === active}
              className="suggest-option"
              id={`${listID}-${index}`}
              key={item.value}
              onMouseDown={(event) => {
                event.preventDefault() // 保持焦点在输入框
                choose(item)
              }}
              onMouseEnter={() => setActive(index)}
              role="option"
              title={item.label ? `${item.value} · ${item.label}` : item.value}
            >
              <span className="suggest-value">{item.value}</span>
              {item.label && <span className="suggest-label">{item.label}</span>}
            </li>
          ))}
        </ul>
      )}
    </span>
  )
}
