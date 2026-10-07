import { describe, expect, it } from 'vitest'
import { formatFeeRatePercent, percentToFeeRate } from './feeRate'

describe('fee rate percent conversion', () => {
  it('converts percent input to exact nine-decimal ratios', () => {
    expect(percentToFeeRate('0.1')).toBe('0.001')
    expect(percentToFeeRate(' 2.5 ')).toBe('0.025')
    expect(percentToFeeRate('0')).toBe('0')
    expect(percentToFeeRate('100')).toBe('1')
    expect(percentToFeeRate('0.0000001')).toBe('0.000000001')
  })

  it('rejects out-of-range, over-precise and malformed input', () => {
    for (const value of ['', '-1', '100.0000001', '101', '0.00000001', '1e-3', '.5', 'abc', '1,5']) {
      expect(percentToFeeRate(value)).toBeNull()
    }
  })

  it('formats ratios as exact percents', () => {
    expect(formatFeeRatePercent('0.001')).toBe('0.1%')
    expect(formatFeeRatePercent('0')).toBe('0%')
    expect(formatFeeRatePercent('1')).toBe('100%')
    expect(formatFeeRatePercent('0.000000001')).toBe('0.0000001%')
    expect(formatFeeRatePercent('0.123456789')).toBe('12.3456789%')
  })
})
