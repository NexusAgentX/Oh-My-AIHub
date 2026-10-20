const timeZone = 'Asia/Shanghai'

const dateTime = new Intl.DateTimeFormat('zh-CN', {
  timeZone,
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})
const fullDateTime = new Intl.DateTimeFormat('zh-CN', {
  timeZone,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})
const timeOnly = new Intl.DateTimeFormat('zh-CN', {
  timeZone,
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})

/** 列表时间：今天只显示时分秒，其余显示月-日 时:分。 */
export function formatTime(value: string | null | undefined, now = new Date()) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return sameShanghaiDay(date, now) ? timeOnly.format(date) : dateTime.format(date)
}

export function formatDateTime(value: string | null | undefined) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return fullDateTime.format(date)
}

/** YYYY-MM-DD（上海时区）。 */
export function shanghaiDate(date: Date) {
  return new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(date)
}

function sameShanghaiDay(left: Date, right: Date) {
  return shanghaiDate(left) === shanghaiDate(right)
}

/** 上海时区某天 00:00 的 RFC 3339 时间。 */
export function startOfShanghaiDay(date: Date) {
  return `${shanghaiDate(date)}T00:00:00+08:00`
}

/** 时间范围预设 → from（RFC 3339）；all 返回 undefined。 */
export type RangePreset = 'today' | '7d' | '30d' | 'all'
export function rangeFrom(preset: RangePreset, now = new Date()) {
  if (preset === 'all') return undefined
  if (preset === 'today') return startOfShanghaiDay(now)
  const days = preset === '7d' ? 6 : 29
  return startOfShanghaiDay(new Date(now.getTime() - days * 86_400_000))
}

/** "2026-10" → 当月起止（上海时区）。 */
export function monthRange(month: string) {
  const match = /^(\d{4})-(\d{2})$/.exec(month)
  if (!match) return { from: undefined, to: undefined }
  const year = Number(match[1])
  const index = Number(match[2])
  const nextYear = index === 12 ? year + 1 : year
  const nextMonth = index === 12 ? 1 : index + 1
  return {
    from: `${match[1]}-${match[2]}-01T00:00:00+08:00`,
    to: `${nextYear}-${String(nextMonth).padStart(2, '0')}-01T00:00:00+08:00`,
  }
}

export function currentMonth(now = new Date()) {
  return shanghaiDate(now).slice(0, 7)
}

/** 剩余秒数 → "12:05"。 */
export function formatCountdown(seconds: number) {
  const safe = Math.max(0, Math.floor(seconds))
  const minutes = Math.floor(safe / 60)
  return `${minutes}:${String(safe % 60).padStart(2, '0')}`
}
