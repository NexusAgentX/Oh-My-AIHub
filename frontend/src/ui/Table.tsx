import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { EmptyState } from './Feedback'

export type Column<T> = {
  /** 稳定的列标识，同时用作 key */
  key: string
  header: string
  cell: (row: T) => ReactNode
  /** 数字列：右对齐并使用等宽数字 */
  numeric?: boolean
  /** 移动端卡片的标题列（每张卡片一列） */
  primary?: boolean
  /** 移动端卡片中隐藏该列 */
  hideOnMobile?: boolean
}

/**
 * 数据表：桌面端为表格，960px 以下自动变为卡片列表。
 * - rowKey：行的稳定 key；
 * - rowHref：提供时整行可点击进入详情（主列作为真实链接，保证键盘可达）；
 * - 空数据展示 empty（默认「暂无数据」）。
 * 复杂单元格（徽标、操作按钮）直接在 cell 中返回 JSX。
 */
export function DataTable<T>({
  caption,
  columns,
  rows,
  rowKey,
  rowHref,
  empty,
}: {
  caption: string
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T) => string
  rowHref?: (row: T) => string
  empty?: ReactNode
}) {
  if (rows.length === 0) return <>{empty ?? <EmptyState title="暂无数据" />}</>
  const primary = columns.find((column) => column.primary) ?? columns[0]
  return (
    <>
      <div className="desktop-table-wrap">
        <table className="data-table">
          <caption className="visually-hidden">{caption}</caption>
          <thead>
            <tr>
              {columns.map((column) => (
                <th
                  className={column.numeric ? 'cell-numeric' : undefined}
                  key={column.key}
                  scope="col"
                >
                  {column.header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const href = rowHref?.(row)
              return (
                <tr key={rowKey(row)}>
                  {columns.map((column) => (
                    <td
                      className={column.numeric ? 'cell-numeric' : undefined}
                      key={column.key}
                    >
                      {href && column === primary ? (
                        <Link className="table-row-link" to={href}>
                          {column.cell(row)}
                        </Link>
                      ) : (
                        column.cell(row)
                      )}
                    </td>
                  ))}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      <div className="mobile-card-list">
        {rows.map((row) => {
          const href = rowHref?.(row)
          const body = (
            <>
              <header>
                <div>{primary.cell(row)}</div>
              </header>
              <dl>
                {columns
                  .filter((column) => column !== primary && !column.hideOnMobile)
                  .map((column) => (
                    <div key={column.key}>
                      <dt>{column.header}</dt>
                      <dd>{column.cell(row)}</dd>
                    </div>
                  ))}
              </dl>
            </>
          )
          return href ? (
            <Link className="mobile-data-card mobile-data-card-link" key={rowKey(row)} to={href}>
              {body}
            </Link>
          ) : (
            <article className="mobile-data-card" key={rowKey(row)}>
              {body}
            </article>
          )
        })}
      </div>
    </>
  )
}

/** 工具栏：搜索、筛选与批量操作的一行容器，窄屏纵向堆叠。 */
export function Toolbar({
  children,
  end,
}: {
  children: ReactNode
  /** 靠右的操作区 */
  end?: ReactNode
}) {
  return (
    <div className="toolbar">
      <div className="toolbar-main">{children}</div>
      {end && <div className="toolbar-end">{end}</div>}
    </div>
  )
}
