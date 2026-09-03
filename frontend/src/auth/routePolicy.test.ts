import { describe, expect, it } from 'vitest'
import type { Account } from '../api/contracts'
import { canEnterAdmin, defaultDestination } from './routePolicy'

const account: Account = {
  id: 'account-id',
  username: 'member.one',
  display_name: '成员一',
  is_admin: false,
  status: 'active',
  must_change_password: false,
  version: 1,
  credit_limit: '10',
  credit_frozen: false,
  posted_balance: '0',
  asset_reserved: '0',
  spend_authorized: '0',
  effective_credit_limit: '10',
  credit_used: '0',
  spendable_capacity: '10',
  over_limit: false,
  created_at: '2026-09-02T00:00:00Z',
  updated_at: '2026-09-02T00:00:00Z',
  password_changed_at: null,
}

describe('route policy', () => {
  it('keeps unauthenticated and first-login users out of ready routes', () => {
    expect(defaultDestination(null)).toBe('/login')
    expect(
      defaultDestination({ ...account, must_change_password: true }),
    ).toBe('/account/password?first=1')
  })

  it('sends ready users to their product boundary', () => {
    expect(defaultDestination(account)).toBe('/dashboard')
    expect(defaultDestination({ ...account, is_admin: true })).toBe(
      '/admin/accounts',
    )
  })

  it('requires an active, ready administrator for admin routes', () => {
    expect(canEnterAdmin({ ...account, is_admin: true })).toBe(true)
    expect(canEnterAdmin(account)).toBe(false)
    expect(
      canEnterAdmin({ ...account, is_admin: true, status: 'disabled' }),
    ).toBe(false)
    expect(
      canEnterAdmin({ ...account, is_admin: true, must_change_password: true }),
    ).toBe(false)
  })
})
