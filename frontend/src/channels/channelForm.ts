import { cellsMatrix, matrixCells } from './Capabilities'
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
 capabilities?: string[]
  key: string
  sell: boolean
  /** 平台模型；上游模型不在目录里时为空，需手动选择 */
  modelId: string
  upstream: string
  formats: Format[]
  multiplier: string
  /** 汇率优惠：只对美元模型生效，按「美元价 × 倍率」扣积分 */
  fxDiscount: boolean
  /** 每种格式最近一次测试是否通过 */
  passed: Record<string, boolean>
  errors?: Record<string, string>
  /** 自定义模型：不在目录中，modelId 为自定的模型名，按 customPrices 直接计费 */
  custom: boolean
  customPrices: CustomPriceForm
  /** 原自定义模型已加入目录：设置倍率并勾选后恢复 */
  pending: boolean
}

/** 自定义模型的四个基准价（积分/百万 token）。 */
export type CustomPriceForm = { input: string; output: string; cacheWrite: string; cacheRead: string }

export const emptyCustomPrices: CustomPriceForm = { input: '', output: '', cacheWrite: '', cacheRead: '' }

/** 自定义模型名的规则同模型目录：字母或数字开头，可含 . _ -，至多 128 个字符。 */
export const customModelNamePattern = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/

const pricePattern = /^(0|[1-9][0-9]{0,5})(\.[0-9]{1,9})?$/

function validPrice(value: string) {
  return pricePattern.test(value.trim()) && Number(value) <= 100000
}

/** 把上游名称转换成合法的自定义模型名（去掉前缀与非法字符）。 */
export function customNameFrom(upstream: string) {
  const base = upstream.trim().split(/[/:]/).pop() ?? ''
  return base.replace(/[^A-Za-z0-9._-]/g, '-').replace(/^[^A-Za-z0-9]+/, '').slice(0, 128)
}

export type HeaderSetRow = { name: string; value: string }

export type AdvancedForm = {
 capabilities?: string[]
  userAgent: string
  headerSet: HeaderSetRow[]
  headerRemove: string[]
  concurrency: string
  rpm: string
  dailyCap: string
  ttftTimeout: string
  requestTimeout: string
  idleTimeout: string
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
  requestTimeout: '',
  idleTimeout: '',
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
    fxDiscount: false,
    passed: {},
    custom: false,
    customPrices: emptyCustomPrices,
    pending: false,
  }))
}

