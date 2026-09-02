const scale = 1_000_000_000n
const amountPattern = /^([+-]?)(\d+)(?:\.(\d{1,9}))?$/

export function parseNanoPoints(value: string): bigint {
  const match = amountPattern.exec(value.trim())
  if (!match) throw new Error('invalid point amount')
  const [, sign, whole, fraction = ''] = match
  const magnitude = BigInt(whole) * scale + BigInt(fraction.padEnd(9, '0'))
  return sign === '-' ? -magnitude : magnitude
}

export function formatNanoPoints(value: bigint): string {
  const negative = value < 0n
  const magnitude = negative ? -value : value
  const whole = magnitude / scale
  const fraction = magnitude % scale
  if (fraction === 0n) return `${negative ? '-' : ''}${whole}`
  const fractionText = fraction.toString().padStart(9, '0').replace(/0+$/, '')
  return `${negative ? '-' : ''}${whole}.${fractionText}`
}
