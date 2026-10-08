import type { AdminModel, ModelPrices, PriceTier, PriceTierInput } from './types'

/**
 * 条件价格档表单（ADR-0012）：输入侧 token 区间 × 带时区的每周时间窗，按序首个命中，整单生效。
 * 校验与 backend/internal/catalog.validatePriceTiers 保持一致。
 */

export const maxPriceTiers = 16

export type PricesForm = {
  input: string
  output: string
  cache_write: string
  cache_read: string
  token_prices?: Record<string, string>
}

export type TierForm = {
  name: string
  serviceTier?: string
  thinkingMode?: string
  useTokens: boolean
  minPromptTokens: string
  maxPromptTokens: string
  useTime: boolean
  timezone: string
  /** ISO 星期 1～7；空表示每天 */
  weekdays: number[]
  /** 时间条件内是否限定时段；否则全天 */
  useWindow: boolean
  startTime: string
  endTime: string
  prices: PricesForm
}

const zeroPrices: PricesForm = { input: '0', output: '0', cache_write: '0', cache_read: '0' }

export function emptyTier(prices: PricesForm = zeroPrices): TierForm {
  return {
    name: '',
    useTokens: true,
    minPromptTokens: '',
    maxPromptTokens: '',
    useTime: false,
    timezone: 'Asia/Shanghai',
    weekdays: [],
    useWindow: false,
    startTime: '',
    endTime: '',
    prices: { ...prices },
  }
}

const weekdayLabels = ['周一', '周二', '周三', '周四', '周五', '周六', '周日']

export function weekdayLabel(weekday: number) {
  return weekdayLabels[weekday - 1] ?? String(weekday)
}

export function minutesToTime(minutes: number) {
  return `${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`
}

/** "08:30" → 510；"24:00" → 1440；格式错误返回 null。 */
export function timeToMinutes(value: string) {
  const match = /^(\d{1,2}):(\d{2})$/.exec(value.trim())
  if (!match) return null
  const hours = Number(match[1])
  const minutes = Number(match[2])
  if (hours === 24 && minutes === 0) return 1440
  if (hours > 23 || minutes > 59) return null
  return hours * 60 + minutes
}

export function tierToForm(tier: PriceTier): TierForm {
  const hasWindow = tier.start_minute_of_day !== null && tier.end_minute_of_day !== null
  return {
    name: tier.name,
    serviceTier: tier.service_tier,
    thinkingMode: tier.thinking_mode,
    useTokens: tier.min_prompt_tokens !== null || tier.max_prompt_tokens !== null,
    minPromptTokens: tier.min_prompt_tokens === null ? '' : String(tier.min_prompt_tokens),
    maxPromptTokens: tier.max_prompt_tokens === null ? '' : String(tier.max_prompt_tokens),
    useTime: (tier.weekdays?.length ?? 0) > 0 || hasWindow,
    timezone: tier.timezone || 'UTC',
    weekdays: [...(tier.weekdays ?? [])].sort((left, right) => left - right),
    useWindow: hasWindow,
    startTime: hasWindow ? minutesToTime(tier.start_minute_of_day!) : '',
    endTime: hasWindow ? minutesToTime(tier.end_minute_of_day!) : '',
    prices: { ...tier.prices },
  }
}

function tokenValue(value: string) {
  const text = value.trim()
  return text === '' ? null : Number(text)
}

/** 只提交开启的条件；关闭的条件一律为 null。 */
export function formToTierInput(form: TierForm): PriceTierInput {
  const window = form.useTime && form.useWindow
  const weekdays = form.useTime && form.weekdays.length > 0 ? [...form.weekdays].sort((a, b) => a - b) : null
  return {
    name: form.name.trim(),
    service_tier: form.serviceTier?.trim() || undefined,
    thinking_mode: (form.thinkingMode || undefined) as PriceTierInput['thinking_mode'],
    timezone: form.timezone.trim() || 'UTC',
    min_prompt_tokens: form.useTokens ? tokenValue(form.minPromptTokens) : null,
    max_prompt_tokens: form.useTokens ? tokenValue(form.maxPromptTokens) : null,
    weekdays,
    start_minute_of_day: window ? timeToMinutes(form.startTime) : null,
    end_minute_of_day: window ? timeToMinutes(form.endTime) : null,
    prices: trimPrices(form.prices),
  }
}

