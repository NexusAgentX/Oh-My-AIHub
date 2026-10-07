import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'

/** 管理员渠道治理的 key 工厂：失效整个领域用 adminChannelKeys.all。 */
export const adminChannelKeys = {
  all: ['admin-channels'] as const,
  list: () => [...adminChannelKeys.all, 'list'] as const,
  detail: (channelID: string) => [...adminChannelKeys.all, 'detail', channelID] as const,
  attempts: (offerID: string) => [...adminChannelKeys.all, 'attempts', offerID] as const,
}

export function useAdminChannelsQuery() {
  return useQuery({
    queryKey: adminChannelKeys.list(),
    queryFn: () => api.adminChannels(),
  })
}

export function useAdminChannelQuery(channelID: string) {
  return useQuery({
    queryKey: adminChannelKeys.detail(channelID),
    queryFn: () => api.adminChannel(channelID),
  })
}

/** 报价的验证记录；offerID 为空时不请求。 */
export function useValidationAttemptsQuery(offerID: string) {
  return useQuery({
    queryKey: adminChannelKeys.attempts(offerID),
    queryFn: () => api.channelValidationAttempts(offerID, true),
    enabled: offerID !== '',
    staleTime: 0,
  })
}

/** 暂停或删除渠道，需要当前版本与原因。 */
export function useGovernChannel() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: {
      channelID: string
      action: 'pause' | 'delete'
      expectedVersion: number
      reason: string
    }) =>
      api.adminSetChannelStatus(
        input.channelID,
        input.action,
        input.expectedVersion,
        input.reason,
      ),
    onSettled: () => client.invalidateQueries({ queryKey: adminChannelKeys.all }),
  })
}

/** 管理员重验报价（会向上游发送最小请求）。 */
export function useValidateOffer() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (offerID: string) => api.validateChannelOffer(offerID, true),
    onSettled: () => client.invalidateQueries({ queryKey: adminChannelKeys.all }),
  })
}
