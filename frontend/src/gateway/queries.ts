import {
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { api } from '../api/client'
import type { APIKeyPoolInput, ChannelProtocol } from '../api/types'

/** 网关领域的 key 工厂：失效整个领域用 gatewayKeys.all。 */
export const gatewayKeys = {
  all: ['gateway'] as const,
  dashboard: () => [...gatewayKeys.all, 'dashboard'] as const,
  keys: () => [...gatewayKeys.all, 'keys'] as const,
  key: (keyID: string) => [...gatewayKeys.keys(), keyID] as const,
  calls: (limit: number) => [...gatewayKeys.all, 'calls', limit] as const,
  call: (callID: string) => [...gatewayKeys.all, 'call', callID] as const,
  pendingItems: () => [...gatewayKeys.all, 'pending-items'] as const,
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

/** 工作台待处理事项：由后端单一接口按当前用户聚合，前端只负责展示。 */
export function usePendingItemsQuery() {
  return useQuery({
    queryKey: gatewayKeys.pendingItems(),
    queryFn: () => api.pendingItems(),
  })
}
