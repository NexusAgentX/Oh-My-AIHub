import { describe, expect, it } from 'vitest'
import {
  c2cFiatFen,
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
})
