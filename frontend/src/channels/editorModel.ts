import { api, ApiError } from '../api/client'
import type { Channel, ChannelOffer, ChannelProtocol } from '../api/contracts'

export const protocols: ChannelProtocol[] = [
  'openai_chat_completions',
  'openai_responses',
  'anthropic_messages',
  'google_gemini_generate_content',
]

export type OfferDraft = {
  id?: string
  selected: boolean
  selectionTouched: boolean
  upstreamModelID: string
  upstreamTouched: boolean
}

export type ModelGroup = {
  modelID: string
  modelName: string
  provider: string
  multiplier: string
  multiplierTouched: boolean
  offers: Record<ChannelProtocol, OfferDraft>
}

export type ChannelFieldDraft = {
  displayName: string
  displayNameTouched: boolean
  baseURL: string
  baseURLTouched: boolean
}

export function emptyOffers(modelID: string): Record<ChannelProtocol, OfferDraft> {
  return Object.fromEntries(protocols.map((protocol) => [protocol, {
    selected: false,
    selectionTouched: false,
    upstreamModelID: modelID,
    upstreamTouched: false,
  }])) as Record<ChannelProtocol, OfferDraft>
}

export function channelGroups(channel: Channel): ModelGroup[] {
  const groups = new Map<string, ModelGroup>()
  for (const offer of channel.offers.filter((item) => item.status !== 'deleted')) {
    const group = groups.get(offer.model_id) ?? {
      modelID: offer.model_id,
      modelName: offer.model_name,
      provider: offer.model_provider,
      multiplier: offer.multiplier,
      multiplierTouched: false,
      offers: emptyOffers(offer.model_id),
    }
    group.offers[offer.protocol] = {
      id: offer.id,
      selected: true,
      selectionTouched: false,
      upstreamModelID: offer.upstream_model_id ?? offer.model_id,
      upstreamTouched: false,
    }
    groups.set(offer.model_id, group)
  }
  return [...groups.values()]
}

function offerChanged(current: ChannelOffer, desired: OfferDraft, multiplier: string) {
  return current.upstream_model_id !== desired.upstreamModelID || current.multiplier !== multiplier
}

export function rebaseGroups(drafts: ModelGroup[], latest: Channel): ModelGroup[] {
  return drafts.map((group) => {
    const currentOffers = latest.offers.filter((offer) =>
      offer.status !== 'deleted' && offer.model_id === group.modelID)
    const currentGroup = currentOffers[0]
    return {
      ...group,
      modelName: currentGroup?.model_name ?? group.modelName,
      provider: currentGroup?.model_provider ?? group.provider,
      multiplier: group.multiplierTouched
        ? group.multiplier
        : currentGroup?.multiplier ?? group.multiplier,
      offers: Object.fromEntries(protocols.map((protocol) => {
        const draft = group.offers[protocol]
        const current = currentOffers.find((offer) => offer.protocol === protocol)
        if (!current) {
          return [protocol, {
            ...draft,
            id: undefined,
            selected: draft.selectionTouched ? draft.selected : false,
          }]
        }
        return [protocol, {
          ...draft,
          id: current.id,
          selected: draft.selectionTouched ? draft.selected : true,
          upstreamModelID: draft.upstreamTouched
            ? draft.upstreamModelID
            : current.upstream_model_id ?? current.model_id,
        }]
      })) as Record<ChannelProtocol, OfferDraft>,
    }
  })
}

export function rebaseChannelFields(draft: ChannelFieldDraft, latest: Channel): ChannelFieldDraft {
  return {
    displayName: draft.displayNameTouched ? draft.displayName : latest.display_name,
    displayNameTouched: draft.displayNameTouched,
    baseURL: draft.baseURLTouched ? draft.baseURL : latest.base_url ?? '',
    baseURLTouched: draft.baseURLTouched,
  }
}

