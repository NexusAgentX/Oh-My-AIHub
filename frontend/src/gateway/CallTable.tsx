import type { GatewayCall } from '../api/contracts'
import { DataTable, EmptyState, type Column } from '../ui'
import {
  GatewayStatusBadge,
  formatDate,
  protocolLabels,
  shortID,
  totalTokens,
} from './presentation'

const columns: Column<GatewayCall>[] = [
  {
    key: 'call',
    header: '调用 / 模型',
    primary: true,
    cell: (call) => (
      <>
        <strong>{call.model_id || '受限调用视图'}</strong>
        <small className="mono">{shortID(call.id)}</small>
      </>
    ),
  },
  { key: 'status', header: '状态', cell: (call) => <GatewayStatusBadge status={call.status} /> },
  {
    key: 'protocol',
    header: 'API 格式',
    cell: (call) => (call.protocol ? protocolLabels[call.protocol] : '—'),
  },
  {
    key: 'key',
    header: 'Key',
    hideOnMobile: true,
    cell: (call) => <span className="mono">{call.key_prefix ? `${call.key_prefix}…` : '—'}</span>,
  },
  { key: 'channel', header: '渠道', cell: (call) => call.final_channel_name || '—' },
  { key: 'attempts', header: '尝试', numeric: true, hideOnMobile: true, cell: (call) => call.attempt_count },
  { key: 'tokens', header: 'Tokens', numeric: true, cell: (call) => totalTokens(call) },
  { key: 'time', header: '时间', cell: (call) => formatDate(call.created_at) },
]

export function CallTable({ calls }: { calls: GatewayCall[] }) {
  return (
    <DataTable
      caption="调用记录"
      columns={columns}
      empty={
        <EmptyState
          description="发起调用后，记录会出现在这里。"
          title="暂无调用记录"
        />
      }
      rowHref={(call) => `/calls/${call.id}`}
      rowKey={(call) => call.id}
      rows={calls}
    />
  )
}
