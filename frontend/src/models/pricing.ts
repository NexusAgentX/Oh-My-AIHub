import type { ModelDetail, ModelPrices, PriceTier } from '../api/types'

const weekdayNames = ['一', '二', '三', '四', '五', '六', '日']

function minuteText(minute: number) {
  return `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`
}

/** 价格档的命中条件，例如「周一至周五 · 22:00–08:00（Asia/Shanghai）· 输入 ≥ 200k」。 */
export function tierCondition(tier: PriceTier) {
  const parts: string[] = []
  if (tier.service_tier) parts.push(`服务档位 ${tier.service_tier}`)
  if (tier.thinking_mode) parts.push(tier.thinking_mode === 'qwen_thinking' ? '百炼实际思考' : '百炼未输出思考')
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
export function currentPrices(detail: Pick<ModelDetail, 'model' | 'price_tiers'>): {
  name: string
  prices: ModelPrices
} {
  const seq = detail.model.current_tier?.seq
  const tier = seq === undefined ? undefined : detail.price_tiers.find((item) => item.seq === seq)
  return tier ? { name: tier.name, prices: tier.prices } : { name: '基准价', prices: detail.model.base_prices }
}

const scale = 1_000_000_000n

function toNano(value: string) {
  const match = /^(\d+)(?:\.(\d{1,9}))?$/.exec(value.trim())
  if (!match) return null
  return BigInt(match[1]) * scale + BigInt((match[2] ?? '').padEnd(9, '0'))
}

function fromNano(value: bigint) {
  const whole = value / scale
  const fraction = (value % scale).toString().padStart(9, '0').replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : `${whole}`
}

/** 价格 × 倍率（字符串十进制，最多 9 位小数，向上取整到 nano）。 */
export function multiplyPrice(price: string, multiplier: string) {
  const a = toNano(price)
  const b = toNano(multiplier)
  if (a === null || b === null) return null
  return fromNano((a * b + scale - 1n) / scale)
}

/**
 * 渠道现价预览：积分价 × 倍率，勾选「汇率优惠」的美元模型再 ÷ 汇率（四舍五入到 nano），
 * 等于「美元价 × 倍率」。
 */
export function channelPrice(price: string, multiplier: string, fxDiscount: boolean, usdCnyRate: string | null | undefined) {
  if (!fxDiscount || !usdCnyRate) return multiplyPrice(price, multiplier)
  const p = toNano(price)
  const m = toNano(multiplier)
  const rate = toNano(usdCnyRate)
  if (p === null || m === null || rate === null || rate === 0n) return null
  return fromNano((p * m + rate / 2n) / rate)
}

/**
 * 实际倍率：渠道对模型积分价实际乘上的倍率。美元模型勾选「汇率优惠」时按
 * 「美元价 × 倍率」扣积分，相当于积分价 × 倍率 ÷ 汇率；其余等于填写的倍率。
 * 与后端 ledger.EffectiveMultiplier 一致，截断到 9 位小数。
 */
export function effectiveMultiplier(multiplier: string, fxDiscount: boolean, usdCnyRate: string | null | undefined) {
  if (!fxDiscount || !usdCnyRate) return multiplier
  const m = toNano(multiplier)
  const rate = toNano(usdCnyRate)
  if (m === null || rate === null || rate === 0n) return null
  return fromNano((m * scale) / rate)
}

export function contextText(tokens: number | null) {
  if (!tokens) return null
  if (tokens >= 1_000_000) return `${tokens / 1_000_000}M`
  return `${Math.round(tokens / 1000)}k`
}
