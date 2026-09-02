import { describe, expect, it } from 'vitest'
import type { Account } from '../api/contracts'
import { accountRiskLabel } from './accountMetrics'

const baseAccount: Account = {
  id: 'account-1',
  username: 'first',
  display_name: 'First',
  is_admin: false,
  status: 'active',
  must_change_password: false,
  version: 1,
  credit_limit: '12.500000001',
  credit_frozen: false,
  posted_balance: '0',
  asset_reserved: '0',
  spend_authorized: '0',
  effective_credit_limit: '12.500000001',
  credit_used: '0',
  spendable_capacity: '12.500000001',
  over_limit: false,
  created_at: '2026-09-02T00:00:00Z',
  updated_at: '2026-09-02T00:00:00Z',
  password_changed_at: null,
}

describe('account risk label', () => {
  it('prioritizes frozen and over-limit states', () => {
    expect(accountRiskLabel({ ...baseAccount, credit_frozen: true })).toBe('信用冻结')
    expect(accountRiskLabel({ ...baseAccount, over_limit: true })).toBe('信用超限')
  })

  it('distinguishes zero spendable capacity from a healthy account', () => {
    expect(accountRiskLabel({ ...baseAccount, spendable_capacity: '0' })).toBe('可消费为零')
    expect(accountRiskLabel(baseAccount)).toBe('正常')
  })
})
