import { describe, expect, it } from 'vitest'
import { ApiError, changesAuthenticatedAccount } from './client'

describe('changesAuthenticatedAccount', () => {
  it.each([
    [401, 'authentication_required'],
    [403, 'password_change_required'],
    [403, 'administrator_required'],
  ])('notifies for %s %s', (status, code) => {
    expect(
      changesAuthenticatedAccount(new ApiError(status, code, 'message')),
    ).toBe(true)
  })

  it('leaves a valid session intact after an incorrect current password', () => {
    expect(
      changesAuthenticatedAccount(
        new ApiError(401, 'invalid_credentials', '当前密码不正确'),
      ),
    ).toBe(false)
  })
})
