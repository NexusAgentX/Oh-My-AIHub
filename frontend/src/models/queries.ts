import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiGet, apiSend, pathSegment } from '../api/client'
import type { RoutingPreferenceInput } from '../api/types'

export const modelKeys = {
  all: ['models'] as const,
  list: ['models', 'list'] as const,
  detail: (id: string) => ['models', 'detail', id] as const,
}

export function useModels() {
  return useQuery({ queryKey: modelKeys.list, queryFn: () => apiGet<'listModels'>('/api/models') })
}

export function useModel(id: string | null) {
  return useQuery({
    queryKey: modelKeys.detail(id ?? ''),
    queryFn: () => apiGet<'getModel'>(`/api/models/${pathSegment(id ?? '')}`),
    enabled: Boolean(id),
  })
}

/** 一组模型的详情（渠道向导按当前价格档计算实际价时使用）。 */
export function useModelDetails(ids: string[]) {
  return useQueries({
    queries: ids.map((id) => ({
      queryKey: modelKeys.detail(id),
      queryFn: () => apiGet<'getModel'>(`/api/models/${pathSegment(id)}`),
    })),
  })
}

/** 账号级路由：对该用户所有 Key 生效。 */
export function useSetRouting(modelId: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: RoutingPreferenceInput) =>
      apiSend<'setRouting'>('PUT', `/api/routing/${pathSegment(modelId)}`, input),
    onSuccess: () => client.invalidateQueries({ queryKey: modelKeys.detail(modelId) }),
  })
}
