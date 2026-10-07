import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import type { ModelInput } from '../api/types'

/** 管理员模型目录的 key 工厂：失效整个领域用 adminModelKeys.all。 */
export const adminModelKeys = {
  all: ['admin-models'] as const,
  list: (search: string) => [...adminModelKeys.all, 'list', search] as const,
  detail: (modelID: string) => [...adminModelKeys.all, 'detail', modelID] as const,
}

export function useAdminModelsQuery(search: string) {
  return useQuery({
    queryKey: adminModelKeys.list(search),
    queryFn: () => api.models(search, true),
  })
}

/** 模型变更同时失效面向用户的目录缓存。 */
function useInvalidateModels() {
  const client = useQueryClient()
  return () =>
    Promise.all([
      client.invalidateQueries({ queryKey: adminModelKeys.all }),
      client.invalidateQueries({ queryKey: ['models'] }),
    ])
}

export function useCreateModel() {
  const invalidate = useInvalidateModels()
  return useMutation({
    mutationFn: (input: ModelInput) => api.createModel(input),
    onSuccess: invalidate,
  })
}

export function useUpdateModel() {
  const invalidate = useInvalidateModels()
  return useMutation({
    mutationFn: (input: {
      modelID: string
      expectedVersion: number
      update: Omit<ModelInput, 'id'>
    }) => api.updateModel(input.modelID, input.expectedVersion, input.update),
    onSuccess: invalidate,
  })
}

/** 版本冲突后按需读取最新版本（总是绕过缓存）。 */
export function useFetchLatestModel() {
  const client = useQueryClient()
  return (modelID: string) =>
    client.fetchQuery({
      queryKey: adminModelKeys.detail(modelID),
      queryFn: () => api.model(modelID, true),
      staleTime: 0,
    })
}
