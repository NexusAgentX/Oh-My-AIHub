import type { ApiKey, ApiKeyCreateRequest, ApiKeyUpdateRequest } from '../api/types'
import { isPositiveAmount } from '../money/format'
import { parseNanoPoints } from '../money/amount'

export type AliasRow = { from: string; to: string }

/** Key 表单草稿；空字符串表示未设置。 */
export type KeyForm = {
  name: string
  enabled: boolean
  modelScope: 'all' | 'some'
  allowedModels: string[]
  budgetDaily: string
  budgetMonthly: string
  budgetTotal: string
  aliases: AliasRow[]
  /** datetime-local 值（本地时间），空为永不过期 */
  expiresAt: string
}

export const emptyKeyForm: KeyForm = {
  name: '',
  enabled: true,
  modelScope: 'all',
  allowedModels: [],
  budgetDaily: '',
  budgetMonthly: '',
  budgetTotal: '',
  aliases: [],
  expiresAt: '',
}

function toLocalInput(value: string | null) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (number: number) => String(number).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export function formFromKey(key: ApiKey): KeyForm {
  return {
    name: key.name,
    enabled: key.status === 'enabled',
    modelScope: key.allowed_models.length > 0 ? 'some' : 'all',
    allowedModels: key.allowed_models,
    budgetDaily: key.budget_daily ?? '',
    budgetMonthly: key.budget_monthly ?? '',
    budgetTotal: key.budget_total ?? '',
    aliases: Object.entries(key.model_aliases).map(([from, to]) => ({ from, to })),
    expiresAt: toLocalInput(key.expires_at),
  }
}

/** 高级设置五项中已改的项数：可用模型、预算、路由、别名、过期时间。 */
export function advancedChangedCount(form: KeyForm, routedModels = 0) {
  return [
    form.modelScope === 'some' && form.allowedModels.length > 0,
    Boolean(form.budgetDaily || form.budgetMonthly || form.budgetTotal),
    routedModels > 0,
    form.aliases.some((row) => row.from.trim() && row.to.trim()),
    Boolean(form.expiresAt),
  ].filter(Boolean).length
}

export function validateKeyForm(form: KeyForm) {
  if (!form.name.trim()) return '请填写名称'
  for (const [label, value] of [
    ['每日预算', form.budgetDaily],
    ['每月预算', form.budgetMonthly],
    ['总额预算', form.budgetTotal],
  ] as const) {
    if (value && !isPositiveAmount(value)) return `${label}须为正数，最多 9 位小数`
  }
  if (form.modelScope === 'some' && form.allowedModels.length === 0) return '请至少选择一个模型'
  const names = form.aliases.filter((row) => row.from.trim() || row.to.trim())
  if (names.some((row) => !row.from.trim() || !row.to.trim())) return '别名需要同时填写客户端名称与平台模型'
  if (new Set(names.map((row) => row.from.trim())).size !== names.length) return '客户端名称不能重复'
  return ''
}

function aliasesOf(form: KeyForm) {
  return Object.fromEntries(
    form.aliases.filter((row) => row.from.trim() && row.to.trim()).map((row) => [row.from.trim(), row.to.trim()]),
  )
}

function expiresOf(form: KeyForm) {
  return form.expiresAt ? new Date(form.expiresAt).toISOString() : null
}

export function createRequest(form: KeyForm): ApiKeyCreateRequest {
  return {
    name: form.name.trim(),
    status: form.enabled ? 'enabled' : 'disabled',
    allowed_models: form.modelScope === 'some' ? form.allowedModels : [],
    budget_daily: form.budgetDaily || null,
    budget_monthly: form.budgetMonthly || null,
    budget_total: form.budgetTotal || null,
    model_aliases: aliasesOf(form),
    expires_at: expiresOf(form),
  }
}

/** 只提交与原值不同的字段。 */
export function updateRequest(original: KeyForm, form: KeyForm): ApiKeyUpdateRequest {
  const before = createRequest(original)
  const after = createRequest(form)
  const patch: Record<string, unknown> = {}
  for (const key of Object.keys(after) as Array<keyof ApiKeyCreateRequest>) {
    if (JSON.stringify(before[key]) !== JSON.stringify(after[key])) patch[key] = after[key]
  }
  return patch as ApiKeyUpdateRequest
}

export type BudgetProgress = { label: string; percent: number; warn: boolean }

/** 最紧的一项预算：已花 / 预算。没有预算返回 null。≥80% 时提示。 */
export function budgetProgress(key: Pick<ApiKey, 'spend' | 'budget_daily' | 'budget_monthly' | 'budget_total'>): BudgetProgress | null {
  const items: Array<[string, string, string | null]> = [
    ['今日', key.spend.today, key.budget_daily],
    ['本月', key.spend.month, key.budget_monthly],
    ['总额', key.spend.total, key.budget_total],
  ]
  let best: BudgetProgress | null = null
  for (const [label, spent, budget] of items) {
    if (!budget) continue
    const limit = parseNanoPoints(budget)
    if (limit <= 0n) continue
    const percent = Number((parseNanoPoints(spent) * 10_000n) / limit) / 100
    if (!best || percent > best.percent) best = { label, percent, warn: percent >= 80 }
  }
  return best
}

/** 设置摘要徽标：单独路由 / N 个模型 / 已设别名。 */
export function keyBadges(key: ApiKey) {
  const badges: string[] = []
  if (key.routed_models.length > 0) badges.push('单独路由')
  if (key.allowed_models.length > 0) badges.push(`${key.allowed_models.length} 个模型`)
  if (Object.keys(key.model_aliases).length > 0) badges.push('已设别名')
  return badges
}
