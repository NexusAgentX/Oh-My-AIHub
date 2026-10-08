import type { CallAttempt, CallDetail } from '../api/types'
import { formatMs, formatTokens } from '../money/format'
import { Badge } from '../ui'
import { endReasonLabel } from './labels'
import { attemptSegments, attemptSucceeded, segmentLabels, timelineScale } from './timeline'

function AttemptMetrics({ call }: { call: CallDetail }) {
  const items: Array<[string, string]> = [
    ['首字', formatMs(call.ttft_ms)],
    ['速度', call.output_tokens_per_second === null ? '—' : `${call.output_tokens_per_second.toFixed(1)} tokens/s`],
    ['token 间隔 p50 / p95', `${formatMs(call.inter_token_p50_ms)} / ${formatMs(call.inter_token_p95_ms)}`],
    ['输入 / 输出', `${formatTokens(call.usage.input_tokens)} / ${formatTokens(call.usage.output_tokens)}`],
  ]
  return (
    <dl className="attempt-metrics">
      {items.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd className="num">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function AttemptRow({
  attempt,
  index,
  scale,
  call,
  last,
}: {
  attempt: CallAttempt
  index: number
  scale: number
  call?: CallDetail
  last: boolean
}) {
  const ok = attemptSucceeded(attempt)
  const segments = attemptSegments(attempt, scale)
  return (
    <li className={`attempt ${ok ? 'attempt-ok' : 'attempt-failed'}`}>
      <div className="attempt-head">
        <span className="attempt-index">#{index + 1}</span>
        <strong>{attempt.channel?.name ?? '未选到渠道'}</strong>
        {ok ? (
          <Badge tone="success">成功</Badge>
        ) : (
          <Badge tone="danger">
            {attempt.status_code ? `${attempt.status_code} · ` : ''}
            {endReasonLabel(attempt.end_reason)}
          </Badge>
        )}
        <span className="attempt-duration num">{formatMs(attempt.duration_ms)}</span>
      </div>
      <div
        aria-label={segments.map((segment) => `${segmentLabels[segment.kind]} ${formatMs(segment.ms)}`).join('，') || '无耗时数据'}
        className="attempt-bar"
        role="img"
      >
        {segments.map((segment, position) => (
          <i
            className={`attempt-segment attempt-segment-${segment.kind}`}
            key={`${segment.kind}-${position}`}
            style={{ width: `${segment.percent}%` }}
            title={`${segmentLabels[segment.kind]} ${formatMs(segment.ms)}`}
          />
        ))}
      </div>
      {!ok && attempt.error_message && <pre className="attempt-error">{attempt.error_message}</pre>}
      {!ok && !attempt.error_message && attempt.error_code && <p className="attempt-error-code">{attempt.error_code}</p>}
      {ok && last && call && <AttemptMetrics call={call} />}
    </li>
  )
}

/** 尝试时间线：每次尝试一行，共用时间轴。call 提供时在成功尝试下显示流式指标。 */
export function AttemptTimeline({ attempts, call }: { attempts: CallAttempt[]; call?: CallDetail }) {
  if (attempts.length === 0) return <p className="muted-copy">没有发起上游请求</p>
  const scale = timelineScale(attempts)
  return (
    <div className="attempt-timeline">
      <ul className="attempt-legend" aria-hidden="true">
        {(['connect', 'wait', 'output', 'failed'] as const).map((kind) => (
          <li key={kind}>
            <i className={`attempt-segment-${kind}`} />
            {segmentLabels[kind]}
          </li>
        ))}
      </ul>
      <ol className="attempt-list">
        {attempts.map((attempt, index) => (
          <AttemptRow
            attempt={attempt}
            call={call}
            index={index}
            key={index}
            last={index === attempts.length - 1}
            scale={scale}
          />
        ))}
      </ol>
    </div>
  )
}
