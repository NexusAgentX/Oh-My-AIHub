import type { CallDetail } from '../api/types'
import { diagnosticLabel } from './CallDiagnostics'

export type ReplaySpan = { label: string; start: number; end: number; side: 'gateway' | 'upstream' | 'response' }
export const networkLabels: Record<string, string> = {
  prepare: '出站准备（含 DNS 缓存/解析）', connection: '获取连接', tls: 'TLS 握手', send: '发送请求', headers: '等待响应头',
}
export function replaySpans(call: CallDetail, index: number): ReplaySpan[] {
  const result: ReplaySpan[] = []
  for (const stage of call.diagnostics?.stages ?? []) {
    if (stage.offset_ms != null) result.push({ label: diagnosticLabel(stage.stage), start: stage.offset_ms, end: stage.offset_ms + stage.duration_ms, side: 'gateway' })
  }
  const attempt = call.attempts[index]
  const network = call.diagnostics?.network?.attempts[index]
  if (attempt?.offset_ms != null) {
    const offset = attempt.offset_ms
    for (const phase of network?.phases ?? []) result.push({ label: networkLabels[phase.stage], start: offset + phase.offset_ms, end: offset + phase.offset_ms + phase.duration_ms, side: 'upstream' })
    const headers = network?.phases.find(p => p.stage === 'headers')
    if (headers && attempt.ttft_ms != null) {
      const start = offset + headers.offset_ms + headers.duration_ms
      result.push({ label: '等待响应正文', start, end: offset + attempt.ttft_ms, side: 'upstream' })
    }
    if (attempt.ttft_ms != null && attempt.duration_ms != null) result.push({
      label: call.stream ? '持续接收与转发' : '接收与转发正文', start: offset + attempt.ttft_ms, end: offset + attempt.duration_ms, side: 'response',
    })
  }
  return result.filter(p => Number.isFinite(p.start) && Number.isFinite(p.end) && p.start >= 0 && p.end >= p.start).sort((a, b) => a.start - b.start)
}
export function replayDuration(call: CallDetail, spans: ReplaySpan[]) {
  return Math.max(1, call.duration_ms ?? 0, ...spans.map(s => s.end))
}
export function mapPoint(location: { latitude: number; longitude: number } | null | undefined, slot: number) {
  return location ? { x: (location.longitude + 180) * 2, y: (90 - location.latitude) * 2 } : { x: 120 + slot * 240, y: 382 }
}
