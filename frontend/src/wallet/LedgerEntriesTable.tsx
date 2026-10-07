import type { LedgerEntry } from '../api/types'
import { Button, DataTable, EmptyState, QueryBoundary, type Column } from '../ui'
import {
  formatPointAmount,
  ledgerCounterparties,
  ledgerEntryLabel,
} from './presentation'
import { useLedgerEntriesQuery } from './queries'

function formatDate(value: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))
}

const columns: Column<LedgerEntry>[] = [
  {
    key: 'time',
    header: '时间',
    cell: (entry) => <span className="ledger-time">{formatDate(entry.created_at)}</span>,
  },
  {
    key: 'kind',
    header: '业务',
    primary: true,
    cell: (entry) => (
      <span className="ledger-kind">
        <strong>{ledgerEntryLabel(entry)}</strong>
        <small>{entry.reason}</small>
      </span>
    ),
  },
  { key: 'counterparty', header: '对手方', cell: (entry) => ledgerCounterparties(entry) },
  {
    key: 'amount',
    header: '变化',
    numeric: true,
    cell: (entry) => (
      <span className={entry.amount.startsWith('-') ? 'amount-negative' : 'amount-positive'}>
        {formatPointAmount(entry.amount, true)}
      </span>
    ),
  },
  {
    key: 'balance',
    header: '分录后余额',
    numeric: true,
    cell: (entry) => formatPointAmount(entry.posted_balance_after),
  },
  {
    key: 'reference',
    header: '关联记录',
    hideOnMobile: true,
    cell: (entry) => (
      <span className="ledger-reference">
        {entry.reference_type}
        <small>{entry.reference_id || '—'}</small>
      </span>
    ),
  },
]

export function LedgerEntriesTable({
  entries,
  loadingMore,
  nextBefore,
  onLoadMore,
}: {
  entries: LedgerEntry[]
  loadingMore: boolean
  nextBefore: string
  onLoadMore: () => void
}) {
  return (
    <>
      <DataTable
        caption="不可变账本分录"
        columns={columns}
        empty={<EmptyState title="暂无账本分录" description="积分发生变动后会在这里留下不可修改的记录。" />}
        rowKey={(entry) => entry.id}
        rows={entries}
      />
      {nextBefore && (
        <div className="table-pagination">
          <Button disabled={loadingMore} onClick={onLoadMore} variant="secondary">
            {loadingMore ? '正在加载' : '加载更早分录'}
          </Button>
        </div>
      )}
    </>
  )
}

/** 钱包分录面板：自带取数、加载/错误/空态与「加载更早分录」，钱包页使用。 */
export function LedgerEntriesPanel() {
  const query = useLedgerEntriesQuery()
  return (
    <QueryBoundary errorFallback="账本分录加载失败" query={query}>
      {(data) => (
        <LedgerEntriesTable
          entries={data.pages.flatMap((page) => page.entries)}
          loadingMore={query.isFetchingNextPage}
          nextBefore={query.hasNextPage ? 'more' : ''}
          onLoadMore={() => void query.fetchNextPage()}
        />
      )}
    </QueryBoundary>
  )
}
