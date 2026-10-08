import { describe, expect, it } from 'vitest'
import type { ApiKey } from '../api/types'
import { advancedChangedCount, budgetProgress, createRequest, emptyKeyForm, formFromKey, keyBadges, updateRequest, validateKeyForm } from './keyForm'

const key: ApiKey = {
  id: 'k',
  name: '默认',
  prefix: 'oma_ab12',
  status: 'enabled',
  is_default: true,
  expires_at: null,
  allowed_models: [],
  budget_daily: '10',
  budget_monthly: '100',
  budget_total: null,
  model_aliases: {},
  routed_models: [],
  spend: { today: '8.5', month: '20', total: '20' },
  last_used_at: null,
  created_at: '2026-10-08T00:00:00Z',
  updated_at: '2026-10-08T00:00:00Z',
}

describe('API key form', () => {
  it('reports the tightest budget and warns from 80%', () => {
    expect(budgetProgress(key)).toEqual({ label: '今日', percent: 85, warn: true })
    expect(budgetProgress({ ...key, spend: { today: '1', month: '20', total: '20' } })).toEqual({ label: '本月', percent: 20, warn: false })
    expect(budgetProgress({ ...key, budget_daily: null, budget_monthly: null })).toBeNull()
  })

  it('counts the five advanced items that differ from defaults', () => {
    expect(advancedChangedCount(emptyKeyForm)).toBe(0)
    const form = { ...formFromKey(key), aliases: [{ from: 'gpt-4', to: 'gpt-5' }], expiresAt: '2027-01-01T00:00' }
    expect(advancedChangedCount(form, 2)).toBe(4)
  })

  it('sends only changed fields on update', () => {
    const original = formFromKey(key)
    expect(updateRequest(original, original)).toEqual({})
    expect(updateRequest(original, { ...original, name: '新名字', budgetDaily: '' })).toEqual({ name: '新名字', budget_daily: null })
  })

  it('validates budgets, model scope and aliases', () => {
    expect(validateKeyForm({ ...emptyKeyForm, name: '' })).toBe('请填写名称')
    expect(validateKeyForm({ ...emptyKeyForm, name: 'x', budgetDaily: '-1' })).toContain('每日预算')
    expect(validateKeyForm({ ...emptyKeyForm, name: 'x', modelScope: 'some' })).toBe('请至少选择一个模型')
    expect(validateKeyForm({ ...emptyKeyForm, name: 'x', aliases: [{ from: 'a', to: '' }] })).toContain('别名')
    expect(createRequest({ ...emptyKeyForm, name: ' x ' })).toMatchObject({ name: 'x', allowed_models: [], budget_daily: null, expires_at: null })
  })

  it('summarises settings as badges', () => {
    expect(keyBadges({ ...key, routed_models: ['m'], allowed_models: ['a', 'b'], model_aliases: { x: 'y' } })).toEqual([
      '单独路由',
      '2 个模型',
      '已设别名',
    ])
  })
})
