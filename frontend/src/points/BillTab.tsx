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

type SummaryMode = 'entries' | 'day' | 'key'

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
  if (entry.related.type === 'call') return `/usage?call=${entry.related.id}`
  return null
}

const columns: Column<PointsEntry>[] = [
  { key: 'time', header: '时间', cell: (entry) => <span className="num">{formatTime(entry.created_at)}</span> },
  { key: 'type', header: '类型', primary: true, cell: (entry) => transactionTypeLabels[entry.type] },
  {
    key: 'reason',
    header: '说明',
    cell: (entry) => {
      const href = relatedLink(entry)
      const text = [entry.reason, entry.api_key?.name].filter(Boolean).join(' · ') || '—'
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
  if (!period.data) return null
  return <ReconcileBar period={period.data} />
}

export function BillTab() {
  const [month, setMonth] = useState(currentMonth())
  const [type, setType] = useState<'' | TransactionType>('')
  const [keyId, setKeyId] = useState('')
  const [mode, setMode] = useState<SummaryMode>('entries')
  const [exportError, setExportError] = useState('')
  const [exporting, setExporting] = useState(false)
  const keys = useKeys()
  const range = monthRange(month)
  const params: EntryParams = { ...range, type: type || undefined, api_key_id: keyId || undefined }
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
    <div className="bill-tab">
      <Reconciliation from={range.from} to={range.to} />
      <div className="call-filters">
        <label className="filter-select">
          <span className="visually-hidden">月份</span>
          <input aria-label="月份" className="input" onChange={(event) => setMonth(event.target.value)} type="month" value={month} />
        </label>
        <label className="filter-select">
          <span className="visually-hidden">类型</span>
          <select aria-label="类型" className="input select-input" onChange={(event) => setType(event.target.value as TransactionType | '')} value={type}>
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
          <select aria-label="Key" className="input select-input" onChange={(event) => setKeyId(event.target.value)} value={keyId}>
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
          onChange={setMode}
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
              <EmptyState title="本月没有账单" />
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
              <EmptyState title="本月没有调用支出" />
            )
          }
          return (
            <div className="panel panel-flush">
              <DataTable caption="账单" columns={columns} empty={<EmptyState title="本月没有账单" />} rowKey={(entry) => entry.id} rows={data} />
              {entries.hasNextPage && (
                <div className="table-pagination">
                  <Button loading={entries.isFetchingNextPage} onClick={() => void entries.fetchNextPage()} size="sm" type="button" variant="secondary">
                    加载更多
                  </Button>
                </div>
              )}
            </div>
          )
        }}
      </QueryBoundary>
    </div>
  )
}
