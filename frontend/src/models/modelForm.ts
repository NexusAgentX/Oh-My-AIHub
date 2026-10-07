import type { CatalogModel, ModelInput, PriceTier } from '../api/contracts'

export type TierForm = {
  name: string
  /** 条件一：输入 Token 区间。关闭时 min/max 不会提交。 */
  useTokens: boolean
  minPromptTokens: string
  maxPromptTokens: string
  /** 条件二：星期与时间窗（带时区）。关闭时 weekdays 与时间窗不会提交。 */
  useTime: boolean
  timezone: string
  weekdays: number[]
  /** 时间条件内的「限定时段」子开关；关闭则全天。 */
  useWindow: boolean
  startTime: string
  endTime: string
  inputPrice: string
  outputPrice: string
  cacheWritePrice: string
  cacheReadPrice: string
}

export type TierInput = ModelInput['price_tiers'][number]

export const emptyTierForm: TierForm = {
  name: '',
  useTokens: true,
  minPromptTokens: '',
  maxPromptTokens: '',
  useTime: false,
  timezone: 'UTC',
  weekdays: [],
  useWindow: false,
  startTime: '',
  endTime: '',
  inputPrice: '0',
  outputPrice: '0',
  cacheWritePrice: '0',
  cacheReadPrice: '0',
}

const weekdayLabels = ['周一', '周二', '周三', '周四', '周五', '周六', '周日']

export function weekdayLabel(weekday: number): string {
  return weekdayLabels[weekday - 1] ?? String(weekday)
}

export function minutesToTime(minutes: number): string {
  const hours = Math.floor(minutes / 60)
  return `${String(hours).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`
}

export function timeToMinutes(value: string): number | null {
  const match = /^([0-9]{1,2}):([0-9]{2})$/.exec(value.trim())
  if (!match) return null
  const hours = Number(match[1])
  const minutes = Number(match[2])
  if (hours === 24 && minutes === 0) return 1440
  if (hours > 23 || minutes > 59) return null
  return hours * 60 + minutes
}

export function tierToForm(tier: PriceTier): TierForm {
  return {
    name: tier.name,
    useTokens: tier.min_prompt_tokens !== null || tier.max_prompt_tokens !== null,
    minPromptTokens: tier.min_prompt_tokens === null ? '' : String(tier.min_prompt_tokens),
    maxPromptTokens: tier.max_prompt_tokens === null ? '' : String(tier.max_prompt_tokens),
    useTime: (tier.weekdays?.length ?? 0) > 0 || tier.start_minute_of_day !== null,
    timezone: tier.timezone || 'UTC',
    weekdays: [...(tier.weekdays ?? [])].sort((left, right) => left - right),
    useWindow: tier.start_minute_of_day !== null,
    startTime: tier.start_minute_of_day === null ? '' : minutesToTime(tier.start_minute_of_day),
    endTime: tier.end_minute_of_day === null ? '' : minutesToTime(tier.end_minute_of_day),
    inputPrice: tier.input_price,
    outputPrice: tier.output_price,
    cacheWritePrice: tier.cache_write_price,
    cacheReadPrice: tier.cache_read_price,
  }
}

export function formToTierInput(form: TierForm): TierInput {
  const minPrompt = form.useTokens ? form.minPromptTokens.trim() : ''
  const maxPrompt = form.useTokens ? form.maxPromptTokens.trim() : ''
  const useWindow = form.useTime && form.useWindow
  const start = useWindow ? timeToMinutes(form.startTime) : null
  const end = useWindow && form.endTime.trim() !== '' ? timeToMinutes(form.endTime) : null
  const weekdays = form.useTime ? form.weekdays : []
  return {
    name: form.name.trim(),
    timezone: form.timezone.trim() || 'UTC',
    min_prompt_tokens: minPrompt === '' ? null : Number(minPrompt),
    max_prompt_tokens: maxPrompt === '' ? null : Number(maxPrompt),
    weekdays: weekdays.length > 0 ? [...weekdays].sort((left, right) => left - right) : null,
    start_minute_of_day: useWindow ? start : null,
    end_minute_of_day: useWindow ? (end ?? 1440) : null,
    input_price: form.inputPrice.trim(),
    output_price: form.outputPrice.trim(),
    cache_write_price: form.cacheWritePrice.trim(),
    cache_read_price: form.cacheReadPrice.trim(),
  }
}

