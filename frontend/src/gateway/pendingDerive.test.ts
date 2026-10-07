import { describe, expect, it } from 'vitest'
import type {
  APIKey,
  C2CTrade,
  Channel,
  GatewayCall,
  MarketChannel,
} from '../api/types'
import { derivePendingItems, usedChannelIDs } from './pendingDerive'

const member = (offer: string, channel: string, eligible = true) => ({
  offer_id: offer,
  channel_id: channel,
  channel_name: `渠道 ${channel}`,
  eligible,
})

const key = (pools: unknown[], status = 'active') =>
  ({ id: 'k1', display_name: '主力', status, pools }) as unknown as APIKey

const pool = (members: unknown[]) => ({
  id: 'p1',
  model_id: 'openai/gpt-5',
  model_name: 'GPT-5',
  protocol: 'openai_responses',
  members,
})

const empty = { accountID: 'me', keys: [], trades: [], channels: [], marketChannels: [] }

describe('pending items', () => {
  it('lists only the C2C trades the current user must act on', () => {
    const trades = [
      { id: 't1', status: 'paid', seller_account_id: 'me', buyer_account_id: 'x', buyer_display_name: '买家', quantity: '10', fiat_amount_fen: 1000 },
      { id: 't2', status: 'paid', seller_account_id: 'x', buyer_account_id: 'me', quantity: '10', fiat_amount_fen: 1000 },
      { id: 't3', status: 'awaiting_payment', seller_account_id: 'x', buyer_account_id: 'me', seller_display_name: '卖家', quantity: '5', fiat_amount_fen: 500 },
    ] as unknown as C2CTrade[]
    const items = derivePendingItems({ ...empty, trades })
    expect(items.map((item) => item.id)).toEqual(['c2c-release-t1', 'c2c-payment-t3'])
    expect(items[0].to).toBe('/c2c/trades/t1')
  })

  it('reports failed validation and paused channels of the user', () => {
    const channels = [
      { id: 'c1', display_name: '失败渠道', status: 'published', offers: [{ status: 'active', model_name: 'GPT-5', protocol: 'openai_responses', latest_validation: { status: 'failed' } }] },
      { id: 'c2', display_name: '暂停渠道', status: 'paused', offers: [] },
      { id: 'c3', display_name: '正常', status: 'published', offers: [{ status: 'active', latest_validation: { status: 'passed' } }] },
    ] as unknown as Channel[]
    const items = derivePendingItems({ ...empty, channels })
    expect(items.map((item) => item.kind).sort()).toEqual(['channel_failed', 'channel_paused'])
  })

  it('flags single-channel routes and ineligible members of active keys only', () => {
    const active = key([pool([member('o1', 'c1')]), { ...pool([member('o2', 'c2'), member('o3', 'c3', false)]), id: 'p2' }])
    const disabled = { ...key([pool([member('o9', 'c9')])], 'disabled'), id: 'k2' }
    const items = derivePendingItems({ ...empty, keys: [active, disabled] })
    expect(items.map((item) => item.kind)).toEqual(['route_ineligible', 'route_single'])
    expect(items[1].to).toBe('/market?model=openai%2Fgpt-5&protocol=openai_responses')
  })

  it('asks to rate used channels except own and already rated ones', () => {
    const marketChannels = [
      { id: 'a', display_name: 'A', owner_account_id: 'x', owner_display_name: 'X', current_user_rating: null },
      { id: 'b', display_name: 'B', owner_account_id: 'x', owner_display_name: 'X', current_user_rating: 4 },
      { id: 'c', display_name: 'C', owner_account_id: 'me', owner_display_name: '我', current_user_rating: null },
    ] as unknown as MarketChannel[]
    const items = derivePendingItems({ ...empty, marketChannels })
    expect(items.map((item) => item.id)).toEqual(['unrated-a'])
  })

  it('derives used channels from succeeded calls, newest first, deduplicated', () => {
    const keys = [key([pool([member('o1', 'c1'), member('o2', 'c2')])])]
    const calls = [
      { status: 'succeeded', final_offer_id: 'o1', created_at: '2026-10-01T00:00:00Z' },
      { status: 'succeeded', final_offer_id: 'o2', created_at: '2026-10-02T00:00:00Z' },
      { status: 'failed', final_offer_id: 'o1', created_at: '2026-10-03T00:00:00Z' },
      { status: 'succeeded', final_offer_id: 'o1', created_at: '2026-10-04T00:00:00Z' },
      { status: 'succeeded', final_offer_id: 'gone', created_at: '2026-10-05T00:00:00Z' },
    ] as unknown as GatewayCall[]
    expect(usedChannelIDs(keys, calls)).toEqual(['c1', 'c2'])
  })
})
