import type {
  C2COrder,
  C2COrderStatus,
  C2CResolutionAction,
  C2CTrade,
  C2CTradeView,
  C2CPaymentMethodType,
  C2CTradeStatus,
} from '../api/types'
import { parseNanoPoints } from '../money/amount'

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

export type C2CDisputeParty = {
  role: 'buyer' | 'seller'
  label: string
  accountID: string
  displayName: string
  creditFrozen: boolean | undefined
  restrictAction: Extract<C2CResolutionAction, 'restrict_buyer' | 'restrict_seller'>
}

export function c2cDisputeParties(trade: C2CTradeView): C2CDisputeParty[] {
  return [
    {
      role: 'buyer', label: '买家', accountID: trade.buyer_account_id,
      displayName: trade.buyer_display_name, creditFrozen: 'buyer_credit_frozen' in trade ? trade.buyer_credit_frozen : undefined,
      restrictAction: 'restrict_buyer',
    },
    {
      role: 'seller', label: '卖家', accountID: trade.seller_account_id,
      displayName: trade.seller_display_name, creditFrozen: 'seller_credit_frozen' in trade ? trade.seller_credit_frozen : undefined,
      restrictAction: 'restrict_seller',
    },
  ]
}

export type C2CAdminDisputeActions = {
  /** 放行 / 退还 / 限制账户：后端仅接受 paid 与 disputed 的交易。 */
  resolve: boolean
  /** 延长复核：后端仅接受 disputed。 */
  extendReview: boolean
}

export function c2cAdminDisputeActions(status: C2CTradeStatus): C2CAdminDisputeActions {
  return {
    resolve: status === 'paid' || status === 'disputed',
    extendReview: status === 'disputed',
  }
}

export function isC2COrderCancellable(order: Pick<C2COrder, 'status'>) {
  return order.status === 'open' || order.status === 'allocated'
}

/** 我在交易中的角色；与交易无关时返回 null。 */
export function c2cTradeRole(trade: C2CTrade, accountID: string) {
  if (trade.buyer_account_id === accountID) return 'buyer' as const
  if (trade.seller_account_id === accountID) return 'seller' as const
  return null
}

/** 需要当前用户处理的交易提示：买家待付款、卖家待放行；其余返回空串。 */
export function c2cTradeActionHint(trade: C2CTrade, accountID: string) {
  const role = c2cTradeRole(trade, accountID)
  if (trade.status === 'awaiting_payment' && role === 'buyer') return '待你付款'
  if (trade.status === 'paid' && role === 'seller') return '待你放行'
  return ''
}

/** 承接数量校验：返回错误文案，合法时返回空串。 */
export function c2cTakeQuantityError(order: C2COrder, quantity: string) {
  let value: bigint
  try {
    value = parseNanoPoints(quantity)
  } catch {
    return '请输入有效的积分数量'
  }
  if (value <= 0n) return '请输入有效的积分数量'
  const available = parseNanoPoints(order.available)
  if (value > available) return '超过可成交数量'
  if (value > parseNanoPoints(order.maximum)) return '数量不在单次限额内'
  // 剩余量不足单次最少时，允许一次吃完。
  if (value < parseNanoPoints(order.minimum) && value !== available) return '数量不在单次限额内'
  return ''
}

/** 返回表单校验错误文案；合法时返回空串。至少填写收款账号、备注或收款码之一。 */
export function validateOrderDraft(input: {
  price: string
  total: string
  minimum: string
  maximum: string
  methods: Array<{ contact: string; instructions: string; qr: File | null }>
}) {
  try {
    parseC2CPriceFen(input.price)
    const total = parseNanoPoints(input.total)
    const minimum = parseNanoPoints(input.minimum)
    const maximum = parseNanoPoints(input.maximum)
    if (total <= 0n || minimum <= 0n || maximum < minimum || maximum > total) {
      return '请检查数量范围：0 < 单次最少 ≤ 单次最多 ≤ 挂单数量'
    }
  } catch {
    return '请检查单价与数量格式'
  }
  const incomplete = input.methods.some(
    (method) => !method.contact.trim() && !method.instructions.trim() && !method.qr,
  )
  return incomplete ? '请至少填写收款账号、备注或上传收款码' : ''
}

const c2cEventLabels: Record<string, string> = {
  'order.created': '挂单已发布',
  'order.cancelled': '挂单已取消',
  'order.admin_cancelled': '管理员取消挂单',
  'trade.created': '交易已创建',
  'trade.paid': '买家声明已付款',
  'trade.released': '卖家确认收款，积分已放行',
  'trade.cancelled': '交易已取消',
  'trade.expired': '付款超时，交易已过期',
  'trade.disputed': '已发起争议',
  'trade.statement_added': '补充了陈述',
  'dispute.extended': '管理员延长核实',
  'dispute.released': '管理员裁决：放行给买家',
  'dispute.returned': '管理员裁决：退回卖家',
}

/** 交易记录的中文说明；未知动作回退为后端原因或动作名。 */
export function c2cEventLabel(event: { action: string; reason: string }) {
  return c2cEventLabels[event.action] ?? (event.reason || event.action)
}
