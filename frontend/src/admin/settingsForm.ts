import { formatPointAmount } from '../money/format'
import { feeRateToPercent, isAmount, percentToFeeRate } from './format'
import type { Settings, SettingsUpdateRequest } from './types'

export type SettingsForm = {
  feePercent: string
  paymentTimeoutMinutes: string
  defaultCreditLimit: string
  defaultMaxAttempts: string
  /** 秒；接口为毫秒 */
  defaultTtftSeconds: string
  defaultTotalSeconds: string
  defaultCooldownFailures: string
  defaultCooldownSeconds: string
  /** 每行一个主机 */
  extraBlockedHosts: string
}

/** 平台出厂默认值（baseline seed），用于「高级设置 · 已改 N 项」。 */
const factoryAdvanced = {
  defaultCreditLimit: '0',
  defaultMaxAttempts: '3',
  defaultTtftSeconds: '30',
  defaultTotalSeconds: '600',
  defaultCooldownFailures: '3',
  defaultCooldownSeconds: '300',
  extraBlockedHosts: '',
} satisfies Partial<SettingsForm>

export function settingsToForm(settings: Settings): SettingsForm {
  return {
    feePercent: feeRateToPercent(settings.fee_rate_nano),
    paymentTimeoutMinutes: String(settings.c2c_payment_timeout_minutes),
    defaultCreditLimit: formatPointAmount(settings.default_credit_limit),
    defaultMaxAttempts: String(settings.default_max_attempts),
    defaultTtftSeconds: String(settings.default_ttft_timeout_ms / 1000),
    defaultTotalSeconds: String(settings.default_total_timeout_ms / 1000),
    defaultCooldownFailures: String(settings.default_cooldown_failures),
    defaultCooldownSeconds: String(settings.default_cooldown_seconds),
    extraBlockedHosts: settings.extra_blocked_hosts.join('\n'),
  }
}

export function changedAdvancedCount(form: SettingsForm) {
  return (Object.keys(factoryAdvanced) as Array<keyof typeof factoryAdvanced>).filter(
    (key) => form[key].trim() !== factoryAdvanced[key],
  ).length
}

function integerIn(value: string, min: number, max: number) {
  if (!/^\d+$/.test(value.trim())) return null
  const number = Number(value)
  return number >= min && number <= max ? number : null
}

export type SettingsErrors = Partial<Record<keyof SettingsForm, string>>

/** 表单 → 请求体；任何字段越界时返回 errors（范围与 openapi Settings 一致）。 */
export function formToSettings(
  form: SettingsForm,
): { body: SettingsUpdateRequest; errors?: undefined } | { body?: undefined; errors: SettingsErrors } {
  const errors: SettingsErrors = {}
  const fee = percentToFeeRate(form.feePercent)
  if (fee === null) errors.feePercent = '0～100，最多 7 位小数'
  const timeout = integerIn(form.paymentTimeoutMinutes, 5, 1440)
  if (timeout === null) errors.paymentTimeoutMinutes = '5～1440 分钟'
  const credit = form.defaultCreditLimit.trim()
  if (!isAmount(credit) || credit.startsWith('-')) errors.defaultCreditLimit = '不小于 0，最多 9 位小数'
  const attempts = integerIn(form.defaultMaxAttempts, 1, 10)
  if (attempts === null) errors.defaultMaxAttempts = '1～10'
  const ttft = integerIn(form.defaultTtftSeconds, 1, 600)
  if (ttft === null) errors.defaultTtftSeconds = '1～600 秒'
  const total = integerIn(form.defaultTotalSeconds, 1, 3600)
  if (total === null) errors.defaultTotalSeconds = '1～3600 秒'
  const failures = integerIn(form.defaultCooldownFailures, 1, 100)
  if (failures === null) errors.defaultCooldownFailures = '1～100 次'
  const cooldown = integerIn(form.defaultCooldownSeconds, 10, 86400)
  if (cooldown === null) errors.defaultCooldownSeconds = '10～86400 秒'
  const hosts = [
    ...new Set(
      form.extraBlockedHosts
        .split(/[\s,]+/)
        .map((host) => host.trim().toLowerCase())
        .filter(Boolean),
    ),
  ]
  if (hosts.length > 100) errors.extraBlockedHosts = '最多 100 个主机'
  if (Object.keys(errors).length > 0) return { errors }
  return {
    body: {
      fee_rate_nano: fee!,
      c2c_payment_timeout_minutes: timeout!,
      default_credit_limit: credit,
      default_max_attempts: attempts!,
      default_ttft_timeout_ms: ttft! * 1000,
      default_total_timeout_ms: total! * 1000,
      default_cooldown_failures: failures!,
      default_cooldown_seconds: cooldown!,
      extra_blocked_hosts: hosts,
    },
  }
}
