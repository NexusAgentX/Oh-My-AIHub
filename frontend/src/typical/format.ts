import type { components } from '../api/schema.gen'

const amountFormat = new Intl.NumberFormat('zh-CN', { maximumSignificantDigits: 2 })
const units: Record<string, string> = { tokens: 'tokens', successful_images: '张成功图片', requests: '次请求' }

export function formatTypicalAmount(amount: string | null) {
  return amount === null ? '未知' : `${amountFormat.format(Number(amount))} 积分`
}

export function formatTypicalUnit(unit: string, quantity: number) {
  return unit === 'tokens' && quantity === 1_000_000 ? 'M' : `${quantity.toLocaleString()} ${units[unit] ?? unit}`
}

export function formatTypicalPrice(value: components['schemas']['MarketBrowsePrice'] | null | undefined) {
  if (!value) return '未知'
  if (value.self_net_zero) return '站内净支付 0'
  return `${formatTypicalAmount(value.amount)} / ${formatTypicalUnit(value.unit, value.quantity)}`
}
