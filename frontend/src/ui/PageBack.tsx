import { createContext, useContext } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { Icon } from './Icon'

/** 详情页的上一级：顶栏左侧显示“‹ 名称”，点击回到 to。 */
export type PageBack = { to: string; label: string }

/** 外壳把顶栏左侧的位置交给页面；没有外壳时（测试、独立页面）返回入口留在标题上方。 */
export const PageBackSlot = createContext<HTMLElement | null>(null)

export function PageBackLink({ back }: { back: PageBack }) {
  const slot = useContext(PageBackSlot)
  const link = (
    <Link aria-label={`返回${back.label}`} className="page-back" to={back.to}>
      <Icon name="chevron-left" />
      {back.label}
    </Link>
  )
  return slot ? createPortal(link, slot) : link
}