export function formatTokenBound(value: number): string {
  if (value >= 1_000_000 && value % 1_000_000 === 0) return `${value / 1_000_000}M`
  if (value >= 1_000 && value % 1_000 === 0) return `${value / 1_000}K`
  return value.toLocaleString('zh-CN')
}

function weekdaysSummary(weekdays: number[]): string {
  const sorted = [...weekdays].sort((left, right) => left - right)
  if (sorted.join() === '1,2,3,4,5') return '工作日'
  if (sorted.join() === '6,7') return '周末'
  return sorted.map(weekdayLabel).join('/')
}

/** 档位条件的单行摘要，用于列表展示（只描述当前开启并会被提交的条件）。 */
export function tierConditionSummary(form: TierForm): string {
  const input = formToTierInput(form)
  const parts: string[] = []
  const { min_prompt_tokens: min, max_prompt_tokens: max } = input
  if (min !== null && max !== null) parts.push(`输入 ${formatTokenBound(min)}–${formatTokenBound(max)}`)
  else if (min !== null) parts.push(`输入 ≥ ${formatTokenBound(min)}`)
  else if (max !== null) parts.push(`输入 < ${formatTokenBound(max)}`)
  const days = input.weekdays ? weekdaysSummary(input.weekdays) : ''
  if (input.start_minute_of_day !== null && input.end_minute_of_day !== null) {
    const window = `${minutesToTime(input.start_minute_of_day)}–${minutesToTime(input.end_minute_of_day)}`
    parts.push(`${days || '每天'} ${window}（${input.timezone}）`)
  } else if (days) {
    parts.push(`${days}（${input.timezone}）`)
  }
  return parts.length > 0 ? parts.join(' · ') : '未设置条件'
}

/** 档位按序首个命中，调整顺序即调整优先级。越界时原样返回。 */
export function moveTier<T>(tiers: T[], index: number, delta: -1 | 1): T[] {
  const target = index + delta
  if (index < 0 || index >= tiers.length || target < 0 || target >= tiers.length) return tiers
  const next = [...tiers]
  ;[next[index], next[target]] = [next[target], next[index]]
  return next
}

/** 开关某类条件；关闭时清空对应字段，避免残留值误导。 */
export function setTierCondition(
  tier: TierForm,
  condition: 'tokens' | 'time',
  enabled: boolean,
): TierForm {
  if (condition === 'tokens') {
    return enabled
      ? { ...tier, useTokens: true }
      : { ...tier, useTokens: false, minPromptTokens: '', maxPromptTokens: '' }
  }
  return enabled
    ? { ...tier, useTime: true }
    : {
        ...tier,
        useTime: false,
        weekdays: [],
        useWindow: false,
        startTime: '',
        endTime: '',
      }
}

export function toggleWeekday(tier: TierForm, weekday: number, checked: boolean): TierForm {
  const weekdays = checked
    ? [...new Set([...tier.weekdays, weekday])].sort((left, right) => left - right)
    : tier.weekdays.filter((item) => item !== weekday)
  return { ...tier, weekdays }
}

function validTimezone(timezone: string): boolean {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: timezone })
    return true
  } catch {
    return false
  }
}

export type ModelForm = {
  id: string
  name: string
  provider: string
  contextWindow: string
  parameterInfo: string
  inputModalities: string[]
  outputModalities: string[]
  supportsTools: boolean
  supportsStructuredOutput: boolean
  supportsVision: boolean
  inputPrice: string
  outputPrice: string
  cacheWritePrice: string
  cacheReadPrice: string
  tiers: TierForm[]
  status: 'active' | 'disabled'
}

