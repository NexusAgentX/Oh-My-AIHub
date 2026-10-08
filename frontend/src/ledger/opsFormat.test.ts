import { describe, expect, it } from 'vitest'
import {
  drilldownPath,
  formatFen,
  formatShare,
  isWindowedTab,
  parseTab,
  parseWindow,
  windowHours,
} from './opsFormat'

describe('ops console url state', () => {
  it('falls back to the default window and tab for unknown values', () => {
    expect(parseWindow(null)).toBe('24')
    expect(parseWindow('999')).toBe('24')
    expect(windowHours(parseWindow('720'))).toBe(720)
    expect(parseTab('ledger')).toBe('ledger')
    expect(parseTab('providers')).toBe('overview')
    expect(parseTab('evidence')).toBe('overview')
    expect(parseTab('x')).toBe('overview')
  })

  it('applies the time window only to overview and provider income', () => {
    expect(isWindowedTab('overview')).toBe(true)
    expect(isWindowedTab('ledger')).toBe(false)
  })
})

describe('ops formatting', () => {
  it('keeps empty samples as a dash', () => {
    expect(formatFen(null)).toBe('—')
    expect(formatFen(1234)).toBe('¥12.34')
    expect(formatShare(null)).toBe('—')
    expect(formatShare('0.1234')).toBe('12.34%')
  })

  it('keeps anomaly drill-downs fixed to their path', () => {
    expect(drilldownPath('/admin/accounts?filter=over_limit')).toBe('/admin/accounts')
  })
})
