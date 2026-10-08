import { useSearchParams } from 'react-router-dom'
import { Badge, Card, DataTable, PageHeader, QueryBoundary, SelectField, Toolbar, type Column } from '../ui'
import { LoadMore } from './components'
import { formatDateTime, formatPoints } from './format'
import { useAdminAccounts, useAdminCalls } from './queries'
import type { AdminCall } from './types'

const columns: Column<AdminCall>[] = [
  {
    key: 'time',
    header: '时间',
    primary: true,
    cell: (call) => (
      <>
        <strong>{formatDateTime(call.created_at)}</strong>
        <small className="mono">{call.id.slice(0, 8)}</small>
      </>
    ),
  },
  { key: 'user', header: '用户', cell: (call) => call.account.display_name },
  { key: 'model', header: '模型', cell: (call) => <span className="mono">{call.model_id ?? call.requested_model}</span> },
  { key: 'channel', header: '渠道', cell: (call) => call.channel?.name ?? '—' },
  {
    key: 'outcome',
    header: '结果',
    cell: (call) => (
      <Badge tone={call.outcome.startsWith('succeeded') ? 'success' : call.outcome === 'in_progress' ? 'info' : 'danger'}>
        {call.outcome}
      </Badge>
    ),
  },
  { key: 'charged', header: '花费', numeric: true, cell: (call) => formatPoints(call.charged) },
]

export function CallsPage() {
  const [params, setParams] = useSearchParams()
  const accountID = params.get('account_id') ?? ''
  const channelID = params.get('channel_id') ?? ''
  const accounts = useAdminAccounts({})
  const calls = useAdminCalls({ account_id: accountID || undefined, channel_id: channelID || undefined })
  const setFilter = (key: string, value: string) =>
    setParams(
      (current) => {
        const next = new URLSearchParams(current)
        if (value) next.set(key, value)
        else next.delete(key)
        return next
      },
      { replace: true },
    )
  return (
    <>
      <PageHeader title="调用" />
      <Toolbar>
        <SelectField label="用户" onChange={(event) => setFilter('account_id', event.target.value)} value={accountID}>
          <option value="">全部用户</option>
          {accounts.data?.map((account) => (
            <option key={account.id} value={account.id}>
              {account.display_name}
            </option>
          ))}
        </SelectField>
        {channelID && <Badge tone="info">渠道 {channelID.slice(0, 8)}</Badge>}
      </Toolbar>
      <Card flush>
        <QueryBoundary query={calls}>
          {(rows) => (
            <>
              <DataTable
                caption="全部调用"
                columns={columns}
                empty={<p className="muted-copy empty-pad">没有调用</p>}
                rowKey={(call) => call.id}
                rows={rows}
              />
              <LoadMore hasMore={Boolean(calls.hasNextPage)} loading={calls.isFetchingNextPage} onLoad={() => void calls.fetchNextPage()} />
            </>
          )}
        </QueryBoundary>
      </Card>
    </>
  )
}
