import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Mascot } from '../ui'
import { Brand } from './Brand'

/** 身份类页面（登录、初始化、首次改密）与 404 共用的居中卡片外壳；mascot 按 DESIGN.md 的吉祥物用法选择。 */
export function AuthShell({
  title,
  description,
  badge,
  mascot,
  children,
}: {
  title?: string
  description?: string
  badge?: string
  mascot?: 'oh' | 'drop'
  children: ReactNode
}) {
  return (
    <main className="identity-page">
      <section className="identity-card">
        <Link aria-label="Oh-My-AIHub 首页" className="identity-brand" to="/">
          <Brand />
        </Link>
        {title && (
          <header className="identity-heading">
            {mascot && <Mascot kind={mascot} size={64} />}
            {badge && <span className="step-badge">{badge}</span>}
            <h1>{title}</h1>
            {description && <p>{description}</p>}
          </header>
        )}
        {children}
      </section>
    </main>
  )
}
