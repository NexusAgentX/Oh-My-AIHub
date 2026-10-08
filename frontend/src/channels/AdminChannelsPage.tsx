import { useMemo, useState } from 'react'
import { ButtonLink, Card, DataTable, EmptyState, Metric, MetricGrid, PageHeader, QueryBoundary, Segmented, Toolbar } from '../ui'
import { AdminChannelBadge } from './AdminChannelBadge'
import { useAdminChannelsQuery } from './adminQueries'
import { formatDate, ratingText } from '../gateway/presentation'

type Filter = 'all' | 'published' | 'paused' | 'no_credential'

const filters: Array<{ key: Filter; label: string }> = [
  { key: 'all', label: '全部' },
  { key: 'published', label: '已发布' },
  { key: 'paused', label: '已暂停' },
  { key: 'no_credential', label: '凭据缺失' },
]

export function AdminChannelsPage() {
  const query = useAdminChannelsQuery()
  const [filter, setFilter] = useState<Filter>('all')
  const channels = query.data
  const metrics = useMemo(() => {
    const items = channels ?? []
    return {
      total: items.length,
      published: items.filter((item) => item.status === 'published').length,
      paused: items.filter((item) => item.status === 'paused').length,
      missingCredential: items.filter((item) => item.status !== 'deleted' && !item.credential_configured).length,
    }
  }, [channels])

  return (
    <>
      <PageHeader title="渠道治理" />
      <MetricGrid label="渠道治理概况">
        <Metric hint="含历史状态" label="全部渠道" value={metrics.total} />
        <Metric hint="生命周期状态" label="已发布" tone="accent" value={metrics.published} />
        <Metric hint="待处理" label="已暂停" tone="warm" value={metrics.paused} />
        <Metric hint="不可路由" label="凭据缺失" value={metrics.missingCredential} />
      </MetricGrid>
      <Toolbar>
        <Segmented label="渠道筛选" onChange={setFilter} options={filters} value={filter} />
      </Toolbar>
      <Card flush>
        <QueryBoundary errorFallback="渠道治理列表加载失败" query={query}>
          {(items) => (
            <DataTable
              caption="渠道治理列表"
              columns={[
                {
                  key: 'channel',
                  header: '渠道',
                  primary: true,
                  cell: (item) => (
                    <>
                      <strong>{item.display_name}</strong>
                      <small>{item.credential_configured ? `凭据 v${item.credential_version}` : '凭据缺失'}</small>
                    </>
                  ),
                },
                { key: 'owner', header: '共享者', cell: (item) => item.owner_display_name },
                { key: 'status', header: '状态', cell: (item) => <AdminChannelBadge status={item.status} /> },
                {
                  key: 'offers',
                  header: '报价',
                  numeric: true,
                  cell: (item) => item.offers.filter((offer) => offer.status !== 'deleted').length,
                },
                { key: 'rating', header: '评分', cell: (item) => ratingText(item.average_rating, item.rating_count) },
                { key: 'updated', header: '更新', cell: (item) => formatDate(item.updated_at) },
                {
                  key: 'actions',
                  header: '操作',
                  cell: (item) => (
                    <ButtonLink size="sm" to={`/admin/channels/${item.id}`}>
                      治理
                    </ButtonLink>
                  ),
                },
              ]}
              empty={<EmptyState title="没有渠道" />}
              rowKey={(item) => item.id}
              rows={items.filter((item) => {
                if (filter === 'published') return item.status === 'published'
                if (filter === 'paused') return item.status === 'paused'
                if (filter === 'no_credential') return item.status !== 'deleted' && !item.credential_configured
                return true
              })}
            />
          )}
        </QueryBoundary>
      </Card>
    </>
  )
}
