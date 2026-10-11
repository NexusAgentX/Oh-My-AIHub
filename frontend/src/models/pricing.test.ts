import { describe, expect, it } from 'vitest'
import { channelPrice, effectiveMultiplier } from './pricing'

describe('channel multipliers', () => {
  it('divides by the exchange rate only for discounted USD channels', () => {
    expect(effectiveMultiplier('0.13', true, '6.8')).toBe('0.019117647')
    expect(effectiveMultiplier('0.13', false, '6.8')).toBe('0.13')
    expect(effectiveMultiplier('2', true, null)).toBe('2')
    expect(effectiveMultiplier('abc', true, '6.8')).toBeNull()
  })
  it('previews the points a discounted channel charges', () => {
    // $1 per million at 6.8 points per dollar, multiplier 1 with the discount: 1 point.
    expect(channelPrice('6.8', '1', true, '6.8')).toBe('1')
    expect(channelPrice('6.8', '0.13', true, '6.8')).toBe('0.13')
    expect(channelPrice('6.8', '1', false, '6.8')).toBe('6.8')
  })
})
