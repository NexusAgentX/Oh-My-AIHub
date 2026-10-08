import { describe, expect, it } from 'vitest'
import type { Account } from '../api/types'
import { defaultDestination } from './routePolicy'

const account: Account = {
  id: 'account-id',
  username: 'member.one',
  display_name: '成员一',
  is_admin: false,
  status: 'active',
  must_change_password: false,
  created_at: '2026-09-02T00:00:00Z',
}

describe('route policy', () => {
  it('keeps unauthenticated and first-login users out of ready routes', () => {
    expect(defaultDestination(null)).toBe('/login')
    expect(defaultDestination({ ...account, must_change_password: true })).toBe('/account/password?first=1')
  })

  it('sends every ready user to the home page', () => {
    expect(defaultDestination(account)).toBe('/home')
    expect(defaultDestination({ ...account, is_admin: true })).toBe('/home')
  })
})
