import type { Channel, ChannelOffer } from '../api/contracts'
import { formatNanoPoints, parseNanoPoints } from '../money/amount'

export type ChannelSummary = {
  /** 未删除的报价 */
  offers: ChannelOffer[]
  enabledCount: number
  eligibleCount: number
  calls: number
  /** 按调用次数加权的成功率（0–1 小数串）；没有调用时为 null */
  successRate: string | null
  /** 累计收入（积分，定点小数串）；没有任何收入记录时为 null */
  income: string | null
  /** 已删除报价贡献了收入 */
  incomeIncludesDeleted: boolean
}

function incomeOf(offer: ChannelOffer) {
  return offer.provider_income ? parseNanoPoints(offer.provider_income) : null
}

/** 汇总渠道下各报价的调用与收入；收入含已删除报价，因为它们已经入账。 */
export function summarizeChannel(channel: Pick<Channel, 'offers'>): ChannelSummary {
  const live = channel.offers.filter((offer) => offer.status !== 'deleted')
  let income: bigint | null = null
  let incomeIncludesDeleted = false
  for (const offer of channel.offers) {
    const value = incomeOf(offer)
    if (value === null) continue
    income = (income ?? 0n) + value
    if (offer.status === 'deleted' && value > 0n) incomeIncludesDeleted = true
  }
  let calls = 0
  let succeeded = 0
  for (const offer of live) {
    const count = offer.call_count ?? 0
    if (count <= 0 || offer.call_success_rate == null) continue
    calls += count
    succeeded += count * Number(offer.call_success_rate)
  }
  return {
    offers: live,
    enabledCount: live.filter((offer) => offer.status === 'active').length,
    eligibleCount: live.filter((offer) => offer.eligible).length,
    calls,
    successRate: calls > 0 ? (succeeded / calls).toFixed(4) : null,
    income: income === null ? null : formatNanoPoints(income),
    incomeIncludesDeleted,
  }
}

/** 多个渠道的收入合计；全部没有收入记录时为 null。 */
export function sumIncome(values: Array<string | null>): string | null {
  const present = values.filter((value): value is string => value !== null)
  if (present.length === 0) return null
  return formatNanoPoints(present.reduce((sum, value) => sum + parseNanoPoints(value), 0n))
}
