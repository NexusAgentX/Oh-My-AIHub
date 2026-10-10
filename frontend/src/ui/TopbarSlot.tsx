import { createContext, useContext, useEffect, useState, type ReactNode, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { Icon } from './Icon'

/** 详情页的上一级：顶栏左侧显示“‹ 名称”，点击回到 to。 */
export type PageBack = { to: string; label: string }

/** 外壳把顶栏左侧的位置交给页面标题区；没有外壳时（测试、独立页面）返回入口留在标题上方。 */
export const TopbarSlot = createContext<HTMLElement | null>(null)

/** 页面标题是否已滚到顶栏下面（看不见了）。 */
function useScrolledPast(target: RefObject<HTMLElement | null>, slot: HTMLElement | null) {
  const [past, setPast] = useState(false)
  useEffect(() => {
    const element = target.current
    if (!element || !slot || typeof IntersectionObserver === 'undefined') return
    const offset = Math.round(slot.closest('.topbar')?.getBoundingClientRect().height ?? 0)
    const observer = new IntersectionObserver(
      ([entry]) => setPast(!entry.isIntersecting && entry.boundingClientRect.top < offset),
      { rootMargin: `-${offset}px 0px 0px 0px` },
    )
    observer.observe(element)
    return () => observer.disconnect()
  }, [target, slot])
  return past
}

/**
 * 顶栏左侧的页面上下文：详情页的“‹ 上一级”，以及标题滚出屏幕后才出现的页面标题。
 * 页面顶部时标题只在页面里出现一次；顶栏里的标题是重复内容，对读屏隐藏。
 */
export function TopbarContext({ back, title, heading }: { back?: PageBack; title: ReactNode; heading: RefObject<HTMLElement | null> }) {
  const slot = useContext(TopbarSlot)
  const scrolledPast = useScrolledPast(heading, slot)
  const link = back && (
    <Link aria-label={`返回${back.label}`} className="page-back" to={back.to}>
      <Icon name="chevron-left" />
      {back.label}
    </Link>
  )
  if (!slot) return link || null
  return createPortal(
    <>
      {link}
      {scrolledPast && (
        <span aria-hidden="true" className="topbar-title">
          {back && <span className="topbar-title-sep">/</span>}
          <span className="topbar-title-text">{title}</span>
        </span>
      )}
    </>,
    slot,
  )
}
