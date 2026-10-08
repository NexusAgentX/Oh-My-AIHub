import { useState } from 'react'
import type { ChannelEvent, ChannelFailure, ChannelStats } from '../api/types'
import { formatMs, formatPoints, formatRatio } from '../money/format'
import { formatDateTime } from '../money/time'
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

function speed(value: number | null) {
  return value === null ? '—' : `${value.toFixed(1)} t/s`
}

const hourFormat = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', hour12: false, timeZone: 'Asia/Shanghai' })

/** 失败按上游状态码分布；null 表示没有拿到响应（连接失败或超时）。 */
export function StatusCodes({ codes }: { codes: ChannelStats['status_codes'] }) {
  if (codes.length === 0) return <EmptyState title="没有失败" />
  return (
    <BarChart
      data={codes.map((row) => ({
        key: String(row.status_code ?? 'none'),
        label: row.status_code === null ? '无响应' : String(row.status_code),
        value: row.count,
        display: `${row.count} 次`,
      }))}
      label="失败按状态码"
      orientation="horizontal"
    />
  )
}

/** 最近失败：时间、模型、状态码与错误码、上游原始错误。 */
export function RecentFailures({ failures }: { failures: ChannelFailure[] }) {
  if (failures.length === 0) return <EmptyState title="没有失败" />
  return (
    <ol className="channel-failures">
      {failures.map((failure, index) => (
        <li key={`${failure.call_id}-${index}`}>
          <div className="channel-failure-head">
            <strong className="num">
              {[failure.status_code ?? '无响应', failure.error_code].filter(Boolean).join(' · ')}
            </strong>
            <span className="muted">{failure.model_id ?? '—'}</span>
            <time className="muted num" dateTime={failure.created_at}>
              {formatDateTime(failure.created_at)}
            </time>
          </div>
          {failure.error_message ? (
            <pre className="channel-failure-message">{failure.error_message}</pre>
          ) : (
            <span className="muted-copy">{endReasonLabel(failure.end_reason)}</span>
          )}
        </li>
      ))}
    </ol>
  )
}

function endReasonLabel(reason: string) {
  return reason ? `结束原因：${reason}` : '无原始错误'
}

/** 渠道统计：24h/7d 成功率、收入与今日上限进度、首字与速度 p50/p95、调用趋势、按模型收入、失败分布与最近失败。 */
export function ChannelStatsPanel({ channelId }: { channelId: string }) {
  const [range, setRange] = useState<'24h' | '7d'>('7d')
  const stats = useChannelStats(channelId)
  return (
    <Card
      actions={
        <Segmented
          label="趋势范围"
          onChange={setRange}
          options={[
            { key: '24h', label: '24 小时' },
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
              <Metric
                hint={`${data.last_24h.succeeded} / ${data.last_24h.calls} 次 · 7 天 ${formatRatio(data.last_7d.success_rate)}`}
                label="24 小时成功率"
                value={formatRatio(data.last_24h.success_rate)}
              />
              <Metric
                hint={data.today.daily_cap ? `上限 ${formatPoints(data.today.daily_cap)}` : '未设上限'}
                label="今日收入"
                progress={data.today.progress === null ? undefined : Number(data.today.progress) * 100}
                value={formatPoints(data.today.revenue)}
              />
              <Metric hint={`p95 ${formatMs(data.ttft_p95_ms)}`} label="首字 p50" value={formatMs(data.ttft_p50_ms)} />
              <Metric
                hint={`p95 ${speed(data.output_tokens_per_second_p95)}`}
                label="速度 p50"
                value={speed(data.output_tokens_per_second_p50)}
              />
            </MetricGrid>
            {range === '24h' ? (
              <BarChart
                data={data.hourly.map((hour) => ({
                  key: hour.hour,
                  label: hourFormat.format(new Date(hour.hour)),
                  value: hour.calls,
                  display: `${hour.calls} 次 · 成功 ${hour.succeeded}`,
                }))}
                label="每小时调用量"
              />
            ) : (
              data.daily.length > 0 && (
                <BarChart
                  data={data.daily.map((day) => ({
                    key: day.date,
                    label: day.date.slice(5),
                    value: day.calls,
                    display: `${day.calls} 次 · 成功 ${day.succeeded}`,
                  }))}
                  label="每日调用量"
                />
              )
            )}
            {data.by_model.length > 0 && (
              <BarChart
                data={data.by_model.map((model) => ({
                  key: model.model_id,
                  label: model.model_id,
                  value: Number(model.revenue),
                  display: `${formatPoints(model.revenue)} · ${model.calls} 次`,
                }))}
                label="按模型收入（7 天）"
                orientation="horizontal"
              />
            )}
            <section className="channel-stats-section" aria-label="失败按状态码">
              <h3 className="section-title">失败按状态码（7 天）</h3>
              <StatusCodes codes={data.status_codes} />
            </section>
            <section className="channel-stats-section" aria-label="最近失败">
              <h3 className="section-title">最近失败</h3>
              <RecentFailures failures={data.recent_failures} />
            </section>
          </div>
        )}
      </QueryBoundary>
    </Card>
  )
}
