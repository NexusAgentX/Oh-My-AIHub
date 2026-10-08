import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { APIKey } from '../api/types'
import { APIKeyConflictBanner } from './KeyEditorDrawer'
import {
  draftID,
  draftRoutesFromKey,
  existingMemberMap,
  isAPIKeySaveBlocked,
  moveOfferIDs,
  routeInputs,
  validateKeyDraft,
} from './routeDraft'
import { formatNullableMetric, formatRate, gatewayStatusLabel } from './presentation'

describe('route draft', () => {
  it('keys routes by model and native protocol', () => {
    expect(draftID('openai/gpt-5', 'openai_responses')).not.toBe(
      draftID('openai/gpt-5', 'openai_chat_completions'),
    )
  })

  it('keeps backup order continuous when moving candidates', () => {
    expect(moveOfferIDs(['one', 'two', 'three'], 'three', 0)).toEqual(['three', 'one', 'two'])
    expect(moveOfferIDs(['one', 'two'], 'missing', 0)).toEqual(['one', 'two'])
    expect(moveOfferIDs(['one', 'two'], 'one', 9)).toEqual(['two', 'one'])
  })

  it('retains an existing ineligible member outside the public market', () => {
    const member = {
      offer_id: 'stale-offer',
      channel_name: '历史渠道',
      eligible: false,
      ineligible_reason: 'validation_stale',
    }
    const key = { pools: [{ members: [member] }] } as unknown as APIKey
    expect(existingMemberMap(key)['stale-offer']).toMatchObject(member)
  })

  it('round-trips a key into draft routes and API input', () => {
    const key = {
      pools: [{ model_id: 'm', protocol: 'openai_responses', members: [{ offer_id: 'a' }, { offer_id: 'b' }] }],
    } as unknown as APIKey
    const drafts = draftRoutesFromKey(key)
    expect(drafts[0].offer_ids).toEqual(['a', 'b'])
    expect(routeInputs(drafts)).toEqual([
      { model_id: 'm', protocol: 'openai_responses', offer_ids: ['a', 'b'] },
    ])
  })

  it('validates name and at least one channel per route', () => {
    const route = { draft_id: 'x', model_id: 'm', protocol: 'openai_responses' as const, offer_ids: ['a'] }
    expect(validateKeyDraft(' ', [route])).toBe('请输入 Key 名称')
    expect(validateKeyDraft('名称', [])).toBe('至少配置一个路由')
    expect(validateKeyDraft('名称', [{ ...route, offer_ids: [] }])).toBe('每个路由至少需要一个渠道')
    expect(validateKeyDraft('名称', [route])).toBe('')
  })

  it('blocks a stale save until the user explicitly reloads server state', () => {
    const conflict = { localVersion: 4, serverVersion: 5 }
    const markup = renderToStaticMarkup(
      createElement(APIKeyConflictBanner, { busy: false, conflict, onReload: () => undefined }),
    )
    expect(markup).toContain('服务器版本 v5')
    expect(markup).toContain('本地草稿基于 v4')
    expect(markup).toContain('重新加载最新版本')
    expect(isAPIKeySaveBlocked(false, conflict)).toBe(true)
    expect(isAPIKeySaveBlocked(false, null)).toBe(false)
  })
})

describe('gateway metrics presentation', () => {
  it('formats ratio values as percentages without floating point', () => {
    expect(formatRate('0.9750')).toBe('97.5%')
    expect(formatRate('1.0000')).toBe('100.0%')
    expect(formatRate(null)).toBe('—')
  })

  it('keeps a legitimate zero metric distinct from missing data', () => {
    expect(formatNullableMetric(0)).toBe(0)
    expect(formatNullableMetric('0.000000000')).toBe('0.000000000')
    expect(formatNullableMetric(null)).toBe('—')
    expect(formatNullableMetric(undefined)).toBe('—')
  })

  it('labels pending delivery as a visible transient state', () => {
    expect(gatewayStatusLabel('pending_delivery')).toBe('交付确认中')
  })
})
