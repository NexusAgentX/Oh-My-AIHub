import type { CallAttempt } from '../api/types'

export type SegmentKind = 'connect' | 'wait' | 'output' | 'failed'

export type Segment = {
  kind: SegmentKind
  ms: number
  /** 占时间轴的百分比（0～100） */
  percent: number
}

export const segmentLabels: Record<SegmentKind, string> = {
  connect: '连接',
  wait: '等首字',
  output: '输出',
  failed: '失败',
}

export function attemptSucceeded(attempt: CallAttempt) {
  return attempt.end_reason === 'completed'
}

/** 尝试的总耗时：优先 duration_ms，否则取已知的最大阶段时间。 */
export function attemptTotalMs(attempt: CallAttempt) {
  return attempt.duration_ms ?? Math.max(attempt.connect_ms ?? 0, attempt.ttft_ms ?? 0)
}

/** 所有尝试共用的时间轴长度（至少 1ms，避免除零）。 */
export function timelineScale(attempts: CallAttempt[]) {
  return Math.max(1, ...attempts.map(attemptTotalMs))
}

/**
 * 把一次尝试拆成水平条的分段：连接 → 等首字 → 输出；
 * 没有首字就失败的尝试，连接之后的部分记为「失败」。
 */
export function attemptSegments(attempt: CallAttempt, scaleMs: number): Segment[] {
  const total = attemptTotalMs(attempt)
  const connect = Math.min(attempt.connect_ms ?? 0, total)
  const parts: Array<[SegmentKind, number]> = []
  if (connect > 0) parts.push(['connect', connect])
  if (attempt.ttft_ms !== null) {
    const ttft = Math.min(Math.max(attempt.ttft_ms, connect), total)
    parts.push(['wait', ttft - connect])
    parts.push([attemptSucceeded(attempt) ? 'output' : 'failed', total - ttft])
  } else {
    parts.push([attemptSucceeded(attempt) ? 'wait' : 'failed', total - connect])
  }
  return parts
    .filter(([, ms]) => ms > 0)
    .map(([kind, ms]) => ({ kind, ms, percent: (ms / scaleMs) * 100 }))
}