function trimPrices(prices: PricesForm): ModelPrices {
  return {
    ...(prices.token_prices
      ? {
          token_prices: Object.fromEntries(
            Object.entries(prices.token_prices)
              .filter(([, v]) => v.trim() !== '')
              .map(([k, v]) => [k, v.trim()]),
          ),
        }
      : {}),
    input: prices.input.trim(),
    output: prices.output.trim(),
    cache_write: prices.cache_write.trim(),
    cache_read: prices.cache_read.trim(),
  }
}

const pricePattern = /^(0|[1-9]\d{0,5})(\.\d{1,9})?$/

/** 单价：0～100000 积分/百万 token，最多 9 位小数。 */
export function isValidPrice(value: string) {
  const text = value.trim()
  if (!pricePattern.test(text)) return false
  return Number(text.split('.')[0]) < 100000 || /^100000(\.0+)?$/.test(text)
}

export function isValidTimezone(timezone: string) {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: timezone })
    return true
  } catch {
    return false
  }
}

export type TierErrors = Partial<Record<'name' | 'tokens' | 'time' | 'timezone' | 'prices' | 'condition', string>>

export function validatePrices(prices: PricesForm) {
  return [
    prices.input,
    prices.output,
    prices.cache_read,
    prices.cache_write,
    ...Object.values(prices.token_prices ?? {}).filter((v) => v.trim() !== ''),
  ].every(isValidPrice)
    ? ''
    : '单价为 0～100000，最多 9 位小数'
}

export function validateTier(form: TierForm): TierErrors {
  const errors: TierErrors = {}
  const input = formToTierInput(form)
  if (input.name && input.name.length > 64) errors.name = '名称最多 64 个字符'
  if (!form.name.trim()) errors.name = '请填写档位名称'
  if (form.useTokens) {
    const bounds = [form.minPromptTokens, form.maxPromptTokens].map((value) => value.trim())
    if (bounds.some((value) => value !== '' && !/^\d+$/.test(value))) {
      errors.tokens = 'token 数为非负整数'
    } else if (
      input.min_prompt_tokens != null &&
      input.max_prompt_tokens != null &&
      input.min_prompt_tokens >= input.max_prompt_tokens
    ) {
      errors.tokens = '下限必须小于上限'
    }
  }
  if (form.useTime) {
    if (form.useWindow) {
      const start = timeToMinutes(form.startTime)
      const end = timeToMinutes(form.endTime)
      if (start === null || start > 1439 || end === null || end < 1) {
        errors.time = '开始 00:00～23:59，结束 00:01～24:00'
      } else if (start === end) {
        errors.time = '开始与结束不能相同'
      }
    }
    if (!isValidTimezone(form.timezone.trim() || 'UTC')) errors.timezone = '无效的时区'
  }
  const hasPredicate =
    Boolean(input.service_tier || input.thinking_mode) ||
    input.min_prompt_tokens != null ||
    input.max_prompt_tokens != null ||
    input.start_minute_of_day != null ||
    (input.weekdays?.length ?? 0) > 0
  if (!hasPredicate && !errors.tokens && !errors.time) errors.condition = '至少设置一个条件'
  const prices = validatePrices(form.prices)
  if (prices) errors.prices = prices
  return errors
}

/** 档位按序首个命中，上下移动即调整优先级；越界时原样返回。 */
export function moveTier<T>(tiers: T[], index: number, delta: -1 | 1): T[] {
  const target = index + delta
  if (index < 0 || index >= tiers.length || target < 0 || target >= tiers.length) return tiers
  const next = [...tiers]
  ;[next[index], next[target]] = [next[target], next[index]]
  return next
}

