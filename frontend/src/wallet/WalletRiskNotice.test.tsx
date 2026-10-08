import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import type { Wallet, WalletRecoveryAction } from '../api/types'
import { WalletRiskNotice } from './WalletPage'

const wallet: Wallet = {
  posted_balance: '1',
  asset_reserved: '0',
  spend_authorized: '0',
  credit_limit: '10',
  effective_credit_limit: '10',
  credit_used: '0',
  credit_frozen: false,
  spendable_capacity: '1',
  over_limit: false,
  risk_status: 'normal',
  updated_at: '2026-10-07T00:00:00Z',
}

const actions: WalletRecoveryAction[] = [
  { kind: 'market', href: '/c2c' },
  { kind: 'my_orders', href: '/c2c/me' },
]

function render(status: Wallet['risk_status']) {
  return renderToStaticMarkup(
    <MemoryRouter>
      <WalletRiskNotice actions={actions} wallet={{ ...wallet, risk_status: status }} />
    </MemoryRouter>,
  )
}

describe('WalletRiskNotice', () => {
  it('renders nothing for a healthy wallet', () => {
    expect(render('normal')).toBe('')
  })

  it.each(['insufficient', 'over_limit'] as const)('offers C2C recovery actions when %s', (status) => {
    const markup = render(status)
    expect(markup).toContain('href="/c2c"')
    expect(markup).toContain('href="/c2c/me"')
    expect(markup).toContain('我的挂单')
  })

  it('asks for administrator help instead of offering recovery when credit is frozen', () => {
    const markup = render('credit_frozen')
    expect(markup).toContain('信用冻结')
    expect(markup).toContain('联系管理员')
    expect(markup).not.toContain('href="/c2c')
  })
})
