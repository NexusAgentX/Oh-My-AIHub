import { useState } from 'react'
import type { ChannelEvent } from '../api/types'
import { formatMs, formatPoints, formatRatio } from '../money/format'
import { formatDateTime, rangeFrom } from '../money/time'
import { BarChart, Badge, Card, EmptyState, MetricGrid, Metric, QueryBoundary, Segmented, type BadgeTone } from '../ui'
import { useChannelStats } from './queries'

const eventLabels: Record<ChannelEvent['kind'], { label: string; tone: BadgeTone }> = {
  cooldown_started: { label: '进入冷却', tone: 'warning' },
  cooldown_ended: { label: '恢复', tone: 'success' },
  limit_reached: { label: '触发限额', tone: 'warning' },
  suspended: { label: '被下架', tone: 'danger' },
  unsuspended: { label: '恢复上架', tone: 'success' },
  listed: { label: '上架', tone: 'info' },
  unlisted: { label: '下架', tone: 'neutral' },
  test_failed: { label: '测试失败', tone: 'danger' },
}

/** 健康事件列表：冷却、恢复、触发限额、被下架，带原因。 */
export function ChannelEvents({ events }: { events: ChannelEvent[] }) {
  if (events.length === 0) return <EmptyState title="暂无健康事件" />
  return (
    <ol className="channel-events">
      {events.map((event) => (
        <li key={event.id}>
          <Badge tone={eventLabels[event.kind].tone}>{eventLabels[event.kind].label}</Badge>
          <span className="channel-event-reason">{event.reason || '—'}</span>
          <time className="muted num" dateTime={event.created_at}>
            {formatDateTime(event.created_at)}
          </time>
        </li>
      ))}
    </ol>
  )
}

/** 渠道统计（G 提供）：成功率、调用量、收入、首字与速度、按天趋势与按模型。 */
export function ChannelStatsPanel({ channelId }: { channelId: string }) {
  const [range, setRange] = useState<'today' | '7d'>('7d')
  const from = rangeFrom(range)
  const stats = useChannelStats(channelId, from)
  return (
    <Card
      actions={
        <Segmented
          label="统计范围"
          onChange={setRange}
          options={[
            { key: 'today', label: '今天' },
            { key: '7d', label: '7 天' },
          ]}
          value={range}
        />
      }
      title="渠道统计"
    >
      <QueryBoundary errorFallback="渠道统计加载失败" query={stats}>
        {(data) => (
          <div className="channel-stats">
            <MetricGrid label="渠道统计">
              <Metric hint={`${data.succeeded} / ${data.calls} 次`} label="成功率" value={formatRatio(data.success_rate)} />
              <Metric label="收入" value={formatPoints(data.revenue)} />
              <Metric label="首字 p50" value={formatMs(data.ttft_p50_ms)} />
              <Metric
                label="速度 p50"
                value={data.output_tokens_per_second_p50 === null ? '—' : `${data.output_tokens_per_second_p50.toFixed(1)} t/s`}
              />
            </MetricGrid>
            {data.daily.length > 0 && (
              <BarChart
                data={data.daily.map((day) => ({
                  key: day.date,
                  label: day.date.slice(5),
                  value: day.calls,
                  display: `${day.calls} 次 · 成功 ${day.succeeded}`,
                }))}
                label="每日调用量"
              />
            )}
            {data.by_model.length > 0 && (
              <BarChart
                data={data.by_model.map((model) => ({
                  key: model.model_id,
                  label: model.model_id,
                  value: Number(model.revenue),
                  display: `${formatPoints(model.revenue)} · ${model.calls} 次`,
                }))}
                label="按模型收入"
                orientation="horizontal"
              />
            )}
          </div>
        )}
      </QueryBoundary>
    </Card>
  )
}
