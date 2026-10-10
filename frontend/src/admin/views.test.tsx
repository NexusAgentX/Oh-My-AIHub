import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter, matchRoutes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { appRoutes } from '../App'
import { adminStreamPath } from './CallsPage'
import { adminNavigation, adminTabCount, findAdminNavItem } from './navigation'
import { formatLastActive } from './format'
import { segments } from './LineChart'
import { AttentionList, Indicators, attentionMeta } from './OverviewPage'
import { LedgerEquation, riskColumns, trendGroups } from './PointsPage'
import { ledgerChecks, ledgerEquation } from './pointsView'
import { PriceSnapshotView, TransactionDetail, pricingNotes } from './transactions'
import type { AdminOverview, AdminPoints, AdminPointsBalances, LedgerChecks, LedgerTransaction } from './types'

const balances: AdminPointsBalances = {
  user_positive: '130.5',
  user_negative: '-60.03',
  credit_issued: '500',
  c2c_escrow: '30',
  platform_revenue: '0.03',
  bad_debt: '-100.5',
  total: '0',
  total_credit_limit: '800',
  bad_debt_writeoffs: 1,
  escrow_orders: 1,
  escrow_trades_in_progress: 0,
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

  it('turns all five live checks into rows', () => {
    const check = { balanced: true, total: '0', checked_at: '2026-10-08T00:00:00Z' }
    const rows = ledgerChecks(check, passingChecks)
    expect(rows.map((row) => row.key)).toEqual(['zero_sum', 'account_balances', 'escrow', 'billing_calls', 'released_trades'])
    expect(rows.every((row) => row.ok)).toBe(true)
  })

  it('reports each failing check with its difference', () => {
    const rows = ledgerChecks({ balanced: false, total: '1', checked_at: '2026-10-08T00:00:00Z' }, failingChecks)
    expect(rows.every((row) => !row.ok)).toBe(true)
    expect(rows.map((row) => row.detail)).toEqual([
      '差额 1.00',
      '1 个账户不一致',
      '差额 -2.00',
      '3 次调用漏记',
      '1 笔交易漏记',
    ])
  })
})

const passingChecks: LedgerChecks = {
  checked_at: '2026-10-08T00:00:00Z',
  all_passed: true,
  account_balances: { passed: true, mismatches: [] },
  escrow: { passed: true, escrow_balance: '30', orders_total: '30', difference: '0' },
  billing_calls: { passed: true, missing_count: 0, missing: [] },
  released_trades: { passed: true, missing_count: 0, missing: [] },
}

const failingChecks: LedgerChecks = {
  checked_at: '2026-10-08T00:00:00Z',
  all_passed: false,
  account_balances: {
    passed: false,
    mismatches: [{ ledger_account: { kind: 'system', system_code: 'c2c_escrow', account: null }, balance: '30', entries_total: '28' }],
  },
  escrow: { passed: false, escrow_balance: '28', orders_total: '30', difference: '-2' },
  billing_calls: {
    passed: false,
    missing_count: 3,
    missing: [
      {
        call_id: '0b6f3d1e-2f4a-4c2b-9d7e-1a2b3c4d5e6f',
        created_at: '2026-10-08T00:00:00Z',
        account: { id: 'u1', username: 'wang', display_name: '小王' },
        channel: { id: 'c1', name: '老陈的中转' },
        outcome: 'succeeded',
        charged: '0.3',
      },
    ],
  },
  released_trades: {
    passed: false,
    missing_count: 1,
    missing: [{ trade_id: 't1', status: 'released', amount: '30', resolved_at: '2026-10-08T00:00:00Z' }],
  },
}

