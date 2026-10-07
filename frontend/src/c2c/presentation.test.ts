import { describe, expect, it } from 'vitest'
import type { C2CTrade } from '../api/contracts'
import {
  c2cDisputeParties,
  c2cFiatFen,
  canRestrictC2CParty,
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
    } as C2CTrade
    expect(c2cDisputeParties(trade).map((party) => [party.accountID, party.restrictAction, party.creditFrozen])).toEqual([
      ['buyer-id', 'restrict_buyer', true],
      ['seller-id', 'restrict_seller', false],
    ])
    const participantView = { ...trade, buyer_credit_frozen: undefined, seller_credit_frozen: undefined }
    expect(c2cDisputeParties(participantView).every((party) => party.creditFrozen === undefined)).toBe(true)
  })

  it('allows party restriction only while administrators can still resolve the trade', () => {
    expect(canRestrictC2CParty('disputed')).toBe(true)
    expect(canRestrictC2CParty('paid')).toBe(true)
    for (const status of ['awaiting_payment', 'released_to_buyer', 'returned_to_seller', 'cancelled', 'expired'] as const) {
      expect(canRestrictC2CParty(status)).toBe(false)
    }
  })
})