export function formatTokenBound(value: number) {
  if (value >= 1_000_000 && value % 1_000_000 === 0) return `${value / 1_000_000}M`
  if (value >= 1_000 && value % 1_000 === 0) return `${value / 1_000}k`
  return value.toLocaleString('zh-CN')
}

function timezoneLabel(timezone: string) {
  if (timezone === 'Asia/Shanghai') return '北京时间'
  return timezone
}

function weekdaysSummary(weekdays: number[]) {
  const key = [...weekdays].sort((a, b) => a - b).join()
  if (key === '1,2,3,4,5') return '工作日'
  if (key === '6,7') return '周末'
  if (key === '1,2,3,4,5,6,7') return '每天'
  return weekdays.map(weekdayLabel).join('/')
}

/** 条件摘要，例如「输入侧 > 200k」「每天 00:30–08:30 北京时间」「每天 22:00–次日 06:00 北京时间」。 */
export function tierConditionSummary(tier: PriceTierInput) {
  const parts: string[] = []
  if (tier.service_tier) parts.push(`服务档位 ${tier.service_tier}`)
  if (tier.thinking_mode) parts.push(tier.thinking_mode === 'qwen_thinking' ? '百炼实际思考' : '百炼未输出思考')
  const min = tier.min_prompt_tokens ?? null
  const max = tier.max_prompt_tokens ?? null
  if (min !== null && max !== null) parts.push(`输入侧 ${formatTokenBound(min)}–${formatTokenBound(max)}`)
  else if (min !== null) parts.push(`输入侧 ≥ ${formatTokenBound(min)}`)
  else if (max !== null) parts.push(`输入侧 < ${formatTokenBound(max)}`)
  const days = tier.weekdays && tier.weekdays.length > 0 ? weekdaysSummary(tier.weekdays) : ''
  const start = tier.start_minute_of_day ?? null
  const end = tier.end_minute_of_day ?? null
  const timezone = timezoneLabel(tier.timezone || 'UTC')
  if (start !== null && end !== null) {
    const crosses = end < start
    parts.push(`${days || '每天'} ${minutesToTime(start)}–${crosses ? '次日 ' : ''}${minutesToTime(end)} ${timezone}`)
  } else if (days) {
    parts.push(`${days} ${timezone}`)
  }
  return parts.length > 0 ? parts.join(' · ') : '未设置条件'
}

export function toggleWeekday(form: TierForm, weekday: number, checked: boolean): TierForm {
  const weekdays = checked
    ? [...new Set([...form.weekdays, weekday])].sort((a, b) => a - b)
    : form.weekdays.filter((item) => item !== weekday)
  return { ...form, weekdays }
}

// ---------------------------------------------------------------------------
// 模型表单

export type ModelForm = {
  id: string
  displayName: string
  prices: PricesForm
  enabled: boolean
  sortOrder: string
  provider: string
  contextWindow: string
  inputModalities: string[]
  outputModalities: string[]
  supportsTools: boolean
  supportsStructuredOutput: boolean
  supportsVision: boolean
  parameterInfo: string
  tiers: PriceTierInput[]
}

export const modalityOptions = ['text', 'image', 'audio', 'video', 'file']

export function emptyModelForm(): ModelForm {
  return {
    id: '',
    displayName: '',
    prices: { ...zeroPrices },
    enabled: true,
    sortOrder: '0',
    provider: '',
    contextWindow: '',
    inputModalities: ['text'],
    outputModalities: ['text'],
    supportsTools: false,
    supportsStructuredOutput: false,
    supportsVision: false,
    parameterInfo: '',
    tiers: [],
  }
}

