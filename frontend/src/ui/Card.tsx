import { useRef, type ElementType, type ReactNode } from 'react'
import { TopbarContext, type PageBack } from './TopbarSlot'

/**
 * 卡片：白底、12px 圆角、细边框。
 * - title / actions 渲染标题栏；
 * - flush 用于内部直接放表格或自带布局的内容（不加内边距）；默认内容区带 20px 内边距。
 */
export function Card({
  as: Tag = 'section',
  title,
  actions,
  flush = false,
  className,
  children,
  ...rest
}: {
  as?: ElementType
  title?: ReactNode
  actions?: ReactNode
  flush?: boolean
  className?: string
  children?: ReactNode
  'aria-label'?: string
}) {
  const classes = ['panel', flush ? 'panel-flush' : '', className]
    .filter(Boolean)
    .join(' ')
  return (
    <Tag className={classes} {...rest}>
      {(title || actions) && (
        <header className="panel-heading">
          {title && <h2>{title}</h2>}
          {actions && <div className="panel-actions">{actions}</div>}
        </header>
      )}
      {flush ? children : <div className="panel-body">{children}</div>}
    </Tag>
  )
}

/**
 * 页面标题区：标题、一句说明、右侧主要操作。
 * 详情页用 back 指明上一级，显示在顶栏左侧；标题滚出屏幕后顶栏显示 topbarTitle（默认同 title）。
 */
export function PageHeader({
  title,
  topbarTitle,
  description,
  actions,
  back,
}: {
  title: ReactNode
  topbarTitle?: ReactNode
  description?: ReactNode
  actions?: ReactNode
  back?: PageBack
}) {
  const heading = useRef<HTMLHeadingElement>(null)
  return (
    <header className="page-heading">
      <div className="page-heading-copy">
        <TopbarContext back={back} heading={heading} title={topbarTitle ?? title} />
        <h1 ref={heading}>{title}</h1>
        {description && <p>{description}</p>}
      </div>
      {actions && <div className="page-heading-actions">{actions}</div>}
    </header>
  )
}

/** 指标卡网格：2~4 张 Metric 并排，窄屏自动折行。 */
export function MetricGrid({
  children,
  label,
}: {
  children: ReactNode
  label?: string
}) {
  return (
    <section aria-label={label} className="metric-grid">
      {children}
    </section>
  )
}

export type MetricTone = 'default' | 'accent' | 'warm'

/**
 * 指标：label + 等宽数字 value + 说明。
 * accent 为墨色实底（每屏最多一张，用于最重要的数字），warm 为芥末浅底。
 * progress（0-100）显示芥末黄进度条，用于额度类指标。
 */
export function Metric({
  label,
  value,
  hint,
  tone = 'default',
  progress,
}: {
  label: string
  value: ReactNode
  hint?: ReactNode
  tone?: MetricTone
  progress?: number
}) {
  const toneClass =
    tone === 'accent' ? 'metric-card-accent' : tone === 'warm' ? 'metric-card-warm' : ''
  return (
    <article className={['metric-card', toneClass].filter(Boolean).join(' ')}>
      <span className="metric-label">{label}</span>
      <strong className="metric-value num">{value}</strong>
      {hint && <small className="metric-hint">{hint}</small>}
      {progress !== undefined && (
        <div
          aria-label={`${label}已用 ${Math.round(progress)}%`}
          aria-valuemax={100}
          aria-valuemin={0}
          aria-valuenow={Math.round(progress)}
          className="progress"
          role="progressbar"
        >
          <i style={{ width: `${Math.max(0, Math.min(100, progress))}%` }} />
        </div>
      )}
    </article>
  )
}
