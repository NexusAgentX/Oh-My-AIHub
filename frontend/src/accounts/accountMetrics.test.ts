import { describe, expect, it } from 'vitest'
import type { Account } from '../api/contracts'
import { summarizeAccounts } from './accountMetrics'

const baseAccount: Account = {
  id: 'account-1',
  username: 'first',
  display_name: 'First',
  is_admin: false,
  status: 'active',
  must_change_password: false,
  version: 1,
  balance: '0',
  frozen_balance: '0',
  credit_limit: '12.500000001',
  available_credit: '12.500000001',
  created_at: '2026-09-02T00:00:00Z',
  updated_at: '2026-09-02T00:00:00Z',
  password_changed_at: null,
}

describe('account metrics', () => {
  it('summarizes the complete directory independently of a filtered result', () => {
    const completeDirectory = [
      baseAccount,
      {
        ...baseAccount,
        id: 'account-2',
        username: 'second',
        status: 'disabled' as const,
        credit_limit: '2.5',
      },
    ]
    const filteredResult = [completeDirectory[0]]

    expect(summarizeAccounts(completeDirectory)).toEqual({
      total: 2,
      active: 1,
      disabled: 1,
      credit: 15_000_000_001n,
    })
    expect(summarizeAccounts(filteredResult).total).toBe(1)
  })
})
