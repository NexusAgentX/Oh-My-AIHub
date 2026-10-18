import type { CallAttempt, CallDetail } from '../api/types'
import { formatMs } from '../money/format'
import { Badge, CopyButton } from '../ui'
import { endReasonLabel } from './labels'
import { attemptSegments, attemptSucceeded, segmentLabels, timelineScale } from './timeline'

function AttemptRow({
  attempt,
  index,
  scale,
}: {
  attempt: CallAttempt
  index: number
  scale: number
  call?: CallDetail
}) {
  const ok = attemptSucceeded(attempt)
  const segments = attemptSegments(attempt, scale)
  return (
    <li className={`attempt ${ok ? 'attempt-ok' : 'attempt-failed'}`}>
      <div className="attempt-head">
        <span className="attempt-index">#{index + 1}</span>
        <strong>{attempt.channel?.name ?? '未选到渠道'}</strong>
        {attempt.channel && <CopyButton iconOnly label="复制尝试渠道 ID" value={attempt.channel.id} />}
        {ok ? (
          <Badge tone="success">{attempt.status_code ?? ''} 成功</Badge>
        ) : (
          <Badge tone="danger">
            {attempt.status_code ? `${attempt.status_code} · ` : ''}
            {endReasonLabel(attempt.end_reason)}
          </Badge>
        )}
        <span className="attempt-duration num">{formatMs(attempt.duration_ms)}</span>
      </div>
      <dl className="attempt-metrics">
        {attempt.address_id && <><dt>地址 ID</dt><dd className="mono">{attempt.address_id}<CopyButton iconOnly label="复制尝试地址 ID" value={attempt.address_id}/></dd></>}
        {attempt.fallback_level && <><dt>回退层级</dt><dd>{attempt.fallback_level==='address'?'同渠道地址':'跨渠道'}</dd></>}
        {attempt.upstream_model && (
          <>
            <dt>上游模型</dt>
            <dd>{attempt.upstream_model}</dd>
          </>
        )}
        {attempt.offset_ms !== null && attempt.offset_ms !== undefined && (
          <>
            <dt>开始偏移</dt>
            <dd>{formatMs(attempt.offset_ms)}</dd>
          </>
        )}
        {attempt.reused_connection !== null && attempt.reused_connection !== undefined && (
          <>
            <dt>连接</dt>
            <dd>{attempt.reused_connection ? '复用' : '新建'}</dd>
          </>
        )}
        {attempt.response_bytes !== null && (
          <>
            <dt>已发送</dt>
            <dd>{attempt.response_bytes} bytes</dd>
          </>
        )}
        {attempt.fallback_reason && (
          <>
            <dt>上次回退原因</dt>
            <dd>{endReasonLabel(attempt.fallback_reason)}</dd>
          </>
        )}
        {attempt.timeouts && (
          <>
            <dt>超时阈值</dt>
            <dd>
              {attempt.timeouts.first_byte_ms > 0
                ? `首响应 ${formatMs(attempt.timeouts.first_byte_ms)} · 空闲 ${formatMs(attempt.timeouts.idle_ms)}`
                : `总计 ${formatMs(attempt.timeouts.total_ms)}`}
            </dd>
          </>
        )}
      </dl>
      <div
        aria-label={
          segments.map((segment) => `${segmentLabels[segment.kind]} ${formatMs(segment.ms)}`).join('，') || '无耗时数据'
        }
        className="attempt-bar"
        role="img"
      >
        {(attempt.offset_ms ?? 0) > 0 && (
          <i style={{ width: `${((attempt.offset_ms ?? 0) / scale) * 100}%`, flexShrink: 0 }} />
        )}
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
      {attempt.error_code && <p className="attempt-error-code">{attempt.error_code}</p>}
    </li>
  )
}

/** 尝试时间线：每次尝试一行，共用时间轴。call 提供时在成功尝试下显示流式指标。 */
export function AttemptTimeline({ attempts }: { attempts: CallAttempt[]; call?: CallDetail }) {
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
          <AttemptRow attempt={attempt} index={index} key={index} scale={scale} />
        ))}
      </ol>
    </div>
  )
}
