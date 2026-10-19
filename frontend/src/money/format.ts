import { formatNanoPoints, parseNanoPoints } from './amount'

/** 规范化积分字符串显示；signed 时正数带 + 号。 */
export function formatPointAmount(value: string, signed = false) {
  const amount = parseNanoPoints(value)
  const formatted = formatNanoPoints(amount)
  return signed && amount > 0n ? `+${formatted}` : formatted
}

const nano = 1_000_000_000n

/**
 * 界面用的短格式：最多保留 digits 位小数（向零截断，不四舍五入），
 * 非零但截断后为 0 时显示「<0.0001」这类下限，避免把小额花费显示成 0。
 */
export function formatPoints(value: string | null | undefined, { digits = 4, signed = false } = {}) {
  if (value === null || value === undefined || value === '') return '—'
  let amount: bigint
  try {
    amount = parseNanoPoints(value)
  } catch {
    return value
  }
  const negative = amount < 0n
  const magnitude = negative ? -amount : amount
  const step = nano / 10n ** BigInt(digits)
  const truncated = (magnitude / step) * step
  const sign = negative ? '-' : signed && amount > 0n ? '+' : ''
  if (truncated === 0n && magnitude > 0n) {
    return `${negative ? '>-' : signed ? '+<' : '<'}${formatNanoPoints(step)}`
  }
  const [whole, fraction = ''] = formatNanoPoints(truncated).split('.')
  const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `${sign}${grouped}${fraction ? `.${fraction}` : ''}`
}

/** 金额符号：用于正绿负红。 */
export function amountSign(value: string) {
  try {
    const amount = parseNanoPoints(value)
    return amount > 0n ? 1 : amount < 0n ? -1 : 0
  } catch {
    return 0
  }
}

/** 人民币分 → "¥12.34"。 */
export function formatFen(fen: number) {
  const negative = fen < 0
  const abs = Math.round(Math.abs(fen))
  const yuan = Math.floor(abs / 100).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `${negative ? '-' : ''}¥${yuan}.${String(abs % 100).padStart(2, '0')}`
}

/** "12.34" 元 → 1234 分；非法返回 null。 */
export function parseYuanToFen(value: string) {
  const match = /^(\d+)(?:\.(\d{1,2}))?$/.exec(value.trim())
  if (!match) return null
  return Number(match[1]) * 100 + Number((match[2] ?? '').padEnd(2, '0'))
}

/** 买入应付：积分数量 × 单价（分/积分），按分向上取整。 */
export function tradeTotalFen(amount: string, unitPriceFen: number) {
  const points = parseNanoPoints(amount)
  if (points <= 0n) return 0
  const raw = points * BigInt(unitPriceFen)
  return Number((raw + nano - 1n) / nano)
}

/** 校验用户输入的积分数量（正数、最多 9 位小数）。 */
export function isPositiveAmount(value: string) {
  try {
    return parseNanoPoints(value) > 0n
  } catch {
    return false
  }
}

export function compareAmounts(left: string, right: string) {
  const a = parseNanoPoints(left)
  const b = parseNanoPoints(right)
  return a === b ? 0 : a < b ? -1 : 1
}

/** 比率字符串（0～1）→ "98.5%"。 */
export function formatRatio(value: string | null | undefined) {
  if (value === null || value === undefined) return '—'
  const number = Number(value)
  if (!Number.isFinite(number)) return '—'
  const percent = number * 100
  return `${percent >= 99.95 || Number.isInteger(percent) ? percent.toFixed(0) : percent.toFixed(1)}%`
}

export function formatTokens(value: number | null | undefined) {
  if (value === null || value === undefined) return '—'
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 0 : 1)}M`
  if (value >= 10_000) return `${(value / 1000).toFixed(0)}k`
  return value.toLocaleString('zh-CN')
}

export function formatMs(value: number | null | undefined) {
  if (value === null || value === undefined) return '—'
  if (value >= 10_000) return `${(value / 1000).toFixed(1)} s`
  if (value >= 1000) return `${(value / 1000).toFixed(2)} s`
  return `${Math.round(value)} ms`
}

/** 倍率显示："1" → "×1.0"。 */
export function formatMultiplier(value: string) {
  const number = Number(value)
  if (!Number.isFinite(number)) return value
  return `×${Number.isInteger(number) ? number.toFixed(1) : value}`
}

/** 积分显示：保留全部有效小数，至少两位（"0" → "0.00"，"12.5" → "12.50"）。 */
export function formatExactPoints(value: string, signed = false) {
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


export function formatCount(value: number) {
  return value.toLocaleString('zh-CN')
}


export function shortID(value: string) {
  return value.slice(0, 8)
}
