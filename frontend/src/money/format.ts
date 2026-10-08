import { formatNanoPoints, parseNanoPoints } from './amount'

/** 规范化积分字符串显示；signed 时正数带 + 号。 */
export function formatPointAmount(value: string, signed = false) {
  const amount = parseNanoPoints(value)
  const formatted = formatNanoPoints(amount)
  return signed && amount > 0n ? `+${formatted}` : formatted
}
