import { formatRate } from '../gateway/presentation'
import { Card, DataTable, EmptyState, Metric, MetricGrid, QueryBoundary } from '../ui'
import { formatPointAmount } from '../wallet/presentation'
import { useProviderIncomeQuery } from './queries'

export function OpsProviders({ hours }: { hours: number }) {
  const query = useProviderIncomeQuery(hours)
  return (
    <QueryBoundary errorFallback="共享者收入加载失败" query={query}>
      {(snapshot) => (
        <div className="ops-stack">
          <MetricGrid label="共享者收入">
            <Metric
              label="共享者总收入"
              tone="accent"
              value={formatPointAmount(snapshot.total_income)}
            />
            <Metric label="外部消费收入" value={formatPointAmount(snapshot.other_consumer_income)} />
            <Metric label="自有调用收入" value={formatPointAmount(snapshot.own_usage_income)} />
            <Metric label="活跃共享者" value={snapshot.active_providers} />
          </MetricGrid>
          <Card flush title="按共享者">
            <DataTable
              caption="按共享者收入"
              columns={[
                {
                  key: 'name',
                  header: '共享者',
                  primary: true,
                  cell: (row) => <strong>{row.display_name}</strong>,
                },
                {
                  key: 'total',
                  header: '总收入',
                  numeric: true,
                  cell: (row) => formatPointAmount(row.total_income),
                },
                {
                  key: 'other',
                  header: '外部消费',
                  numeric: true,
                  cell: (row) => formatPointAmount(row.other_consumer_income),
                },
                {
                  key: 'own',
                  header: '自有调用',
                  numeric: true,
                  cell: (row) => formatPointAmount(row.own_usage_income),
                },
                {
                  key: 'rate',
                  header: '成功率',
                  numeric: true,
                  cell: (row) => formatRate(row.success_rate),
                },
              ]}
              empty={<EmptyState title="窗口内没有共享者收入" />}
              rowKey={(row) => row.account_id}
              rows={snapshot.providers}
            />
          </Card>
        </div>
      )}
    </QueryBoundary>
  )
}
