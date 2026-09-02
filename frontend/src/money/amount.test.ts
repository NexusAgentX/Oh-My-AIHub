import { describe, expect, it } from 'vitest'
import { formatNanoPoints, parseNanoPoints } from './amount'

describe('fixed-point point amounts', () => {
  it('adds nano-point values without floating-point loss', () => {
    const total = ['0.000000001', '3.75', '10.250000009'].reduce(
      (sum, value) => sum + parseNanoPoints(value),
      0n,
    )
    expect(formatNanoPoints(total)).toBe('14.00000001')
  })

  it('preserves values beyond Number safe integer precision', () => {
    const value = parseNanoPoints('9223372036.854775807')
    expect(formatNanoPoints(value)).toBe('9223372036.854775807')
  })
})
