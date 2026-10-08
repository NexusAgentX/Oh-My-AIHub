import { describe, expect, it } from 'vitest'
import { tradeTotalFen } from '../money/format'
import { creditUsedPercent, emptySellForm, maxSellable, needsMyAction, sellRequest, sortTrades, validateBuyAmount, validateSell } from './c2c'

const order = { available: '30', min_per_trade: '5', max_per_trade: '20' }

describe('C2C buy and sell', () => {
  it('computes the payable RMB amount live from quantity and unit price', () => {
    expect(tradeTotalFen('30', 92)).toBe(2760)
    expect(tradeTotalFen('12.345', 92)).toBe(1136)
  })

  it('validates the buy quantity against availability and per-trade range', () => {
    expect(validateBuyAmount('', order)).toBe('请填写数量')
    expect(validateBuyAmount('31', order)).toBe('最多可买 30')
    expect(validateBuyAmount('4', order)).toBe('单笔最少 5')
    expect(validateBuyAmount('21', order)).toBe('单笔最多 20')
    expect(validateBuyAmount('10', order)).toBe('')
  })

  it('only sells positive balance and needs a complete payment method', () => {
    expect(maxSellable('-3')).toBe('0')
    const form = { ...emptySellForm, amount: '10', unitPrice: '0.92', methods: [{ channel: '支付宝', account: 'a@b' }] }
    expect(validateSell(form, '5')).toBe('最多可卖 5')
    expect(validateSell(form, '50')).toBe('')
    expect(validateSell({ ...form, methods: [{ channel: '支付宝', account: '' }] }, '50')).toContain('收款方式')
    expect(sellRequest(form)).toEqual({
      amount: '10',
      unit_price_fen: 92,
      min_per_trade: '1',
      max_per_trade: null,
      payment_methods: [{ channel: '支付宝', account: 'a@b' }],
    })
  })

  it('pins trades waiting on me to the top', () => {
    const trades = [
      { id: '1', status: 'released', viewer_role: 'buyer', created_at: '2026-10-08T03:00:00Z' },
      { id: '2', status: 'paid', viewer_role: 'seller', created_at: '2026-10-08T01:00:00Z' },
      { id: '3', status: 'paid', viewer_role: 'buyer', created_at: '2026-10-08T02:00:00Z' },
    ] as const
    expect(needsMyAction(trades[1])).toBe(true)
    expect(needsMyAction(trades[2])).toBe(false)
    expect(sortTrades([...trades]).map((trade) => trade.id)).toEqual(['2', '1', '3'])
  })

  it('reports credit usage only for negative balances', () => {
    expect(creditUsedPercent('-30.03', '100')).toBe(30.03)
    expect(creditUsedPercent('5', '100')).toBe(0)
  })
})
