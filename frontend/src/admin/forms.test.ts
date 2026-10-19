import { describe, expect, it } from 'vitest'
import { feeRateToPercent, formatExactPoints as formatPoints, formatRatio, percentToFeeRate } from '../money'
import { changedAdvancedCount, formToSettings, settingsToForm } from './settingsForm'
import type { Settings } from './types'

const settings: Settings = {
  fee_rate_nano: 1_000_000,
  c2c_payment_timeout_minutes: 30,
  default_credit_limit: '0',
  default_max_attempts: 3,
  default_ttft_timeout_ms: 30000,
  default_request_timeout_ms: 600000,
  default_idle_timeout_ms: 600000,
  default_cooldown_failures: 3,
  default_cooldown_seconds: 300,
  account_max_inflight_calls: 20,
  extra_blocked_hosts: [],
  usd_cny_rate: '6.8',
  fx_auto_sync: false,
  fx_sync_minute: 570,
  usd_cny_rate_source: 'manual',
  usd_cny_rate_updated_at: '2026-10-08T00:00:00Z',
  fx_sync_attempted_at: null,
  fx_sync_error: null,
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
    const {
      updated_at: _updatedAt,
      usd_cny_rate_source: _source,
      usd_cny_rate_updated_at: _rateUpdatedAt,
      fx_sync_attempted_at: _attemptedAt,
      fx_sync_error: _error,
      ...expected
    } = settings
    expect(form.fxSyncTime).toBe('09:30')
    expect(formToSettings(form).body).toEqual(expected)
  })

  it('counts changed advanced items and normalises blocked hosts', () => {
    const form = { ...settingsToForm(settings), defaultMaxAttempts: '5', extraBlockedHosts: 'A.example.com\n a.example.com, b.example.com' }
    expect(changedAdvancedCount(form)).toBe(2)
    expect(formToSettings(form).body?.extra_blocked_hosts).toEqual(['a.example.com', 'b.example.com'])
  })

  it('counts the in-flight cap as an advanced item and accepts 0 as unlimited', () => {
    const base = settingsToForm(settings)
    expect(base.accountMaxInflightCalls).toBe('20')
    expect(changedAdvancedCount({ ...base, accountMaxInflightCalls: '5' })).toBe(1)
    expect(formToSettings({ ...base, accountMaxInflightCalls: '0' }).body?.account_max_inflight_calls).toBe(0)
    expect(formToSettings({ ...base, accountMaxInflightCalls: ' 1000 ' }).body?.account_max_inflight_calls).toBe(1000)
  })

  it('rejects an in-flight cap outside 0～1000', () => {
    const base = settingsToForm(settings)
    for (const value of ['1001', '-1', '2.5', '', 'abc']) {
      expect(formToSettings({ ...base, accountMaxInflightCalls: value }).errors).toMatchObject({
        accountMaxInflightCalls: '0～1000，0 表示不限',
      })
    }
  })

  it('rejects out-of-range values', () => {
    const result = formToSettings({ ...settingsToForm(settings), paymentTimeoutMinutes: '1', defaultCreditLimit: '-1' })
    expect(result.errors).toMatchObject({ paymentTimeoutMinutes: '5～1440 分钟', defaultCreditLimit: expect.any(String) })
  })

  it('validates the exchange rate and the daily sync time', () => {
    const base = settingsToForm(settings)
    expect(formToSettings({ ...base, usdCnyRate: '0', fxSyncTime: '9:30' }).errors).toMatchObject({
      usdCnyRate: expect.any(String),
      fxSyncTime: expect.any(String),
    })
    expect(formToSettings({ ...base, usdCnyRate: '7.1234', fxAutoSync: true, fxSyncTime: '23:05' }).body).toMatchObject({
      usd_cny_rate: '7.1234',
      fx_auto_sync: true,
      fx_sync_minute: 23 * 60 + 5,
    })
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

describe('independent timeout stages', () => {
  it('round-trips distinct values even when idle and request are shorter than TTFT', () => {
    const form = { ...settingsToForm(settings), defaultTtftSeconds: '60', defaultIdleSeconds: '2', defaultRequestSeconds: '10' }
    const result = formToSettings(form)
    expect(result.errors).toBeUndefined()
    expect(result.body).toMatchObject({ default_ttft_timeout_ms: 60000, default_idle_timeout_ms: 2000, default_request_timeout_ms: 10000 })
    expect(changedAdvancedCount(form)).toBe(3)
  })
})
