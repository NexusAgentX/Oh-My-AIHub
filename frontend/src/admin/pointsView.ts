import { formatNanoPoints, parseNanoPoints } from '../money/amount'
import type { AdminAccount, AdminPoints, AdminPointsBalances, LedgerCheck } from './types'

/** 等式「用户 + C2C 托管 + 平台收入 + 坏账 = 合计」的各项（合计正常为 0）。 */
export function ledgerEquation(balances: AdminPointsBalances) {
  const users = formatNanoPoints(parseNanoPoints(balances.user_positive) + parseNanoPoints(balances.user_negative))
  return {
    terms: [
      { label: '用户', value: users },
      { label: 'C2C 托管', value: balances.c2c_escrow },
      { label: '平台收入', value: balances.platform_revenue },
      { label: '坏账', value: balances.bad_debt },
    ],
    total: balances.total,
    balanced: parseNanoPoints(balances.total) === 0n,
  }
}

export function absolute(value: string) {
  const amount = parseNanoPoints(value)
  return formatNanoPoints(amount < 0n ? -amount : amount)
}

export type CheckRow = {
  key: string
  label: string
  ok: boolean
  value: string
  /** 不通过时的明细链接 */
  link?: string
}

/** 实时核对列表；行由契约返回的核对结果生成。 */
export function ledgerChecks(check: LedgerCheck): CheckRow[] {
  return [
    {
      key: 'zero_sum',
      label: '全部账户合计为 0',
      ok: check.balanced,
      value: check.total,
      link: check.balanced ? undefined : '/admin/points?tab=transactions',
    },
  ]
}

/** 持有集中度：持有最多的前 n 个用户占流通积分的比例（0～100）。 */
export function holdingConcentration(accounts: Pick<AdminAccount, 'balance' | 'display_name' | 'id'>[], circulation: string, top = 5) {
  const total = parseNanoPoints(circulation)
  const holders = accounts
    .map((account) => ({ account, balance: parseNanoPoints(account.balance) }))
    .filter((item) => item.balance > 0n)
    .sort((left, right) => (right.balance > left.balance ? 1 : right.balance < left.balance ? -1 : 0))
  const share = (amount: bigint) => (total === 0n ? 0 : Number((amount * 10000n) / total) / 100)
  const leaders = holders.slice(0, top)
  return {
    topShare: share(leaders.reduce((sum, item) => sum + item.balance, 0n)),
    largest: leaders[0] ? { name: leaders[0].account.display_name, id: leaders[0].account.id, share: share(leaders[0].balance) } : null,
  }
}

export type TrendWindow = 7 | 30 | 90

/** 取最近 N 天的走势点（契约按日期升序返回）。 */
export function trendWindow(trend: AdminPoints['trend'], days: TrendWindow) {
  return [...trend].sort((left, right) => left.date.localeCompare(right.date)).slice(-days)
}

export function toNumber(value: string) {
  return Number(value)
}
