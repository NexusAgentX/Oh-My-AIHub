import { formatNanoPoints, parseNanoPoints } from '../money/amount'
import { formatPoints } from './format'
import type { AdminPoints, AdminPointsBalances, LedgerCheck, LedgerChecks } from './types'

export type EquationTerm = { label: string; value: string; note?: string }

/**
 * 天平等式：每个账户按余额符号归边，左侧是负余额（取绝对值），右侧是正余额，两侧相等即账本合计为 0。
 * 用户拆成信用发行（负）与流通（正）；坏账恒在左、托管恒在右；平台收入为负（调账净支出）时移到左侧。
 */
export function ledgerEquation(balances: AdminPointsBalances) {
  const revenue = parseNanoPoints(balances.platform_revenue)
  const left: EquationTerm[] = [
    { label: '信用发行', value: absolute(balances.user_negative) },
    { label: '坏账', value: absolute(balances.bad_debt) },
  ]
  const right: EquationTerm[] = [
    { label: '流通积分', value: balances.user_positive },
    { label: 'C2C 托管', value: balances.c2c_escrow },
  ]
  if (revenue < 0n) left.push({ label: '平台收入', value: absolute(balances.platform_revenue), note: '净支出' })
  else right.push({ label: '平台收入', value: balances.platform_revenue })
  return { left, right, total: balances.total, balanced: parseNanoPoints(balances.total) === 0n }
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