describe('admin points views', () => {
  it('offers credit issued, API settlement and C2C trend series', () => {
    expect(Object.values(trendGroups).flatMap((group) => group.series.map((item) => item.label))).toEqual([
      '流通',
      '信用发行',
      'C2C 托管',
      '平台收入',
      '坏账',
      'API 结算量',
      '手续费',
      'C2C 成交量',
      'C2C 均价（元/积分）',
    ])
    const point = {
      date: '2026-10-08',
      circulation: '100',
      credit_issued: '60',
      platform_revenue: '0.03',
      bad_debt: '0',
      c2c_escrow: '30',
      api_volume: '30.03',
      api_fee: '0.03',
      c2c_volume: '30',
      c2c_avg_price_fen: null,
    } satisfies AdminPoints['trend'][number]
    expect(trendGroups.price.series[0].value(point)).toBeNull()
    expect(trendGroups.price.series[0].value({ ...point, c2c_avg_price_fen: 92 })).toBe(0.92)
  })

  it('breaks chart lines on days without data', () => {
    expect(segments([1, null, 2, 3]).map((segment) => segment.map((item) => item.index))).toEqual([[0], [2, 3]])
  })

  it('shows how long a balance has been negative', () => {
    const cell = riskColumns.find((column) => column.key === 'negative_days')!.cell
    const risk = {
      account: { id: 'u1', username: 'wang', display_name: '小王' },
      balance: '-10',
      credit_limit: '100',
      available: '90',
      kind: 'negative_long' as const,
      negative_days: 42,
      last_activity_at: null,
    }
    expect(cell(risk)).toBe('42 天')
    expect(cell({ ...risk, negative_days: null })).toBe('—')
  })

  it('renders the price snapshot, balance before and recent actions in the transaction drawer', () => {
    const transaction: LedgerTransaction = {
      id: 'tx1',
      type: 'api_call',
      idempotency_key: 'k',
      related: { type: 'call', id: 'call1' },
      actor: null,
      reason: '',
      created_at: '2026-10-08T00:00:00Z',
      related_summary: 'gpt-x · 老陈的中转',
      entries: [
        { ledger_account: { kind: 'user', system_code: null, account: { id: 'u1', username: 'wang', display_name: '小王' } }, amount: '-30.03', balance_before: '0', balance_after: '-30.03' },
        { ledger_account: { kind: 'user', system_code: null, account: { id: 'u2', username: 'chen', display_name: '老陈' } }, amount: '30', balance_before: '0', balance_after: '30' },
        { ledger_account: { kind: 'system', system_code: 'platform_revenue', account: null }, amount: '0.03', balance_before: '0', balance_after: '0.03' },
      ],
      price_snapshot: {
        base_prices: { input: '1', output: '2', cache_read: '0.1', cache_write: '1.25' },
        tier: { seq: 1, name: '夜间档' },
        multiplier: '1.2',
        fee_rate_nano: 1_000_000,
        prices: { input: '1.2', output: '2.4', cache_read: '0.12', cache_write: '1.5' },
      },
      recent_actions: [
        { id: 'a1', actor: { id: 'admin', username: 'admin', display_name: '管理员' }, action: 'ledger.adjust', target_type: 'account', target_id: 'u1', reason: '补偿', detail: {}, created_at: '2026-10-07T00:00:00Z' },
      ],
    }
    const markup = renderToStaticMarkup(
      <MemoryRouter>
        <TransactionDetail transaction={transaction} />
      </MemoryRouter>,
    )
    expect(markup).toContain('gpt-x · 老陈的中转')
    expect(markup).toContain('变动前')
    expect(markup).toContain('价格快照')
    expect(markup).toContain('夜间档')
    expect(markup).toContain('0.1%')
    expect(markup).toContain('调账')
    expect(markup).toContain('补偿')
    expect(renderToStaticMarkup(<PriceSnapshotView snapshot={{ ...transaction.price_snapshot!, tier: null }} />)).toContain('基准价')
  })

  it('condenses pricing notes and hides empty snapshot facts', () => {
    const remainders = ['输入通用余量 499 tokens（无可靠细分，按本组通用价）', '缓存读通用余量 63488 tokens（无可靠细分，按本组通用价）']
    expect(pricingNotes(['未提供可靠细分，按通用价格结算', ...remainders], false)).toEqual(['未提供可靠细分，按通用价格结算'])
    expect(pricingNotes(['缓存TTL未细分，使用通用缓存写价', ...remainders], true)).toEqual([
      '缓存TTL未细分，使用通用缓存写价',
      '其余按通用价：输入 499 · 缓存读 63,488 tokens',
    ])
    const markup = renderToStaticMarkup(
      <PriceSnapshotView
        snapshot={{
          base_prices: { input: '1', output: '2', cache_read: '0.1', cache_write: '1.25' },
          tier: null,
          multiplier: '1',
          fee_rate_nano: 0,
          prices: { input: '1', output: '2', cache_read: '0.1', cache_write: '1.25' },
          detail: { notes: ['未提供可靠细分，按通用价格结算', ...remainders] },
        }}
      />,
    )
    expect(markup.match(/计价说明/g)).toHaveLength(1)
    expect(markup).not.toContain('通用余量')
    expect(markup).not.toContain('服务档位')
    expect(markup).not.toContain('百炼思考模式')
  })

  it('formats last activity', () => {
    const now = Date.parse('2026-10-08T12:00:00Z')
    expect(formatLastActive(null, now)).toBe('从未')
    expect(formatLastActive('2026-10-08T11:30:00Z', now)).toBe('刚刚')
    expect(formatLastActive('2026-10-08T07:00:00Z', now)).toBe('5 小时前')
    expect(formatLastActive('2026-10-05T12:00:00Z', now)).toBe('3 天前')
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

  it('does not append attempt counts as item counts', () => {
    const markup = renderToStaticMarkup(
      <MemoryRouter>
        <AttentionList
          items={[{ kind: 'channel_failing', severity: 'warning', title: '渠道「甲」1 小时成功率 59%（64 次尝试）', count: 64, link: '/admin/channels/c1' }]}
        />
      </MemoryRouter>,
    )
    expect(markup).not.toContain('64 项')
    expect(markup).toContain('href="/admin/channels/c1"')
  })
})

describe('overview indicators', () => {
  const overview: AdminOverview = {
    attention: [],
    ledger: { balanced: true, total: '0', checked_at: '2026-10-08T00:00:00Z' },
    today: { calls: 3, succeeded: 3, success_rate: '1', spend: '1', fee_revenue: '0.001' },
    c2c: { open_orders: 1, awaiting_payment: 0, open_disputes: 0, trades_24h: 2, volume_24h: '50', avg_price_fen_24h: 92.5 },
    credit: { issued: '25', limit: '100' },
    last_24h: { calls: 40, succeeded: 38, success_rate: '0.95' },
  }

  it('shows 24-hour calls with success rate and 24-hour C2C trades with average price', () => {
    const markup = renderToStaticMarkup(<Indicators overview={overview} />)
    expect(markup).toContain('24 小时调用')
    expect(markup).toContain('40')
    expect(markup).toContain('成功率 95%')
    expect(markup).toContain('24 小时 C2C 成交')
    expect(markup).toContain('2 笔')
    expect(markup).toContain('¥0.93')
    expect(markup).toContain('25%')
  })

  it('gives every attention kind its own label and a target', () => {
    const kinds = [
      'dispute',
      'over_limit',
      'negative_balance',
      'credit_concentration',
      'channel_suspended',
      'channel_failing',
      'unbilled_usage',
      'ledger_unbalanced',
      'reconciliation_failed',
      'stuck_call',
    ] as const
    const labels = kinds.map((kind) => attentionMeta({ kind, severity: 'warning', title: '', count: 1, link: '/admin/points' }).label)
    expect(new Set(labels).size).toBe(kinds.length)
    expect(attentionMeta({ kind: 'reconciliation_failed', severity: 'critical', title: '', count: 1, link: '/admin/points' }).link).toBe('/admin/points#checks')
    expect(attentionMeta({ kind: 'credit_concentration', severity: 'warning', title: '', count: 1, link: '/admin/points' }).link).toBe('/admin/points#risks')
    expect(
      attentionMeta({ kind: 'unbilled_usage', severity: 'warning', title: '', count: 1, link: '/admin/calls?outcome=succeeded_unbilled' }).link,
    ).toBe('/admin/calls?outcome=succeeded_unbilled')
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

describe('admin calls live stream', () => {
  it('passes the channel filter to the stream and turns live off for a single user', () => {
    expect(adminStreamPath('', '')).toBe('/api/admin/calls/stream')
    expect(adminStreamPath('', 'c1')).toBe('/api/admin/calls/stream?channel_id=c1')
    expect(adminStreamPath('u1', 'c1')).toBeUndefined()
  })

  it('routes /admin/calls inside the admin layout', () => {
    expect(matchRoutes(appRoutes, '/admin/calls')?.at(-1)?.route.path).toBe('calls')
  })
})
