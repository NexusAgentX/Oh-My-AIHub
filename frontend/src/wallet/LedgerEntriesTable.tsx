import type { LedgerEntry } from '../api/contracts'
import { Button, EmptyState, QueryBoundary } from '../ui'
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
  if (entries.length === 0) {
    return <EmptyState title="暂无账本分录" description="积分发生变动后会在这里留下不可修改的记录。" />
  }

  return (
    <>
      <div className="desktop-table-wrap">
        <table className="data-table ledger-table">
          <caption className="visually-hidden">不可变账本分录</caption>
          <thead>
            <tr>
              <th scope="col">时间</th>
              <th scope="col">业务</th>
              <th scope="col">对手方</th>
              <th scope="col">变化</th>
              <th scope="col">分录后余额</th>
              <th scope="col">关联记录</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((entry) => (
              <tr key={entry.id}>
                <td>{formatDate(entry.created_at)}</td>
                <td>
                  <strong>{ledgerEntryLabel(entry)}</strong>
                  <small>{entry.reason}</small>
                </td>
                <td>{ledgerCounterparties(entry)}</td>
                <td className={entry.amount.startsWith('-') ? 'amount-negative' : 'amount-positive'}>
                  {formatPointAmount(entry.amount, true)}
                </td>
                <td>{formatPointAmount(entry.posted_balance_after)}</td>
                <td>
                  <span className="ledger-reference">{entry.reference_type}</span>
                  <small>{entry.reference_id || '—'}</small>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="mobile-card-list">
        {entries.map((entry) => (
          <article className="mobile-data-card" key={entry.id}>
            <header>
              <div>
                <strong>{ledgerEntryLabel(entry)}</strong>
                <span>{formatDate(entry.created_at)}</span>
              </div>
              <strong className={entry.amount.startsWith('-') ? 'amount-negative' : 'amount-positive'}>
                {formatPointAmount(entry.amount, true)}
              </strong>
            </header>
            <dl>
              <div><dt>原因</dt><dd>{entry.reason}</dd></div>
              <div><dt>对手方</dt><dd>{ledgerCounterparties(entry)}</dd></div>
              <div><dt>分录后余额</dt><dd>{formatPointAmount(entry.posted_balance_after)}</dd></div>
              <div><dt>关联记录</dt><dd>{entry.reference_type} · {entry.reference_id || '—'}</dd></div>
            </dl>
          </article>
        ))}
      </div>
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

/** 钱包分录面板：自带取数、加载/错误/空态与「加载更早分录」，钱包与余额不足页共用。 */
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
