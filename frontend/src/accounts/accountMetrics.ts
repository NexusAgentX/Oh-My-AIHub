import type { Account } from '../api/contracts'

export function accountRiskLabel(account: Account) {
  if (account.credit_frozen) return '信用冻结'
  if (account.over_limit) return '信用超限'
  if (account.spendable_capacity === '0') return '可消费为零'
  return '正常'
}