export function rowsFromChannel(channel: Channel): ModelRow[] {
  return channel.models.map((model) => ({
    key: rowKey(),
    sell: model.enabled,
    modelId: model.model_id,
    upstream: model.upstream_model,
    capabilities: matrixCells(model.compatibility), formats: model.formats,
    // 待设倍率的模型须重新填写倍率。
    multiplier: model.pending_multiplier ? '' : model.multiplier,
    fxDiscount: model.fx_discount,
    custom: model.custom_prices !== null,
    customPrices: model.custom_prices
      ? {
          input: model.custom_prices.input,
          output: model.custom_prices.output,
          cacheWrite: model.custom_prices.cache_write,
          cacheRead: model.custom_prices.cache_read,
        }
      : emptyCustomPrices,
    pending: model.pending_multiplier,
    errors: Object.fromEntries(Object.entries(model.format_tests).filter(([, test]) => !test.ok).map(([format, test]) => [format, test.error || '测试未通过'])),
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
    const errors = { ...row.errors }
    let capabilities = [...(row.capabilities ?? [])]
    for (const result of mine) {
      if (result.scope !== 'model' || result.skipped) continue
      const cell = `${result.protocol}/${result.capability}`
      passed[cell] = result.ok
      capabilities = capabilities.filter(v => v !== cell)
      if (result.ok) { capabilities.push(cell); delete errors[cell] }
      else errors[cell] = result.error || '探测未通过'
    }
    return { ...row, passed, errors, capabilities }

  })
}

export function discoverSummary(models: DiscoveredModel[]) {
  return { total: models.length, matched: models.filter((model) => model.matched_model_id !== null).length }
}

export function modelsInput(rows: ModelRow[]): ChannelModelInput[] {
  return rows
    .filter((row) => row.modelId)
    .map((row): ChannelModelInput => {
      const base = { compatibility: cellsMatrix(row.capabilities) as ChannelModelInput['compatibility'], model_id: row.modelId, upstream_model: row.upstream.trim() || row.modelId, enabled: row.sell }
      if (row.custom) {
        const prices = row.customPrices
        return {
          ...base,
          custom_prices: { input: prices.input.trim(), output: prices.output.trim(), cache_write: prices.cacheWrite.trim(), cache_read: prices.cacheRead.trim() },
        }
      }
      return { ...base, multiplier: row.multiplier.trim() || '1', fx_discount: row.fxDiscount }
    })
}

export function validateRows(rows: ModelRow[]) {
  const selling = rows.filter((row) => row.sell)
  if (selling.length === 0) return '至少勾选一个要卖的模型'
  if (selling.some((row) => !row.modelId)) return '勾选的模型需要选择平台模型或设为自定义模型'
  const ids = rows.filter((row) => row.modelId).map((row) => row.modelId)
  if (new Set(ids).size !== ids.length) return '同一个模型只能出现一次'
  const custom = rows.filter((row) => row.custom && row.modelId)
  if (custom.some((row) => !customModelNamePattern.test(row.modelId))) {
    return '自定义模型名须以字母或数字开头，只含字母、数字、. _ -'
  }
  if (custom.some((row) => !Object.values(row.customPrices).every(validPrice))) {
    return '自定义模型须填写四个价格，0～100000 积分/百万 token'
  }
  const pending = selling.find((row) => row.pending && !row.multiplier.trim())
  if (pending) return `${pending.modelId} 已加入模型目录，请先设置倍率`
  if (selling.some((row) => !row.custom && (!/^\d+(\.\d{1,9})?$/.test(row.multiplier.trim() || '1') || Number(row.multiplier) > 1000))) {
    return '倍率须为 0～1000 的数字'
  }
  return ''
}

function numberOrEmpty(value: number | null) {
  return value === null ? '' : String(value)
}

export function advancedFromChannel(advanced: ChannelAdvanced): AdvancedForm {
  return {
    capabilities: matrixCells(advanced.compatibility), userAgent: advanced.user_agent ?? '',
    headerSet: advanced.header_rules.set.map((rule) => ({ ...rule })),
    headerRemove: advanced.header_rules.remove.slice(),
    concurrency: numberOrEmpty(advanced.concurrency_limit),
    rpm: numberOrEmpty(advanced.rpm_limit),
    dailyCap: advanced.daily_revenue_cap ?? '',
    ttftTimeout: numberOrEmpty(advanced.ttft_timeout_ms),
    requestTimeout: numberOrEmpty(advanced.request_timeout_ms),
    idleTimeout: numberOrEmpty(advanced.idle_timeout_ms),
    cooldownFailures: numberOrEmpty(advanced.cooldown_failures),
    cooldownMinutes: advanced.cooldown_seconds === null ? '' : String(Math.max(1, Math.round(advanced.cooldown_seconds / 60))),
  }
}

/** 已改项数：UA、请求头规则、并发、RPM、每日收入上限、超时、失败冷却（各算一项）。 */
export function advancedChangedCount(form: AdvancedForm) {
  return [
    form.capabilities?.length, form.userAgent.trim(),
    form.headerSet.some((row) => row.name.trim()) || form.headerRemove.some((name) => name.trim()),
    form.concurrency,
    form.rpm,
    form.dailyCap,
    form.ttftTimeout || form.idleTimeout || form.requestTimeout,
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
    ['空闲超时', form.idleTimeout],
    ['请求超时', form.requestTimeout],
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
    compatibility: cellsMatrix(form.capabilities) as ChannelAdvancedInput['compatibility'], user_agent: form.userAgent.trim() || null,
    header_rules: {
      set: form.headerSet.filter((row) => row.name.trim()).map((row) => ({ name: row.name.trim(), value: row.value })),
      remove: form.headerRemove.map((name) => name.trim()).filter(Boolean),
    },
    concurrency_limit: intOrNull(form.concurrency),
    rpm_limit: intOrNull(form.rpm),
    daily_revenue_cap: form.dailyCap.trim() || null,
    ttft_timeout_ms: intOrNull(form.ttftTimeout),
    request_timeout_ms: intOrNull(form.requestTimeout),
    idle_timeout_ms: intOrNull(form.idleTimeout),
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
