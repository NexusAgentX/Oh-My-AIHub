import { Link } from 'react-router-dom'
import { errorMessage } from '../api/query'
import type { Channel } from '../api/types'
import { formatPoints } from '../money/format'
import { Badge, ButtonLink, EmptyState, Icon, InlineError, PageHeader, QueryBoundary, Switch } from '../ui'
import { channelStatus } from './channelForm'
import { useChannels, useUpdateChannel } from './queries'

function ChannelCard({ channel }: { channel: Channel }) {
  const update = useUpdateChannel()
  const status = channelStatus(channel)
  const listed = channel.status === 'listed'
  return (
    <article className="channel-card">
      <header>
        <Link className="channel-card-name" to={`/channels/${channel.id}`}>
          <strong>{channel.name}</strong>
          <Icon name="chevron-right" />
        </Link>
        <Badge tone={status.tone}>{status.label}</Badge>
      </header>
      {channel.status === 'suspended' && channel.suspended_reason && (
        <p className="channel-card-reason">{channel.suspended_reason}</p>
      )}
      <dl className="channel-card-stats">
        <div>
          <dt>模型</dt>
          <dd className="num">{channel.models.filter((model) => model.enabled).length}</dd>
        </div>
        <div>
          <dt>今日调用</dt>
          <dd className="num">{channel.today.calls.toLocaleString('zh-CN')}</dd>
        </div>
        <div>
          <dt>今日收入</dt>
          <dd className="num amount-positive">{formatPoints(channel.today.revenue)}</dd>
        </div>
      </dl>
      <footer>
        <Switch
          checked={listed}
          disabled={channel.status === 'suspended' || update.isPending}
          label={listed ? '已上架' : '未上架'}
          onChange={(checked) => update.mutate({ id: channel.id, body: { status: checked ? 'listed' : 'unlisted' } })}
        />
      </footer>
      <InlineError>{update.isError ? errorMessage(update.error, '操作失败，请重试') : ''}</InlineError>
    </article>
  )
}

export function ChannelsPage() {
  const channels = useChannels()
  return (
    <>
      <PageHeader
        actions={
          <ButtonLink icon={<Icon name="plus" />} to="/channels/new" variant="primary">
            添加渠道
          </ButtonLink>
        }
        title="我的渠道"
      />
      <QueryBoundary
        empty={
          <EmptyState
            action={
              <ButtonLink icon={<Icon name="plus" />} to="/channels/new" variant="primary">
                添加渠道
              </ButtonLink>
            }
            title="还没有渠道"
          />
        }
        errorFallback="渠道加载失败"
        isEmpty={(data) => data.items.length === 0}
        query={channels}
      >
        {(data) => (
          <div className="channel-grid">
            {data.items.map((channel) => (
              <ChannelCard channel={channel} key={channel.id} />
            ))}
          </div>
        )}
      </QueryBoundary>
    </>
  )
}
