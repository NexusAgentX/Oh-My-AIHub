import { describe, expect, it } from 'vitest'
import type { ModelChannel, RoutingPreference } from '../api/types'
import {
  advancedChanged,
  changeMode,
  draftFromPreference,
  moveChannel,
  moveTo,
  orderedChannels,
  setUsed,
  toRoutingInput,
} from './routing'

const channel = (id: string): ModelChannel => ({
  id,
  name: id,
  owner: { id: 'o', display_name: 'o' },
  is_mine: false,
  formats: ['openai_chat'],
  multiplier: '1',
  current_prices: { input: '1', output: '2', cache_read: '0', cache_write: '0' },
  success_rate_24h: null,
  ttft_p50_ms: null,
  state: 'available',
  daily_cap_remaining: null,
  cooldown_remaining_seconds: null,
})

const preference = (patch: Partial<RoutingPreference>): RoutingPreference => ({
  model_id: 'm',
  source: 'account',
  mode: 'cheapest',
  order: [],
  excluded: [],
  max_attempts: null,
  ttft_timeout_ms: null,
  updated_at: null,
  ...patch,
})

const channels = ['a', 'b', 'c'].map(channel)

describe('routing editor logic', () => {
  it('appends new channels to a manual order and leaves them unchecked', () => {
    const draft = draftFromPreference(preference({ mode: 'manual', order: ['b', 'gone', 'a'] }), channels)
    expect(draft.order).toEqual(['b', 'a', 'c'])
    expect(draft.excluded).toEqual(['c'])
    expect(orderedChannels(channels, draft).map((item) => item.id)).toEqual(['b', 'a', 'c'])
  })

  it('keeps every channel checked by default outside manual mode', () => {
    const draft = draftFromPreference(preference({ source: 'default' }), channels)
    expect(draft.excluded).toEqual([])
    expect(orderedChannels(channels, draft)).toBe(channels)
  })

  it('moves channels with the keyboard and by dragging', () => {
    expect(moveChannel(['a', 'b', 'c'], 'c', -1)).toEqual(['a', 'c', 'b'])
    expect(moveChannel(['a', 'b', 'c'], 'a', -1)).toEqual(['a', 'b', 'c'])
    expect(moveTo(['a', 'b', 'c'], 'a', 2)).toEqual(['b', 'c', 'a'])
  })

  it('unchecks channels in any mode and only submits an order for manual', () => {
    let draft = draftFromPreference(preference({}), channels)
    draft = setUsed(draft, 'b', false)
    expect(toRoutingInput(draft)).toEqual({ mode: 'cheapest', order: [], excluded: ['b'], max_attempts: null, ttft_timeout_ms: null })
    draft = changeMode(draft, 'manual', channels)
    expect(toRoutingInput(draft).order).toEqual(['a', 'b', 'c'])
    expect(setUsed(draft, 'b', true).excluded).toEqual([])
  })

  it('counts changed advanced settings', () => {
    const draft = draftFromPreference(preference({ max_attempts: 2 }), channels)
    expect(advancedChanged(draft)).toBe(1)
  })
})