/** 渠道草稿：表单字段 + 按模型分组的协议报价。 */
export type ChannelDraft = {
  displayName: string
  baseURL: string
  credential: string
  groups: ModelGroup[]
}

/** 保存前的本地校验，返回错误文案；通过时返回空串。 */
export function validateDraft(draft: ChannelDraft, editing: boolean): string {
  if (!draft.displayName.trim()) return '请输入渠道名称'
  if (!draft.baseURL.trim()) return '请输入 Base URL'
  if (!editing && !draft.credential) return '请输入上游 API Key'
  const selected = draft.groups.flatMap((group) =>
    protocols.filter((protocol) => group.offers[protocol].selected).map((protocol) => ({ group, protocol })))
  if (selected.length === 0) return '至少启用一个模型协议'
  for (const { group, protocol } of selected) {
    const multiplier = Number(group.multiplier)
    if (group.multiplier.trim() === '' || !Number.isFinite(multiplier) || multiplier < 0 || multiplier > 1000) {
      return `${group.modelName} 的倍率无效`
    }
    if (!group.offers[protocol].upstreamModelID.trim()) return `${group.modelName} 缺少上游模型 ID`
  }
  return ''
}

export function createInput(draft: ChannelDraft) {
  return {
    display_name: draft.displayName.trim(),
    base_url: draft.baseURL.trim(),
    credential: draft.credential,
    offers: draft.groups.flatMap((group) => protocols
      .filter((protocol) => group.offers[protocol].selected)
      .map((protocol) => ({
        model_id: group.modelID,
        protocol,
        upstream_model_id: group.offers[protocol].upstreamModelID.trim(),
        multiplier: group.multiplier,
      }))),
  }
}

/**
 * 把草稿逐步应用到已有渠道：先更新连接，再改报价、停用移除的协议、最后新增协议。
 * 每一步都带最新版本号；isActive 返回 false（用户已离开页面）时停止后续写入。
 * 返回最后一次读取到的渠道。
 */
export async function applyDraft(
  channel: Channel,
  draft: ChannelDraft,
  isActive: () => boolean,
): Promise<Channel> {
  const channelID = channel.id
  let latest = channel
  const each = async (
    visit: (group: ModelGroup, protocol: ChannelProtocol, desired: OfferDraft) => Promise<boolean>,
  ) => {
    for (const group of draft.groups) {
      for (const protocol of protocols) {
        if (!isActive()) return
        if (await visit(group, protocol, group.offers[protocol])) latest = await api.channel(channelID)
      }
    }
  }
  if (draft.displayName.trim() !== channel.display_name || draft.baseURL.trim() !== channel.base_url || draft.credential) {
    latest = await api.updateChannel(channelID, {
      display_name: draft.displayName.trim(),
      base_url: draft.baseURL.trim(),
      expected_version: latest.version,
      ...(draft.credential ? { credential: draft.credential } : {}),
    })
  }
  await each(async (group, _protocol, desired) => {
    if (!desired.id || !desired.selected) return false
    const current = latest.offers.find((offer) => offer.id === desired.id)
    if (!current || !offerChanged(current, desired, group.multiplier)) return false
    await api.updateChannelOffer(current.id, current.version ?? 0, desired.upstreamModelID.trim(), group.multiplier)
    return true
  })
  await each(async (_group, _protocol, desired) => {
    if (!desired.id || desired.selected) return false
    const current = latest.offers.find((offer) => offer.id === desired.id)
    if (!current || current.status === 'deleted') return false
    await api.deleteChannelOffer(current.id, current.version ?? 0)
    return true
  })
  await each(async (group, protocol, desired) => {
    if (desired.id || !desired.selected) return false
    await api.addChannelOffer(channelID, latest.version, {
      model_id: group.modelID,
      protocol,
      upstream_model_id: desired.upstreamModelID.trim(),
      multiplier: group.multiplier,
    })
    return true
  })
  return latest
}

export function isConflict(error: unknown) {
  return error instanceof ApiError && error.code === 'conflict'
}
