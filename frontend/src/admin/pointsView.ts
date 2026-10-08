import { formatNanoPoints, parseNanoPoints } from '../money/amount'
import { formatPoints } from './format'
import type { AdminPoints, AdminPointsBalances, LedgerCheck, LedgerChecks } from './types'

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
  /** 通过时显示的数值，不通过时显示的差异 */
  detail: string
}

/** 五项实时核对：①零和（check）与其余四项（checks）。 */
export function ledgerChecks(check: LedgerCheck, checks: LedgerChecks): CheckRow[] {
  const { account_balances: accounts, escrow, billing_calls: billing, released_trades: trades } = checks
  return [
    {
      key: 'zero_sum',
      label: '全部账户合计为 0',
      ok: check.balanced,
      detail: check.balanced ? `合计 ${formatPoints(check.total)}` : `差额 ${formatPoints(check.total)}`,
    },
    {
      key: 'account_balances',
      label: '每个账户余额等于分录合计',
      ok: accounts.passed,
      detail: accounts.passed ? '全部一致' : `${accounts.mismatches.length} 个账户不一致`,
    },
    {
      key: 'escrow',
      label: 'C2C 托管等于卖单可买与交易中之和',
      ok: escrow.passed,
      detail: escrow.passed ? `托管 ${formatPoints(escrow.escrow_balance)}` : `差额 ${formatPoints(escrow.difference)}`,
    },
    {
      key: 'billing_calls',
      label: '每次成功调用都有记账',
      ok: billing.passed,
      detail: billing.passed ? '全部已记账' : `${billing.missing_count} 次调用漏记`,
    },
    {
      key: 'released_trades',
      label: '每笔放行或判给买家的交易都有记账',
      ok: trades.passed,
      detail: trades.passed ? '全部已记账' : `${trades.missing_count} 笔交易漏记`,
    },
  ]
}

export type TrendWindow = 7 | 30 | 90

/** 走势点按日期升序（时间窗由后端 days 参数决定）。 */
export function sortedTrend(trend: AdminPoints['trend']) {
  return [...trend].sort((left, right) => left.date.localeCompare(right.date))
}
