import type { Account } from '../api/types'

export function defaultDestination(account: Account | null) {
  if (!account) return '/login'
  if (account.must_change_password) return '/account/password?first=1'
  return '/home'
}
