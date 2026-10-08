import type {
  APIKeyStatus,
  ChannelProtocol,
  GatewayAttemptStatus,
  GatewayCall,
  GatewayCallStatus,
  PriceTier,
} from '../api/types'
import { Badge, type BadgeTone } from '../ui'
import { formatPointAmount } from '../wallet/presentation'

type GatewayStatus = APIKeyStatus | GatewayAttemptStatus | GatewayCallStatus

const labels: Record<GatewayStatus, string> = {
  active: '启用',
  disabled: '停用',
  deleted: '已删除',
  rejected: '已拒绝',
  in_progress: '进行中',
  pending_delivery: '交付确认中',
  succeeded: '成功',
  failed: '失败',
  incomplete: '未完成',
  cancelled: '已取消',
}

export function gatewayStatusTone(status: GatewayStatus): BadgeTone {
  if (status === 'active' || status === 'succeeded') return 'success'
  if (status === 'failed' || status === 'rejected' || status === 'deleted') return 'danger'
  if (status === 'disabled' || status === 'incomplete' || status === 'cancelled') return 'warning'
  return 'neutral'
}

export function GatewayStatusBadge({ status }: { status: GatewayStatus }) {
  return <Badge tone={gatewayStatusTone(status)}>{gatewayStatusLabel(status)}</Badge>
}

export function gatewayStatusLabel(status: GatewayStatus) {
  return labels[status]
}

/** 四种原生协议的界面名称，顺序即选择器顺序。 */
export const protocolLabels: Record<ChannelProtocol, string> = {
  openai_chat_completions: 'OpenAI Chat Completions',
  openai_responses: 'OpenAI Responses',
  anthropic_messages: 'Anthropic Messages',
  google_gemini_generate_content: 'Gemini GenerateContent',
}

export const protocols = Object.keys(protocolLabels) as ChannelProtocol[]

export function isProtocol(value: string | null | undefined): value is ChannelProtocol {
  return Boolean(value) && value! in protocolLabels
}

export function formatDate(value?: string | null) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}

/** 价格对：单位为 积分 / 百万 tokens。 */
export function PricePair({
  first,
  second,
  tiers,
}: {
  first?: string | null
  second?: string | null
  tiers?: PriceTier[] | null
}) {
  return (
    <span className="price-pair num">
      <span>
        {first ?? '—'} / {second ?? '—'}
      </span>
      <small>
        积分 / 百万 tokens
        {tiers && tiers.length > 0 && ` · +${tiers.length} 条件档`}
      </small>
    </span>
  )
}

export function formatPoints(value: string) {
  return formatPointAmount(value)
}

export function formatNullableMetric(value: number | string | null | undefined) {
  return value ?? '—'
}

export function totalTokens(call: GatewayCall) {
  if (!call.usage) return '—'
  return new Intl.NumberFormat('zh-CN').format(
    call.usage.input_tokens +
      call.usage.output_tokens +
      call.usage.cache_write_tokens +
      call.usage.cache_read_tokens,
  )
}

export function shortID(value: string) {
  return value.length > 12 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value
}

export function formatRate(value: string | null) {
  if (value === null) return '—'
  const [whole, fraction = ''] = value.split('.')
  if (!/^\d+$/.test(whole) || !/^\d*$/.test(fraction)) return value
  const padded = `${fraction}0000`.slice(0, 4)
  const basisPoints = BigInt(whole) * 10000n + BigInt(padded)
  const percentHundredths = basisPoints
  const integer = percentHundredths / 100n
  const decimal = (percentHundredths % 100n).toString().padStart(2, '0')
  return `${integer}.${decimal.replace(/0+$/, '') || '0'}%`
}

/** 渠道质量的补充说明：调用数 · 首字响应（成功率单独展示）。 */
export function qualitySummary(offer: {
  call_count: number | null
  ttft_milliseconds: number | null
}) {
  return `${offer.call_count ?? 0} 次 · ${offer.ttft_milliseconds ?? '—'} ms`
}

/** 外部协议入口的 Base URL：与当前站点同源（开发与 Compose 环境均代理 /v1）。 */
export function apiBaseURL() {
  return typeof window === 'undefined' ? '' : window.location.origin
}
