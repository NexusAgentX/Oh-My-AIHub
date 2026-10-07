import { Button, Card, CountBadge, Icon, PageHeader, QueryBoundary } from '../ui'
import { CallTable } from './CallTable'
import { useGatewayCallsQuery } from './queries'

export function CallsPage() {
  const query = useGatewayCallsQuery(100)
  return (
    <>
      <PageHeader
        actions={
          <Button
            icon={<Icon name="refresh" />}
            loading={query.isFetching}
            onClick={() => void query.refetch()}
            variant="secondary"
          >
            刷新
          </Button>
        }
        title="调用记录"
      />
      <QueryBoundary errorFallback="调用记录加载失败" query={query}>
        {(calls) => (
          <Card
            actions={<CountBadge>{calls.length}</CountBadge>}
            flush
            title="最近 100 笔"
          >
            <CallTable calls={calls} />
          </Card>
        )}
      </QueryBoundary>
    </>
  )
}
