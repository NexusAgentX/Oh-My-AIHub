import type { ReactNode } from 'react'

export type BadgeTone = 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'accent'

/** 状态徽标：柔和底色 + 深色文字。文字必须自带含义，不能只靠颜色区分。 */
export function Badge({
  tone = 'neutral',
  children,
}: {
  tone?: BadgeTone
  children: ReactNode
}) {
  return <span className={`badge badge-${tone}`}>{children}</span>
}

/** 数量徽标（导航项、卡片标题旁），芥末黄强调。 */
export function CountBadge({ children }: { children: ReactNode }) {
  return <span className="count-badge">{children}</span>
}

export function StatusBadge({ status }: { status: 'active' | 'disabled' }) {
  return (
    <Badge tone={status === 'active' ? 'success' : 'warning'}>
      {status === 'active' ? '启用' : '停用'}
    </Badge>
  )
}
