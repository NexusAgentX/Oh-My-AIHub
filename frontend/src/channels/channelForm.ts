import type {
  Channel,
  ChannelAdvanced,
  ChannelAdvancedInput,
  ChannelModelInput,
  ChannelTestResult,
  DiscoveredModel,
  Format,
} from '../api/types'
import { isPositiveAmount } from '../money/format'

/** 选模型表格的一行。key 为本地稳定标识。 */
export type ModelRow = {
  key: string
  sell: boolean
  /** 平台模型；上游模型不在目录里时为空，需手动选择 */
  modelId: string
  upstream: string
  formats: Format[]
  multiplier: string
  /** 每种格式最近一次测试是否通过 */
  passed: Partial<Record<Format, boolean>>
}

export type HeaderSetRow = { name: string; value: string }

export type AdvancedForm = {
  userAgent: string
  headerSet: HeaderSetRow[]
  headerRemove: string[]
  concurrency: string
  rpm: string
  dailyCap: string
  ttftTimeout: string
  totalTimeout: string
  cooldownFailures: string
  cooldownMinutes: string
}

export const emptyAdvanced: AdvancedForm = {
  userAgent: '',
  headerSet: [],
  headerRemove: [],
  concurrency: '',
  rpm: '',
  dailyCap: '',
  ttftTimeout: '',
  totalTimeout: '',
  cooldownFailures: '',
  cooldownMinutes: '',
}

let sequence = 0
export function rowKey() {
  sequence += 1
  return `row-${sequence}`
}

/** discover 结果 → 表格：在目录里的模型默认勾选，格式按建议预选。 */
export function rowsFromDiscover(models: DiscoveredModel[]): ModelRow[] {
  return models.map((model) => ({
    key: rowKey(),
    sell: model.matched_model_id !== null,
    modelId: model.matched_model_id ?? '',
    upstream: model.id,
    formats: model.suggested_formats,
    multiplier: '1',
    passed: {},
  }))
}

export function rowsFromChannel(channel: Channel): ModelRow[] {
  return channel.models.map((model) => ({
    key: rowKey(),
    sell: model.enabled,
    modelId: model.model_id,
    upstream: model.upstream_model,
    formats: model.formats,
    multiplier: model.multiplier,
    passed: Object.fromEntries(Object.entries(model.format_tests).map(([format, test]) => [format, test.ok])),
  }))
}

/**
 * 把测试结果写回表格：标记每种格式是否通过；
 * 某模型至少一种格式通过时，格式改为通过的那些（自动修正预选）。
 */
export function applyTestResults(rows: ModelRow[], results: ChannelTestResult[]): ModelRow[] {
  return rows.map((row) => {
    const mine = results.filter((result) => result.model_id === row.modelId)
    if (mine.length === 0) return row
    const passed = { ...row.passed }
    for (const result of mine) passed[result.format] = result.ok
    const ok = mine.filter((result) => result.ok).map((result) => result.format)
    return { ...row, passed, formats: ok.length > 0 ? ok : row.formats }
  })
}

export function discoverSummary(models: DiscoveredModel[]) {
  return { total: models.length, matched: models.filter((model) => model.matched_model_id !== null).length }
}

export function modelsInput(rows: ModelRow[]): ChannelModelInput[] {
  return rows
    .filter((row) => row.modelId)
    .map((row) => ({
      model_id: row.modelId,
      upstream_model: row.upstream.trim() || row.modelId,
      multiplier: row.multiplier.trim() || '1',
      formats: row.formats,
      enabled: row.sell,
    }))
}

export function validateRows(rows: ModelRow[]) {
  const selling = rows.filter((row) => row.sell)
  if (selling.length === 0) return '至少勾选一个要卖的模型'
  if (selling.some((row) => !row.modelId)) return '勾选的模型需要选择平台模型'
  if (selling.some((row) => row.formats.length === 0)) return '每个勾选的模型至少选择一种格式'
  const ids = rows.filter((row) => row.modelId).map((row) => row.modelId)
  if (new Set(ids).size !== ids.length) return '同一个平台模型只能出现一次'
  if (selling.some((row) => !/^\d+(\.\d{1,9})?$/.test(row.multiplier.trim() || '1') || Number(row.multiplier) > 1000)) {
    return '倍率须为 0～1000 的数字'
  }
  return ''
}

