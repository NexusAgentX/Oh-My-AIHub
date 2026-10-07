import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Brand } from './Brand'

/** 身份类页面（登录、初始化、首次改密）共用的居中卡片外壳。 */
export function AuthShell({
  title,
  description,
  badge,
  children,
}: {
  title?: string
  description?: string
  badge?: string
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
