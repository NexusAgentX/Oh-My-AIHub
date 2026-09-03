import type {
  APIKeyStatus,
  GatewayAttemptStatus,
  GatewayCall,
  GatewayCallStatus,
} from '../api/contracts'
import { formatPointAmount } from '../wallet/presentation'

const labels: Record<
  APIKeyStatus | GatewayAttemptStatus | GatewayCallStatus,
  string
> = {
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

export function GatewayStatusBadge({
  status,
}: {
  status: APIKeyStatus | GatewayAttemptStatus | GatewayCallStatus
}) {
  const tone = ['active', 'succeeded'].includes(status)
    ? 'positive'
    : ['failed', 'rejected', 'deleted'].includes(status)
      ? 'danger'
      : ['disabled', 'incomplete', 'cancelled'].includes(status)
        ? 'warning'
        : 'neutral'
  return (
    <span className={`channel-state channel-state-${tone}`}>
      {gatewayStatusLabel(status)}
    </span>
  )
}

export function gatewayStatusLabel(
  status: APIKeyStatus | GatewayAttemptStatus | GatewayCallStatus,
) {
  return labels[status]
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
