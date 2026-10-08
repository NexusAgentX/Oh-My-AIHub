import { useId, useState, type ReactNode } from 'react'
import { Icon } from './Icon'

/**
 * 折叠区：用于表单内的「高级设置」。标题旁显示已改项数，
 * 有改动时默认展开以免隐藏生效中的设置。
 */
export function Disclosure({
  title,
  changed = 0,
  defaultOpen,
  children,
}: {
  title: string
  /** 已偏离默认值的项数；0 时不显示徽标 */
  changed?: number
  defaultOpen?: boolean
  children: ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen ?? false)
  const panelID = useId()
  return (
    <section className={`disclosure ${open ? 'disclosure-open' : ''}`}>
      <button
        aria-controls={panelID}
        aria-expanded={open}
        className="disclosure-toggle"
        onClick={() => setOpen((value) => !value)}
        type="button"
      >
        <Icon name="chevron-down" />
        <span>{title}</span>
        {changed > 0 && <span className="badge badge-accent">已改 {changed} 项</span>}
      </button>
      <div className="disclosure-panel" hidden={!open} id={panelID}>
        {children}
      </div>
    </section>
  )
}
