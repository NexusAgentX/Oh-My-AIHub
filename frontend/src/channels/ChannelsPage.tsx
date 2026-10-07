import { useMemo } from 'react'
import type { Channel } from '../api/contracts'
import { formatPoints } from '../gateway/presentation'
import {
  ButtonLink,
  Card,
  DataTable,
  EmptyState,
  Icon,
  Metric,
  MetricGrid,
  PageHeader,
  QueryBoundary,
  type Column,
} from '../ui'
import { ChannelStateBadge, formatDate, ratingText } from './presentation'
import { useChannelsQuery } from './queries'
import { sumIncome, summarizeChannel } from './summary'

type Row = { channel: Channel; summary: ReturnType<typeof summarizeChannel> }

const columns: Column<Row>[] = [
  {
    key: 'name',
    header: '渠道',
    primary: true,
    cell: ({ channel }) => (
      <span className="sharing-name">
        <strong>{channel.display_name}</strong>
        {!channel.credential_configured && channel.status !== 'deleted' && <small>凭据未配置</small>}
      </span>
    ),
  },
  { key: 'status', header: '状态', cell: ({ channel }) => <ChannelStateBadge status={channel.status} /> },
  {
    key: 'offers',
    header: '报价',
    numeric: true,
    cell: ({ summary }) => (
      <span className="sharing-name">
        <span>{summary.enabledCount} / {summary.offers.length}</span>
        <small>可用 {summary.eligibleCount}</small>
      </span>
    ),
  },
  {
    key: 'income',
    header: '累计收入',
    numeric: true,
    cell: ({ summary }) => (summary.income === null ? '—' : `${formatPoints(summary.income)} 积分`),
  },
  { key: 'rating', header: '评分', cell: ({ channel }) => ratingText(channel.average_rating, channel.rating_count) },
  { key: 'updated', header: '更新', cell: ({ channel }) => formatDate(channel.updated_at) },
]

const createLink = (
  <ButtonLink icon={<Icon name="plus" />} to="/channels/new" variant="primary">上架渠道</ButtonLink>
)

export function ChannelsPage() {
  const query = useChannelsQuery()

  return (
    <>
      <PageHeader actions={createLink} title="我的渠道" />
      <QueryBoundary errorFallback="渠道加载失败" query={query}>
        {(channels) => <ChannelList channels={channels} />}
      </QueryBoundary>
    </>
  )
}

function ChannelList({ channels }: { channels: Channel[] }) {
  const rows = useMemo<Row[]>(
    () => channels
      .map((channel) => ({ channel, summary: summarizeChannel(channel) }))
      .sort((a, b) => Number(a.channel.status === 'deleted') - Number(b.channel.status === 'deleted')),
    [channels],
  )
  const live = rows.filter(({ channel }) => channel.status !== 'deleted')
  const income = sumIncome(rows.map(({ summary }) => summary.income))

  return (
    <>
      <MetricGrid label="渠道概况">
        <Metric label="渠道" value={live.length} />
        <Metric label="已发布" value={live.filter(({ channel }) => channel.status === 'published').length} />
        <Metric
          hint="已启用且通过校验"
          label="可用报价"
          value={live.reduce((sum, { summary }) => sum + summary.eligibleCount, 0)}
        />
        <Metric
          hint="积分"
          label="累计收入"
          tone="accent"
          value={income === null ? '—' : formatPoints(income)}
        />
      </MetricGrid>
      <Card flush title="渠道列表">
        <DataTable
          caption="我的渠道"
          columns={columns}
          empty={(
            <EmptyState
              action={createLink}
              description="填入中转站的 Base URL 与 API Key，选择模型后即可校验并发布。"
              title="还没有渠道"
            />
          )}
          rowHref={({ channel }) => `/channels/${channel.id}`}
          rowKey={({ channel }) => channel.id}
          rows={rows}
        />
      </Card>
    </>
  )
}
