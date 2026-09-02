import type { Account } from '../api/contracts'
import { parseNanoPoints } from '../money/amount'

export function summarizeAccounts(accounts: Account[]) {
  return {
    total: accounts.length,
    active: accounts.filter((item) => item.status === 'active').length,
    disabled: accounts.filter((item) => item.status === 'disabled').length,
    credit: accounts.reduce(
      (sum, item) => sum + parseNanoPoints(item.credit_limit || '0'),
      0n,
    ),
  }
}
