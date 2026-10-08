import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter, matchRoutes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { appRoutes } from '../App'
import { adminNavigation, adminTabCount, findAdminNavItem } from './navigation'
import { AttentionList } from './OverviewPage'
import { LedgerEquation } from './PointsPage'
import { holdingConcentration, ledgerChecks, ledgerEquation } from './pointsView'
import type { AdminPointsBalances } from './types'

const balances: AdminPointsBalances = {
  user_positive: '130.5',
  user_negative: '-60.03',
  credit_issued: '500',
  c2c_escrow: '30',
  platform_revenue: '0.03',
  bad_debt: '-100.5',
  total: '0',
}

describe('ledger equation', () => {
  it('sums users, escrow, revenue and bad debt to the total', () => {
    const equation = ledgerEquation(balances)
    expect(equation.terms.map((term) => [term.label, term.value])).toEqual([
      ['用户', '70.47'],
      ['C2C 托管', '30'],
      ['平台收入', '0.03'],
      ['坏账', '-100.5'],
    ])
    expect(equation.balanced).toBe(true)
  })

  it('renders "= 0.00" with a balanced mark', () => {
    const markup = renderToStaticMarkup(<LedgerEquation balances={balances} />)
    expect(markup).toContain('用户')
    expect(markup).toContain('70.47')
    expect(markup).toContain('C2C 托管')
    expect(markup).toContain('平台收入')
    expect(markup).toContain('坏账')
    expect(markup).toContain('=')
    expect(markup).toContain('0.00')
    expect(markup).toContain('✓ 平衡')
  })

  it('flags an unbalanced ledger', () => {
    const markup = renderToStaticMarkup(<LedgerEquation balances={{ ...balances, total: '0.01' }} />)
    expect(markup).toContain('不平衡')
    expect(markup).toContain('ledger-equation-broken')
  })

  it('turns the contract check into check rows with a detail link when failing', () => {
    expect(ledgerChecks({ balanced: true, total: '0', checked_at: '2026-10-08T00:00:00Z' })[0]).toMatchObject({ ok: true })
    expect(ledgerChecks({ balanced: false, total: '1', checked_at: '2026-10-08T00:00:00Z' })[0].link).toBeTruthy()
  })

  it('measures holding concentration among positive balances', () => {
    const accounts = [
      { id: 'a', display_name: '老陈', balance: '60' },
      { id: 'b', display_name: '小王', balance: '-10' },
      { id: 'c', display_name: '阿李', balance: '40' },
    ]
    expect(holdingConcentration(accounts, '100', 1)).toEqual({
      topShare: 60,
      largest: { id: 'a', name: '老陈', share: 60 },
    })
  })
})

describe('needs-attention list', () => {
  it('shows the empty state when nothing needs handling', () => {
    expect(renderToStaticMarkup(<AttentionList items={[]} />)).toContain('暂无需要处理的事')
  })

  it('shows a type badge, one sentence and an action per item, critical first', () => {
    const markup = renderToStaticMarkup(
      <MemoryRouter>
        <AttentionList
          items={[
            { kind: 'over_limit', severity: 'warning', title: '小王超出信用额度 3.20', count: 1, link: '/admin/users' },
            { kind: 'dispute', severity: 'critical', title: '1 笔交易申诉中', count: 1, link: '/admin/disputes/t1' },
          ]}
        />
      </MemoryRouter>,
    )
    expect(markup.indexOf('申诉')).toBeLessThan(markup.indexOf('透支'))
    expect(markup).toContain('href="/admin/disputes/t1"')
    expect(markup).toContain('去仲裁')
    expect(markup).toContain('小王超出信用额度 3.20')
  })
})

describe('admin shell', () => {
  it('uses one icon per item and four mobile tabs', () => {
    expect(adminNavigation.map((item) => item.label)).toEqual(['概览', '调用', '积分', '用户', '模型', '渠道', '申诉', '设置'])
    expect(new Set(adminNavigation.map((item) => item.icon)).size).toBe(adminNavigation.length)
    expect(adminTabCount).toBe(4)
  })

  it('matches nested admin routes to their navigation item', () => {
    expect(findAdminNavItem('/admin')?.label).toBe('概览')
    expect(findAdminNavItem('/admin/channels/abc')?.label).toBe('渠道')
    expect(findAdminNavItem('/admin/disputes/abc')?.label).toBe('申诉')
  })

  it.each([
    ['/admin', undefined],
    ['/admin/points', 'points'],
    ['/admin/disputes/x', 'disputes/:tradeID'],
    ['/admin/settings', 'settings'],
  ])('routes %s inside the admin layout', (pathname, path) => {
    const matches = matchRoutes(appRoutes, pathname)
    expect(matches?.some((match) => match.route.path === '/admin')).toBe(true)
    expect(matches?.at(-1)?.route.path).toBe(path)
  })
})
