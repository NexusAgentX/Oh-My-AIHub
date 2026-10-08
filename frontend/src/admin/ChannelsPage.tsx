import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  Badge,
  Button,
  ButtonLink,
  Card,
  DataTable,
  PageHeader,
  QueryBoundary,
  SearchInput,
  Segmented,
  Toolbar,
  type BadgeTone,
  type Column,
} from '../ui'
import { ChannelStatsPanel } from '../channels/ChannelStatsPanel'
import { ConfirmActionDialog, DetailList, LoadMore } from './components'
import { formatCount, formatDateTime, formatPoints, formatRatio } from './format'
import { useAdminChannel, useAdminChannels, useChannelSuspension } from './queries'
import type { AdminChannel, ChannelEvent, ChannelStatus } from './types'

const statusLabels: Record<ChannelStatus, [string, BadgeTone]> = {
  listed: ['在线', 'success'],
  unlisted: ['已下架', 'neutral'],
  suspended: ['被管理员下架', 'danger'],
}

export function ChannelStatusBadge({ channel }: { channel: Pick<AdminChannel, 'status' | 'cooldown_until'> }) {
  const [label, tone] = statusLabels[channel.status]
  const cooling = channel.status === 'listed' && channel.cooldown_until && new Date(channel.cooldown_until) > new Date()
  return <Badge tone={cooling ? 'warning' : tone}>{cooling ? '冷却中' : label}</Badge>
}

const formatLabels: Record<string, string> = {
  openai_chat: 'OpenAI Chat',
  openai_responses: 'Responses',
  anthropic: 'Anthropic',
  gemini: 'Gemini',
}

const eventLabels: Record<ChannelEvent['kind'], string> = {
  cooldown_started: '开始冷却',
  cooldown_ended: '冷却结束',
  limit_reached: '触发限额',
  suspended: '被下架',
  unsuspended: '恢复',
  listed: '上架',
  unlisted: '下架',
  test_failed: '测试失败',
}

const columns: Column<AdminChannel>[] = [
  {
    key: 'name',
    header: '渠道',
    primary: true,
    cell: (channel) => (
      <>
        <strong>{channel.name}</strong>
        <small>{channel.base_url}</small>
      </>
    ),
  },
  { key: 'owner', header: '所有者', cell: (channel) => channel.owner.display_name },
  { key: 'status', header: '状态', cell: (channel) => <ChannelStatusBadge channel={channel} /> },
  { key: 'models', header: '模型', numeric: true, cell: (channel) => channel.models.length },
  {
    key: 'calls',
    header: '今日调用 / 成功率',
    numeric: true,
    cell: (channel) => `${formatCount(channel.today.calls)} / ${formatRatio(channel.today.success_rate)}`,
  },
  { key: 'revenue', header: '今日收入', numeric: true, cell: (channel) => formatPoints(channel.today.revenue) },
]

export function ChannelsPage() {
  const [q, setQ] = useState('')
  const [status, setStatus] = useState<'all' | ChannelStatus>('all')
  const channels = useAdminChannels({ q: q.trim() || undefined, status: status === 'all' ? undefined : status })
  return (
    <>
      <PageHeader title="渠道" />
      <Toolbar>
        <SearchInput label="搜索渠道" onChange={(event) => setQ(event.target.value)} placeholder="名称或地址" value={q} />
        <Segmented
          label="状态"
          onChange={setStatus}
          options={[
            { key: 'all', label: '全部' },
            { key: 'listed', label: '在线' },
            { key: 'unlisted', label: '已下架' },
            { key: 'suspended', label: '被下架' },
          ]}
          value={status}
        />
      </Toolbar>
      <Card flush>
        <QueryBoundary query={channels}>
          {(rows) => (
            <>
              <DataTable
                caption="全部渠道"
                columns={columns}
                empty={<p className="muted-copy empty-pad">没有渠道</p>}
                rowHref={(channel) => `/admin/channels/${channel.id}`}
                rowKey={(channel) => channel.id}
                rows={rows}
              />
              <LoadMore
                hasMore={Boolean(channels.hasNextPage)}
                loading={channels.isFetchingNextPage}
                onLoad={() => void channels.fetchNextPage()}
              />
            </>
          )}
        </QueryBoundary>
      </Card>
    </>
  )
}

