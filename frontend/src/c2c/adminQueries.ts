import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import type { C2CResolutionAction } from '../api/contracts'

/** 管理员 C2C 争议的 key 工厂：失效整个领域用 adminC2CKeys.all。 */
export const adminC2CKeys = {
  all: ['admin-c2c'] as const,
  disputes: () => [...adminC2CKeys.all, 'disputes'] as const,
  trade: (tradeID: string) => [...adminC2CKeys.all, 'trade', tradeID] as const,
}

export function useAdminDisputesQuery() {
  return useQuery({
    queryKey: adminC2CKeys.disputes(),
    queryFn: () => api.adminC2CDisputes(),
  })
}

export function useAdminDisputeQuery(tradeID: string) {
  return useQuery({
    queryKey: adminC2CKeys.trade(tradeID),
    queryFn: () => api.c2cTrade(tradeID),
  })
}

/** 裁决或限制后失效管理端缓存，以及用户侧 C2C 与钱包缓存。 */
function useInvalidateAfterResolution() {
  const client = useQueryClient()
  return () =>
    Promise.all([
      client.invalidateQueries({ queryKey: adminC2CKeys.all }),
      client.invalidateQueries({ queryKey: ['c2c'] }),
      client.invalidateQueries({ queryKey: ['wallet'] }),
    ])
}

export type DisputeOperation =
  | { kind: 'resolve'; action: C2CResolutionAction }
  | { kind: 'cancel-order'; orderID: string }

export function useDisputeOperation(tradeID: string) {
  const invalidate = useInvalidateAfterResolution()
  return useMutation<unknown, Error, { operation: DisputeOperation; reason: string }>({
    mutationFn: (input) =>
      input.operation.kind === 'resolve'
        ? api.resolveC2CDispute(tradeID, input.operation.action, input.reason)
        : api.adminCancelC2COrder(input.operation.orderID, input.reason),
    onSuccess: invalidate,
  })
}
