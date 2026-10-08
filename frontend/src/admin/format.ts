import { formatNanoPoints, parseNanoPoints } from '../money/amount'

/** 积分显示：保留全部有效小数，至少两位（"0" → "0.00"，"12.5" → "12.50"）。 */
export function formatPoints(value: string, signed = false) {
  const amount = parseNanoPoints(value)
  const text = formatNanoPoints(amount)
  const [whole, fraction = ''] = text.split('.')
  const padded = `${whole}.${fraction.padEnd(2, '0')}`
  return signed && amount > 0n ? `+${padded}` : padded
}

export function isNegative(value: string) {
  return parseNanoPoints(value) < 0n
}

export function isZero(value: string) {
  return parseNanoPoints(value) === 0n
}

export function sumPoints(values: string[]) {
  return formatNanoPoints(values.reduce((sum, value) => sum + parseNanoPoints(value), 0n))
}

/** a 占 b 的百分比（0～100+），b 为 0 时返回 0。 */
export function pointsRatio(part: string, whole: string) {
  const denominator = parseNanoPoints(whole)
  if (denominator === 0n) return 0
  return Number((parseNanoPoints(part) * 10000n) / denominator) / 100
}

/** 1e9 nano = 100%，所以 1% = 1e7 nano。 */
const nanoPerPercent = 10_000_000n
const percentPattern = /^(\d{1,3})(?:\.(\d{1,7}))?$/

/** 费率 nano → 百分比字符串（1000000 → "0.1"）。 */
export function feeRateToPercent(nano: number) {
  const value = BigInt(nano)
  const whole = value / nanoPerPercent
  const fraction = (value % nanoPerPercent).toString().padStart(7, '0').replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : String(whole)
}

/** 百分比字符串 → 费率 nano；格式错误或超过 100% 返回 null。 */
export function percentToFeeRate(percent: string): number | null {
  const match = percentPattern.exec(percent.trim())
  if (!match) return null
  const nano = BigInt(match[1]) * nanoPerPercent + BigInt((match[2] ?? '').padEnd(7, '0'))
  if (nano > 1_000_000_000n) return null
  return Number(nano)
}

const amountPattern = /^-?\d+(\.\d{1,9})?$/

/** 积分输入校验：十进制，最多 9 位小数。 */
export function isAmount(value: string) {
  return amountPattern.test(value.trim())
}

/** 0～1 的比率字符串 → "95.2%"；null 显示 "—"。 */
export function formatRatio(value: string | null | undefined) {
  if (value === null || value === undefined) return '—'
  const percent = Number(value) * 100
  return `${Number.isInteger(percent) ? percent : percent.toFixed(1)}%`
}

const dateTimeFormat = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})

/** 北京时间 "2026/10/08 14:30"。 */
export function formatDateTime(value: string | null | undefined) {
  if (!value) return '—'
  return dateTimeFormat.format(new Date(value))
}

export function formatCount(value: number) {
  return value.toLocaleString('zh-CN')
}

export function formatFen(fen: number) {
  return `¥${(fen / 100).toFixed(2)}`
}

export function shortID(value: string) {
  return value.slice(0, 8)
}

/** 从现在起已过去的整天数。 */
export function daysSince(value: string | null | undefined, now = Date.now()) {
  if (!value) return null
  return Math.max(0, Math.floor((now - new Date(value).getTime()) / 86_400_000))
}

/** 最近活跃：1 小时内「刚刚」，24 小时内「N 小时前」，其余「N 天前」，从未活跃「从未」。 */
export function formatLastActive(value: string | null | undefined, now = Date.now()) {
  if (!value) return '从未'
  const hours = Math.floor((now - new Date(value).getTime()) / 3_600_000)
  if (hours < 1) return '刚刚'
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}
