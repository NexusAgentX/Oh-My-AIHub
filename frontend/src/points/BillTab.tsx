import { useState } from 'react'
import { Link } from 'react-router-dom'
import type { PointsEntry, PointsPeriod, TransactionType } from '../api/types'
import { useKeys } from '../keys/queries'
import { amountSign, formatPoints } from '../money/format'
import { currentMonth, formatTime, monthRange } from '../money/time'
import { BarChart, Button, DataTable, EmptyState, Icon, InlineError, QueryBoundary, Segmented, type Column } from '../ui'
import { transactionTypeLabels } from './c2c'
import { downloadEntriesCsv, usePointsEntries, usePointsPeriod, type EntryParams } from './queries'
import { reconcileItems } from './reconcile'
import { enumField, stringField, useQueryOverlay, useUrlState } from '../ui/urlState'
import { useShanghaiDay } from '../ui/clock'
import { Pager } from '../ui/Pager'
import { errorMessage } from '../api/query'

const billSchema = { month: { defaultValue: '', parse: (value: string) => /^\d{4}-(0[1-9]|1[0-2])$/.test(value) ? value : '' }, type: enumField(['', ...Object.keys(transactionTypeLabels) as TransactionType[]], ''), api_key_id: stringField(), group: enumField(['', 'day', 'key'] as const, ''), transaction_id: stringField() }

function Signed({ value }: { value: string }) {
  const sign = amountSign(value)
  return (
    <span className={`num ${sign > 0 ? 'amount-positive' : sign < 0 ? 'amount-negative' : ''}`}>
      {formatPoints(value, { signed: true })}
    </span>
  )
}

function relatedLink(entry: PointsEntry) {
  if (!entry.related) return null
  if (entry.related.type === 'c2c_trade') return `/points/trades/${entry.related.id}`
  if (entry.related.type === 'call') return `/calls/${entry.related.id}`
  return null
}

const columns = (onCall: (id: string) => void): Column<PointsEntry>[] => [
  { key: 'time', header: '时间', cell: (entry) => <span className="num">{formatTime(entry.created_at)}</span> },
  { key: 'type', header: '类型', primary: true, cell: (entry) => entry.type === 'api_call' ? (amountSign(entry.amount) > 0 ? '渠道收入' : '调用支出') : transactionTypeLabels[entry.type] },
  {
    key: 'reason',
    header: '说明',
    cell: (entry) => {
      const href = relatedLink(entry)
      const text = [entry.reason, entry.api_key?.name].filter(Boolean).join(' · ') || '—'
      if (entry.related?.type === 'call') return <Button variant="quiet" size="sm" onClick={() => onCall(entry.related!.id)}>{text}</Button>
      return href ? <Link to={href}>{text}</Link> : text
    },
  },
  { key: 'amount', header: '变动', numeric: true, cell: (entry) => <Signed value={entry.amount} /> },
  { key: 'after', header: '变动后余额', numeric: true, hideOnMobile: true, cell: (entry) => formatPoints(entry.balance_after) },
]

/** 期间对账条：期初 → 调用支出 / 渠道收入 / C2C 买入 / C2C 卖出 / 调账与核销 → 期末。 */
export function ReconcileBar({ period }: { period: PointsPeriod }) {
  return (
    <div className="recon-bar" aria-label="本期对账">
      <div>
        <span>期初</span>
        <strong className="num">{formatPoints(period.opening_balance)}</strong>
      </div>
      <Icon name="chevron-right" />
      {reconcileItems(period).map((item) => (
        <div key={item.key}>
          <span>{item.label}</span>
          <Signed value={item.value} />
        </div>
      ))}
      <Icon name="chevron-right" />
      <div>
        <span>期末</span>
        <strong className="num">{formatPoints(period.closing_balance)}</strong>
      </div>
      {amountSign(period.difference) !== 0 && <span className="badge badge-danger">差额 {formatPoints(period.difference)}</span>}
    </div>
  )
}

function Reconciliation({ from, to }: { from?: string; to?: string }) {
  const period = usePointsPeriod(from, to)
  if (period.isError) return <InlineError>{errorMessage(period.error, '本期对账加载失败')}<Button variant="quiet" size="sm" onClick={() => void period.refetch()}>重试核对</Button></InlineError>
  if (!period.data) return <p role="status">正在核对本期账单…</p>
  return <ReconcileBar period={period.data} />
}