export const emptyModelForm: ModelForm = {
  id: '',
  name: '',
  provider: '',
  contextWindow: '',
  parameterInfo: '',
  inputModalities: ['text'],
  outputModalities: ['text'],
  supportsTools: false,
  supportsStructuredOutput: false,
  supportsVision: false,
  inputPrice: '0',
  outputPrice: '0',
  cacheWritePrice: '0',
  cacheReadPrice: '0',
  tiers: [],
  status: 'active',
}

export function modelToForm(model: CatalogModel): ModelForm {
  return {
    id: model.id,
    name: model.name,
    provider: model.provider,
    contextWindow: String(model.context_window),
    parameterInfo: model.parameter_info,
    inputModalities: model.input_modalities,
    outputModalities: model.output_modalities,
    supportsTools: model.supports_tools,
    supportsStructuredOutput: model.supports_structured_output,
    supportsVision: model.supports_vision,
    inputPrice: model.input_price,
    outputPrice: model.output_price,
    cacheWritePrice: model.cache_write_price,
    cacheReadPrice: model.cache_read_price,
    tiers: (model.price_tiers ?? []).map(tierToForm),
    status: model.status,
  }
}

const modelIDPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]*(?:\/[A-Za-z0-9][A-Za-z0-9._:-]*)*$/
const pricePattern = /^(0|[1-9][0-9]{0,5})(\.[0-9]{1,9})?$/

export function validateModelForm(form: ModelForm): Record<string, string> {
  const errors: Record<string, string> = {}
  const modelID = form.id.trim()
  if (modelID.length > 128 || !modelIDPattern.test(modelID)) errors.id = '请输入有效的模型 ID'
  if (!form.name.trim()) errors.name = '请输入模型名称'
  if (!form.provider.trim()) errors.provider = '请输入官方提供商'
  if (!/^\d+$/.test(form.contextWindow) || Number(form.contextWindow) <= 0) {
    errors.contextWindow = '请输入大于 0 的整数'
  }
  if (form.inputModalities.length === 0) errors.inputModalities = '至少选择一种输入模态'
  if (form.outputModalities.length === 0) errors.outputModalities = '至少选择一种输出模态'
  for (const [key, value] of [
    ['inputPrice', form.inputPrice],
    ['outputPrice', form.outputPrice],
    ['cacheWritePrice', form.cacheWritePrice],
    ['cacheReadPrice', form.cacheReadPrice],
  ] as const) {
    if (!pricePattern.test(value.trim()) || Number(value) > 100_000) {
      errors[key] = '请输入 0–100000，最多 9 位小数'
    }
  }
  if (form.tiers.length > 16) errors.tiers = '最多 16 条条件档位'
  form.tiers.forEach((tier, index) => {
    Object.assign(errors, validateTierForm(tier, `tiers.${index}`))
  })
  return errors
}

