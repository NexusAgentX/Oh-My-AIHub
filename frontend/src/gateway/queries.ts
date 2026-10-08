import {
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { useMemo } from 'react'
import { api } from '../api/client'
import type { APIKeyPoolInput, ChannelProtocol } from '../api/types'
import { useAuth } from '../auth/AuthProvider'
import { marketKeys } from '../channels/marketQueries'
import { derivePendingItems, usedChannelIDs } from './pendingDerive'

/** 网关领域的 key 工厂：失效整个领域用 gatewayKeys.all。 */
export const gatewayKeys = {
  all: ['gateway'] as const,
  dashboard: () => [...gatewayKeys.all, 'dashboard'] as const,
  keys: () => [...gatewayKeys.all, 'keys'] as const,
  key: (keyID: string) => [...gatewayKeys.keys(), keyID] as const,
  calls: (limit: number) => [...gatewayKeys.all, 'calls', limit] as const,
  call: (callID: string) => [...gatewayKeys.all, 'call', callID] as const,
  pendingC2C: () => [...gatewayKeys.all, 'pending', 'c2c'] as const,
  pendingChannels: () => [...gatewayKeys.all, 'pending', 'channels'] as const,
}

export function useGatewayDashboardQuery() {
  return useQuery({
    queryKey: gatewayKeys.dashboard(),
    queryFn: () => api.gatewayDashboard(),
  })
}

export function useAPIKeysQuery() {
  return useQuery({
    queryKey: gatewayKeys.keys(),
    queryFn: () => api.apiKeys(),
  })
}

export function useAPIKeyQuery(keyID: string) {
  return useQuery({
    queryKey: gatewayKeys.key(keyID),
    queryFn: () => api.apiKey(keyID),
    enabled: Boolean(keyID),
  })
}

export function useGatewayCallsQuery(limit = 100) {
  return useQuery({
    queryKey: gatewayKeys.calls(limit),
    queryFn: () => api.gatewayCalls(limit),
  })
}

export function useGatewayCallQuery(callID: string) {
  return useQuery({
    queryKey: gatewayKeys.call(callID),
    queryFn: () => api.gatewayCall(callID),
    enabled: Boolean(callID),
  })
}

function useInvalidateGateway() {
  const client = useQueryClient()
  return () => client.invalidateQueries({ queryKey: gatewayKeys.all })
}

export function useCreateKeyMutation() {
  const invalidate = useInvalidateGateway()
  return useMutation({
    mutationFn: (input: { displayName: string; pools: APIKeyPoolInput[] }) =>
      api.createAPIKey(input.displayName, input.pools),
    onSuccess: invalidate,
  })
}

export function useUpdateKeyMutation() {
  const invalidate = useInvalidateGateway()
  return useMutation({
    mutationFn: (input: {
      keyID: string
      version: number
      displayName: string
      pools: APIKeyPoolInput[]
    }) => api.updateAPIKey(input.keyID, input.version, input.displayName, input.pools),
    onSuccess: invalidate,
  })
}

export type KeyAction = 'rotate' | 'disable' | 'enable' | 'delete'

/** 轮换 / 启停 / 删除统一走 CAS；rotate 额外返回只显示一次的完整 Key。 */
export function useKeyActionMutation() {
  const invalidate = useInvalidateGateway()
  return useMutation({
    mutationFn: async (input: { keyID: string; version: number; action: KeyAction }) => {
      if (input.action === 'rotate') {
        const rotated = await api.rotateAPIKey(input.keyID, input.version)
        return { key: rotated.key, secret: rotated.secret }
      }
      if (input.action === 'delete') {
        return { key: await api.deleteAPIKey(input.keyID, input.version), secret: '' }
      }
      return {
        key: await api.setAPIKeyStatus(input.keyID, input.action, input.version),
        secret: '',
      }
    },
    onSuccess: invalidate,
  })
}

export function useAddPoolMemberMutation() {
  const invalidate = useInvalidateGateway()
  return useMutation({
    mutationFn: (input: {
      keyID: string
      version: number
      modelID: string
      protocol: ChannelProtocol
      offerID: string
      priority: number
    }) =>
      api.addAPIKeyPoolMember(input.keyID, input.version, {
        model_id: input.modelID,
        protocol: input.protocol,
        offer_id: input.offerID,
        priority: input.priority,
      }),
    onSuccess: invalidate,
  })
}

const maxRatingChecks = 8

/**
 * 工作台待处理事项：由现有接口在前端组合，不依赖专门的后端接口。
 * 数据来源：C2C 活动（待放行 / 待付款）、我的渠道（校验失败 / 暂停）、
 * API Key（单渠道路由 / 渠道需更新）、最近调用 + 市场渠道详情（已使用未评分）。
 */
export function usePendingItems() {
  const { account } = useAuth()
  const accountID = account?.id ?? ''
  const keys = useAPIKeysQuery()
  const calls = useGatewayCallsQuery(100)
  const c2c = useQuery({
    queryKey: gatewayKeys.pendingC2C(),
    queryFn: () => api.c2cActivity(),
  })
  const channels = useQuery({
    queryKey: gatewayKeys.pendingChannels(),
    queryFn: () => api.channels(),
  })

  const channelIDs = useMemo(
    () => usedChannelIDs(keys.data ?? [], calls.data ?? []).slice(0, maxRatingChecks),
    [keys.data, calls.data],
  )
  const ratingQueries = useQueries({
    queries: channelIDs.map((channelID) => ({
      queryKey: marketKeys.channel(channelID),
      queryFn: () => api.marketChannel(channelID),
      retry: false,
    })),
  })

  const marketChannels = ratingQueries.flatMap((query) => (query.data ? [query.data] : []))
  const items = useMemo(
    () =>
      derivePendingItems({
        accountID,
        keys: keys.data ?? [],
        trades: c2c.data?.trades ?? [],
        channels: channels.data ?? [],
        marketChannels,
      }),
    [accountID, keys.data, c2c.data, channels.data, ...ratingQueries.map((query) => query.data)],
  )

  const sources = [keys, calls, c2c, channels]
  const failed = sources.filter((query) => query.isError).length
  return {
    items,
    loading: sources.some((query) => query.isPending),
    failed,
    allFailed: failed === sources.length,
    refetch: () => {
      for (const query of sources) {
        if (query.isError) void query.refetch()
      }
    },
  }
}
