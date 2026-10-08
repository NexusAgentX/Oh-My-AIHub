import type { PointsPeriod } from '../api/types'
import { formatNanoPoints, parseNanoPoints } from '../money/amount'

export type ReconcileItem = { key: string; label: string; value: string }

const sum = (...values: string[]) => formatNanoPoints(values.reduce((total, value) => total + parseNanoPoints(value), 0n))

/** 对账条的中间项：调用支出 / 渠道收入 / C2C 买入 / C2C 卖出（含退回）/ 调账与核销，均带符号。 */
export function reconcileItems(period: PointsPeriod): ReconcileItem[] {
  return [
    { key: 'call_spend', label: '调用支出', value: period.call_spend },
    { key: 'channel_income', label: '渠道收入', value: period.channel_income },
    { key: 'c2c_buy', label: 'C2C 买入', value: period.c2c_buy },
    { key: 'c2c_sell', label: 'C2C 卖出', value: sum(period.c2c_sell, period.c2c_return) },
    { key: 'adjustments', label: '调账与核销', value: sum(period.adjustments, period.write_offs) },
  ]
}
