import type { ReactNode } from 'react'
import type { CallOutcome, CallSummary, ChannelCall, Format, Usage } from '../api/types'
import { formatMs, formatPoints, formatTokens } from '../money/format'
import { formatTime } from '../money/time'
import { EmptyState } from '../ui'
import { formatLabels } from './labels'
import { OutcomeBadge } from './OutcomeBadge'

/** 各类调用记录（用户、渠道所有者、管理员）共有的字段。 */
export type CallRowBase = {
  id: string
  created_at: string
  model_id: string | null
  format: Format
  stream: boolean
  outcome: CallOutcome
  usage: Usage
  ttft_ms: number | null
  duration_ms: number | null
}

export type CallColumn<T> = {
  key: string
  header: string
  cell: (row: T) => ReactNode
  numeric?: boolean
  /** 移动端卡片中隐藏 */
  hideOnMobile?: boolean
}

function modelName(row: CallRowBase & { requested_model?: string }) {
  const model = row.model_id ?? row.requested_model ?? '—'
  const aliased = row.requested_model && row.model_id && row.requested_model !== row.model_id
  return (
    <>
      <strong className="call-model">{model}</strong>
      {aliased && <small>别名 {row.requested_model}</small>}
    </>
  )
}

/** 基础列：任何调用记录都可用。 */
export const callColumns = {
  time: <T extends CallRowBase>(): CallColumn<T> => ({
    key: 'time',
    header: '时间',
    cell: (row) => <span className="num">{formatTime(row.created_at)}</span>,
  }),
  model: <T extends CallRowBase>(): CallColumn<T> => ({ key: 'model', header: '模型', cell: modelName }),
  format: <T extends CallRowBase>(): CallColumn<T> => ({
    key: 'format',
    header: '格式',
    hideOnMobile: true,
    cell: (row) => (
      <span className="call-format">
        {formatLabels[row.format]}
        {row.stream && <small>流式</small>}
      </span>
    ),
  }),
  tokens: <T extends CallRowBase>(): CallColumn<T> => ({
    key: 'tokens',
    header: 'tokens 入/出',
    numeric: true,
    cell: (row) => `${formatTokens(row.usage.input_tokens + row.usage.cache_read_tokens + row.usage.cache_write_tokens)} / ${formatTokens(row.usage.output_tokens)}`,
  }),
  ttft: <T extends CallRowBase>(): CallColumn<T> => ({
    key: 'ttft',
    header: '首字',
    numeric: true,
    hideOnMobile: true,
    cell: (row) => formatMs(row.ttft_ms),
  }),
  outcome: <T extends CallRowBase>(): CallColumn<T> => ({
    key: 'outcome',
    header: '结果',
    cell: (row) => <OutcomeBadge outcome={row.outcome} />,
  }),
}

/** 调用者视角（用户与管理员）：含渠道、Key 与花费。 */
export function summaryColumns<T extends CallSummary>({
  showKey = true,
  showFormat = true,
}: { showKey?: boolean; showFormat?: boolean } = {}): CallColumn<T>[] {
  const columns: CallColumn<T>[] = [
    callColumns.time<T>(),
    callColumns.model<T>(),
    {
      key: 'channel',
      header: '渠道',
      cell: (row) => row.channel?.name ?? <span className="muted">—</span>,
    },
  ]
  if (showKey) {
    columns.push({ key: 'key', header: 'Key', hideOnMobile: true, cell: (row) => row.api_key?.name ?? '—' })
  }
  if (showFormat) columns.push(callColumns.format<T>())
  columns.push(
    callColumns.tokens<T>(),
    callColumns.ttft<T>(),
    { key: 'charged', header: '花费', numeric: true, cell: (row) => formatPoints(row.charged) },
    callColumns.outcome<T>(),
  )
  return columns
}

/** 渠道所有者视角：收入与错误，不含调用者身份。 */
export function channelCallColumns(): CallColumn<ChannelCall>[] {
  return [
    callColumns.time<ChannelCall>(),
    callColumns.model<ChannelCall>(),
    callColumns.format<ChannelCall>(),
    callColumns.tokens<ChannelCall>(),
    callColumns.ttft<ChannelCall>(),
    { key: 'revenue', header: '收入', numeric: true, cell: (row) => <span className="amount-positive">{formatPoints(row.revenue)}</span> },
    callColumns.outcome<ChannelCall>(),
    {
      key: 'error',
      header: '错误',
      hideOnMobile: true,
      cell: (row) =>
        row.error ? (
          <span className="call-error" title={row.error.error_message ?? undefined}>
            <strong>{[row.error.status_code, row.error.error_code].filter(Boolean).join(' · ') || '错误'}</strong>
            {row.error.error_message && <small className="call-error-message">{row.error.error_message}</small>}
          </span>
        ) : (
          '—'
        ),
    },
  ]
}

/**
 * 调用列表：桌面为表格，窄屏为卡片。onSelect 提供时整行可点（主列为真实按钮，键盘可达）。
 * highlight 中的 ID（实时新到）短暂高亮。
 */
export function CallTable<T extends CallRowBase>({
  caption,
  rows,
  columns,
  onSelect,
  empty,
  highlight,
}: {
  caption: string
  rows: T[]
  columns: CallColumn<T>[]
  onSelect?: (row: T) => void
  empty?: ReactNode
  highlight?: ReadonlySet<string>
}) {
  if (rows.length === 0) return <>{empty ?? <EmptyState title="暂无调用" />}</>
  const [primary, ...rest] = columns
  const selectable = (row: T, content: ReactNode) =>
    onSelect ? (
      <button className="call-row-button" onClick={() => onSelect(row)} type="button">
        {content}
        <span className="visually-hidden">查看详情</span>
      </button>
    ) : (
      content
    )
  return (
    <>
      <div className="desktop-table-wrap">
        <table className="data-table call-table">
          <caption className="visually-hidden">{caption}</caption>
          <thead>
            <tr>
              {columns.map((column) => (
                <th className={column.numeric ? 'cell-numeric' : undefined} key={column.key} scope="col">
                  {column.header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr
                className={[onSelect ? 'call-row-selectable' : '', highlight?.has(row.id) ? 'call-row-new' : ''].join(' ')}
                key={row.id}
              >
                {columns.map((column) => (
                  <td className={column.numeric ? 'cell-numeric' : undefined} key={column.key}>
                    {column === primary ? selectable(row, column.cell(row)) : column.cell(row)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="mobile-card-list">
        {rows.map((row) => {
          const body = (
            <>
              <header>
                <div>{primary.cell(row)}</div>
              </header>
              <dl>
                {rest
                  .filter((column) => !column.hideOnMobile)
                  .map((column) => (
                    <div key={column.key}>
                      <dt>{column.header}</dt>
                      <dd>{column.cell(row)}</dd>
                    </div>
                  ))}
              </dl>
            </>
          )
          return onSelect ? (
            <button
              className={`mobile-data-card call-card-button ${highlight?.has(row.id) ? 'call-row-new' : ''}`}
              key={row.id}
              onClick={() => onSelect(row)}
              type="button"
            >
              {body}
            </button>
          ) : (
            <article className="mobile-data-card" key={row.id}>
              {body}
            </article>
          )
        })}
      </div>
    </>
  )
}
