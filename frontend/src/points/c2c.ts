import type { C2CTrade, PaymentMethod, TradeStatus, TransactionType } from '../api/types'
import type { BadgeTone } from '../ui'
import { parseNanoPoints, formatNanoPoints } from '../money/amount'
import { isPositiveAmount, parseYuanToFen } from '../money/format'

export const tradeStatusLabels: Record<TradeStatus, { label: string; tone: BadgeTone }> = {
  awaiting_payment: { label: '待付款', tone: 'warning' },
  paid: { label: '已付款', tone: 'info' },
  released: { label: '已完成', tone: 'success' },
  cancelled: { label: '已取消', tone: 'neutral' },
  disputed: { label: '申诉中', tone: 'danger' },
  resolved_to_buyer: { label: '判给买家', tone: 'success' },
  resolved_to_seller: { label: '退回卖家', tone: 'neutral' },
}

export const transactionTypeLabels: Record<TransactionType, string> = {
  api_call: '调用',
  c2c_list: 'C2C 挂单',
  c2c_release: 'C2C 成交',
  c2c_return: 'C2C 退回',
  admin_adjust: '调账',
  bad_debt_writeoff: '坏账核销',
}

/** 需要我处理：买家待付款、卖家待放行。 */
export function needsMyAction(trade: Pick<C2CTrade, 'status' | 'viewer_role'>) {
  return (
    (trade.viewer_role === 'buyer' && trade.status === 'awaiting_payment') ||
    (trade.viewer_role === 'seller' && trade.status === 'paid')
  )
}

/** 待我处理的置顶，其余按创建时间倒序。 */
export function sortTrades<T extends Pick<C2CTrade, 'status' | 'viewer_role' | 'created_at'>>(trades: T[]) {
  return trades.slice().sort((a, b) => {
    const urgent = Number(needsMyAction(b)) - Number(needsMyAction(a))
    return urgent || b.created_at.localeCompare(a.created_at)
  })
}

/** 可卖上限：只能卖正余额。 */
export function maxSellable(balance: string) {
  const value = parseNanoPoints(balance)
  return formatNanoPoints(value > 0n ? value : 0n)
}

export type SellForm = {
  amount: string
  unitPrice: string
  minPerTrade: string
  maxPerTrade: string
  methods: PaymentMethod[]
}

export const emptySellForm: SellForm = {
  amount: '',
  unitPrice: '',
  minPerTrade: '1',
  maxPerTrade: '',
  methods: [{ channel: '', account: '' }],
}

export const maxPaymentMethods = 5

export function validateSell(form: SellForm, balance: string) {
  if (!isPositiveAmount(form.amount)) return '请填写卖出数量'
  if (parseNanoPoints(form.amount) > parseNanoPoints(maxSellable(balance))) return `最多可卖 ${maxSellable(balance)}`
  const fen = parseYuanToFen(form.unitPrice)
  if (fen === null || fen <= 0) return '单价须为大于 0 的金额，最多 2 位小数'
  if (!isPositiveAmount(form.minPerTrade)) return '单笔最少须为正数'
  if (form.maxPerTrade) {
    if (!isPositiveAmount(form.maxPerTrade)) return '单笔最多须为正数'
    if (parseNanoPoints(form.maxPerTrade) < parseNanoPoints(form.minPerTrade)) return '单笔最多不能小于单笔最少'
  }
  if (parseNanoPoints(form.minPerTrade) > parseNanoPoints(form.amount)) return '单笔最少不能超过卖出数量'
  const methods = form.methods.filter((method) => method.channel.trim() || method.account.trim())
  if (methods.length === 0) return '至少填写一种收款方式'
  if (methods.length > maxPaymentMethods) return `收款方式最多 ${maxPaymentMethods} 条`
  if (methods.some((method) => !method.channel.trim() || !method.account.trim())) return '收款方式需要同时填写方式与账号'
  return ''
}

export function sellRequest(form: SellForm) {
  return {
    amount: form.amount.trim(),
    unit_price_fen: parseYuanToFen(form.unitPrice) ?? 0,
    min_per_trade: form.minPerTrade.trim(),
    max_per_trade: form.maxPerTrade.trim() || null,
    payment_methods: form.methods
      .filter((method) => method.channel.trim() && method.account.trim())
      .map((method) => ({ channel: method.channel.trim(), account: method.account.trim() })),
  }
}

/** 买入数量校验：正数、不超过可买、在单笔范围内。 */
export function validateBuyAmount(amount: string, order: { available: string; min_per_trade: string; max_per_trade: string | null }) {
  if (!isPositiveAmount(amount)) return '请填写数量'
  const value = parseNanoPoints(amount)
  if (value > parseNanoPoints(order.available)) return `最多可买 ${order.available}`
  if (value < parseNanoPoints(order.min_per_trade)) return `单笔最少 ${order.min_per_trade}`
  if (order.max_per_trade && value > parseNanoPoints(order.max_per_trade)) return `单笔最多 ${order.max_per_trade}`
  return ''
}

/** 信用额度已用比例（余额为负时）。 */
export function creditUsedPercent(balance: string, creditLimit: string) {
  const limit = parseNanoPoints(creditLimit)
  const value = parseNanoPoints(balance)
  if (limit <= 0n || value >= 0n) return 0
  return Math.min(100, Number((-value * 10_000n) / limit) / 100)
}
