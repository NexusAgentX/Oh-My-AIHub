import { Badge, Card, DataTable, EmptyState, QueryBoundary } from '../ui'
import { formatDateTime } from './opsFormat'
import { useOpsInspectionsQuery, useOpsTrialSummaryQuery } from './queries'

const triggerLabels: Record<string, string> = { startup: '启动', periodic: '周期', manual: '手动' }

function Check({ ok }: { ok: boolean }) {
  return <Badge tone={ok ? 'success' : 'danger'}>{ok ? '正常' : '异常'}</Badge>
}

/** 试用证据摘要与跨模块巡检历史：只展示聚合计数与状态，不推断参与者身份。 */
export function OpsEvidence() {
  const trial = useOpsTrialSummaryQuery()
  const inspections = useOpsInspectionsQuery()
  return (
    <div className="ops-stack">
      <QueryBoundary errorFallback="试用证据加载失败" query={trial}>
        {(data) => (
          <Card title="试用证据摘要">
            <dl className="ops-list">
              <div><dt>非管理员账户</dt><dd className="num">{data.non_admin_accounts}</dd></div>
              <div><dt>已发布渠道 / 通过报价 / 活跃 Key</dt><dd className="num">{data.published_channels} / {data.passed_offers} / {data.active_api_keys}</dd></div>
              <div><dt>调用成功 / 失败 / 不完整</dt><dd className="num">{data.calls_succeeded} / {data.calls_failed} / {data.calls_incomplete}</dd></div>
              <div><dt>首次调用</dt><dd>{data.first_call_at ? formatDateTime(data.first_call_at) : '尚无调用'}</dd></div>
              <div><dt>C2C 挂单 / 完成交易 / 争议中</dt><dd className="num">{data.c2c_open_orders} / {data.c2c_released_trades} / {data.c2c_disputed_open}</dd></div>
              <div><dt>零和状态</dt><dd><Check ok={data.ledger_zero_sum_ok} /></dd></div>
              <div><dt>巡检通过</dt><dd className="num">{data.inspection_pass_count} / {data.inspection_total_count}</dd></div>
            </dl>
          </Card>
        )}
      </QueryBoundary>
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
    </div>
  )
}
