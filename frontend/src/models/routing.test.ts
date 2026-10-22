import { describe, expect, it } from 'vitest'
import type { ModelRoutingChannel, RoutingPreference } from '../api/types'
import {
  advancedChanged,
  changeMode,
  channelStateLabel,
  draftFromPreference,
  moveChannel,
  moveTo,
  orderedChannels,
  setUsed,
  toRoutingInput,
} from './routing'

const channel = (id: string): ModelRoutingChannel => ({
  id,
  name: id,
  is_mine: false,
  formats: ['openai_chat'],
  multiplier: '1',
  typical_price: null,
  state: 'available',
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
  idle_timeout_ms: null,
  request_timeout_ms: null,
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
    expect(toRoutingInput(draft)).toEqual({ mode: 'cheapest', order: [], excluded: ['b'], max_attempts: null, ttft_timeout_ms: null, idle_timeout_ms: null, request_timeout_ms: null })
    draft = changeMode(draft, 'manual', channels)
    expect(toRoutingInput(draft).order).toEqual(['a', 'b', 'c'])
    expect(setUsed(draft, 'b', true).excluded).toEqual([])
  })

  it('counts changed advanced settings', () => {
    const draft = draftFromPreference(preference({ max_attempts: 2 }), channels)
    expect(advancedChanged(draft)).toBe(1)
  })
})

describe('channel state label', () => {
  it('shows the concurrency or RPM limit', () => {
    expect(channelStateLabel({ ...channel('a'), state: 'limited' }).label).toBe('已达上限')
  })

  it('shows cooldown minutes and online', () => {
    expect(channelStateLabel({ ...channel('a'), state: 'cooldown', cooldown_remaining_seconds: 90 }).label).toBe('冷却中 2 分钟')
    expect(channelStateLabel(channel('a')).label).toBe('在线')
  })
})

it('round-trips all timeout overrides independently and counts them', () => {
  const draft = draftFromPreference(preference({ ttft_timeout_ms: 60000, idle_timeout_ms: 2000, request_timeout_ms: 10000 }), channels)
  expect(toRoutingInput(draft)).toMatchObject({ ttft_timeout_ms: 60000, idle_timeout_ms: 2000, request_timeout_ms: 10000 })
  expect(advancedChanged(draft)).toBe(3)
  expect(toRoutingInput({ ...draft, idle_timeout_ms: null })).toMatchObject({ ttft_timeout_ms: 60000, idle_timeout_ms: null, request_timeout_ms: 10000 })
})
