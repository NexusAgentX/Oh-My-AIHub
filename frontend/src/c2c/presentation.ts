import type {
  C2COrderStatus,
  C2CPaymentMethodType,
  C2CSide,
  C2CTradeStatus,
} from '../api/contracts'
import { parseNanoPoints } from '../money/amount'

export const c2cSideLabels: Record<C2CSide, string> = {
  sell: '卖单',
  buy: '买单',
}

export const c2cPaymentLabels: Record<C2CPaymentMethodType, string> = {
  wechat: '微信',
  alipay: '支付宝',
  bank_transfer: '银行转账',
  other: '其他',
}

export const c2cOrderStatusLabels: Record<C2COrderStatus, string> = {
  open: '挂单中',
  allocated: '成交处理中',
  filled: '已成交',
  cancelled: '已取消',
}

export const c2cTradeStatusLabels: Record<C2CTradeStatus, string> = {
  awaiting_payment: '待付款',
  paid: '待放行',
  disputed: '争议中',
  released_to_buyer: '已放行',
  returned_to_seller: '已退还卖家',
  cancelled: '已取消',
  expired: '已超时',
}

export function c2cStatusTone(status: C2COrderStatus | C2CTradeStatus) {
  if (status === 'open' || status === 'released_to_buyer' || status === 'filled') return 'positive'
  if (status === 'disputed') return 'danger'
  if (status === 'cancelled' || status === 'expired' || status === 'returned_to_seller') return 'neutral'
  return 'warning'
}

export function formatC2CPrice(unitPriceFen: number) {
  return `¥${(unitPriceFen / 100).toFixed(2)}`
}

export function parseC2CPriceFen(value: string) {
  const match = /^(0|[1-9]\d{0,6})(?:\.(\d{1,2}))?$/.exec(value.trim())
  if (!match) throw new Error('invalid C2C price')
  const fen = Number(match[1]) * 100 + Number((match[2] ?? '').padEnd(2, '0'))
  if (!Number.isSafeInteger(fen) || fen <= 0) throw new Error('invalid C2C price')
  return fen
}

export function formatC2CFiat(fiatFen: number) {
  return `¥${(fiatFen / 100).toFixed(2)}`
}

export function c2cFiatFen(quantity: string, unitPriceFen: number) {
  const nano = parseNanoPoints(quantity)
  if (nano <= 0n || unitPriceFen <= 0) return 0
  const scale = 1_000_000_000n
  return Number((nano * BigInt(unitPriceFen) + scale - 1n) / scale)
}

export function formatC2CDate(value: string | null) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(date)
}

export function isC2CTradeTerminal(status: C2CTradeStatus) {
  return status === 'released_to_buyer' || status === 'returned_to_seller' || status === 'cancelled' || status === 'expired'
}
