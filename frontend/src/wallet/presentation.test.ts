import { describe, expect, it } from 'vitest'
import type { LedgerEntry } from '../api/contracts'
import {
  formatPointAmount,
  ledgerCounterparties,
  ledgerEntryLabel,
  walletRiskLabel,
} from './presentation'

const entry: LedgerEntry = {
  id: '7',
  transaction_id: 'transaction-1',
  entry_ordinal: 1,
  business_role: 'consumer',
  amount: '-0.000000001',
  posted_balance_before: '1',
  posted_balance_after: '0.999999999',
  created_at: '2026-09-02T00:00:00Z',
  transaction_kind: 'hold_capture',
  reason: '调用成功',
  reference_type: 'api_call',
  reference_id: 'call-1',
  actor_account_id: '',
  reversal_of_transaction_id: '',
  hold_id: 'hold-1',
  counterparties: [
    {
      account_kind: 'platform_incentive',
      account_id: '',
      business_role: 'platform_fee',
      amount: '0.000000001',
    },
  ],
}

describe('wallet presentation', () => {
  it('preserves nano precision and signs positive amounts', () => {
    expect(formatPointAmount('0.000000001', true)).toBe('+0.000000001')
    expect(formatPointAmount('-0.000000001', true)).toBe('-0.000000001')
  })

  it('maps traceable entries without exposing implementation jargon', () => {
    expect(ledgerEntryLabel(entry)).toBe('预授权结算')
    expect(ledgerCounterparties(entry)).toBe('平台激励账户')
  })

  it('keeps risk states distinct', () => {
    expect(walletRiskLabel('insufficient')).toBe('可消费额度不足')
    expect(walletRiskLabel('over_limit')).toBe('信用超限')
    expect(walletRiskLabel('credit_frozen')).toBe('信用冻结')
  })
})
