import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import type { Channel, ChannelOffer } from '../api/types'
import { gatewayKeys } from '../gateway/queries'
import { applyDraft, createInput, type ChannelDraft } from './editorModel'

/** 共享渠道领域的 key 工厂：失效整个领域用 channelKeys.all。 */
export const channelKeys = {
  all: ['channels'] as const,
  list: () => [...channelKeys.all, 'list'] as const,
  detail: (channelID: string) => [...channelKeys.all, 'detail', channelID] as const,
  attempts: (offerID: string) => [...channelKeys.all, 'attempts', offerID] as const,
  catalog: () => [...channelKeys.all, 'catalog'] as const,
}

export function useChannelsQuery() {
  return useQuery({ queryKey: channelKeys.list(), queryFn: () => api.channels() })
}

export function useChannelQuery(channelID: string) {
  return useQuery({
    queryKey: channelKeys.detail(channelID),
    queryFn: () => api.channel(channelID),
    enabled: Boolean(channelID),
  })
}

/** 模型目录，仅用于上架时选择模型。 */
export function useCatalogModelsQuery() {
  return useQuery({ queryKey: channelKeys.catalog(), queryFn: () => api.models() })
}

/** 某个报价的校验历史；offerID 为空时不请求。每次打开都重新取。 */
export function useValidationAttemptsQuery(offerID: string | null) {
  return useQuery({
    queryKey: channelKeys.attempts(offerID ?? ''),
    queryFn: () => api.channelValidationAttempts(offerID ?? ''),
    enabled: Boolean(offerID),
    staleTime: 0,
  })
}

/** 写操作：无论成败都失效渠道领域（冲突后需要最新版本）和依赖渠道健康度的工作台数据。 */
function useChannelWrite<Variables, Result>(mutationFn: (variables: Variables) => Promise<Result>) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn,
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: channelKeys.all }),
        queryClient.invalidateQueries({ queryKey: gatewayKeys.all }),
      ]),
  })
}

export function useChannelStatusMutation() {
  return useChannelWrite(({ channel, action }: { channel: Channel; action: 'publish' | 'pause' }) =>
    api.setChannelStatus(channel.id, action, channel.version))
}

export function useDeleteChannelMutation() {
  return useChannelWrite((channel: Channel) => api.deleteChannel(channel.id, channel.version))
}

export function useRevokeCredentialMutation() {
  return useChannelWrite((channel: Channel) => api.revokeChannelCredential(channel.id, channel.version))
}

export function useOfferStatusMutation() {
  return useChannelWrite((offer: ChannelOffer) =>
    api.setChannelOfferStatus(offer.id, offer.status === 'active' ? 'disable' : 'resume', offer.version ?? 0))
}

export function useDeleteOfferMutation() {
  return useChannelWrite((offer: ChannelOffer) => api.deleteChannelOffer(offer.id, offer.version ?? 0))
}

/** 向上游发送最小请求，可能产生费用；调用方必须先取得用户确认。 */
export function useValidateOfferMutation() {
  return useChannelWrite((offer: ChannelOffer) => api.validateChannelOffer(offer.id))
}

export function useCreateChannelMutation() {
  return useChannelWrite((draft: ChannelDraft) => api.createChannel(createInput(draft)))
}

/** 把草稿逐步应用到已有渠道；isActive 返回 false 时停止后续写入。 */
export function useUpdateChannelMutation() {
  return useChannelWrite(({ channel, draft, isActive }: {
    channel: Channel
    draft: ChannelDraft
    isActive: () => boolean
  }) => applyDraft(channel, draft, isActive))
}

/** 版本冲突后强制读取最新渠道。 */
export function useLoadLatestChannel() {
  const queryClient = useQueryClient()
  return (channelID: string) => queryClient.fetchQuery({
    queryKey: channelKeys.detail(channelID),
    queryFn: () => api.channel(channelID),
    staleTime: 0,
  })
}