function ChannelDetail({ channel, events }: { channel: AdminChannel; events: ChannelEvent[] }) {
  const [dialog, setDialog] = useState(false)
  const suspension = useChannelSuspension()
  const suspended = channel.status === 'suspended'
  const advanced = channel.advanced
  const orDefault = (value: number | string | null, unit = '') => (value === null ? '平台默认' : `${value}${unit}`)
  return (
    <>
      <PageHeader
        actions={
          <>
            <ButtonLink size="sm" to={`/admin/calls?channel_id=${channel.id}`}>
              调用列表
            </ButtonLink>
            <Button onClick={() => setDialog(true)} size="sm" type="button" variant={suspended ? 'primary' : 'danger'}>
              {suspended ? '恢复' : '强制下架'}
            </Button>
          </>
        }
        back={
          <Link className="back-link" to="/admin/channels">
            ← 渠道
          </Link>
        }
        title={channel.name}
      />
      {suspended && channel.suspended_reason && (
        <p className="notice notice-danger" role="status">
          <span>下架原因：{channel.suspended_reason}</span>
        </p>
      )}
      <div className="admin-two-column">
        <Card title="配置摘要">
          <DetailList
            items={[
              ['状态', <ChannelStatusBadge channel={channel} />],
              ['所有者', `${channel.owner.display_name}（@${channel.owner.username}）`],
              ['Base URL', <span className="mono break">{channel.base_url}</span>],
              ['上游 Key', '已加密保存，不回显'],
              ['User-Agent', advanced.user_agent ?? '透传客户端'],
              [
                '请求头规则',
                `设置 ${advanced.header_rules.set.length} 条 · 删除 ${advanced.header_rules.remove.length} 条`,
              ],
              ['并发 / 每分钟请求', `${orDefault(advanced.concurrency_limit)} / ${orDefault(advanced.rpm_limit)}`],
              ['每日收入上限', advanced.daily_revenue_cap === null ? '不限' : formatPoints(advanced.daily_revenue_cap)],
              ['首字 / 总超时', `${orDefault(advanced.ttft_timeout_ms, ' ms')} / ${orDefault(advanced.total_timeout_ms, ' ms')}`],
              [
                '失败冷却',
                advanced.cooldown_failures === null && advanced.cooldown_seconds === null
                  ? '平台默认'
                  : `连续 ${orDefault(advanced.cooldown_failures)} 次 → ${orDefault(advanced.cooldown_seconds, ' 秒')}`,
              ],
              ['创建时间', formatDateTime(channel.created_at)],
            ]}
          />
        </Card>
        <Card title="今日">
          <DetailList
            items={[
              ['调用', formatCount(channel.today.calls)],
              ['成功率', formatRatio(channel.today.success_rate)],
              ['收入', formatPoints(channel.today.revenue)],
              ['冷却至', channel.cooldown_until ? formatDateTime(channel.cooldown_until) : '—'],
            ]}
          />
        </Card>
      </div>
      <ChannelStatsPanel channelId={channel.id} />
      <Card flush title={`模型（${channel.models.length}）`}>
        <DataTable
          caption="渠道模型"
          columns={[
            {
              key: 'model',
              header: '模型',
              primary: true,
              cell: (model) => (
                <>
                  <strong className="mono">{model.model_id}</strong>
                  {model.upstream_model !== model.model_id && <small>上游：{model.upstream_model}</small>}
                </>
              ),
            },
            {
              key: 'formats',
              header: '格式',
              cell: (model) => (
                <span className="badge-row">
                  {model.formats.map((format) => (
                    <Badge key={format} tone={model.format_tests[format]?.ok ? 'success' : 'neutral'}>
                      {formatLabels[format] ?? format}
                    </Badge>
                  ))}
                </span>
              ),
            },
            { key: 'multiplier', header: '倍率', numeric: true, cell: (model) => `×${model.multiplier}` },
            {
              key: 'prices',
              header: '现价 输入/输出',
              numeric: true,
              cell: (model) => `${model.current_prices.input} / ${model.current_prices.output}`,
            },
            {
              key: 'enabled',
              header: '状态',
              cell: (model) => <Badge tone={model.enabled ? 'success' : 'neutral'}>{model.enabled ? '在卖' : '停卖'}</Badge>,
            },
          ]}
          empty={<p className="muted-copy empty-pad">没有模型</p>}
          rowKey={(model) => model.model_id}
          rows={channel.models}
        />
      </Card>
      <Card title="健康事件">
        {events.length === 0 ? (
          <p className="muted-copy">暂无事件</p>
        ) : (
          <ol className="timeline">
            {events.map((event) => (
              <li key={event.id}>
                <span className="timeline-time">{formatDateTime(event.created_at)}</span>
                <strong>{eventLabels[event.kind]}</strong>
                {event.reason && <span className="muted-copy">{event.reason}</span>}
              </li>
            ))}
          </ol>
        )}
      </Card>
      <ConfirmActionDialog
        confirmLabel={suspended ? '确认恢复' : '确认下架'}
        danger={!suspended}
        onClose={() => setDialog(false)}
        onConfirm={(reason) => suspension.mutateAsync({ id: channel.id, suspend: !suspended, reason })}
        open={dialog}
        summary={
          suspended ? (
            <p>
              恢复 <strong>{channel.name}</strong>，所有者可以重新上架。
            </p>
          ) : (
            <p>
              强制下架 <strong>{channel.name}</strong>：立即停止接收调用，所有者无法自行上架。
            </p>
          )
        }
        title={suspended ? '恢复渠道' : '强制下架'}
      />
    </>
  )
}

export function ChannelDetailPage() {
  const { channelID = '' } = useParams()
  const detail = useAdminChannel(channelID)
  if (detail.data) return <ChannelDetail channel={detail.data.channel} events={detail.data.events} />
  return (
    <>
      <PageHeader
        back={
          <Link className="back-link" to="/admin/channels">
            ← 渠道
          </Link>
        }
        title="渠道详情"
      />
      <Card>
        <QueryBoundary query={detail}>{() => null}</QueryBoundary>
      </Card>
    </>
  )
}
