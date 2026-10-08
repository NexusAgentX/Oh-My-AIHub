import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import type { PointsPeriod } from '../api/types'
import { ReconcileBar } from './BillTab'
import { reconcileItems } from './reconcile'

const period: PointsPeriod = {
  from: '2026-10-01T00:00:00+08:00',
  to: '2026-11-01T00:00:00+08:00',
  opening_balance: '0',
  closing_balance: '-0.03',
  income: '60',
  spend: '60.03',
  call_spend: '-30.03',
  channel_income: '0',
  c2c_buy: '30',
  c2c_sell: '-40',
  c2c_return: '10',
  adjustments: '30',
  write_offs: '0',
  difference: '0',
}

describe('period reconciliation', () => {
  it('splits the period by category, folding returns into C2C sells and write-offs into adjustments', () => {
    expect(reconcileItems(period).map((item) => [item.label, item.value])).toEqual([
      ['调用支出', '-30.03'],
      ['渠道收入', '0'],
      ['C2C 买入', '30'],
      ['C2C 卖出', '-30'],
      ['调账与核销', '30'],
    ])
  })

  it('renders opening → categories → closing without a difference badge when balanced', () => {
    const markup = renderToStaticMarkup(
      <MemoryRouter>
        <ReconcileBar period={period} />
      </MemoryRouter>,
    )
    for (const label of ['期初', '调用支出', '渠道收入', 'C2C 买入', 'C2C 卖出', '调账与核销', '期末']) {
      expect(markup).toContain(label)
    }
    expect(markup.indexOf('期初')).toBeLessThan(markup.indexOf('调用支出'))
    expect(markup.indexOf('调账与核销')).toBeLessThan(markup.indexOf('期末'))
    expect(markup).not.toContain('差额')
    expect(renderToStaticMarkup(<ReconcileBar period={{ ...period, difference: '0.01' }} />)).toContain('差额')
  })
})
