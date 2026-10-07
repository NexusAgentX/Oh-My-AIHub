import { formatNanoPoints, parseNanoPoints } from '../money/amount'

const percentPattern = /^(\d{1,3})(?:\.(\d{1,7}))?$/
const percentNanoScale = 10_000_000n
const maxRateNano = 1_000_000_000n

/**
 * Converts a percent input (e.g. "0.1") to the API ratio string ("0.001").
 * Seven percent decimals equal the nine-decimal ratio precision of the API.
 * Returns null when the input is malformed, too precise or outside 0%–100%.
 */
export function percentToFeeRate(value: string): string | null {
  const match = percentPattern.exec(value.trim())
  if (!match) return null
  const [, whole, fraction = ''] = match
  const nano = BigInt(whole) * percentNanoScale + BigInt(fraction.padEnd(7, '0'))
  if (nano > maxRateNano) return null
  return formatNanoPoints(nano)
}

/** Formats an API ratio string ("0.001") as an exact percent ("0.1%"). */
export function formatFeeRatePercent(feeRate: string): string {
  const nano = parseNanoPoints(feeRate)
  const whole = nano / percentNanoScale
  const fraction = (nano % percentNanoScale).toString().padStart(7, '0').replace(/0+$/, '')
  return `${whole}${fraction ? `.${fraction}` : ''}%`
}