export function BillTab() {
  const [filters, update] = useUrlState(billSchema)
  const [, setCall] = useQueryOverlay('call')
  const day = useShanghaiDay()
  const month = filters.month || day.slice(0, 7), type = filters.type, keyId = filters.api_key_id
  const mode = filters.group || 'entries'
  const transactionID = filters.transaction_id || undefined
  const [exportError, setExportError] = useState('')
  const [exporting, setExporting] = useState(false)
  const keys = useKeys()
  const range = monthRange(month)
  const params: EntryParams = { ...(transactionID?{transaction_id:transactionID}:range), type: type || undefined, api_key_id: keyId || undefined }
  const entries = usePointsEntries({ ...params, group: mode === 'entries' ? undefined : mode })
  const rows = entries.data?.pages.flatMap((page) => page.items) ?? []
  const summary = entries.data?.pages[0]?.summary

  const exportCsv = async () => {
    setExportError('')
    setExporting(true)
    try {
      await downloadEntriesCsv(params)
    } catch (caught) {
      setExportError(caught instanceof Error ? caught.message : '导出失败，请稍后重试')
    } finally {
      setExporting(false)
    }
  }

  return (
    <div className="bill-tab" aria-busy={entries.isPlaceholderData}>
      {!transactionID&&<Reconciliation from={range.from} to={range.to} />}
 {transactionID&&<div className="call-drill"><span>交易 {transactionID}</span><Button variant="quiet" onClick={() => update({ transaction_id: '' })}>清除交易筛选</Button></div>}
      <div className="call-filters">
        <label className="filter-select">
          <span className="visually-hidden">月份</span>
          <input disabled={Boolean(transactionID)} aria-label="月份" className="input" onChange={event => update({ month: event.target.value === currentMonth() ? '' : event.target.value })} type="month" value={month} />
        </label>
        <label className="filter-select">
          <span className="visually-hidden">类型</span>
          <select aria-label="类型" className="input select-input" onChange={event => update({ type: event.target.value as TransactionType | '' })} value={type}>
            <option value="">类型：全部</option>
            {(Object.keys(transactionTypeLabels) as TransactionType[]).map((item) => (
              <option key={item} value={item}>
                {transactionTypeLabels[item]}
              </option>
            ))}
          </select>
        </label>
        <label className="filter-select">
          <span className="visually-hidden">Key</span>
          <select aria-label="Key" className="input select-input" onChange={event => update({ api_key_id: event.target.value })} value={keyId}>
            <option value="">Key：全部</option>
            {(keys.data?.items ?? []).map((key) => (
              <option key={key.id} value={key.id}>
                {key.name}
              </option>
            ))}
          </select>
        </label>
        <Segmented
          label="汇总方式"
          onChange={value => update({ group: value === 'entries' ? '' : value })}
          options={[
            { key: 'entries', label: '逐笔' },
            { key: 'day', label: '按天' },
            { key: 'key', label: '按 Key' },
          ]}
          value={mode}
        />
        <Button icon={<Icon name="download" />} loading={exporting} onClick={() => void exportCsv()} size="sm" type="button" variant="secondary">
          导出 CSV
        </Button>
      </div>
      <InlineError>{exportError}</InlineError>
      <QueryBoundary errorFallback="账单加载失败" query={{ ...entries, data: entries.data ? rows : undefined }}>
        {(data) => {
          if (mode === 'day') {
            return summary && summary.by_day.length > 0 ? (
              <BarChart
                data={summary.by_day.map((day) => ({
                  key: day.date,
                  label: day.date.slice(5),
                  value: Math.abs(Number(day.net)),
                  display: `收入 ${formatPoints(day.income)} · 支出 ${formatPoints(day.spend)}`,
                }))}
                label="按天汇总"
              />
            ) : (
              <EmptyState mascot={false} title={transactionID?"没有匹配的账单":"本月没有账单"} />
            )
          }
          if (mode === 'key') {
            return summary && summary.by_key.length > 0 ? (
              <BarChart
                data={summary.by_key.map((row) => ({
                  key: row.api_key?.id ?? 'none',
                  label: row.api_key?.name ?? '无 Key',
                  value: Math.abs(Number(row.spend)),
                  display: `${formatPoints(row.spend)} · ${row.entries} 笔`,
                }))}
                label="按 Key 汇总"
                orientation="horizontal"
              />
            ) : (
              <EmptyState mascot={false} title="本月没有调用支出" />
            )
          }
          return (
            <div className="panel panel-flush">
              <DataTable caption="账单" columns={columns(id => setCall(id))} empty={<EmptyState title={transactionID?"没有匹配的账单":"本月没有账单"} />} rowKey={(entry) => entry.id} rows={data} />
              <Pager hasMore={Boolean(entries.hasNextPage)} loading={entries.isFetchingNextPage} limited={(entries.data?.pages.length ?? 0) >= 10}
                error={entries.isFetchNextPageError ? errorMessage(entries.error, '加载下一页失败') : undefined} onMore={() => void entries.fetchNextPage()} />
            </div>
          )
        }}
      </QueryBoundary>
    </div>
  )
}
