import type {
  APIKey,
  C2CTrade,
  Channel,
  GatewayCall,
  MarketChannel,
} from '../api/contracts'
import { protocolLabels } from './presentation'

function formatFiat(fen: number) {
  return `¥${(fen / 100).toFixed(2)}`
}

export type PendingKind =
  | 'c2c_release'
  | 'c2c_payment'
  | 'channel_failed'
  | 'channel_paused'
  | 'route_ineligible'
  | 'route_single'
  | 'unrated'

export type PendingTone = 'danger' | 'warning' | 'info'

export type PendingItem = {
  id: string
  kind: PendingKind
  /** 左侧徽标文字：事项类别 */
  label: string
  tone: PendingTone
  title: string
  detail: string
  to: string
}

const kindOrder: PendingKind[] = [
  'c2c_release',
  'c2c_payment',
  'channel_failed',
  'channel_paused',
  'route_ineligible',
  'route_single',
  'unrated',
]

/**
 * 调用中已成功使用过的渠道：成功调用的最终报价经 Key 路由成员映射到渠道，
 * 按最近使用顺序去重。调用记录只覆盖最近 100 笔。
 */
export function usedChannelIDs(keys: APIKey[], calls: GatewayCall[]) {
  const channelByOffer = new Map<string, string>()
  for (const key of keys) {
    for (const pool of key.pools) {
      for (const member of pool.members) channelByOffer.set(member.offer_id, member.channel_id)
    }
  }
  const ordered = [...calls]
    .filter((call) => call.status === 'succeeded' && call.final_offer_id)
    .sort((a, b) => b.created_at.localeCompare(a.created_at))
  const ids: string[] = []
  for (const call of ordered) {
    const channelID = channelByOffer.get(call.final_offer_id)
    if (channelID && !ids.includes(channelID)) ids.push(channelID)
  }
  return ids
}

export function derivePendingItems(input: {
  accountID: string
  keys: APIKey[]
  trades: C2CTrade[]
  channels: Channel[]
  marketChannels: MarketChannel[]
}): PendingItem[] {
  const { accountID } = input
  const items: PendingItem[] = []

  for (const trade of input.trades) {
    const fiat = formatFiat(trade.fiat_amount_fen)
    if (trade.status === 'paid' && trade.seller_account_id === accountID) {
      items.push({
        id: `c2c-release-${trade.id}`,
        kind: 'c2c_release',
        label: '待放行',
        tone: 'warning',
        title: `${trade.buyer_display_name} 已付款 ${fiat}`,
        detail: `确认收款后放行 ${trade.quantity} 积分`,
        to: `/c2c/trades/${trade.id}`,
      })
    } else if (trade.status === 'awaiting_payment' && trade.buyer_account_id === accountID) {
      items.push({
        id: `c2c-payment-${trade.id}`,
        kind: 'c2c_payment',
        label: '待付款',
        tone: 'warning',
        title: `向 ${trade.seller_display_name} 付款 ${fiat}`,
        detail: `付款后标记已付款，买入 ${trade.quantity} 积分`,
        to: `/c2c/trades/${trade.id}`,
      })
    }
  }

  for (const channel of input.channels) {
    if (channel.status === 'paused') {
      items.push({
        id: `channel-paused-${channel.id}`,
        kind: 'channel_paused',
        label: '已暂停',
        tone: 'warning',
        title: channel.display_name,
        detail: '渠道已暂停，暂时不会被调用',
        to: `/channels/${channel.id}`,
      })
    }
    if (channel.status === 'deleted') continue
    const failed = channel.offers.filter(
      (offer) => offer.status !== 'deleted' && offer.latest_validation?.status === 'failed',
    )
    if (failed.length > 0) {
      items.push({
        id: `channel-failed-${channel.id}`,
        kind: 'channel_failed',
        label: '校验失败',
        tone: 'danger',
        title: channel.display_name,
        detail: failed
          .map((offer) => `${offer.model_name} · ${protocolLabels[offer.protocol]}`)
          .join('、'),
        to: `/channels/${channel.id}`,
      })
    }
  }

  for (const key of input.keys) {
    if (key.status !== 'active') continue
    for (const pool of key.pools) {
      const route = `${pool.model_name} · ${protocolLabels[pool.protocol]}`
      for (const member of pool.members.filter((item) => !item.eligible)) {
        items.push({
          id: `route-ineligible-${key.id}-${pool.id}-${member.offer_id}`,
          kind: 'route_ineligible',
          label: '需更新',
          tone: 'danger',
          title: `${member.channel_name} 暂不可用`,
          detail: `${key.display_name} · ${route}`,
          to: `/keys/${key.id}`,
        })
      }
      if (pool.members.length === 1) {
        items.push({
          id: `route-single-${key.id}-${pool.id}`,
          kind: 'route_single',
          label: '单渠道',
          tone: 'info',
          title: route,
          detail: `${key.display_name} · 只有 ${pool.members[0].channel_name}，没有备用`,
          to: `/market?model=${encodeURIComponent(pool.model_id)}&protocol=${pool.protocol}`,
        })
      }
    }
  }

  for (const channel of input.marketChannels) {
    if (channel.current_user_rating === null && channel.owner_account_id !== accountID) {
      items.push({
        id: `unrated-${channel.id}`,
        kind: 'unrated',
        label: '待评分',
        tone: 'info',
        title: channel.display_name,
        detail: `${channel.owner_display_name} · 已使用，尚未评分`,
        to: `/market/channels/${channel.id}`,
      })
    }
  }

  return items.sort((a, b) => kindOrder.indexOf(a.kind) - kindOrder.indexOf(b.kind))
}
