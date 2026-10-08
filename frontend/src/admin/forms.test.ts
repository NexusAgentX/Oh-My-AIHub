import { describe, expect, it } from 'vitest'
import { feeRateToPercent, formatPoints, formatRatio, percentToFeeRate } from './format'
import { changedAdvancedCount, formToSettings, settingsToForm } from './settingsForm'
import type { Settings } from './types'

const settings: Settings = {
  fee_rate_nano: 1_000_000,
  c2c_payment_timeout_minutes: 30,
  default_credit_limit: '0',
  default_max_attempts: 3,
  default_ttft_timeout_ms: 30000,
  default_total_timeout_ms: 600000,
  default_cooldown_failures: 3,
  default_cooldown_seconds: 300,
  extra_blocked_hosts: [],
  updated_at: '2026-10-08T00:00:00Z',
}

describe('fee rate', () => {
  it('converts nano to percent and back without floating point', () => {
    expect(feeRateToPercent(1_000_000)).toBe('0.1')
    expect(feeRateToPercent(1_000_000_000)).toBe('100')
    expect(feeRateToPercent(1)).toBe('0.0000001')
    expect(percentToFeeRate('0.1')).toBe(1_000_000)
    expect(percentToFeeRate('2.5')).toBe(25_000_000)
    expect(percentToFeeRate('100.1')).toBeNull()
    expect(percentToFeeRate('0.00000001')).toBeNull()
  })
})

describe('settings form', () => {
  it('round-trips the default settings and counts no advanced changes', () => {
    const form = settingsToForm(settings)
    expect(changedAdvancedCount(form)).toBe(0)
    const { updated_at: _updatedAt, ...expected } = settings
    expect(formToSettings(form).body).toEqual(expected)
  })

  it('counts changed advanced items and normalises blocked hosts', () => {
    const form = { ...settingsToForm(settings), defaultMaxAttempts: '5', extraBlockedHosts: 'A.example.com\n a.example.com, b.example.com' }
    expect(changedAdvancedCount(form)).toBe(2)
    expect(formToSettings(form).body?.extra_blocked_hosts).toEqual(['a.example.com', 'b.example.com'])
  })

  it('rejects out-of-range values', () => {
    const result = formToSettings({ ...settingsToForm(settings), paymentTimeoutMinutes: '1', defaultCreditLimit: '-1' })
    expect(result.errors).toMatchObject({ paymentTimeoutMinutes: '5～1440 分钟', defaultCreditLimit: expect.any(String) })
  })
})

describe('display formats', () => {
  it('pads points to two decimals and keeps precision', () => {
    expect(formatPoints('0')).toBe('0.00')
    expect(formatPoints('12.5')).toBe('12.50')
    expect(formatPoints('-0.03')).toBe('-0.03')
    expect(formatPoints('3.123456789')).toBe('3.123456789')
    expect(formatPoints('5', true)).toBe('+5.00')
  })

  it('formats ratios', () => {
    expect(formatRatio('0.952')).toBe('95.2%')
    expect(formatRatio('1')).toBe('100%')
    expect(formatRatio(null)).toBe('—')
  })
})
