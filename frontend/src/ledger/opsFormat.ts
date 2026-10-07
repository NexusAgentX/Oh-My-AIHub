export type WindowKey = '24' | '168' | '720'

export const windowOptions: Array<{ key: WindowKey; label: string; hours: number }> = [
  { key: '24', label: '24 小时', hours: 24 },
  { key: '168', label: '7 天', hours: 24 * 7 },
  { key: '720', label: '30 天', hours: 24 * 30 },
]

export function parseWindow(value: string | null): WindowKey {
  return windowOptions.find((option) => option.key === value)?.key ?? '24'
}

export function windowHours(key: WindowKey) {
  return windowOptions.find((option) => option.key === key)?.hours ?? 24
}

export type OpsTab = 'overview' | 'providers' | 'evidence' | 'ledger'

export const opsTabs: Array<{ key: OpsTab; label: string }> = [
  { key: 'overview', label: '总览' },
  { key: 'providers', label: '共享者收入' },
  { key: 'evidence', label: '试用与巡检' },
  { key: 'ledger', label: '账本与费率' },
]

export function parseTab(value: string | null): OpsTab {
  return opsTabs.find((tab) => tab.key === value)?.key ?? 'overview'
}

/** 仅这两个分区随时间窗口变化。 */
export function isWindowedTab(tab: OpsTab) {
  return tab === 'overview' || tab === 'providers'
}

export function formatFen(fen: number | null) {
  if (fen === null) return '—'
  return `¥${(fen / 100).toFixed(2)}`
}

export function formatShare(value: string | null) {
  if (value === null) return '—'
  return `${(Number(value) * 100).toFixed(2)}%`
}

export function formatDateTime(value: string | null) {
  return value ? new Date(value).toLocaleString('zh-CN') : '—'
}

/** 异常下钻只保留路径，丢弃后端附带的查询串（固定下钻）。 */
export function drilldownPath(drilldown: string) {
  return drilldown.split('?')[0]
}
