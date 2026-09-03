import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { APIKey } from '../api/contracts'
import {
  APIKeyConflictBanner,
  draftID,
  existingMemberMap,
  isAPIKeySaveBlocked,
  moveOfferIDs,
} from './APIKeyEditorPage'
import { formatNullableMetric, formatRate, gatewayStatusLabel } from './presentation'

describe('API Key pool editor', () => {
  it('keys pools by model and native protocol', () => {
    expect(draftID('openai/gpt-5', 'openai_responses')).not.toBe(
      draftID('openai/gpt-5', 'openai_chat_completions'),
    )
  })

  it('keeps priority continuous when moving candidates', () => {
    expect(moveOfferIDs(['one', 'two', 'three'], 'three', 0)).toEqual([
      'three',
      'one',
      'two',
    ])
    expect(moveOfferIDs(['one', 'two'], 'missing', 0)).toEqual(['one', 'two'])
  })

  it('retains an existing ineligible member outside the public market', () => {
    const member = {
      offer_id: 'stale-offer',
      channel_name: '历史渠道',
      eligible: false,
      ineligible_reason: 'validation_stale',
    }
    const key = {
      pools: [{ members: [member] }],
    } as unknown as APIKey
    expect(existingMemberMap(key)['stale-offer']).toMatchObject(member)
  })

  it('blocks a stale save until the user explicitly reloads server state', () => {
    const conflict = { localVersion: 4, serverVersion: 5 }
    const markup = renderToStaticMarkup(createElement(APIKeyConflictBanner, {
      busy: false,
      conflict,
      onReload: () => undefined,
    }))
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
