import type { Account } from '../api/contracts'

export function defaultDestination(account: Account | null) {
  if (!account) return '/login'
  if (account.must_change_password) return '/account/password?first=1'
  return account.is_admin ? '/admin/accounts' : '/dashboard'
}

export function canEnterAdmin(account: Account | null) {
  return Boolean(
    account &&
      account.status === 'active' &&
      !account.must_change_password &&
      account.is_admin,
  )
}
