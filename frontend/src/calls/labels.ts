import type { BadgeTone } from '../ui'
import type { CallOutcome, Format, RoutingMode } from '../api/types'

export const formats: Format[] = ['openai_chat', 'openai_responses', 'anthropic', 'gemini']

export const formatLabels: Record<Format, string> = {
  openai_chat: 'OpenAI Chat',
  openai_responses: 'Responses',
  anthropic: 'Anthropic',
  gemini: 'Gemini',
}

export const routingModeLabels: Record<RoutingMode, string> = {
  cheapest: '便宜优先',
  reliable: '稳定优先',
  fastest: '快速优先',
  manual: '手动',
}

export const outcomeLabels: Record<CallOutcome, string> = {
  succeeded: '成功',
  succeeded_unbilled: '成功·未计费',
  in_progress: '进行中',
  upstream_failed: '上游失败',
  interrupted: '中断',
  client_disconnected: '客户端断开',
  rejected_balance: '余额不足',
  rejected_budget: '超出预算',
  rejected_key: 'Key 无效',
  rejected_model: '模型不可用',
  rejected_format: '接口不支持',
  rejected_no_channel: '无可用渠道',
}

export function outcomeTone(outcome: CallOutcome): BadgeTone {
  if (outcome === 'succeeded') return 'success'
  if (outcome === 'succeeded_unbilled' || outcome === 'in_progress') return 'info'
  if (outcome === 'client_disconnected' || outcome.startsWith('rejected_')) return 'warning'
  return 'danger'
}

export const outcomeOptions = Object.keys(outcomeLabels) as CallOutcome[]

export function isFailure(outcome: CallOutcome) {
  return !(outcome === 'succeeded' || outcome === 'succeeded_unbilled' || outcome === 'in_progress')
}

const endReasonLabels: Record<string, string> = {
  completed: '完成',
  upstream_error: '上游错误',
  timeout_ttft: '首字超时',
  timeout_total: '总超时',
  client_disconnected: '客户端断开',
  connect_error: '连接失败',
}

export function endReasonLabel(reason: string) {
  return endReasonLabels[reason] ?? reason
}
