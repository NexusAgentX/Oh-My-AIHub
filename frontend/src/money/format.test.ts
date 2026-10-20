import { describe, expect, it } from 'vitest'
import { formatFen, formatPoints, formatRatio, parseYuanToFen, tradeTotalFen } from './format'

describe('display formatting', () => {
  it('truncates points to four decimals and keeps tiny amounts visible', () => {
    expect(formatPoints('12345.678912')).toBe('12,345.6789')
    expect(formatPoints('-30.03')).toBe('-30.03')
    expect(formatPoints('0.00000002')).toBe('<0.0001')
    expect(formatPoints('1.5', { signed: true })).toBe('+1.5')
    expect(formatPoints(null)).toBe('—')
  })

  it('formats RMB fen and ratios', () => {
    expect(formatFen(2760)).toBe('¥27.60')
    expect(formatFen(123456789)).toBe('¥1,234,567.89')
    expect(parseYuanToFen('0.92')).toBe(92)
    expect(parseYuanToFen('1.2.3')).toBeNull()
    expect(formatRatio('0.985')).toBe('98.5%')
    expect(formatRatio(null)).toBe('—')
  })

  it('computes the amount payable in fen, rounding up', () => {
    expect(tradeTotalFen('30', 92)).toBe(2760)
    expect(tradeTotalFen('0.5', 93)).toBe(47)
    expect(tradeTotalFen('0', 92)).toBe(0)
  })
})
