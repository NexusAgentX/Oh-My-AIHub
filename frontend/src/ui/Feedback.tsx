import type { ReactNode } from 'react'
import { Button } from './Button'
import { Mascot } from './Mascot'

/** 内联错误：无内容时不渲染，可直接传 `error` 字符串。 */
export function InlineError({ children }: { children: ReactNode }) {
  if (!children) return null
  return (
    <div aria-live="polite" className="inline-error" role="alert">
      {children}
    </div>
  )
}

export function SuccessMessage({ children }: { children: ReactNode }) {
  if (!children) return null
  return (
    <div className="success-message" role="status">
      <Mascot kind="drop" size={22} />
      <span>{children}</span>
    </div>
  )
}

/** 页面级提示条（风险、说明）。tone 决定底色，内容保持一句话。 */
export function Notice({
  tone = 'warning',
  children,
  action,
}: {
  tone?: 'info' | 'warning' | 'danger' | 'success'
  children: ReactNode
  action?: ReactNode
}) {
  return (
    <div
      className={`notice notice-${tone}`}
      role={tone === 'danger' ? 'alert' : 'status'}
    >
      <span>{children}</span>
      {action}
    </div>
  )
}

export function LoadingState({ label = '正在加载' }: { label?: string }) {
  return (
    <div aria-live="polite" className="loading-state" role="status">
      <span className="spinner" /> {label}
    </div>
  )
}

/** 整页加载（路由懒加载、会话与实例检查）：轻轻弹跳的芥末滴；卡片内的加载用 LoadingState。 */
export function PageLoading({ label = '正在加载' }: { label?: string }) {
  return (
    <main className="route-loading">
      <div aria-live="polite" className="loading-state" role="status">
        <Mascot bounce kind="drop" size={48} />
        {label}
      </div>
    </main>
  )
}

/**
 * 空态：惊讶脸吉祥物加一句说明，必要时给出下一步操作。
 * 同一屏可能同时出现多个空态（如统计面板的几个分区）时，次要的传 mascot={false}，保证每屏最多一个吉祥物。
 */
export function EmptyState({
  title,
  description,
  action,
  mascot = true,
}: {
  title: string
  description?: string
  action?: ReactNode
  mascot?: boolean
}) {
  return (
    <div className="empty-state">
      {mascot && <Mascot kind="oh" size={48} />}
      <strong>{title}</strong>
      {description && <p>{description}</p>}
      {action}
    </div>
  )
}

/** 错误态：展示可读信息并提供重试。 */
export function ErrorState({
  message,
  onRetry,
}: {
  message: string
  onRetry?: () => void
}) {
  return (
    <div className="empty-state error-state" role="alert">
      <Mascot kind="oh" size={48} />
      <strong>{message}</strong>
      {onRetry && (
        <Button onClick={onRetry} size="sm" variant="secondary">
          重试
        </Button>
      )}
    </div>
  )
}