function numberOrEmpty(value: number | null) {
  return value === null ? '' : String(value)
}

export function advancedFromChannel(advanced: ChannelAdvanced): AdvancedForm {
  return {
    userAgent: advanced.user_agent ?? '',
    headerSet: advanced.header_rules.set.map((rule) => ({ ...rule })),
    headerRemove: advanced.header_rules.remove.slice(),
    concurrency: numberOrEmpty(advanced.concurrency_limit),
    rpm: numberOrEmpty(advanced.rpm_limit),
    dailyCap: advanced.daily_revenue_cap ?? '',
    ttftTimeout: numberOrEmpty(advanced.ttft_timeout_ms),
    totalTimeout: numberOrEmpty(advanced.total_timeout_ms),
    cooldownFailures: numberOrEmpty(advanced.cooldown_failures),
    cooldownMinutes: advanced.cooldown_seconds === null ? '' : String(Math.max(1, Math.round(advanced.cooldown_seconds / 60))),
  }
}

/** 已改项数：UA、请求头规则、并发、RPM、每日收入上限、超时、失败冷却（各算一项）。 */
export function advancedChangedCount(form: AdvancedForm) {
  return [
    form.userAgent.trim(),
    form.headerSet.some((row) => row.name.trim()) || form.headerRemove.some((name) => name.trim()),
    form.concurrency,
    form.rpm,
    form.dailyCap,
    form.ttftTimeout || form.totalTimeout,
    form.cooldownFailures || form.cooldownMinutes,
  ].filter(Boolean).length
}

function intOrNull(value: string) {
  if (!value.trim()) return null
  const number = Number(value)
  return Number.isInteger(number) && number > 0 ? number : null
}

export function validateAdvanced(form: AdvancedForm) {
  const ints: Array<[string, string]> = [
    ['并发上限', form.concurrency],
    ['每分钟请求数', form.rpm],
    ['首字超时', form.ttftTimeout],
    ['总超时', form.totalTimeout],
    ['连续失败次数', form.cooldownFailures],
    ['冷却时长', form.cooldownMinutes],
  ]
  for (const [label, value] of ints) {
    if (value.trim() && intOrNull(value) === null) return `${label}须为正整数`
  }
  if (form.dailyCap && !isPositiveAmount(form.dailyCap)) return '每日收入上限须为正数'
  if (form.headerSet.some((row) => !row.name.trim() && row.value.trim())) return '请求头规则缺少名称'
  return ''
}

export function advancedInput(form: AdvancedForm): ChannelAdvancedInput {
  return {
    user_agent: form.userAgent.trim() || null,
    header_rules: {
      set: form.headerSet.filter((row) => row.name.trim()).map((row) => ({ name: row.name.trim(), value: row.value })),
      remove: form.headerRemove.map((name) => name.trim()).filter(Boolean),
    },
    concurrency_limit: intOrNull(form.concurrency),
    rpm_limit: intOrNull(form.rpm),
    daily_revenue_cap: form.dailyCap.trim() || null,
    ttft_timeout_ms: intOrNull(form.ttftTimeout),
    total_timeout_ms: intOrNull(form.totalTimeout),
    cooldown_failures: intOrNull(form.cooldownFailures),
    cooldown_seconds: intOrNull(form.cooldownMinutes) === null ? null : (intOrNull(form.cooldownMinutes) as number) * 60,
  }
}

export function channelStatus(channel: Channel) {
  if (channel.status === 'suspended') return { label: '被管理员下架', tone: 'danger' as const }
  if (channel.status === 'unlisted') return { label: '已下架', tone: 'neutral' as const }
  if (channel.cooldown_until && new Date(channel.cooldown_until).getTime() > Date.now()) {
    return { label: '冷却中', tone: 'warning' as const }
  }
  return { label: '在线', tone: 'success' as const }
}
