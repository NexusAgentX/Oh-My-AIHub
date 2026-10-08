import { Badge, Card, DataTable, EmptyState, QueryBoundary } from '../ui'
import { formatDateTime } from './opsFormat'
import { useOpsInspectionsQuery } from './queries'

const triggerLabels: Record<string, string> = { startup: '启动', periodic: '周期', manual: '手动' }

function Check({ ok }: { ok: boolean }) {
  return <Badge tone={ok ? 'success' : 'danger'}>{ok ? '正常' : '异常'}</Badge>
}

/** 跨模块巡检历史：只展示聚合状态与差异计数。 */
export function OpsInspections() {
  const inspections = useOpsInspectionsQuery()
  return (
    <Card flush title="巡检历史">
      <QueryBoundary errorFallback="巡检历史加载失败" query={inspections}>
        {(rows) => (
          <DataTable
            caption="跨模块巡检历史"
            columns={[
              {
                key: 'time',
                header: '时间',
                primary: true,
                cell: (row) => formatDateTime(row.checked_at),
              },
              {
                key: 'by',
                header: '触发',
                cell: (row) => triggerLabels[row.triggered_by] ?? row.triggered_by,
              },
              { key: 'zero', header: '零和', cell: (row) => <Check ok={row.zero_sum_ok} /> },
              { key: 'projection', header: '投影', cell: (row) => <Check ok={row.projection_ok} /> },
              { key: 'settlement', header: '调用结算', cell: (row) => <Check ok={row.call_settlement_ok} /> },
              { key: 'c2c', header: 'C2C 一致', cell: (row) => <Check ok={row.c2c_consistency_ok} /> },
              {
                key: 'diff',
                header: '差异',
                numeric: true,
                cell: (row) =>
                  row.successful_calls_without_settlement +
                  row.settlements_without_ledger_transaction +
                  row.c2c_quantity_violations +
                  row.c2c_hold_violations,
              },
            ]}
            empty={<EmptyState title="还没有巡检记录" />}
            rowKey={(row) => row.id}
            rows={rows}
          />
        )}
      </QueryBoundary>
    </Card>
  )
}
