import type { ModelDetail, ModelPrices, PriceTier } from '../api/types'

const weekdayNames = ['一', '二', '三', '四', '五', '六', '日']

function minuteText(minute: number) {
  return `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`
}

/** 价格档的命中条件，例如「周一至周五 · 22:00–08:00（Asia/Shanghai）· 输入 ≥ 200k」。 */
export function tierCondition(tier: PriceTier) {
  const parts: string[] = []
  if (tier.weekdays && tier.weekdays.length > 0 && tier.weekdays.length < 7) {
    parts.push(`周${tier.weekdays.map((day) => weekdayNames[day - 1] ?? day).join('、')}`)
  }
  if (tier.start_minute_of_day !== null && tier.end_minute_of_day !== null) {
    parts.push(`${minuteText(tier.start_minute_of_day)}–${minuteText(tier.end_minute_of_day)}（${tier.timezone}）`)
  }
  if (tier.min_prompt_tokens !== null || tier.max_prompt_tokens !== null) {
    const min = tier.min_prompt_tokens ?? 0
    parts.push(
      tier.max_prompt_tokens === null
        ? `输入 ≥ ${min.toLocaleString('zh-CN')}`
        : `输入 ${min.toLocaleString('zh-CN')}–${tier.max_prompt_tokens.toLocaleString('zh-CN')}`,
    )
  }
  return parts.length > 0 ? parts.join(' · ') : '始终'
}

/** 当前生效的价格：命中档位的价格，未命中时为基准价。 */
export function currentPrices(detail: Pick<ModelDetail, 'model' | 'price_tiers'>): { name: string; prices: ModelPrices } {
  const seq = detail.model.current_tier?.seq
  const tier = seq === undefined ? undefined : detail.price_tiers.find((item) => item.seq === seq)
  return tier ? { name: tier.name, prices: tier.prices } : { name: '基准价', prices: detail.model.base_prices }
}

/** 价格 × 倍率（字符串十进制，最多 9 位小数，向上取整到 nano）。 */
export function multiplyPrice(price: string, multiplier: string) {
  const toNano = (value: string) => {
    const match = /^(\d+)(?:\.(\d{1,9}))?$/.exec(value.trim())
    if (!match) return null
    return BigInt(match[1]) * 1_000_000_000n + BigInt((match[2] ?? '').padEnd(9, '0'))
  }
  const a = toNano(price)
  const b = toNano(multiplier)
  if (a === null || b === null) return null
  const scale = 1_000_000_000n
  const product = (a * b + scale - 1n) / scale
  const whole = product / scale
  const fraction = (product % scale).toString().padStart(9, '0').replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : `${whole}`
}

export function contextText(tokens: number | null) {
  if (!tokens) return null
  if (tokens >= 1_000_000) return `${tokens / 1_000_000}M`
  return `${Math.round(tokens / 1000)}k`
}