/** 单个条件档的校验；key 为 `${prefix}.字段名`，与列表内联校验和抽屉编辑共用。 */
export function validateTierForm(tier: TierForm, prefix = 'tier'): Record<string, string> {
  const errors: Record<string, string> = {}
  for (const [field, value] of [
    ['inputPrice', tier.inputPrice],
    ['outputPrice', tier.outputPrice],
    ['cacheWritePrice', tier.cacheWritePrice],
    ['cacheReadPrice', tier.cacheReadPrice],
  ] as const) {
    if (!pricePattern.test(value.trim()) || Number(value) > 100_000) {
      errors[`${prefix}.${field}`] = '请输入 0–100000，最多 9 位小数'
    }
  }
  if (tier.name.trim().length > 64) errors[`${prefix}.name`] = '名称最多 64 个字符'

  const minPrompt = tier.useTokens ? tier.minPromptTokens.trim() : ''
  const maxPrompt = tier.useTokens ? tier.maxPromptTokens.trim() : ''
  const hasMin = minPrompt !== ''
  const hasMax = maxPrompt !== ''
  if (tier.useTokens && !hasMin && !hasMax) {
    errors[`${prefix}.minPromptTokens`] = '请至少填写一个 Token 界限'
  }
  if (hasMin && (!/^\d+$/.test(minPrompt) || Number(minPrompt) <= 0)) {
    errors[`${prefix}.minPromptTokens`] = '请输入正整数'
  }
  if (hasMax && (!/^\d+$/.test(maxPrompt) || Number(maxPrompt) <= 0)) {
    errors[`${prefix}.maxPromptTokens`] = '请输入正整数'
  }
  if (hasMin && hasMax && Number(minPrompt) >= Number(maxPrompt)) {
    errors[`${prefix}.minPromptTokens`] = '下界必须小于上界'
  }

  const windowOn = tier.useTime && tier.useWindow
  const start = windowOn ? timeToMinutes(tier.startTime) : null
  const end = windowOn ? timeToMinutes(tier.endTime) : null
  if (windowOn && (start === null || end === null || start === 1440)) {
    errors[`${prefix}.startTime`] = '请按 HH:MM 输入时间窗'
  }
  if (start !== null && end !== null && start === end) {
    errors[`${prefix}.endTime`] = '结束时间不能等于开始时间'
  }
  if (tier.useTime && !windowOn && tier.weekdays.length === 0) {
    errors[`${prefix}.weekdays`] = '请选择星期或限定时段'
  }
  if (tier.weekdays.some((weekday) => weekday < 1 || weekday > 7)) {
    errors[`${prefix}.weekdays`] = '星期取值无效'
  }
  if (!validTimezone(tier.timezone.trim() || 'UTC')) {
    errors[`${prefix}.timezone`] = '无效的 IANA 时区'
  }

  const hasPredicate = hasMin || hasMax || windowOn || (tier.useTime && tier.weekdays.length > 0)
  if (!hasPredicate && !errors[`${prefix}.minPromptTokens`] && !errors[`${prefix}.weekdays`]) {
    errors[`${prefix}.conditions`] = '至少配置一个条件（Token 区间或星期 / 时间窗）'
  }
  return errors
}

export function formToModelInput(form: ModelForm): ModelInput {
  return {
    id: form.id.trim(),
    name: form.name.trim(),
    provider: form.provider.trim(),
    context_window: Number(form.contextWindow),
    parameter_info: form.parameterInfo.trim(),
    input_modalities: [...new Set(form.inputModalities)].sort(),
    output_modalities: [...new Set(form.outputModalities)].sort(),
    supports_tools: form.supportsTools,
    supports_structured_output: form.supportsStructuredOutput,
    supports_vision: form.supportsVision,
    input_price: form.inputPrice.trim(),
    output_price: form.outputPrice.trim(),
    cache_write_price: form.cacheWritePrice.trim(),
    cache_read_price: form.cacheReadPrice.trim(),
    price_tiers: form.tiers.map(formToTierInput),
    status: form.status,
  }
}

export function modelStatusUpdate(
  model: CatalogModel,
  status: ModelForm['status'],
): Omit<ModelInput, 'id'> {
  const { id: _id, ...update } = formToModelInput({
    ...modelToForm(model),
    status,
  })
  return update
}

export function modelFormHasChanges(
  form: ModelForm,
  persisted: CatalogModel | null,
) {
  const baseline = persisted ? modelToForm(persisted) : emptyModelForm
  return JSON.stringify(form) !== JSON.stringify(baseline)
}

export function mergeModelDraft(
  baseline: CatalogModel,
  draft: ModelForm,
  latest: CatalogModel,
): ModelForm {
  const baselineForm = modelToForm(baseline)
  const latestForm = modelToForm(latest)
  const merged = Object.fromEntries(
    (Object.keys(latestForm) as Array<keyof ModelForm>).map((key) => [
      key,
      modelFormValueEquals(draft[key], baselineForm[key])
        ? latestForm[key]
        : draft[key],
    ]),
  ) as ModelForm

  // ID is immutable and status has its own immediate action, so neither should
  // be revived from a stale edit form during a conflict merge.
  return { ...merged, id: latestForm.id, status: latestForm.status }
}

function modelFormValueEquals(
  left: ModelForm[keyof ModelForm],
  right: ModelForm[keyof ModelForm],
) {
  if (Array.isArray(left) && Array.isArray(right)) {
    return JSON.stringify(left) === JSON.stringify(right)
  }
  return left === right
}
