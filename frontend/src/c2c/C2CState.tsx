import { Badge, type BadgeTone } from '../ui'

const tones: Record<string, BadgeTone> = {
  positive: 'success',
  danger: 'danger',
  neutral: 'neutral',
  warning: 'warning',
}

/** 订单与交易状态徽标；tone 取自 presentation 的 c2cStatusTone。 */
export function C2CState({ label, tone }: { label: string; tone: string }) {
  return <Badge tone={tones[tone] ?? 'neutral'}>{label}</Badge>
}
