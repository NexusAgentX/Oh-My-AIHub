import { describe, expect, it } from 'vitest'
import type { C2CAdminTrade, C2COrder, C2CTrade, C2CTradeView } from '../api/types'
import {
  c2cDisputeParties,
  c2cEventLabel,
  c2cFiatFen,
  c2cTakeLabel,
  c2cTakeQuantityError,
  c2cTradeActionHint,
  c2cTradeRole,
  isC2COrderCancellable,
  validateOrderDraft,
  c2cAdminDisputeActions,
  formatC2CFiat,
  isC2CTradeTerminal,
  parseC2CPriceFen,
} from './presentation'

describe('C2C presentation', () => {
  it('rounds external fiat up to an exact fen like the backend', () => {
    expect(c2cFiatFen('0.000000001', 1)).toBe(1)
    expect(c2cFiatFen('1.25', 101)).toBe(127)
    expect(formatC2CFiat(127)).toBe('¥1.27')
  })

  it('accepts only positive non-exponent prices with at most two decimals', () => {
    expect(parseC2CPriceFen('1')).toBe(100)
    expect(parseC2CPriceFen('1.05')).toBe(105)
    for (const value of ['0', '-1', '1.001', '1e2', '']) {
      expect(() => parseC2CPriceFen(value)).toThrow()
    }
  })

  it('distinguishes terminal trade states', () => {
    expect(isC2CTradeTerminal('released_to_buyer')).toBe(true)
    expect(isC2CTradeTerminal('expired')).toBe(true)
    expect(isC2CTradeTerminal('paid')).toBe(false)
    expect(isC2CTradeTerminal('disputed')).toBe(false)
  })

  it('maps each dispute party to its own restrict action and admin-only credit state', () => {
    const trade = {
      buyer_account_id: 'buyer-id', buyer_display_name: '买家A', buyer_credit_frozen: true,
      seller_account_id: 'seller-id', seller_display_name: '卖家B', seller_credit_frozen: false,
    } as C2CAdminTrade
    expect(c2cDisputeParties(trade).map((party) => [party.accountID, party.restrictAction, party.creditFrozen])).toEqual([
      ['buyer-id', 'restrict_buyer', true],
      ['seller-id', 'restrict_seller', false],
    ])
    const participantView = { ...trade, buyer_credit_frozen: undefined, seller_credit_frozen: undefined } as unknown as C2CTradeView
    expect(c2cDisputeParties(participantView).every((party) => party.creditFrozen === undefined)).toBe(true)
  })

  it('enables dispute actions exactly as the backend accepts them for each trade status', () => {
    expect(c2cAdminDisputeActions('disputed')).toEqual({ resolve: true, extendReview: true })
    expect(c2cAdminDisputeActions('paid')).toEqual({ resolve: true, extendReview: false })
    expect(c2cAdminDisputeActions('awaiting_payment')).toEqual({ resolve: false, extendReview: false })
    for (const status of ['released_to_buyer', 'returned_to_seller', 'cancelled', 'expired'] as const) {
      expect(c2cAdminDisputeActions(status)).toEqual({ resolve: false, extendReview: false })
    }
  })
})

const order = { minimum: '10', maximum: '50', available: '30' } as C2COrder
const trade = { buyer_account_id: 'buyer', seller_account_id: 'seller', status: 'awaiting_payment' } as C2CTrade

describe('C2C taking and trade roles', () => {
  it('labels the taker action by the order side', () => {
    expect(c2cTakeLabel('sell')).toBe('购买')
    expect(c2cTakeLabel('buy')).toBe('出售')
  })

  it('validates a taken quantity against limits and the available amount', () => {
    expect(c2cTakeQuantityError(order, '10')).toBe('')
    expect(c2cTakeQuantityError(order, '30')).toBe('')
    expect(c2cTakeQuantityError(order, '9')).toBe('数量不在单次限额内')
    expect(c2cTakeQuantityError(order, '31')).toBe('超过可成交数量')
    expect(c2cTakeQuantityError(order, 'abc')).toBe('请输入有效的积分数量')
    expect(c2cTakeQuantityError(order, '0')).toBe('请输入有效的积分数量')
  })

  it('allows a final partial take below the minimum when it clears the remainder', () => {
    const remainder = { ...order, available: '4' } as C2COrder
    expect(c2cTakeQuantityError(remainder, '4')).toBe('')
    expect(c2cTakeQuantityError(remainder, '3')).toBe('数量不在单次限额内')
  })

  it('derives the viewer role and pending action from the trade', () => {
    expect(c2cTradeRole(trade, 'buyer')).toBe('buyer')
    expect(c2cTradeRole(trade, 'seller')).toBe('seller')
    expect(c2cTradeRole(trade, 'other')).toBeNull()
    expect(c2cTradeActionHint(trade, 'buyer')).toBe('待你付款')
    expect(c2cTradeActionHint(trade, 'seller')).toBe('')
    expect(c2cTradeActionHint({ ...trade, status: 'paid' }, 'seller')).toBe('待你放行')
    expect(c2cTradeActionHint({ ...trade, status: 'paid' }, 'buyer')).toBe('')
  })

  it('lets only open or partly allocated orders be cancelled', () => {
    expect(isC2COrderCancellable({ status: 'open' })).toBe(true)
    expect(isC2COrderCancellable({ status: 'allocated' })).toBe(true)
    expect(isC2COrderCancellable({ status: 'filled' })).toBe(false)
    expect(isC2COrderCancellable({ status: 'cancelled' })).toBe(false)
  })
})

describe('C2C order draft validation', () => {
  const base = { price: '1.00', total: '100', minimum: '10', maximum: '100' }
  const contact = { contact: 'wx-id', instructions: '', qr: null }

  it('accepts sell orders with a QR code only and buy orders with contact', () => {
    const qr = new File([new Uint8Array([1])], 'qr.png', { type: 'image/png' })
    expect(validateOrderDraft({ ...base, side: 'sell', methods: [{ contact: '', instructions: '', qr }] })).toBe('')
    expect(validateOrderDraft({ ...base, side: 'buy', methods: [contact] })).toBe('')
  })

  it('requires buy orders to carry a contact and sell orders some payment info', () => {
    expect(validateOrderDraft({ ...base, side: 'buy', methods: [{ contact: '', instructions: 'x', qr: null }] })).toBe('请填写联系方式')
    expect(validateOrderDraft({ ...base, side: 'sell', methods: [{ contact: '', instructions: '', qr: null }] })).toContain('收款')
  })

  it('rejects inverted ranges and malformed prices', () => {
    expect(validateOrderDraft({ ...base, side: 'sell', maximum: '5', methods: [contact] })).toContain('数量范围')
    expect(validateOrderDraft({ ...base, side: 'sell', total: '50', methods: [contact] })).toContain('数量范围')
    expect(validateOrderDraft({ ...base, side: 'sell', price: '1.234', methods: [contact] })).toContain('格式')
  })
})

describe('C2C event labels', () => {
  it('translates known actions and falls back to the recorded reason', () => {
    expect(c2cEventLabel({ action: 'trade.paid', reason: 'buyer declared external payment' })).toBe('买家声明已付款')
    expect(c2cEventLabel({ action: 'restricted', reason: 'admin note' })).toBe('admin note')
    expect(c2cEventLabel({ action: 'unknown', reason: '' })).toBe('unknown')
  })
})