/** 把已保存档位转成输入结构（去掉 seq；顺序即优先级）。 */
export function tierToInput(tier: PriceTier): PriceTierInput {
  return {
    name: tier.name,
    service_tier: tier.service_tier,
    thinking_mode: tier.thinking_mode,
    timezone: tier.timezone,
    min_prompt_tokens: tier.min_prompt_tokens,
    max_prompt_tokens: tier.max_prompt_tokens,
    weekdays: tier.weekdays,
    start_minute_of_day: tier.start_minute_of_day,
    end_minute_of_day: tier.end_minute_of_day,
    prices: tier.prices,
  }
}

export function modelToForm(model: AdminModel): ModelForm {
  return {
    id: model.id,
    displayName: model.display_name,
    prices: { ...model.base_prices },
    enabled: model.enabled,
    sortOrder: String(model.sort_order),
    provider: model.provider,
    contextWindow: model.context_window === null ? '' : String(model.context_window),
    inputModalities: model.input_modalities,
    outputModalities: model.output_modalities,
    supportsTools: model.supports_tools,
    supportsStructuredOutput: model.supports_structured_output,
    supportsVision: model.supports_vision,
    parameterInfo: model.parameter_info,
    tiers: [...model.price_tiers].sort((a, b) => a.seq - b.seq).map(tierToInput),
  }
}

/** 「高级设置」中偏离默认值的项数。 */
export function changedModelAdvanced(form: ModelForm) {
  const isTextOnly = (values: string[]) => values.length === 1 && values[0] === 'text'
  return [
    form.provider.trim() !== '',
    form.contextWindow.trim() !== '',
    !isTextOnly(form.inputModalities),
    !isTextOnly(form.outputModalities),
    form.supportsTools,
    form.supportsStructuredOutput,
    form.supportsVision,
    form.parameterInfo.trim() !== '',
  ].filter(Boolean).length
}

const modelIDPattern = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/

export type ModelErrors = Partial<
  Record<'id' | 'displayName' | 'prices' | 'sortOrder' | 'contextWindow' | 'parameterInfo' | 'tiers', string>
>

export function validateModelForm(form: ModelForm, creating: boolean): ModelErrors {
  const errors: ModelErrors = {}
  if (creating && !modelIDPattern.test(form.id.trim())) {
    errors.id = '字母或数字开头，只含字母、数字、. _ -，最多 128 个字符'
  }
  const name = form.displayName.trim()
  if (!name) errors.displayName = '请填写显示名'
  else if (name.length > 128) errors.displayName = '最多 128 个字符'
  const prices = validatePrices(form.prices)
  if (prices) errors.prices = prices
  if (!/^-?\d+$/.test(form.sortOrder.trim())) errors.sortOrder = '请输入整数'
  const contextWindow = form.contextWindow.trim()
  if (contextWindow && (!/^\d+$/.test(contextWindow) || Number(contextWindow) < 1)) {
    errors.contextWindow = '正整数，或留空'
  }
  if (form.parameterInfo.length > 500) errors.parameterInfo = '最多 500 个字符'
  if (form.tiers.length > maxPriceTiers) errors.tiers = `最多 ${maxPriceTiers} 档`
  return errors
}

function modelFields(form: ModelForm) {
  return {
    display_name: form.displayName.trim(),
    base_prices: trimPrices(form.prices),
    price_tiers: form.tiers,
    enabled: form.enabled,
    sort_order: Number(form.sortOrder.trim()),
    provider: form.provider.trim(),
    context_window: form.contextWindow.trim() ? Number(form.contextWindow.trim()) : null,
    input_modalities: form.inputModalities.length > 0 ? form.inputModalities : ['text'],
    output_modalities: form.outputModalities.length > 0 ? form.outputModalities : ['text'],
    supports_tools: form.supportsTools,
    supports_structured_output: form.supportsStructuredOutput,
    supports_vision: form.supportsVision,
    parameter_info: form.parameterInfo.trim(),
  }
}

export function formToCreateRequest(form: ModelForm) {
  return { id: form.id.trim(), ...modelFields(form) }
}

/** 编辑时整体提交（price_tiers 整组替换）。 */
export function formToUpdateRequest(form: ModelForm) {
  return modelFields(form)
}
